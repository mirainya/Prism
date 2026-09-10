package migrate

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDeepAuditReadyForCleanupRequiresCompleteEvidence(t *testing.T) {
	report := DeepAuditReport{MigrationRunCount: 1, SucceededMigrationRuns: 1}
	if !report.ReadyForCleanup() {
		t.Fatal("complete audit should be ready for cleanup")
	}
	report.OpenMigrationIssues = 1
	if report.ReadyForCleanup() {
		t.Fatal("open migration issues must block cleanup")
	}
	report.OpenMigrationIssues = 0
	report.LegacyTablesPresent = []string{"gw_abilities"}
	if !report.ReadyForCleanup() {
		t.Fatal("mapped legacy tables may remain until the cleanup step")
	}
	report.UnmappedLegacyKeys = 1
	if report.ReadyForCleanup() {
		t.Fatal("unmapped legacy rows must block cleanup")
	}
}

func TestDeepAuditReadyForCleanupIgnoresSupersededFailedRun(t *testing.T) {
	report := DeepAuditReport{MigrationRunCount: 2, SucceededMigrationRuns: 1}
	if !report.ReadyForCleanup() {
		t.Fatal("a superseded failed snapshot must not permanently block cleanup")
	}
	report.RunningMigrationRuns = 1
	if report.ReadyForCleanup() {
		t.Fatal("an active migration run must block cleanup")
	}
}

func TestDeepAuditDoesNotApproveIncompleteHistory(t *testing.T) {
	for _, table := range deepAuditHistoryTables {
		t.Run(table, func(t *testing.T) {
			report := DeepAuditReport{MigrationRunCount: 1, SucceededMigrationRuns: 1, UnverifiedLegacyHistory: []string{table}}
			if report.ReadyForCleanup() {
				t.Fatal("unverified history must block cleanup")
			}
		})
	}
	for _, table := range deepAuditMetadataTables {
		t.Run(table, func(t *testing.T) {
			report := DeepAuditReport{MigrationRunCount: 1, SucceededMigrationRuns: 1, MissingAuditTables: []string{table}}
			if report.ReadyForCleanup() {
				t.Fatal("missing migration evidence must block cleanup")
			}
		})
	}
}

func TestDeepAuditTracksActualLegacyRequestLogTable(t *testing.T) {
	for _, table := range deepAuditHistoryTables {
		if table == "channel_request_logs" {
			return
		}
	}
	t.Fatal("channel_request_logs must be audited as legacy history")
}

func TestAuditVideoAssetHistoryRejectsUnmappedSource(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*FROM video_assets").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	ok, err := auditVideoAssetHistory(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("unmapped source row must not be verified")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditVideoAssetHistoryRejectsDuplicateMap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*FROM video_assets").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*duplicates").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	ok, err := auditVideoAssetHistory(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("duplicate source mappings must not be verified")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditVideoAssetHistoryAcceptsCompleteMapping(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*FROM video_assets").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*duplicates").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*gw_migration_object_map").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	ok, err := auditVideoAssetHistory(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("complete mapping should be verified")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditAIFileHistoryRejectsUnmappedSource(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*FROM ai_files").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	ok, err := auditAIFileHistory(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("unmapped source file must not be verified")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditAIFileHistoryRejectsDuplicateMap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*FROM ai_files").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*duplicates").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	ok, err := auditAIFileHistory(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("duplicate source mappings must not be verified")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditAIFileHistoryAcceptsCompleteMapping(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*FROM ai_files").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*duplicates").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*gw_migration_object_map").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	ok, err := auditAIFileHistory(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("complete file mapping should be verified")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditRuntimeHistoryAcceptsCompleteMapping(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	spec := runtimeHistoryAuditSpecs["api_calls"]
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `api_calls`").
		WithArgs(runtimeImportOperation, "api_calls", "api_call").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM \\(").
		WithArgs(runtimeImportOperation, "api_calls", "api_call").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gw_migration_object_map").
		WithArgs(runtimeImportOperation, "api_calls", "api_call").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	ok, err := auditRuntimeHistory(context.Background(), db, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("complete runtime mapping should be verified")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuditRuntimeHistoryRejectsMissingProjection(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	spec := runtimeHistoryAuditSpecs["video_tasks"]
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `video_tasks`").
		WithArgs(runtimeImportOperation, "video_tasks", "video_task", "video_task").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	ok, err := auditRuntimeHistory(context.Background(), db, spec)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("missing domain projection must not be verified")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
