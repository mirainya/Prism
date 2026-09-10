package migrate

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRecordMappingProofIsReplaySafeAndRevisionBound(t *testing.T) {
	for _, test := range []struct {
		name        string
		inserted    int64
		existing    string
		wantErrPart string
	}{
		{name: "insert", inserted: 1},
		{name: "same revision replay", existing: strings.Repeat("a", 64)},
		{name: "different revision replay", existing: strings.Repeat("b", 64), wantErrPart: "revision conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectBegin()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			revision := strings.Repeat("a", 64)
			mock.ExpectExec("INSERT INTO gw_migration_mapping_proofs").
				WithArgs(int64(7), int64(11), revision, sqlmock.AnyArg(), int64(11), revision, int64(7), int64(11)).
				WillReturnResult(sqlmock.NewResult(1, test.inserted))
			if test.inserted == 0 {
				mock.ExpectQuery("SELECT source_hmac FROM gw_migration_mapping_proofs").
					WithArgs(int64(7), int64(11)).
					WillReturnRows(sqlmock.NewRows([]string{"source_hmac"}).AddRow(test.existing))
			}
			err = recordMappingProof(context.Background(), tx, 7, 11, revision, time.Now().UTC())
			if test.wantErrPart == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErrPart != "" && (err == nil || !strings.Contains(err.Error(), test.wantErrPart)) {
				t.Fatalf("error=%v, want %q", err, test.wantErrPart)
			}
			mock.ExpectRollback()
			_ = tx.Rollback()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImportLegacyRuntimeRequiresHMACKey(t *testing.T) {
	if _, err := ImportLegacyRuntime(context.Background(), (*sql.DB)(nil), ImportOptions{}); !errors.Is(err, ErrImportRequiresKeyring) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBeginRuntimeImportRunRetryResolvesTransientFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT id,status FROM gw_migration_runs.*FOR UPDATE").
		WithArgs(runtimeImportOperation, "revision").
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(31, "failed"))
	mock.ExpectExec("UPDATE gw_migration_runs SET status='running'").
		WithArgs(sqlmock.AnyArg(), int64(31)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_migration_issues SET status='resolved'").
		WithArgs(int64(31)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	id, succeeded, err := beginRuntimeImportRun(context.Background(), tx, "revision")
	if err != nil || succeeded || id != 31 {
		t.Fatalf("id=%d succeeded=%t err=%v", id, succeeded, err)
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeImporterRetainsIssueWriteFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("issue table unavailable")
	mock.ExpectQuery("SELECT EXISTS.*gw_migration_issues").
		WithArgs(int64(7), "tasks", "2", "invalid", "detail").
		WillReturnError(want)
	importer := runtimeImporter{ctx: context.Background(), tx: tx, runID: 7}
	importer.issue(runtimeSourceRow{Table: "tasks", PrimaryKey: "2"}, "invalid", "detail")
	if !errors.Is(importer.issueErr, want) {
		t.Fatalf("issue error=%v", importer.issueErr)
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAsyncStateRequiresTerminalEvidence(t *testing.T) {
	row := runtimeSourceRow{Values: map[string]runtimeValue{
		"status": {Bytes: []byte("completed"), Valid: true},
	}}
	state, terminal, valid := runtimeAsyncState(row, "video_task")
	if !valid || !terminal || state != "succeeded" {
		t.Fatalf("state=%q terminal=%t valid=%t", state, terminal, valid)
	}
	row.Values["status"] = runtimeValue{Bytes: []byte("tracking"), Valid: true}
	state, terminal, valid = runtimeAsyncState(row, "video_task")
	if !valid || terminal || state != "running" {
		t.Fatalf("state=%q terminal=%t valid=%t", state, terminal, valid)
	}
}

func TestRuntimeCallRequiresReservationReconciliation(t *testing.T) {
	for _, test := range []struct {
		status   string
		required bool
	}{
		{status: "completed", required: false},
		{status: "failed", required: false},
		{status: "cancelled", required: false},
		{status: "received", required: true},
		{status: "retry_pending", required: true},
		{status: "in_progress", required: true},
		{status: "indeterminate", required: true},
	} {
		if got := runtimeCallRequiresReservationReconciliation(test.status); got != test.required {
			t.Errorf("status %q: required=%t, want %t", test.status, got, test.required)
		}
	}
}
