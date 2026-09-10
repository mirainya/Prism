package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestSubmitRejectsMissingExecutablePayloadBeforeTransaction(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	if _, err := service.Submit(context.Background(), SubmitInput{}); !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("missing payload accepted: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAttemptOutcomeCannotReleaseOrRetryUnknownExecution(t *testing.T) {
	for _, tc := range []struct {
		attempt execution.AttemptState
		call    execution.CallState
		valid   bool
	}{
		{execution.AttemptCompleted, execution.CallCompleted, true},
		{execution.AttemptFailed, execution.CallRetryPending, true},
		{execution.AttemptNotCreated, execution.CallFailed, true},
		{execution.AttemptTerminatedUnknown, execution.CallIndeterminate, true},
		{execution.AttemptTerminatedUnknown, execution.CallFailed, false},
		{execution.AttemptTerminatedUnknown, execution.CallRetryPending, false},
		{execution.AttemptCompleted, execution.CallFailed, false},
		{execution.AttemptStarted, execution.CallCompleted, false},
	} {
		if got := validAttemptOutcome(tc.attempt, tc.call); got != tc.valid {
			t.Errorf("%s/%s valid=%v", tc.attempt, tc.call, got)
		}
	}
}
