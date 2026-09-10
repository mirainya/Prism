package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreateAttemptOutboxPinsAttemptVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	tx, _ := db.Begin()
	availableAt := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT a.call_id,a.state,a.state_version,c.current_attempt_id,c.status")).
		WithArgs(uint64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "state", "state_version", "current_attempt_id", "status"}).AddRow(9, "started", 1, 12, "in_progress"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(MAX(action_seq),0) FROM gw_async_outbox WHERE attempt_id=?")).
		WithArgs(uint64(12)).WillReturnRows(sqlmock.NewRows([]string{"sequence"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gw_async_outbox(attempt_id,action_seq,action,status,state_version,available_at,attempt_count,created_at,updated_at) VALUES (?,?,?,'pending',?,?,0,?,?)")).
		WithArgs(uint64(12), uint64(1), "submit", uint64(1), availableAt, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(31, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_api_calls SET next_action_at=?,updated_at=? WHERE id=? AND current_attempt_id=? AND status='in_progress'")).
		WithArgs(availableAt, sqlmock.AnyArg(), uint64(9), uint64(12)).WillReturnResult(sqlmock.NewResult(0, 1))
	item, err := store.CreateAttemptOutbox(context.Background(), tx, AttemptOutboxInput{AttemptID: 12, Action: "submit", ExpectedStateVersion: 1, AvailableAt: availableAt})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != 31 || item.AttemptID != 12 || item.ActionSeq != 1 || item.StateVersion != 1 || item.Action != "submit" {
		t.Fatalf("item=%+v", item)
	}
	mock.ExpectCommit()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimAttemptOutboxClaimsCallInSameTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	mock.ExpectBegin()
	tx, _ := db.Begin()
	availableAt := time.Now().UTC().Add(-time.Second)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT o.id,o.attempt_id,o.action_seq,o.action,o.state_version,o.attempt_count,o.available_at,o.status,o.lease_expires_at,a.call_id")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "attempt_id", "action_seq", "action", "state_version", "attempt_count", "available_at", "status", "lease_expires_at", "call_id"}).AddRow(31, 12, 1, "submit", 1, 0, availableAt, "pending", nil, 9))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_api_calls\nSET lease_owner=?,lease_expires_at=?,next_action_at=NULL,updated_at=?")).
		WithArgs("worker-1", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(9), uint64(12)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT call_id,state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE")).
		WithArgs(uint64(12)).WillReturnRows(sqlmock.NewRows([]string{"call_id", "state", "state_version"}).AddRow(9, "started", 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_async_outbox SET status='dispatching',lease_owner=?,lease_expires_at=?,attempt_count=attempt_count+1,updated_at=?")).
		WithArgs("worker-1", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(31), uint64(12), uint64(1), uint64(1), "submit").
		WillReturnResult(sqlmock.NewResult(0, 1))
	item, err := store.ClaimAttemptOutbox(context.Background(), tx, "worker-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if item.Attempts != 1 || item.MayHaveDispatched || item.CallLease.CallID != 9 || item.CallLease.AttemptID != 12 || !item.CallLease.ExpiresAt.Equal(item.LeaseExpiresAt) {
		t.Fatalf("item=%+v", item)
	}
	mock.ExpectCommit()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClaimAttemptOutboxRollsBackWhenCallLeaseIsBusy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	availableAt := time.Now().UTC().Add(-time.Second)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT o.id,o.attempt_id,o.action_seq,o.action,o.state_version,o.attempt_count,o.available_at,o.status,o.lease_expires_at,a.call_id")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "attempt_id", "action_seq", "action", "state_version", "attempt_count", "available_at", "status", "lease_expires_at", "call_id"}).AddRow(31, 12, 1, "submit", 1, 0, availableAt, "pending", nil, 9))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_api_calls\nSET lease_owner=?,lease_expires_at=?,next_action_at=NULL,updated_at=?")).
		WithArgs("worker-1", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(9), uint64(12)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		_, claimErr := store.ClaimAttemptOutbox(context.Background(), tx, "worker-1", time.Minute)
		return claimErr
	})
	if err != ErrConflict {
		t.Fatalf("err=%v, want conflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAssertAttemptOutboxLeaseRejectsStaleAttemptVersion(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := New(db)
	expiresAt := time.Now().UTC().Add(time.Minute).Truncate(time.Millisecond)
	item := OutboxItem{
		ID: 31, AttemptID: 12, ActionSeq: 1, Action: "submit", StateVersion: 1,
		Attempts: 1, LeaseOwner: "worker-1", LeaseExpiresAt: expiresAt,
		CallLease: CallLease{CallID: 9, AttemptID: 12, Owner: "worker-1", ExpiresAt: expiresAt},
	}
	mock.ExpectBegin()
	tx, _ := db.Begin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_api_calls")).
		WithArgs(uint64(9), uint64(12), "worker-1", expiresAt).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT call_id,state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE")).
		WithArgs(uint64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "state", "state_version"}).AddRow(9, "started", 2))
	if err := store.AssertAttemptOutboxLease(context.Background(), tx, item); !errors.Is(err, ErrConflict) {
		t.Fatalf("err=%v, want conflict", err)
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
