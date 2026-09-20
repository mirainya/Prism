package runtime

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type outboxDispatcherFunc struct{}

func (outboxDispatcherFunc) Dispatch(context.Context, repository.OutboxItem) error { return nil }
func (outboxDispatcherFunc) Recover(context.Context, repository.OutboxItem) error  { return nil }

type failingOutboxDispatcher struct{ err error }

func (d failingOutboxDispatcher) Dispatch(context.Context, repository.OutboxItem) error { return d.err }
func (d failingOutboxDispatcher) Recover(context.Context, repository.OutboxItem) error  { return d.err }

type attemptDispatcherFunc struct{}

func (attemptDispatcherFunc) DispatchAttempt(context.Context, repository.OutboxItem) error {
	return nil
}
func (attemptDispatcherFunc) RecoverAttempt(context.Context, repository.OutboxItem) error {
	return nil
}

func TestOutboxRetryDelayRemainsBoundedWithoutAbandoningWork(t *testing.T) {
	for _, tc := range []struct {
		attempt uint64
		want    time.Duration
	}{{1, time.Second}, {2, 2 * time.Second}, {9, 256 * time.Second}, {10, 5 * time.Minute}, {1000000, 5 * time.Minute}} {
		if got := outboxRetryDelay(time.Second, tc.attempt); got != tc.want {
			t.Errorf("attempt=%d delay=%s want=%s", tc.attempt, got, tc.want)
		}
	}
}

func TestPermanentAsyncDispatchTargetPreservesUnknownRecovery(t *testing.T) {
	for _, test := range []struct {
		action string
		state  string
		ok     bool
	}{
		{"submit", "failed", true},
		{"query", "failed", true},
		{"cancel", "failed", true},
		{"recover", "terminated_unknown", true},
		{"invalid", "", false},
	} {
		state, ok := permanentAsyncDispatchTarget(test.action)
		if string(state) != test.state || ok != test.ok {
			t.Errorf("action=%q state=%q ok=%v, want %q/%v", test.action, state, ok, test.state, test.ok)
		}
	}
}

func TestDisabledReadinessPreventsEveryOutboxClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	gate := NewReadinessGate(false)
	service, err := NewWithReadiness(store, gate.Require)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	assertRejected := func(name string, worked bool, err error) {
		t.Helper()
		if worked || !errors.Is(err, ErrNotReady) {
			t.Fatalf("%s: worked=%v err=%v, want false/ErrNotReady", name, worked, err)
		}
	}

	worked, err := service.ProcessOne(ctx, "async", time.Minute, time.Second, outboxDispatcherFunc{})
	assertRejected("async", worked, err)
	worked, err = service.ProcessAttemptOne(ctx, "response", attemptDispatcherFunc{})
	assertRejected("response", worked, err)
	worked, err = service.ProcessCallbackOne(ctx, "provider-callback", time.Minute)
	assertRejected("provider callback", worked, err)
	worked, err = service.ProcessDeliveryOne(ctx, "delivery", time.Minute, time.Second, deliveryReconcilerFunc(func(context.Context, repository.OutboxItem) error { return nil }))
	assertRejected("delivery", worked, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("a disabled worker reached its claim transaction: %v", err)
	}
}

