package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/filestorage"
)

const legacyAIFileImportOperation = "legacy_ai_files_import"

var (
	uploadLegacyAIFile = filestorage.UploadReaderAtPath
	verifyLegacyAIFile = filestorage.VerifyURL
	deleteLegacyAIFile = filestorage.DeleteURL
)

type AIFileImportReport struct {
	Imported, Skipped, Issues int64
	RunID                     int64
}

type legacyAIFileRow struct {
	ID, Filename, Purpose, MimeType, Status, ContentSHA256 string
	UserID, TokenID                                        uint64
	Bytes, ContentBytes                                    int64
	CreatedAt                                              time.Time
}

func ImportLegacyAIFiles(ctx context.Context, db *sql.DB, options ImportOptions) (AIFileImportReport, error) {
	if db == nil || len(options.HMACKey) != security.KeySize {
		return AIFileImportReport{}, ErrImportRequiresKeyring
	}
	if config.C == nil || strings.TrimSpace(config.C.FileStorage.BaseURL) == "" || strings.TrimSpace(config.C.FileStorage.APIKey) == "" {
		return AIFileImportReport{}, fmt.Errorf("file storage is required for legacy AI file import")
	}
	source, err := readLegacyAIFileSnapshot(ctx, db)
	if err != nil {
		return AIFileImportReport{}, err
	}
	revision := legacyAIFileSnapshotDigest(options.HMACKey, source)
	runID, complete, err := beginAIFileImportRun(ctx, db, revision)
	if err != nil {
		return AIFileImportReport{}, err
	}
	report := AIFileImportReport{RunID: runID}
	if complete {
		report.Skipped = int64(len(source))
		return report, nil
	}
	for _, row := range source {
		outcome, importErr := importLegacyAIFileRow(ctx, db, options.HMACKey, runID, row)
		switch outcome {
		case "imported":
			report.Imported++
		case "skipped":
			report.Skipped++
		case "issue":
			report.Issues++
		}
		if importErr != nil {
			_ = finishAIFileImportRun(context.WithoutCancel(ctx), db, runID, "failed", "file_import_error")
			return report, importErr
		}
	}
	status, code := "succeeded", ""
	if report.Issues > 0 {
		status, code = "failed", "unresolved_migration_issues"
	}
	if err := finishAIFileImportRun(ctx, db, runID, status, code); err != nil {
		return report, err
	}
	if report.Issues > 0 {
		return report, fmt.Errorf("legacy AI file import found %d unresolved rows", report.Issues)
	}
	return report, nil
}

