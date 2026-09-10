package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/security"
)

var (
	ErrCatalogImportRunning = errors.New("legacy catalog import is already running")
	ErrCatalogImportIssues  = errors.New("legacy catalog import has unresolved issues")
	ErrCatalogSourceChanged = errors.New("legacy catalog source changed after a successful import")
)

type catalogIssue struct {
	Row          runtimeSourceRow
	Code, Detail string
}

type catalogImporter struct {
	ctx      context.Context
	tx       *sql.Tx
	options  ImportOptions
	snapshot catalogSnapshot
	runID    int64
	now      time.Time
	report   ImportReport
	plan     *catalogPlan
}

// ImportLegacyCatalog performs the stopped-service, one-way catalog import.
// The whole source snapshot is authenticated, every source row receives an
// object mapping and proof, and no source table is changed.
func ImportLegacyCatalog(ctx context.Context, db *sql.DB, options ImportOptions) (ImportReport, error) {
	if db == nil || len(options.KEK) != security.KeySize || len(options.HMACKey) != security.KeySize {
		return ImportReport{}, ErrImportRequiresKeyring
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return ImportReport{}, err
	}
	defer conn.Close()
	var locked int
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK('prism_legacy_catalog_import',60)`).Scan(&locked); err != nil {
		return ImportReport{}, fmt.Errorf("acquire catalog import lock: %w", err)
	}
	if locked != 1 {
		return ImportReport{}, ErrCatalogImportRunning
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('prism_legacy_catalog_import')`)
	}()

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return ImportReport{}, err
	}
	snapshot, err := loadCatalogSnapshot(ctx, tx, options.HMACKey)
	if err != nil {
		_ = tx.Rollback()
		return ImportReport{}, err
	}
	report := ImportReport{
		SourceRows: int64(snapshot.Rows), SourceRevisionHMAC: snapshot.HMAC,
		Abilities:        int64(len(snapshot.Tables["gw_abilities"].Rows)),
		GatewayAbilities: int64(len(snapshot.Tables["gw_abilities"].Rows)),
		VideoChannels:    int64(len(snapshot.Tables["video_channels"].Rows)),
		Endpoints:        int64(len(snapshot.Tables["endpoints"].Rows)),
	}
	runID, alreadySucceeded, err := beginCatalogImportRun(ctx, tx, snapshot.HMAC)
	if errors.Is(err, ErrCatalogSourceChanged) {
		_ = tx.Rollback()
		_ = recordCatalogImportFailure(ctx, conn, snapshot.HMAC, "source_revision_conflict", "the legacy catalog changed after a successful import; target identities were not modified")
		return report, ErrCatalogSourceChanged
	}
	if err != nil {
		_ = tx.Rollback()
		return report, err
	}
	report.RunID = runID
	if alreadySucceeded {
		report, err = loadCatalogImportReport(ctx, tx, snapshot, runID, true)
		if err != nil {
			_ = tx.Rollback()
			return report, err
		}
		if err := tx.Commit(); err != nil {
			return report, err
		}
		return report, nil
	}

	importer := &catalogImporter{
		ctx: ctx, tx: tx, options: options, snapshot: snapshot, runID: runID,
		now: time.Now().UTC(), report: report,
	}
	importer.plan = buildCatalogPlan(snapshot)
	if snapshot.Rows == 0 {
		importer.plan.issue(runtimeSourceRow{Table: "*", PrimaryKey: "*"}, "source_catalog_empty", "no legacy catalog rows were found")
	}
	if len(importer.plan.issues) > 0 {
		if err := importer.failWithIssues(); err != nil {
			_ = tx.Rollback()
			return importer.report, err
		}
		if err := tx.Commit(); err != nil {
			return importer.report, err
		}
		return importer.report, fmt.Errorf("%w: run_id=%d open_issues=%d", ErrCatalogImportIssues, runID, importer.report.Issues)
	}
	// Do not remove an existing V1 draft until the complete source plan has
	// passed validation. A failed plan must leave the previously verified
	// target graph available for retry or rollback.
	if empty, table, checkErr := catalogTargetsEmpty(ctx, tx); checkErr != nil {
		_ = tx.Rollback()
		_ = recordCatalogImportFailure(ctx, conn, snapshot.HMAC, "target_probe_failed", checkErr.Error())
		return importer.report, checkErr
	} else if !empty {
		replaced, reason, replaceErr := replaceLegacyGatewayDraft(ctx, tx, options)
		if replaceErr != nil {
			_ = tx.Rollback()
			_ = recordCatalogImportFailure(ctx, conn, snapshot.HMAC, "legacy_draft_replacement_failed", replaceErr.Error())
			return importer.report, replaceErr
		}
		if !replaced {
			if reason == "" {
				reason = "target table " + table + " contains rows not owned by a replaceable legacy import"
			}
			importer.plan.issue(runtimeSourceRow{Table: "*", PrimaryKey: "*"}, "target_catalog_not_replaceable", reason)
			if err := importer.failWithIssues(); err != nil {
				_ = tx.Rollback()
				return importer.report, err
			}
			if err := tx.Commit(); err != nil {
				return importer.report, err
			}
			return importer.report, fmt.Errorf("%w: run_id=%d open_issues=%d", ErrCatalogImportIssues, runID, importer.report.Issues)
		}
	}
	if err := importer.persist(); err != nil {
		_ = tx.Rollback()
		_ = recordCatalogImportFailure(ctx, conn, snapshot.HMAC, "catalog_import_error", err.Error())
		return importer.report, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status='succeeded',finished_at=?,error_code='' WHERE id=? AND status='running'`, importer.now, runID); err != nil {
		_ = tx.Rollback()
		return importer.report, err
	}
	if err := tx.Commit(); err != nil {
		return importer.report, err
	}
	return importer.report, nil
}

func beginCatalogImportRun(ctx context.Context, tx *sql.Tx, revision string) (int64, bool, error) {
	var id int64
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id,status FROM gw_migration_runs WHERE operation=? AND source_revision_hmac=? FOR UPDATE`, catalogImportOperation, revision).Scan(&id, &status)
	if err == nil {
		switch status {
		case "succeeded":
			return id, true, nil
		case "running":
			return 0, false, ErrCatalogImportRunning
		case "failed":
			if _, err := tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status='running',started_at=?,finished_at=NULL,error_code='' WHERE id=? AND status='failed'`, time.Now().UTC(), id); err != nil {
				return 0, false, err
			}
			_, err = tx.ExecContext(ctx, `UPDATE gw_migration_issues SET status='resolved' WHERE run_id=? AND status='open'`, id)
			return id, false, err
		default:
			return 0, false, fmt.Errorf("catalog import run %d has invalid status %q", id, status)
		}
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	var succeeded int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_migration_runs WHERE operation=? AND status='succeeded'`, catalogImportOperation).Scan(&succeeded); err != nil {
		return 0, false, err
	}
	if succeeded > 0 {
		return 0, false, ErrCatalogSourceChanged
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_runs(operation,source_revision_hmac,status,started_at) VALUES (?,?,'running',?)`, catalogImportOperation, revision, time.Now().UTC())
	if err != nil {
		return 0, false, err
	}
	id, err = result.LastInsertId()
	return id, false, err
}

func catalogTargetsEmpty(ctx context.Context, tx *sql.Tx) (bool, string, error) {
	for _, table := range []string{"gateway_channels", "gw_models", "gw_credentials", "gw_catalog_releases"} {
		var count int64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&count); err != nil {
			return false, table, err
		}
		if count != 0 {
			return false, table, nil
		}
	}
	var active sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`).Scan(&active); err != nil {
		return false, "gw_catalog_runtime_state", err
	}
	if active.Valid {
		return false, "gw_catalog_runtime_state", nil
	}
	return true, "", nil
}

