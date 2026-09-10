package migrate

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/security"
)

// VideoAssetImportReport describes the non-destructive legacy asset import.
// Invalid or unverifiable rows are recorded as migration issues and are never
// converted into guessed target records.
type VideoAssetImportReport struct {
	Imported, Skipped, Issues int64
	RunID                     int64
}

// legacyVideoAssetRow mirrors the legacy table without narrowing its source
// types. In particular, video_assets.id is a VARCHAR(32), not an integer, and
// storage_path/created_at may be NULL in databases created by older releases.
// The validity bits are part of the snapshot digest so a repaired NULL value
// cannot accidentally reuse an older migration result.
type legacyVideoAssetRow struct {
	ID               string
	TokenID          int64
	UserID           int64
	SHA256           string
	SizeBytes        int64
	Kind             string
	ContentType      string
	Status           string
	StoragePath      string
	ExpiresAt        time.Time
	CreatedAt        time.Time
	IDValid          bool
	TokenIDValid     bool
	UserIDValid      bool
	SHA256Valid      bool
	SizeBytesValid   bool
	KindValid        bool
	ContentTypeValid bool
	StatusValid      bool
	StoragePathValid bool
	ExpiresAtValid   bool
	CreatedAtValid   bool
}

type legacyVideoAssetDigestRow struct {
	ID, SHA256, Kind, ContentType, Status, StoragePath string
	TokenID, UserID, SizeBytes                         int64
	ExpiresAt, CreatedAt                               string
	IDValid, TokenIDValid, UserIDValid                 bool
	SHA256Valid, SizeBytesValid, KindValid             bool
	ContentTypeValid, StatusValid, StoragePathValid    bool
	ExpiresAtValid, CreatedAtValid                     bool
}