func readLegacyAIFileSnapshot(ctx context.Context, db *sql.DB) ([]legacyAIFileRow, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,user_id,token_id,filename,purpose,bytes,mime_type,status,created_at,OCTET_LENGTH(content),LOWER(SHA2(content,256)) FROM ai_files ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []legacyAIFileRow
	for rows.Next() {
		var row legacyAIFileRow
		var id, filename, purpose, mimeType, status, digest sql.NullString
		var userID, tokenID sql.NullInt64
		var declaredBytes, contentBytes sql.NullInt64
		var createdAt sql.NullTime
		if err := rows.Scan(&id, &userID, &tokenID, &filename, &purpose, &declaredBytes, &mimeType, &status, &createdAt, &contentBytes, &digest); err != nil {
			return nil, err
		}
		row = legacyAIFileRow{
			ID: id.String, UserID: uint64(userID.Int64), TokenID: uint64(tokenID.Int64),
			Filename: filename.String, Purpose: purpose.String, Bytes: declaredBytes.Int64,
			MimeType: mimeType.String, Status: status.String, CreatedAt: createdAt.Time,
			ContentBytes: contentBytes.Int64, ContentSHA256: digest.String,
		}
		if !id.Valid || !userID.Valid || userID.Int64 <= 0 || !tokenID.Valid || tokenID.Int64 <= 0 ||
			!filename.Valid || !purpose.Valid || !declaredBytes.Valid || !mimeType.Valid || !status.Valid ||
			!createdAt.Valid || !contentBytes.Valid || !digest.Valid {
			row.ID = strings.TrimSpace(row.ID)
			row.Status = "invalid_source_row"
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func beginAIFileImportRun(ctx context.Context, db *sql.DB, revision string) (int64, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var id int64
	var status string
	err = tx.QueryRowContext(ctx, `SELECT id,status FROM gw_migration_runs WHERE operation=? AND source_revision_hmac=? FOR UPDATE`, legacyAIFileImportOperation, revision).Scan(&id, &status)
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_migration_runs(operation,source_revision_hmac,status,started_at) VALUES (?,?,'running',?)`, legacyAIFileImportOperation, revision, time.Now().UTC())
		if insertErr != nil {
			return 0, false, insertErr
		}
		id, err = result.LastInsertId()
	} else if err == nil {
		switch status {
		case "succeeded":
			if err := tx.Commit(); err != nil {
				return 0, false, err
			}
			return id, true, nil
		case "failed":
			_, err = tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status='running',started_at=?,finished_at=NULL,error_code='' WHERE id=?`, time.Now().UTC(), id)
			if err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE gw_migration_issues SET status='resolved' WHERE run_id=? AND status='open'`, id)
			}
		case "running":
			return 0, false, fmt.Errorf("legacy AI file import is already running")
		default:
			return 0, false, fmt.Errorf("legacy AI file import run has invalid status %q", status)
		}
	}
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, false, nil
}

func importLegacyAIFileRow(ctx context.Context, db *sql.DB, hmacKey []byte, runID int64, row legacyAIFileRow) (string, error) {
	rowDigest := legacyAIFileRowDigest(hmacKey, row)
	if issue, detail := validateLegacyAIFileRow(ctx, db, row); issue != "" {
		return "issue", recordAIFileImportIssue(ctx, db, runID, row.ID, issue, detail)
	}
	var mapID, mediaAssetID int64
	var fileID string
	err := db.QueryRowContext(ctx, `SELECT id,target_id,target_discriminator FROM gw_migration_object_map WHERE source_table='ai_files' AND source_pk=? AND target_type='file_resource'`, row.ID).Scan(&mapID, &mediaAssetID, &fileID)
	if err == nil {
		if fileID != row.ID || mediaAssetID <= 0 {
			return "issue", recordAIFileImportIssue(ctx, db, runID, row.ID, "mapping_conflict", "existing mapping has an invalid target identity")
		}
		var revisionCount int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_migration_source_revisions WHERE object_map_id=? AND source_hmac=?`, mapID, rowDigest).Scan(&revisionCount); err != nil {
			return "", err
		}
		if revisionCount != 1 || verifyImportedAIFile(ctx, db, row, mediaAssetID) != nil {
			return "issue", recordAIFileImportIssue(ctx, db, runID, row.ID, "source_or_target_conflict", "existing file mapping no longer proves the current source row")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return "", err
		}
		defer tx.Rollback()
		if err := recordMappingProof(ctx, tx, runID, mapID, rowDigest, time.Now().UTC()); err != nil {
			return "", err
		}
		return "skipped", tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	readTx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return "", err
	}
	reader := &legacyAIFileBlobReader{ctx: ctx, tx: readTx, fileID: row.ID, size: row.ContentBytes}
	hash := sha256.New()
	counter := &importByteCounter{}
	storageRoot := strings.Trim(strings.TrimSpace(config.C.FileStorage.UploadPath), "/")
	if storageRoot != "" {
		storageRoot += "/"
	}
	storagePath := storageRoot + "legacy-files/" + strconv.FormatUint(row.TokenID, 10) + "/" + row.ID + "/"
	uploaded, uploadErr := uploadLegacyAIFile(ctx, io.TeeReader(reader, io.MultiWriter(hash, counter)), row.MimeType, storagePath)
	if commitErr := readTx.Commit(); uploadErr == nil {
		uploadErr = commitErr
	}
	if uploadErr != nil {
		if uploaded.URL != "" {
			_ = deleteLegacyAIFile(context.WithoutCancel(ctx), uploaded.URL)
		}
		return "issue", recordAIFileImportIssue(ctx, db, runID, row.ID, "object_upload_failed", "legacy file could not be copied to object storage")
	}
	actualDigest := hex.EncodeToString(hash.Sum(nil))
	if counter.Bytes != row.ContentBytes || !strings.EqualFold(actualDigest, row.ContentSHA256) || (uploaded.Size > 0 && uploaded.Size != row.ContentBytes) {
		_ = deleteLegacyAIFile(context.WithoutCancel(ctx), uploaded.URL)
		return "issue", recordAIFileImportIssue(ctx, db, runID, row.ID, "object_integrity_mismatch", "uploaded object length or SHA-256 differs from the source")
	}
	if err := verifyLegacyAIFile(ctx, uploaded.URL, row.ContentBytes, actualDigest); err != nil {
		_ = deleteLegacyAIFile(context.WithoutCancel(ctx), uploaded.URL)
		return "issue", recordAIFileImportIssue(ctx, db, runID, row.ID, "object_verification_failed", "stored object could not be verified after upload")
	}
	objectCommitted := false
	defer func() {
		if !objectCommitted {
			_ = deleteLegacyAIFile(context.WithoutCancel(ctx), uploaded.URL)
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_file_resources(id,user_id,token_id,filename,purpose,bytes,mime_type,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'processed',?,?)`, row.ID, row.UserID, row.TokenID, row.Filename, row.Purpose, row.Bytes, row.MimeType, row.CreatedAt.UTC(), now); err != nil {
		return "", err
	}
	version := strings.TrimSpace(uploaded.ObjectID)
	if version == "" {
		version = strings.TrimSpace(uploaded.ID)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_media_assets(user_id,token_id,purpose,object_key,storage_locator,object_version,content_type,content_length,sha256,state,state_version,created_at,updated_at) VALUES (?,?,'file',?,?,?,?,?,?,'active',1,?,?)`, row.UserID, row.TokenID, uploaded.StorageKey(), uploaded.URL, nullableAIFileImportString(version), row.MimeType, row.Bytes, actualDigest, row.CreatedAt.UTC(), now)
	if err != nil {
		return "", err
	}
	mediaAssetID, err = result.LastInsertId()
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_media_asset_refs(media_asset_id,user_id,token_id,role,ordinal,ai_file_id,created_at) VALUES (?,?,?,'file',0,?,?)`, mediaAssetID, row.UserID, row.TokenID, row.ID, now); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_media_asset_state_events(media_asset_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,NULL,'active',1,'legacy_file_imported',?)`, mediaAssetID, now); err != nil {
		return "", err
	}
	mapResult, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_object_map(run_id,source_table,source_pk,target_type,target_id,target_discriminator,created_at) VALUES (?,'ai_files',?,'file_resource',?,?,?)`, runID, row.ID, mediaAssetID, row.ID, now)
	if err != nil {
		return "", err
	}
	mapID, err = mapResult.LastInsertId()
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_source_revisions(object_map_id,source_hmac,observed_at) VALUES (?,?,?)`, mapID, rowDigest, now); err != nil {
		return "", err
	}
	if err := recordMappingProof(ctx, tx, runID, mapID, rowDigest, now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	objectCommitted = true
	return "imported", nil
}