func (m *catalogImporter) failWithIssues() error {
	for _, issue := range m.plan.issues {
		if err := insertCatalogIssue(m.ctx, m.tx, m.runID, issue.Row, issue.Code, issue.Detail, m.now); err != nil {
			return err
		}
	}
	m.report.Issues = int64(len(m.plan.issues))
	_, err := m.tx.ExecContext(m.ctx, `UPDATE gw_migration_runs SET status='failed',finished_at=?,error_code='unresolved_migration_issues' WHERE id=? AND status='running'`, m.now, m.runID)
	return err
}

func insertCatalogIssue(ctx context.Context, tx *sql.Tx, runID int64, row runtimeSourceRow, code, detail string, now time.Time) error {
	if row.Table == "" {
		row.Table = "*"
	}
	if row.PrimaryKey == "" {
		row.PrimaryKey = "*"
	}
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_issues(run_id,source_table,source_pk,issue_code,detail,status,created_at) VALUES (?,?,?,?,?,'open',?)`, runID, row.Table, row.PrimaryKey, code, detail, now)
	return err
}

func recordCatalogImportFailure(ctx context.Context, conn *sql.Conn, revision, code, detail string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var runID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM gw_migration_runs WHERE operation=? AND source_revision_hmac=? FOR UPDATE`, catalogImportOperation, revision).Scan(&runID)
	now := time.Now().UTC()
	if err == sql.ErrNoRows {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_migration_runs(operation,source_revision_hmac,status,started_at,finished_at,error_code) VALUES (?,?,'failed',?,?,?)`, catalogImportOperation, revision, now, now, code)
		if insertErr != nil {
			return insertErr
		}
		runID, err = result.LastInsertId()
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status='failed',finished_at=?,error_code=? WHERE id=? AND status<>'succeeded'`, now, code, runID)
	}
	if err != nil {
		return err
	}
	if err := insertCatalogIssue(ctx, tx, runID, runtimeSourceRow{Table: "*", PrimaryKey: "*"}, code, detail, now); err != nil {
		return err
	}
	return tx.Commit()
}

