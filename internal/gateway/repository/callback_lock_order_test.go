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

func TestScheduleCallbackQueryLocksExecutionOutboxReceiptInOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	item := OutboxItem{
		ID: 11, CallbackReceiptID: 22, ActionSeq: 1, StateVersion: 3,
		Action: "callback", Attempts: 2, LeaseOwner: "callback-worker",
	}
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)

	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state,state_version,action_seq FROM gw_async_executions WHERE id=? FOR UPDATE")).
		WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "action_seq"}).AddRow("accepted", uint64(4), uint64(7)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM gw_async_outbox WHERE id=? AND callback_receipt_id=? AND status='dispatching' AND lease_owner=? AND attempt_count=? AND action_seq=? AND state_version=? AND action='callback' AND lease_expires_at>CURRENT_TIMESTAMP(3) FOR UPDATE")).
		WithArgs(uint64(11), uint64(22), "callback-worker", uint64(2), uint64(1), uint64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status,state_version,encrypted_payload_blob_id FROM gw_upstream_callback_receipts WHERE id=? AND async_execution_id=? FOR UPDATE")).
		WithArgs(uint64(22), uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "state_version", "encrypted_payload_blob_id"}).AddRow("received", uint64(3), nil))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_async_executions SET action_seq=action_seq+1,updated_at=? WHERE id=? AND state_version=? AND action_seq=?")).
		WithArgs(sqlmock.AnyArg(), uint64(9), uint64(4), uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gw_async_outbox(async_execution_id,action_seq,action,status,state_version,available_at,attempt_count,created_at,updated_at) VALUES (?,?, 'query','pending',?,?,?,?,?)")).
		WithArgs(uint64(9), uint64(8), uint64(4), at, 0, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(31, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_upstream_callback_receipts SET status='processed',state_version=state_version+1 WHERE id=? AND status='received' AND state_version=?")).
		WithArgs(uint64(22), uint64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.ScheduleCallbackQuery(context.Background(), tx, item, 9, at); err != nil {
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

func TestScheduleCallbackQueryAllowsTerminatedUnknownWithLiveTaskIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	item := OutboxItem{ID: 11, CallbackReceiptID: 22, ActionSeq: 1, StateVersion: 3, Action: "callback", Attempts: 2, LeaseOwner: "callback-worker"}
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state,state_version,action_seq FROM gw_async_executions WHERE id=? FOR UPDATE")).
		WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "action_seq"}).AddRow("terminated_unknown", uint64(4), uint64(7)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_upstream_task_identities WHERE async_execution_id=? AND status='bound' AND expires_at>CURRENT_TIMESTAMP(3) FOR SHARE")).
		WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(uint64(31)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM gw_async_outbox WHERE id=? AND callback_receipt_id=? AND status='dispatching' AND lease_owner=? AND attempt_count=? AND action_seq=? AND state_version=? AND action='callback' AND lease_expires_at>CURRENT_TIMESTAMP(3) FOR UPDATE")).
		WithArgs(uint64(11), uint64(22), "callback-worker", uint64(2), uint64(1), uint64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status,state_version,encrypted_payload_blob_id FROM gw_upstream_callback_receipts WHERE id=? AND async_execution_id=? FOR UPDATE")).
		WithArgs(uint64(22), uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "state_version", "encrypted_payload_blob_id"}).AddRow("received", uint64(3), nil))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_async_executions SET action_seq=action_seq+1,updated_at=? WHERE id=? AND state_version=? AND action_seq=?")).
		WithArgs(sqlmock.AnyArg(), uint64(9), uint64(4), uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO gw_async_outbox(async_execution_id,action_seq,action,status,state_version,available_at,attempt_count,created_at,updated_at) VALUES (?,?, 'query','pending',?,?,?,?,?)")).
		WithArgs(uint64(9), uint64(8), uint64(4), at, 0, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(31, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE gw_upstream_callback_receipts SET status='processed',state_version=state_version+1 WHERE id=? AND status='received' AND state_version=?")).
		WithArgs(uint64(22), uint64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := store.ScheduleCallbackQuery(context.Background(), tx, item, 9, at); err != nil {
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

func TestScheduleCallbackQueryRejectsTerminatedUnknownWithoutLiveTaskIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	item := OutboxItem{ID: 11, CallbackReceiptID: 22, ActionSeq: 1, StateVersion: 3, Action: "callback", Attempts: 2, LeaseOwner: "callback-worker"}
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state,state_version,action_seq FROM gw_async_executions WHERE id=? FOR UPDATE")).
		WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "action_seq"}).AddRow("terminated_unknown", uint64(4), uint64(7)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_upstream_task_identities WHERE async_execution_id=? AND status='bound' AND expires_at>CURRENT_TIMESTAMP(3) FOR SHARE")).
		WithArgs(uint64(9)).
		WillReturnError(sql.ErrNoRows)

	if err := store.ScheduleCallbackQuery(context.Background(), tx, item, 9, time.Now().UTC()); !errors.Is(err, ErrTaskIdentityUnavailable) {
		t.Fatalf("err=%v, want live task identity error", err)
	}
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