// ImportLegacyVideoAssets migrates ready legacy video_assets rows whose object
// key and ownership can be proved. The source table is read-only; object bytes
// are not copied by this database operation and must remain available at the
// recorded private object key until the storage verifier completes.
func ImportLegacyVideoAssets(ctx context.Context, db *sql.DB, options ImportOptions) (VideoAssetImportReport, error) {
	if db == nil || len(options.HMACKey) != security.KeySize {
		return VideoAssetImportReport{}, ErrImportRequiresKeyring
	}
	var report VideoAssetImportReport
	rows, err := db.QueryContext(ctx, `SELECT a.id,a.token_id,t.user_id,a.sha256,a.size_bytes,a.kind,a.content_type,a.status,a.storage_path,a.expires_at,a.created_at FROM video_assets a LEFT JOIN tokens t ON t.id=a.token_id ORDER BY a.id`)
	if err != nil {
		return report, err
	}
	var source []legacyVideoAssetRow
	for rows.Next() {
		var id, sha256Value, kind, contentType, status, storagePath sql.NullString
		var tokenID, userID, sizeBytes sql.NullInt64
		var expiresAt, createdAt sql.NullTime
		if err := rows.Scan(&id, &tokenID, &userID, &sha256Value, &sizeBytes, &kind, &contentType, &status, &storagePath, &expiresAt, &createdAt); err != nil {
			rows.Close()
			return report, err
		}
		r := legacyVideoAssetRow{
			ID: id.String, TokenID: tokenID.Int64, UserID: userID.Int64,
			SHA256: sha256Value.String, SizeBytes: sizeBytes.Int64,
			Kind: kind.String, ContentType: contentType.String, Status: status.String,
			StoragePath: storagePath.String, ExpiresAt: expiresAt.Time, CreatedAt: createdAt.Time,
			IDValid: id.Valid, TokenIDValid: tokenID.Valid, UserIDValid: userID.Valid,
			SHA256Valid: sha256Value.Valid, SizeBytesValid: sizeBytes.Valid,
			KindValid: kind.Valid, ContentTypeValid: contentType.Valid, StatusValid: status.Valid,
			StoragePathValid: storagePath.Valid, ExpiresAtValid: expiresAt.Valid, CreatedAtValid: createdAt.Valid,
		}
		source = append(source, r)
	}
	if err := rows.Close(); err != nil {
		return report, err
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	runHash := legacyVideoAssetSnapshotDigest(options.HMACKey, source)
	now := time.Now().UTC()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	var runID int64
	var runStatus string
	err = tx.QueryRowContext(ctx, `SELECT id,status FROM gw_migration_runs WHERE operation='legacy_video_assets_import' AND source_revision_hmac=? FOR UPDATE`, runHash).Scan(&runID, &runStatus)
	if errors.Is(err, sql.ErrNoRows) {
		res, e := tx.ExecContext(ctx, `INSERT INTO gw_migration_runs(operation,source_revision_hmac,status,started_at) VALUES ('legacy_video_assets_import',?,'running',?)`, runHash, now)
		if e != nil {
			return report, e
		}
		runID, err = res.LastInsertId()
		if err != nil {
			return report, err
		}
	} else if err != nil {
		return report, err
	} else {
		if runStatus == "succeeded" {
			return VideoAssetImportReport{RunID: runID, Skipped: int64(len(source))}, nil
		}
		if runStatus == "running" {
			return VideoAssetImportReport{RunID: runID}, fmt.Errorf("video asset import is already running")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status='running',started_at=?,finished_at=NULL,error_code='' WHERE id=?`, now, runID); err != nil {
			return report, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE gw_migration_issues SET status='resolved' WHERE run_id=? AND status='open'`, runID); err != nil {
			return report, err
		}
	}
	report.RunID = runID
	seenObjectKeys := make(map[string]string, len(source))
	for _, r := range source {
		var mappedID int64
		var revisionCount int64
		currentDigest := sourceRowDigest(options.HMACKey, r)
		sourcePK := r.ID
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(m.id),0) FROM gw_migration_object_map m WHERE m.source_table='video_assets' AND m.source_pk=? AND m.target_type='media_asset'`, sourcePK).Scan(&mappedID); err != nil {
			return report, err
		}
		if mappedID > 0 {
			var mappingCount int64
			var targetID int64
			var targetDiscriminator string
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MIN(target_id),0),COALESCE(MIN(target_discriminator),'') FROM gw_migration_object_map WHERE source_table='video_assets' AND source_pk=? AND target_type='media_asset'`, sourcePK).Scan(&mappingCount, &targetID, &targetDiscriminator); err != nil {
				return report, err
			}
			if mappingCount != 1 {
				if err := recordVideoAssetIssue(ctx, tx, runID, sourcePK, "mapping_ambiguous", "source row has multiple media-asset mappings", now); err != nil {
					return report, err
				}
				report.Issues++
				continue
			}
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_migration_source_revisions WHERE object_map_id=? AND source_hmac=?`, mappedID, currentDigest).Scan(&revisionCount); err != nil {
				return report, err
			}
			if revisionCount == 0 {
				if err := recordVideoAssetIssue(ctx, tx, runID, sourcePK, "source_conflict", "source row changed after its target mapping was created", now); err != nil {
					return report, err
				}
				report.Issues++
				continue
			}
			if err := verifyMappedVideoAsset(ctx, tx, targetID, targetDiscriminator, r); err != nil {
				if issueErr := recordVideoAssetIssue(ctx, tx, runID, sourcePK, "target_conflict", err.Error(), now); issueErr != nil {
					return report, issueErr
				}
				report.Issues++
				continue
			}
			if err := recordMappingProof(ctx, tx, runID, mappedID, currentDigest, now); err != nil {
				return report, err
			}
			report.Skipped++
			continue
		}
		issue := ""
		detail := ""
		kind := strings.ToLower(strings.TrimSpace(r.Kind))
		contentType := strings.ToLower(strings.TrimSpace(r.ContentType))
		statusValue := strings.ToLower(strings.TrimSpace(r.Status))
		sha256Value := strings.ToLower(strings.TrimSpace(r.SHA256))
		objectKey := strings.TrimSpace(r.StoragePath)
		if !r.IDValid || strings.TrimSpace(r.ID) == "" || len(r.ID) > 128 {
			issue, detail = "invalid_source_id", "asset id is missing or exceeds the migration key limit"
		} else if !r.TokenIDValid || !r.UserIDValid || r.TokenID == 0 || r.UserID == 0 {
			issue, detail = "missing_owner", "asset or token owner is missing"
		} else if !r.StatusValid || statusValue != "ready" {
			issue, detail = "unsupported_status", "only ready assets can be imported without a storage transfer"
		} else if !r.ExpiresAtValid || !r.ExpiresAt.After(now) {
			issue, detail = "expired_source", "asset retention has already expired"
		} else if !r.SizeBytesValid || r.SizeBytes <= 0 || !r.SHA256Valid || len(sha256Value) != 64 || !isHex(sha256Value) {
			issue, detail = "invalid_integrity", "size or SHA-256 is invalid"
		} else if !r.StoragePathValid || objectKey == "" || len(objectKey) > 512 || isRemoteObjectURL(objectKey) {
			issue, detail = "unverifiable_object", "storage path is empty, remote, or exceeds the target limit"
		} else if !r.KindValid || (kind != "image" && kind != "video" && kind != "audio") {
			issue, detail = "invalid_kind", "asset kind is not supported"
		} else if !r.ContentTypeValid || contentType == "" || len(contentType) > 128 {
			issue, detail = "invalid_content_type", "content type is missing or too long"
		} else if !r.CreatedAtValid || r.CreatedAt.IsZero() {
			issue, detail = "invalid_created_at", "created_at is missing"
		}
		if issue != "" {
			if err := recordVideoAssetIssue(ctx, tx, runID, sourcePK, issue, detail, now); err != nil {
				return report, err
			}
			report.Issues++
			continue
		}
		if previous, exists := seenObjectKeys[objectKey]; exists && previous != r.ID {
			if err := recordVideoAssetIssue(ctx, tx, runID, sourcePK, "duplicate_object_key", "multiple source rows use the same storage path", now); err != nil {
				return report, err
			}
			report.Issues++
			continue
		}
		seenObjectKeys[objectKey] = r.ID
		var existingTargetID int64
		existingErr := tx.QueryRowContext(ctx, `SELECT id FROM gw_media_assets WHERE object_key=? FOR SHARE`, objectKey).Scan(&existingTargetID)
		if existingErr == nil {
			if err := recordVideoAssetIssue(ctx, tx, runID, sourcePK, "target_object_conflict", "storage path is already owned by another media asset", now); err != nil {
				return report, err
			}
			report.Issues++
			continue
		}
		if !errors.Is(existingErr, sql.ErrNoRows) {
			return report, existingErr
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO gw_media_assets(user_id,token_id,purpose,object_key,content_type,content_length,sha256,state,retention_until,created_at,updated_at) VALUES (?,?, 'input',?,?,?,?, 'active',?,?,?)`, r.UserID, r.TokenID, objectKey, contentType, r.SizeBytes, sha256Value, nullableRetentionAt(r.ExpiresAt, now), r.CreatedAt.UTC(), now)
		if err != nil {
			return report, fmt.Errorf("insert media asset %s: %w", sourcePK, err)
		}
		targetID, err := res.LastInsertId()
		if err != nil {
			return report, err
		}
		mapRes, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_object_map(run_id,source_table,source_pk,target_type,target_id,target_discriminator,created_at) VALUES (?,?,?,?,?,?,?)`, runID, "video_assets", sourcePK, "media_asset", targetID, objectKey, now)
		if err != nil {
			return report, err
		}
		mapID, err := mapRes.LastInsertId()
		if err != nil {
			return report, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_source_revisions(object_map_id,source_hmac,observed_at) VALUES (?,?,?)`, mapID, currentDigest, now); err != nil {
			return report, err
		}
		if err := recordMappingProof(ctx, tx, runID, mapID, currentDigest, now); err != nil {
			return report, err
		}
		report.Imported++
	}
	status := "succeeded"
	var runErr error
	if report.Issues > 0 {
		status, runErr = "failed", fmt.Errorf("video asset import found %d unverifiable rows", report.Issues)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status=?,finished_at=?,error_code=? WHERE id=?`, status, now, errorCode(runErr), runID); err != nil {
		return report, err
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	return report, runErr
}

func sourceRowDigest(key []byte, r legacyVideoAssetRow) string {
	canonical := digestVideoAssetRow(r)
	encoded, _ := json.Marshal(canonical)
	d := security.HMACSHA256(key, encoded)
	return hex.EncodeToString(d[:])
}

func legacyVideoAssetSnapshotDigest(key []byte, rows []legacyVideoAssetRow) string {
	canonical := make([]legacyVideoAssetDigestRow, 0, len(rows))
	for _, row := range rows {
		canonical = append(canonical, digestVideoAssetRow(row))
	}
	sort.Slice(canonical, func(i, j int) bool {
		return canonical[i].ID < canonical[j].ID
	})
	encoded, _ := json.Marshal(canonical)
	d := security.HMACSHA256(key, encoded)
	return hex.EncodeToString(d[:])
}

func digestVideoAssetRow(r legacyVideoAssetRow) legacyVideoAssetDigestRow {
	return legacyVideoAssetDigestRow{
		ID: r.ID, SHA256: r.SHA256, Kind: r.Kind, ContentType: r.ContentType,
		Status: r.Status, StoragePath: r.StoragePath, TokenID: r.TokenID,
		UserID: r.UserID, SizeBytes: r.SizeBytes,
		ExpiresAt: formatOptionalTime(r.ExpiresAt, r.ExpiresAtValid),
		CreatedAt: formatOptionalTime(r.CreatedAt, r.CreatedAtValid),
		IDValid:   r.IDValid, TokenIDValid: r.TokenIDValid, UserIDValid: r.UserIDValid,
		SHA256Valid: r.SHA256Valid, SizeBytesValid: r.SizeBytesValid,
		KindValid: r.KindValid, ContentTypeValid: r.ContentTypeValid,
		StatusValid: r.StatusValid, StoragePathValid: r.StoragePathValid,
		ExpiresAtValid: r.ExpiresAtValid, CreatedAtValid: r.CreatedAtValid,
	}
}

func formatOptionalTime(value time.Time, valid bool) string {
	if !valid {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableRetentionAt(t, now time.Time) any {
	if t.IsZero() || !t.After(now) {
		return nil
	}
	return t.UTC()
}

func isHex(v string) bool {
	_, err := hex.DecodeString(v)
	return err == nil
}

func isRemoteObjectURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func recordVideoAssetIssue(ctx context.Context, tx *sql.Tx, runID int64, sourcePK, code, detail string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_issues(run_id,source_table,source_pk,issue_code,detail,status,created_at) VALUES (?,?,?,?,?,'open',?)`, runID, "video_assets", sourcePK, code, detail, now)
	return err
}

func verifyMappedVideoAsset(ctx context.Context, tx *sql.Tx, targetID int64, discriminator string, source legacyVideoAssetRow) error {
	if targetID <= 0 || discriminator != strings.TrimSpace(source.StoragePath) {
		return fmt.Errorf("mapped media asset does not match its source object key")
	}
	var userID, tokenID, contentLength int64
	var purpose, objectKey, contentType, sha256Value string
	if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id,purpose,object_key,content_type,content_length,sha256 FROM gw_media_assets WHERE id=? FOR SHARE`, targetID).Scan(&userID, &tokenID, &purpose, &objectKey, &contentType, &contentLength, &sha256Value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("mapped media asset %d no longer exists", targetID)
		}
		return err
	}
	if userID != source.UserID || tokenID != source.TokenID || purpose != "input" || objectKey != strings.TrimSpace(source.StoragePath) || contentLength != source.SizeBytes || !strings.EqualFold(contentType, strings.TrimSpace(source.ContentType)) || !strings.EqualFold(sha256Value, strings.TrimSpace(source.SHA256)) {
		return fmt.Errorf("mapped media asset %d does not match immutable source fields", targetID)
	}
	return nil
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	return "asset_integrity_unverified"
}
