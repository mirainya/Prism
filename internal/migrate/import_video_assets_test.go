package migrate

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestImportLegacyVideoAssetsEmptySource(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := make([]byte, security.KeySize)
	mock.ExpectQuery("SELECT a.id,a.token_id.*FROM video_assets").WillReturnRows(sqlmock.NewRows([]string{
		"id", "token_id", "user_id", "sha256", "size_bytes", "kind", "content_type", "status", "storage_path", "expires_at", "created_at",
	}))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,status FROM gw_migration_runs.*FOR UPDATE").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
	// An empty row set is treated as a new migration run.
	mock.ExpectExec(`INSERT INTO gw_migration_runs\(operation,source_revision_hmac,status,started_at\)`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectExec("UPDATE gw_migration_runs SET status=.*WHERE id=\\?").WithArgs("succeeded", sqlmock.AnyArg(), "", int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	report, err := ImportLegacyVideoAssets(context.Background(), db, ImportOptions{HMACKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if report.RunID != 11 || report.Imported != 0 || report.Issues != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportLegacyVideoAssetsRequiresKey(t *testing.T) {
	if _, err := ImportLegacyVideoAssets(context.Background(), (*sql.DB)(nil), ImportOptions{}); err == nil {
		t.Fatal("expected keyring error")
	}
}

func TestImportLegacyVideoAssetsSuccessfulSnapshotIsIdempotent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT a.id,a.token_id.*FROM video_assets").WillReturnRows(sqlmock.NewRows([]string{
		"id", "token_id", "user_id", "sha256", "size_bytes", "kind", "content_type", "status", "storage_path", "expires_at", "created_at",
	}))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,status FROM gw_migration_runs.*FOR UPDATE").WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(23, "succeeded"))
	mock.ExpectRollback()
	report, err := ImportLegacyVideoAssets(context.Background(), db, ImportOptions{HMACKey: make([]byte, security.KeySize)})
	if err != nil || report.RunID != 23 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportLegacyVideoAssetsRetryResolvesPriorIssues(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT a.id,a.token_id.*FROM video_assets").WillReturnRows(sqlmock.NewRows([]string{
		"id", "token_id", "user_id", "sha256", "size_bytes", "kind", "content_type", "status", "storage_path", "expires_at", "created_at",
	}))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,status FROM gw_migration_runs.*FOR UPDATE").WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(24, "failed"))
	mock.ExpectExec("UPDATE gw_migration_runs SET status='running'").WithArgs(sqlmock.AnyArg(), int64(24)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_migration_issues SET status='resolved'").WithArgs(int64(24)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE gw_migration_runs SET status=.*WHERE id=\\?").WithArgs("succeeded", sqlmock.AnyArg(), "", int64(24)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	report, err := ImportLegacyVideoAssets(context.Background(), db, ImportOptions{HMACKey: make([]byte, security.KeySize)})
	if err != nil || report.RunID != 24 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportLegacyVideoAssetsUsesStringIDAndCreatesTarget(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := make([]byte, security.KeySize)
	expires := time.Now().UTC().Add(time.Hour)
	created := time.Now().UTC().Add(-time.Minute)
	sha := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	mock.ExpectQuery("SELECT a.id,a.token_id.*FROM video_assets").WillReturnRows(sqlmock.NewRows([]string{
		"id", "token_id", "user_id", "sha256", "size_bytes", "kind", "content_type", "status", "storage_path", "expires_at", "created_at",
	}).AddRow("asset_abc123", int64(7), int64(42), sha, int64(12), "video", "VIDEO/MP4", "READY", "private/video/asset.mp4", expires, created))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,status FROM gw_migration_runs.*FOR UPDATE").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
	mock.ExpectExec(`INSERT INTO gw_migration_runs\(operation,source_revision_hmac,status,started_at\)`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(11, 1))
	mock.ExpectQuery("SELECT COALESCE\\(MIN\\(m.id\\),0\\).*gw_migration_object_map").WithArgs("asset_abc123").WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0))
	mock.ExpectQuery("SELECT id FROM gw_media_assets WHERE object_key=.*FOR SHARE").WithArgs("private/video/asset.mp4").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(`INSERT INTO gw_media_assets\(`).WithArgs(int64(42), int64(7), "private/video/asset.mp4", "video/mp4", int64(12), sha, sqlmock.AnyArg(), created, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(21, 1))
	mock.ExpectExec(`INSERT INTO gw_migration_object_map\(`).WithArgs(int64(11), "video_assets", "asset_abc123", "media_asset", int64(21), "private/video/asset.mp4", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(31, 1))
	mock.ExpectExec(`INSERT INTO gw_migration_source_revisions\(`).WithArgs(int64(31), sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(41, 1))
	mock.ExpectExec(`INSERT INTO gw_migration_mapping_proofs\(`).WithArgs(int64(11), int64(31), sqlmock.AnyArg(), sqlmock.AnyArg(), int64(31), sqlmock.AnyArg(), int64(11), int64(31)).WillReturnResult(sqlmock.NewResult(51, 1))
	mock.ExpectExec("UPDATE gw_migration_runs SET status=.*WHERE id=\\?").WithArgs("succeeded", sqlmock.AnyArg(), "", int64(11)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	report, err := ImportLegacyVideoAssets(context.Background(), db, ImportOptions{HMACKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if report.RunID != 11 || report.Imported != 1 || report.Skipped != 0 || report.Issues != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportLegacyVideoAssetsRecordsObjectConflict(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := make([]byte, security.KeySize)
	expires := time.Now().UTC().Add(time.Hour)
	created := time.Now().UTC().Add(-time.Minute)
	sha := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	mock.ExpectQuery("SELECT a.id,a.token_id.*FROM video_assets").WillReturnRows(sqlmock.NewRows([]string{
		"id", "token_id", "user_id", "sha256", "size_bytes", "kind", "content_type", "status", "storage_path", "expires_at", "created_at",
	}).AddRow("asset_conflict", int64(7), int64(42), sha, int64(12), "video", "video/mp4", "ready", "private/video/existing.mp4", expires, created))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,status FROM gw_migration_runs.*FOR UPDATE").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
	mock.ExpectExec(`INSERT INTO gw_migration_runs\(operation,source_revision_hmac,status,started_at\)`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(12, 1))
	mock.ExpectQuery("SELECT COALESCE\\(MIN\\(m.id\\),0\\).*gw_migration_object_map").WithArgs("asset_conflict").WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0))
	mock.ExpectQuery("SELECT id FROM gw_media_assets WHERE object_key=.*FOR SHARE").WithArgs("private/video/existing.mp4").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(99))
	mock.ExpectExec(`INSERT INTO gw_migration_issues\(`).WithArgs(int64(12), "video_assets", "asset_conflict", "target_object_conflict", sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE gw_migration_runs SET status=.*WHERE id=\\?").WithArgs("failed", sqlmock.AnyArg(), "asset_integrity_unverified", int64(12)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	report, err := ImportLegacyVideoAssets(context.Background(), db, ImportOptions{HMACKey: key})
	if err == nil || report.Issues != 1 || err.Error() != "video asset import found 1 unverifiable rows" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImportLegacyVideoAssetsRejectsNullStoragePathAsIssue(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := make([]byte, security.KeySize)
	expires := time.Now().UTC().Add(time.Hour)
	created := time.Now().UTC().Add(-time.Minute)
	sha := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	mock.ExpectQuery("SELECT a.id,a.token_id.*FROM video_assets").WillReturnRows(sqlmock.NewRows([]string{
		"id", "token_id", "user_id", "sha256", "size_bytes", "kind", "content_type", "status", "storage_path", "expires_at", "created_at",
	}).AddRow("asset_null_path", int64(7), int64(42), sha, int64(12), "video", "video/mp4", "ready", nil, expires, created))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,status FROM gw_migration_runs.*FOR UPDATE").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
	mock.ExpectExec(`INSERT INTO gw_migration_runs\(operation,source_revision_hmac,status,started_at\)`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(13, 1))
	mock.ExpectQuery("SELECT COALESCE\\(MIN\\(m.id\\),0\\).*gw_migration_object_map").WithArgs("asset_null_path").WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(0))
	mock.ExpectExec(`INSERT INTO gw_migration_issues\(`).WithArgs(int64(13), "video_assets", "asset_null_path", "unverifiable_object", sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE gw_migration_runs SET status=.*WHERE id=\\?").WithArgs("failed", sqlmock.AnyArg(), "asset_integrity_unverified", int64(13)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	report, err := ImportLegacyVideoAssets(context.Background(), db, ImportOptions{HMACKey: key})
	if err == nil || report.Issues != 1 || err.Error() != "video asset import found 1 unverifiable rows" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