func TestReadyWorkerClaimDoesNotReadDeploymentPointer(t *testing.T) {
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
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,call_id,attempt_id,async_execution_id").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	worked, err := service.ProcessOne(context.Background(), "worker", time.Minute, time.Second, outboxDispatcherFunc{})
	if err != nil || worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPermanentQueryFailureFinalizesExecutionAndProjection(t *testing.T) {
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

	const (
		outboxID      = uint64(31)
		asyncID       = uint64(9)
		attemptID     = uint64(12)
		callID        = uint64(7)
		reservationID = uint64(14)
		accountID     = uint64(21)
		windowID      = uint64(22)
		userID        = uint64(30)
		tokenID       = uint64(40)
		resourceID    = uint64(70)
		slotID        = uint64(80)
	)
	now := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id,call_id,attempt_id,async_execution_id").
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "attempt_id", "async_execution_id", "action_seq", "action", "state_version", "attempt_count", "available_at", "status", "lease_expires_at"}).
			AddRow(outboxID, nil, nil, asyncID, 4, "query", 3, 0, now, "pending", nil))
	mock.ExpectExec("UPDATE gw_async_outbox SET status='dispatching'").
		WithArgs("worker", sqlmock.AnyArg(), sqlmock.AnyArg(), outboxID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	expectBillingLock := func() {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT call_id,billing_account_id,budget_window_id FROM billing_reservations WHERE id=?")).
			WithArgs(reservationID).
			WillReturnRows(sqlmock.NewRows([]string{"call_id", "billing_account_id", "budget_window_id"}).AddRow(callID, accountID, windowID))
		mock.ExpectQuery("SELECT user_id,currency_code,currency_version,posted_balance,held_amount,credit_limit,status,state_version FROM billing_accounts").
			WithArgs(accountID).
			WillReturnRows(sqlmock.NewRows([]string{"user_id", "currency_code", "currency_version", "posted_balance", "held_amount", "credit_limit", "status", "state_version"}).
				AddRow(userID, "USD", 1, "100", "1", "0", "open", 2))
		mock.ExpectQuery("SELECT token_id,limit_amount,used_amount,held_amount,window_start,window_end FROM token_budget_windows").
			WithArgs(windowID).
			WillReturnRows(sqlmock.NewRows([]string{"token_id", "limit_amount", "used_amount", "held_amount", "window_start", "window_end"}).
				AddRow(tokenID, nil, "0", "1", now.Add(-time.Hour), nil))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id,token_id FROM gw_api_calls WHERE id=? FOR UPDATE")).
			WithArgs(callID).
			WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id"}).AddRow(userID, tokenID))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT user_id FROM tokens WHERE id=?")).
			WithArgs(tokenID).
			WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(userID))
		mock.ExpectQuery("SELECT call_id,billing_account_id,budget_window_id,amount,state,state_version FROM billing_reservations").
			WithArgs(reservationID).
			WillReturnRows(sqlmock.NewRows([]string{"call_id", "billing_account_id", "budget_window_id", "amount", "state", "state_version"}).
				AddRow(callID, accountID, windowID, "1", "unknown_hold", 2))
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT a.call_id,a.id FROM gw_async_executions").
		WithArgs(asyncID).
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "attempt_id"}).AddRow(callID, attemptID))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM billing_reservations WHERE call_id=?")).
		WithArgs(callID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(reservationID))
	expectBillingLock()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status,state_version FROM gw_api_calls WHERE id=? FOR UPDATE")).
		WithArgs(callID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "state_version"}).AddRow("in_progress", 5))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE")).
		WithArgs(attemptID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version"}).AddRow("started", 2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT attempt_id,state,state_version FROM gw_async_executions WHERE id=? FOR UPDATE")).
		WithArgs(asyncID).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_id", "state", "state_version"}).AddRow(attemptID, "running", 3))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state,state_version,action_seq FROM gw_async_executions WHERE id=?")).
		WithArgs(asyncID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version", "action_seq"}).AddRow("running", 3, 4))
	mock.ExpectQuery("SELECT 1 FROM gw_async_outbox").
		WillReturnRows(sqlmock.NewRows([]string{"valid"}).AddRow(1))
	mock.ExpectExec("UPDATE gw_async_outbox SET status='dead_letter'").
		WithArgs("provider_result_host_not_allowed", sqlmock.AnyArg(), outboxID, "worker", uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_async_executions SET state=\\?,state_version=state_version\\+1").
		WithArgs("failed", sqlmock.AnyArg(), asyncID, "running", uint64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT action_seq FROM gw_async_executions WHERE id=?")).
		WithArgs(asyncID).
		WillReturnRows(sqlmock.NewRows([]string{"action_seq"}).AddRow(5))
	mock.ExpectExec("INSERT INTO gw_state_transition_events\\(async_execution_id").
		WithArgs(asyncID, "running", "failed", uint64(4), "provider_result_host_not_allowed", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT r.id,r.call_id,r.user_id,r.token_id,r.public_id,r.resource_kind").
		WithArgs(asyncID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "user_id", "token_id", "public_id", "resource_kind"}).
			AddRow(resourceID, callID, userID, tokenID, "video_public", "video_task"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT status,progress FROM gw_video_tasks WHERE resource_id=? FOR UPDATE")).
		WithArgs(resourceID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "progress"}).AddRow("tracking", 0))
	mock.ExpectExec("UPDATE gw_video_tasks SET status=\\?,progress=\\?").
		WithArgs("failed", uint8(0), nil, sqlmock.AnyArg(), resourceID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_api_call_attempts SET state=\\?,state_version=state_version\\+1").
		WithArgs("failed", sqlmock.AnyArg(), attemptID, "started", uint64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events\\(attempt_id").
		WithArgs(attemptID, "started", "failed", uint64(3), "provider_result_host_not_allowed", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE gw_api_calls SET status=\\?,state_version=state_version\\+1").
		WithArgs("failed", true, attemptID, sqlmock.AnyArg(), callID, "in_progress", uint64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events\\(call_id").
		WithArgs(callID, "in_progress", "failed", uint64(6), "provider_result_host_not_allowed", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state FROM gw_api_call_attempts WHERE id=? FOR UPDATE")).
		WithArgs(attemptID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow("failed"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id,state FROM gw_credential_slots WHERE active_attempt_id=? FOR UPDATE")).
		WithArgs(attemptID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "state"}).AddRow(slotID, "active"))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state,scope FROM gw_credential_slots WHERE id=? FOR UPDATE")).
		WithArgs(slotID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "scope"}).AddRow("active", "task"))
	mock.ExpectExec("UPDATE gw_credential_slots SET state=\\?,state_version=state_version\\+1").
		WithArgs("released", sqlmock.AnyArg(), slotID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT catalog_release_id,sku_id FROM gw_api_calls WHERE id=?")).
		WithArgs(callID).
		WillReturnError(billing.ErrMissingFact)
	expectBillingLock()
	mock.ExpectCommit()

	permanent := &PermanentDispatchError{Code: "provider_result_host_not_allowed"}
	worked, processErr := service.ProcessOne(context.Background(), "worker", time.Minute, time.Second, failingOutboxDispatcher{err: permanent})
	if !worked || !errors.Is(processErr, permanent) {
		t.Fatalf("worked=%v err=%v, want worked permanent failure", worked, processErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
