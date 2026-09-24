package runtime

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type callbackHandlerFunc func(context.Context, repository.OutboxItem) error

func (f callbackHandlerFunc) HandleCallback(ctx context.Context, item repository.OutboxItem) error {
	return f(ctx, item)
}

func TestCallbackWorkerRetriesTransientFailureWithoutChangingReceipt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewWithReadiness(store, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,callback_receipt_id,action_seq,action,state_version,attempt_count,available_at,status,lease_expires_at FROM gw_async_outbox").
		WillReturnRows(sqlmock.NewRows([]string{"id", "callback_receipt_id", "action_seq", "action", "state_version", "attempt_count", "available_at", "status", "lease_expires_at"}).
			AddRow(uint64(11), uint64(22), uint64(1), "callback", uint64(1), uint64(0), now, "pending", nil))
	mock.ExpectExec("UPDATE gw_async_outbox SET status='dispatching'").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	asyncExpectation := mock.ExpectQuery(regexp.QuoteMeta("SELECT async_execution_id FROM gw_upstream_callback_receipts WHERE id=?"))
	asyncExpectation.WithArgs(uint64(22)).WillReturnRows(sqlmock.NewRows([]string{"async_execution_id"}).AddRow(uint64(9)))
	stateExpectation := mock.ExpectQuery(regexp.QuoteMeta("SELECT state FROM gw_async_executions WHERE id=? FOR UPDATE"))
	stateExpectation.WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("accepted"))
	mock.ExpectQuery("SELECT 1 FROM gw_async_outbox WHERE id=\\? AND callback_receipt_id=\\?").
		WithArgs(uint64(11), uint64(22), "callback-worker", uint64(1), uint64(1), uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(1))
	receiptExpectation := mock.ExpectQuery(regexp.QuoteMeta("SELECT async_execution_id,status,state_version FROM gw_upstream_callback_receipts WHERE id=? FOR UPDATE"))
	receiptExpectation.WithArgs(uint64(22)).WillReturnRows(sqlmock.NewRows([]string{"async_execution_id", "status", "state_version"}).AddRow(uint64(9), "received", uint64(1)))
	mock.ExpectQuery("SELECT 1 FROM gw_async_outbox[[:space:]]+WHERE id=\\? AND status='dispatching'").
		WithArgs(uint64(11), "callback-worker", uint64(1), uint64(1), uint64(1), "callback", nil, nil, nil, uint64(22), nil).
		WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(1))
	mock.ExpectExec("UPDATE gw_async_outbox SET status='pending'").
		WithArgs("callback_processing_failed", sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(11), "callback-worker", uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	ctx, cancel := context.WithCancel(context.Background())
	handler := callbackHandlerFunc(func(context.Context, repository.OutboxItem) error {
		cancel()
		return errors.New("temporary database outage")
	})
	if err := service.RunCallbackOutboxWithHandler(ctx, "callback-worker", handler, nil); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCallbackTerminalStateExcludesUnresolvedTerminalEvidence(t *testing.T) {
	if !callbackTerminalState("succeeded") || !callbackTerminalState("failed") || !callbackTerminalState("cancelled") {
		t.Fatal("provider terminal states were not recognized")
	}
	if callbackTerminalState("not_created") || callbackTerminalState("terminated_unknown") || callbackTerminalState("running") {
		t.Fatal("non-provider or unresolved states were treated as late terminal callbacks")
	}
}

func TestCallbackObservationApplicableStateAllowsTerminatedUnknownRecovery(t *testing.T) {
	for _, state := range []execution.AsyncState{
		execution.AsyncAccepted,
		execution.AsyncRunning,
		execution.AsyncManualReview,
		execution.AsyncTerminatedUnknown,
	} {
		if !callbackObservationApplicableState(state) {
			t.Fatalf("callback observation rejected recoverable state %q", state)
		}
	}

	for _, state := range []execution.AsyncState{
		execution.AsyncAllocated,
		execution.AsyncSubmitting,
		execution.AsyncSubmissionUnknown,
		execution.AsyncNotCreated,
		execution.AsyncSucceeded,
		execution.AsyncFailed,
		execution.AsyncCancelled,
	} {
		if callbackObservationApplicableState(state) {
			t.Fatalf("callback observation accepted invalid source state %q", state)
		}
	}
}

func TestScheduleCallbackQueryMarksUnknownWithoutTaskIdentityForReview(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	item := repository.OutboxItem{
		ID: 11, CallbackReceiptID: 22, ActionSeq: 1, StateVersion: 3,
		Action: "callback", Attempts: 2, LeaseOwner: "callback-worker",
	}

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state,state_version,action_seq FROM gw_async_executions WHERE id=? FOR UPDATE")).
		WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "action_seq"}).AddRow("terminated_unknown", uint64(4), uint64(7)))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM gw_upstream_task_identities WHERE async_execution_id=? AND status='bound' AND expires_at>CURRENT_TIMESTAMP(3) FOR SHARE")).
		WithArgs(uint64(9)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT encrypted_payload_blob_id FROM gw_upstream_callback_receipts").
		WithArgs(uint64(22), "received").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_payload_blob_id"}).AddRow(nil))
	mock.ExpectExec("UPDATE gw_upstream_callback_receipts SET status=").
		WithArgs("manual_review", uint64(22), "received").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT state_version FROM gw_upstream_callback_receipts").
		WithArgs(uint64(22)).
		WillReturnRows(sqlmock.NewRows([]string{"state_version"}).AddRow(uint64(2)))
	mock.ExpectExec("INSERT INTO gw_state_transition_events\\(callback_receipt_id").
		WithArgs(uint64(22), "received", "manual_review", uint64(2), callbackTaskIdentityUnavailable, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT 1 FROM gw_async_outbox").
		WithArgs(uint64(11), "callback-worker", uint64(2), uint64(1), uint64(3), "callback", nil, nil, nil, uint64(22), nil).
		WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(1))
	mock.ExpectExec("UPDATE gw_async_outbox SET status='dead_letter'").
		WithArgs(callbackTaskIdentityUnavailable, sqlmock.AnyArg(), uint64(11), "callback-worker", uint64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		handled, err := service.scheduleCallbackQuery(context.Background(), tx, item, callbackProcessingRows{
			AsyncID: 9, State: execution.AsyncTerminatedUnknown, ReceiptStatus: "received",
		})
		if err != nil {
			return err
		}
		if !handled {
			t.Fatal("missing task identity was not handled")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
