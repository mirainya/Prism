package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestUnifiedAsyncOutcomeUsesAttemptNotIntermediateCallState(t *testing.T) {
	for _, tc := range []struct {
		name         string
		attempt      execution.AttemptState
		from, target execution.AsyncState
	}{
		{"succeeded_before_delivery", execution.AttemptCompleted, execution.AsyncAccepted, execution.AsyncSucceeded},
		{"failed_submission", execution.AttemptFailed, execution.AsyncSubmitting, execution.AsyncFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, _ := repository.New(db)
			l := &unifiedLifecycle{store: store, callID: 1, attemptID: 2, asyncID: 3}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT status,state_version FROM gw_api_calls").WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"status", "state_version"}).AddRow("in_progress", 2))
			mock.ExpectQuery("SELECT state,state_version FROM gw_api_call_attempts").WithArgs(uint64(2), uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"state", "state_version"}).AddRow("started", 1))
			mock.ExpectExec("UPDATE gw_api_call_attempts").WithArgs(string(tc.attempt), sqlmock.AnyArg(), uint64(2), "started", uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO gw_state_transition_events").WillReturnResult(sqlmock.NewResult(5, 1))
			mock.ExpectQuery("SELECT state,state_version FROM gw_async_executions").WithArgs(uint64(3)).WillReturnRows(sqlmock.NewRows([]string{"state", "state_version"}).AddRow(string(tc.from), 2))
			mock.ExpectExec("UPDATE gw_async_executions").WithArgs(string(tc.target), sqlmock.AnyArg(), uint64(3), string(tc.from), uint64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectQuery("SELECT action_seq FROM gw_async_executions").WithArgs(uint64(3)).WillReturnRows(sqlmock.NewRows([]string{"action_seq"}).AddRow(3))
			mock.ExpectExec("INSERT INTO gw_state_transition_events").WillReturnResult(sqlmock.NewResult(6, 1))
			mock.ExpectQuery("SELECT state FROM gw_api_call_attempts").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow(string(tc.attempt)))
			mock.ExpectQuery("SELECT id,state FROM gw_credential_slots").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "state"}))
			mock.ExpectExec("UPDATE gw_api_calls SET current_attempt_id=NULL").WithArgs(uint64(1), uint64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			if err := l.finish(context.Background(), tc.attempt, execution.CallInProgress, tc.name); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnifiedTransitionEventFailureIsNotCommitted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	l := &unifiedLifecycle{store: store, callID: 1, attemptID: 2}
	want := errors.New("transition event failed")
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status,state_version FROM gw_api_calls").WillReturnRows(sqlmock.NewRows([]string{"status", "state_version"}).AddRow("in_progress", 2))
	mock.ExpectQuery("SELECT state,state_version FROM gw_api_call_attempts").WillReturnRows(sqlmock.NewRows([]string{"state", "state_version"}).AddRow("started", 1))
	mock.ExpectExec("UPDATE gw_api_call_attempts").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events").WillReturnError(want)
	mock.ExpectRollback()
	if err := l.finish(context.Background(), execution.AttemptCompleted, execution.CallCompleted, "completed"); !errors.Is(err, want) {
		t.Fatalf("transition failure swallowed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUnifiedCallCompletionProjectsResponseInSameTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	lifecycle := &unifiedLifecycle{
		store: store, callID: 1, attemptID: 2,
		resourceID: 3, resourceKind: "response", responseStatus: "incomplete",
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status,state_version FROM gw_api_calls").WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"status", "state_version"}).AddRow("in_progress", 4))
	mock.ExpectQuery("SELECT state,state_version FROM gw_api_call_attempts").WithArgs(uint64(2), uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "state_version"}).AddRow("completed", 2))
	mock.ExpectQuery("SELECT resource_kind FROM gw_api_resources").WithArgs(uint64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"resource_kind"}).AddRow("response"))
	mock.ExpectQuery("SELECT status FROM gw_ai_responses").WithArgs(uint64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("in_progress"))
	mock.ExpectExec("UPDATE gw_ai_responses SET status=").
		WithArgs("incomplete", nil, sqlmock.AnyArg(), uint64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_api_calls SET status=").
		WithArgs("completed", true, uint64(2), sqlmock.AnyArg(), uint64(1), "in_progress", uint64(4)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO gw_state_transition_events").WillReturnResult(sqlmock.NewResult(8, 1))
	mock.ExpectCommit()
	if err := lifecycle.finish(context.Background(), "", execution.CallCompleted, "call_completed"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResponseStatusForCallPreservesProtocolTerminalState(t *testing.T) {
	for _, status := range []string{"completed", "failed", "cancelled", "incomplete"} {
		lifecycle := &unifiedLifecycle{responseStatus: status}
		got, terminal := lifecycle.responseStatusForCall(execution.CallCompleted)
		if !terminal || got != status {
			t.Fatalf("status=%q got=%q terminal=%t", status, got, terminal)
		}
	}
}
