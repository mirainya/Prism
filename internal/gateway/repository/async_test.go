package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestClaimAsyncOutboxFencesLeaseAndMarksReplayRisk(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	available := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,call_id,attempt_id,async_execution_id,action_seq,action,state_version,attempt_count,available_at,status,lease_expires_at FROM gw_async_outbox")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "attempt_id", "async_execution_id", "action_seq", "action", "state_version", "attempt_count", "available_at", "status", "lease_expires_at"}).AddRow(7, nil, nil, 9, 3, "submit", 2, 1, available, "pending", nil))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_async_outbox SET status='dispatching'")).
		WithArgs("worker-a", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	item, err := store.ClaimAsyncOutbox(ctx, tx, "worker-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if item.Attempts != 2 || !item.MayHaveDispatched || item.LeaseOwner != "worker-a" || item.LeaseExpiresAt.IsZero() {
		t.Fatalf("unexpected lease item: %+v", item)
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_async_outbox SET status=?,last_error_code=?")).
		WithArgs("succeeded", "", sqlmock.AnyArg(), uint64(7), "worker-a", uint64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := store.CompleteAsyncOutbox(ctx, tx, item, true, ""); err != nil {
		t.Fatal(err)
	}
	mock.ExpectCommit()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteAsyncOutboxRejectsStaleLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	item := OutboxItem{ID: 7, LeaseOwner: "old-worker", Attempts: 4}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_async_outbox SET status=?,last_error_code=?")).
		WithArgs("succeeded", "", sqlmock.AnyArg(), uint64(7), "old-worker", uint64(4)).WillReturnResult(sqlmock.NewResult(0, 0))
	if err := store.CompleteAsyncOutbox(context.Background(), tx, item, true, ""); err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
