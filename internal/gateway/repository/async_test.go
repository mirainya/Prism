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
	mock.ExpectQuery("SELECT 1 FROM gw_async_outbox").
		WithArgs(uint64(7), "worker-a", uint64(2), uint64(3), uint64(2), "submit", nil, nil, uint64(9), nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
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
	mock.ExpectQuery("SELECT 1 FROM gw_async_outbox").
		WithArgs(uint64(7), "old-worker", uint64(4), uint64(0), uint64(0), "", nil, nil, nil, nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"1"}))
	if err := store.CompleteAsyncOutbox(context.Background(), tx, item, true, ""); err != ErrConflict {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAssertAsyncOutboxLeaseFencesActionIdentity(t *testing.T) {
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
	item := OutboxItem{ID: 7, AsyncExecutionID: 9, ActionSeq: 3, StateVersion: 2, Action: "submit", LeaseOwner: "worker-a", Attempts: 2}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM gw_async_outbox")).
		WithArgs(uint64(7), "worker-a", uint64(2), uint64(3), uint64(2), "submit", nil, nil, uint64(9), nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	if err := store.AssertAsyncOutboxLease(context.Background(), tx, item); err != nil {
		t.Fatal(err)
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