func loadCatalogImportReport(ctx context.Context, tx *sql.Tx, snapshot catalogSnapshot, runID int64, reused bool) (ImportReport, error) {
	report := ImportReport{
		RunID: runID, SourceRows: snapshot.Rows, SourceRevisionHMAC: snapshot.HMAC,
		Skipped: snapshot.Rows, Reused: reused,
		Abilities:        int64(len(snapshot.Tables["gw_abilities"].Rows)),
		GatewayAbilities: int64(len(snapshot.Tables["gw_abilities"].Rows)),
		VideoChannels:    int64(len(snapshot.Tables["video_channels"].Rows)),
		Endpoints:        int64(len(snapshot.Tables["endpoints"].Rows)),
	}
	if err := tx.QueryRowContext(ctx, `SELECT target_id FROM gw_migration_object_map WHERE run_id=? AND source_table=? AND source_pk=? AND target_type='catalog_release'`, runID, catalogSnapshotMappingTable, snapshot.HMAC).Scan(&report.ReleaseID); err != nil {
		return report, err
	}
	counts := []struct {
		query         string
		destination   *int64
		releaseScoped bool
	}{
		{`SELECT COUNT(*) FROM gateway_channels`, &report.Channels, false},
		{`SELECT COUNT(*) FROM gw_credential_pools`, &report.CredentialPools, false},
		{`SELECT COUNT(*) FROM gw_credentials`, &report.Credentials, false},
		{`SELECT COUNT(*) FROM gw_catalog_models WHERE release_id=?`, &report.Models, true},
		{`SELECT COUNT(*) FROM gw_model_operations WHERE release_id=?`, &report.Operations, true},
		{`SELECT COUNT(*) FROM gw_skus WHERE release_id=?`, &report.SKUs, true},
		{`SELECT COUNT(*) FROM gw_products WHERE release_id=?`, &report.Products, true},
		{`SELECT COUNT(*) FROM gw_channel_transports WHERE release_id=?`, &report.Transports, true},
		{`SELECT COUNT(*) FROM gw_offerings WHERE release_id=?`, &report.Offerings, true},
		{`SELECT COUNT(*) FROM gw_routes WHERE release_id=?`, &report.Routes, true},
	}
	for _, count := range counts {
		var err error
		if count.releaseScoped {
			err = tx.QueryRowContext(ctx, count.query, report.ReleaseID).Scan(count.destination)
		} else {
			err = tx.QueryRowContext(ctx, count.query).Scan(count.destination)
		}
		if err != nil {
			return report, err
		}
	}
	return report, nil
}

func catalogSemanticDigest() string { return adapter.SemanticDigest() }