func validateLegacyAIFileRow(ctx context.Context, db *sql.DB, row legacyAIFileRow) (string, string) {
	if row.Status == "invalid_source_row" || row.ID == "" || len(row.ID) > 64 {
		return "invalid_source", "required legacy file metadata is missing"
	}
	if row.UserID == 0 || row.TokenID == 0 {
		return "missing_owner", "legacy file owner is missing"
	}
	if row.Bytes <= 0 || row.Bytes != row.ContentBytes || row.Bytes > 64<<20 || len(row.ContentSHA256) != 64 || !isHex(row.ContentSHA256) {
		return "invalid_integrity", "declared size, content size, or SHA-256 is invalid"
	}
	if strings.TrimSpace(row.Filename) == "" || len(row.Filename) > 255 || strings.TrimSpace(row.Purpose) == "" || len(row.Purpose) > 40 || strings.TrimSpace(row.MimeType) == "" || len(row.MimeType) > 120 || strings.ToLower(row.Status) != "processed" || row.CreatedAt.IsZero() {
		return "invalid_metadata", "legacy file metadata cannot be represented by the target contract"
	}
	var owner uint64
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM tokens WHERE id=?`, row.TokenID).Scan(&owner); err != nil || owner != row.UserID {
		return "owner_conflict", "token ownership does not match the legacy file"
	}
	return "", ""
}

func verifyImportedAIFile(ctx context.Context, db *sql.DB, source legacyAIFileRow, mediaAssetID int64) error {
	var userID, tokenID uint64
	var filename, purpose, mimeType, status, digest string
	var bytes int64
	err := db.QueryRowContext(ctx, `SELECT f.user_id,f.token_id,f.filename,f.purpose,f.bytes,f.mime_type,f.status,a.sha256 FROM gw_file_resources f JOIN gw_media_asset_refs r ON r.ai_file_id=f.id AND r.role='file' AND r.ordinal=0 JOIN gw_media_assets a ON a.id=r.media_asset_id WHERE f.id=? AND a.id=? AND a.state='active'`, source.ID, mediaAssetID).Scan(&userID, &tokenID, &filename, &purpose, &bytes, &mimeType, &status, &digest)
	if err != nil {
		return err
	}
	if userID != source.UserID || tokenID != source.TokenID || filename != source.Filename || purpose != source.Purpose || bytes != source.Bytes || !strings.EqualFold(mimeType, source.MimeType) || status != "processed" || !strings.EqualFold(digest, source.ContentSHA256) {
		return errors.New("imported file does not match immutable source fields")
	}
	return nil
}

func legacyAIFileRowDigest(key []byte, row legacyAIFileRow) string {
	encoded, _ := json.Marshal(row)
	digest := security.HMACSHA256(key, encoded)
	return hex.EncodeToString(digest[:])
}

func legacyAIFileSnapshotDigest(key []byte, rows []legacyAIFileRow) string {
	encoded, _ := json.Marshal(rows)
	digest := security.HMACSHA256(key, encoded)
	return hex.EncodeToString(digest[:])
}

func recordAIFileImportIssue(ctx context.Context, db *sql.DB, runID int64, sourcePK, code, detail string) error {
	if sourcePK == "" {
		sourcePK = "*"
	}
	_, err := db.ExecContext(ctx, `INSERT INTO gw_migration_issues(run_id,source_table,source_pk,issue_code,detail,status,created_at) VALUES (?,'ai_files',?,?,?,'open',?)`, runID, sourcePK, code, detail, time.Now().UTC())
	return err
}

func finishAIFileImportRun(ctx context.Context, db *sql.DB, runID int64, status, code string) error {
	result, err := db.ExecContext(ctx, `UPDATE gw_migration_runs SET status=?,finished_at=?,error_code=? WHERE id=? AND status='running'`, status, time.Now().UTC(), code, runID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("legacy AI file import run %d is no longer running", runID)
	}
	return nil
}

func nullableAIFileImportString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

type importByteCounter struct{ Bytes int64 }

func (w *importByteCounter) Write(data []byte) (int, error) {
	w.Bytes += int64(len(data))
	return len(data), nil
}

type legacyAIFileBlobReader struct {
	ctx            context.Context
	tx             *sql.Tx
	fileID         string
	size, position int64
}

func (r *legacyAIFileBlobReader) Read(buffer []byte) (int, error) {
	if r.position >= r.size {
		return 0, io.EOF
	}
	if len(buffer) == 0 {
		return 0, nil
	}
	length := int64(len(buffer))
	if remaining := r.size - r.position; length > remaining {
		length = remaining
	}
	if length > 1<<20 {
		length = 1 << 20
	}
	var chunk []byte
	if err := r.tx.QueryRowContext(r.ctx, `SELECT SUBSTRING(content,?,?) FROM ai_files WHERE id=?`, r.position+1, length, r.fileID).Scan(&chunk); err != nil {
		return 0, err
	}
	if len(chunk) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	count := copy(buffer, chunk)
	r.position += int64(count)
	return count, nil
}
