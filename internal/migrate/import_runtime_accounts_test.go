package migrate

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRuntimeImporterRejectsNegativeUserBalance(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT currency_code,currency_version FROM billing_system_state WHERE id=1 FOR SHARE")).
		WillReturnRows(sqlmock.NewRows([]string{"currency_code", "currency_version"}).AddRow("CREDIT", 1))
	expectNoRuntimeMapping(mock, "users", "7", "account_snapshot")
	expectRuntimeIssue(mock, 41, "users", "7", "negative_account_balance", "negative user balance requires manual reconciliation")

	importer := runtimeImporter{ctx: context.Background(), tx: tx, runID: 41, now: time.Now().UTC()}
	if err := importer.importUserAccountSnapshots([]runtimeSourceRow{runtimeAccountRow("users", "7", map[string]string{
		"id": "7", "balance": "-1", "status": "1", "created_at": "2026-09-06 12:00:00",
	})}); err != nil {
		t.Fatal(err)
	}
	if importer.issueErr != nil {
		t.Fatal(importer.issueErr)
	}
	mock.ExpectCommit()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeImporterRejectsNegativeTokenBalance(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	expectNoRuntimeMapping(mock, "tokens", "9", "account_snapshot")
	expectRuntimeIssue(mock, 42, "tokens", "9", "negative_token_balance", "negative token balance requires manual reconciliation")

	importer := runtimeImporter{
		ctx: context.Background(), tx: tx, runID: 42, now: time.Now().UTC(),
		owners: map[uint64]runtimeOwner{9: {UserID: 7}},
	}
	if err := importer.importTokenBudgetSnapshots([]runtimeSourceRow{runtimeAccountRow("tokens", "9", map[string]string{
		"id": "9", "user_id": "7", "balance": "-1", "total_used": "5", "status": "1", "created_at": "2026-09-06 12:00:00",
	})}); err != nil {
		t.Fatal(err)
	}
	if importer.issueErr != nil {
		t.Fatal(importer.issueErr)
	}
	mock.ExpectCommit()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func runtimeAccountRow(table, primaryKey string, values map[string]string) runtimeSourceRow {
	row := runtimeSourceRow{Table: table, PrimaryKey: primaryKey, Revision: "revision", Values: make(map[string]runtimeValue, len(values))}
	for key, value := range values {
		row.Values[key] = runtimeValue{Bytes: []byte(value), Valid: true}
	}
	return row
}

func expectNoRuntimeMapping(mock sqlmock.Sqlmock, table, primaryKey, targetType string) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,target_id,target_discriminator FROM gw_migration_object_map WHERE source_table=? AND source_pk=? AND target_type=? ORDER BY id FOR UPDATE")).
		WithArgs(table, primaryKey, targetType).
		WillReturnRows(sqlmock.NewRows([]string{"id", "target_id", "target_discriminator"}))
}

func expectRuntimeIssue(mock sqlmock.Sqlmock, runID int64, table, primaryKey, code, detail string) {
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(runID, table, primaryKey, code, detail).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gw_migration_issues(run_id,source_table,source_pk,issue_code,detail,status,created_at) VALUES (?,?,?,?,?,'open',?)")).
		WithArgs(runID, table, primaryKey, code, detail, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
}
