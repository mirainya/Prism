package runtime

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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
