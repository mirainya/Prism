package runtime

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestRecordAsyncResultAcceptsOnlyTerminalSubmitObservations(t *testing.T) {
	status := uint16(http.StatusOK)
	response := repository.RequestLogResult{
		HTTPStatus:        &status,
		ResponseComplete:  true,
		ResponseBytesHMAC: "response-hmac",
	}
	terminal := []struct {
		name   string
		state  execution.AsyncState
		result *repository.BlobInput
	}{
		{name: "succeeded", state: execution.AsyncSucceeded, result: &repository.BlobInput{Plaintext: []byte(`{"schema_version":1}`)}},
		{name: "failed", state: execution.AsyncFailed},
		{name: "cancelled", state: execution.AsyncCancelled},
	}
	for _, test := range terminal {
		t.Run(test.name, func(t *testing.T) {
			dispatcher, mock := testDispatcher(t, nil)
			persistenceErr := errors.New("persistence reached")
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT a.call_id,a.id FROM gw_async_executions").
				WithArgs(uint64(9)).
				WillReturnError(persistenceErr)
			mock.ExpectRollback()

			err := dispatcher.service.RecordAsyncResult(context.Background(), AsyncResultInput{
				Item:      repository.OutboxItem{AsyncExecutionID: 9, Action: "submit"},
				RequestID: 1,
				Response:  response,
				State:     test.state,
				Result:    test.result,
			})
			if !errors.Is(err, persistenceErr) {
				t.Fatalf("RecordAsyncResult() error = %v, want persistence error", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}

	dispatcher, mock := testDispatcher(t, nil)
	err := dispatcher.service.RecordAsyncResult(context.Background(), AsyncResultInput{
		Item:      repository.OutboxItem{AsyncExecutionID: 9, Action: "submit"},
		RequestID: 1,
		Response:  response,
		State:     execution.AsyncAccepted,
	})
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("non-terminal submit error = %v, want invalid input", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestImmediateTerminalAcceptanceEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		from   execution.AsyncState
		target execution.AsyncState
		want   bool
	}{
		{name: "submit succeeded", from: execution.AsyncSubmitting, target: execution.AsyncSucceeded, want: true},
		{name: "submit cancelled", from: execution.AsyncSubmitting, target: execution.AsyncCancelled, want: true},
		{name: "submit failed", from: execution.AsyncSubmitting, target: execution.AsyncFailed},
		{name: "polled success", from: execution.AsyncRunning, target: execution.AsyncSucceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := immediateTerminalProvesAcceptance(test.from, test.target); got != test.want {
				t.Fatalf("immediateTerminalProvesAcceptance(%s, %s) = %t, want %t", test.from, test.target, got, test.want)
			}
		})
	}
}
