package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestAPICallTerminalTransitionsExposeReadyConversationOutput(t *testing.T) {
	testCases := []struct {
		name   string
		status model.APICallStatus
		finish func(*APICallService, string, *ConversationProjectionOutputRequest) error
	}{
		{
			name: "completed", status: model.APICallStatusCompleted,
			finish: func(calls *APICallService, callID string, projection *ConversationProjectionOutputRequest) error {
				return calls.CompleteCall(callID, &CompleteCallRequest{
					HTTPStatus: 200, ConversationProjection: projection,
				})
			},
		},
		{
			name: "failed", status: model.APICallStatusFailed,
			finish: func(calls *APICallService, callID string, projection *ConversationProjectionOutputRequest) error {
				return calls.FailCall(callID, &FailCallRequest{
					HTTPStatus: 502, ErrorCode: "upstream_failed", ConversationProjection: projection,
				})
			},
		},
		{
			name: "cancelled", status: model.APICallStatusCancelled,
			finish: func(calls *APICallService, callID string, projection *ConversationProjectionOutputRequest) error {
				return calls.CancelCall(callID, &CancelCallRequest{
					ErrorCode: "client_cancelled", ConversationProjection: projection,
				})
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupAPICallProjectionTerminalTestDB(t)
			calls := NewAPICallService()
			call := startAPICallForTest(t, calls, 41, false, "request-terminal-"+testCase.name, "model-a")
			enableAPICallConversationProjection(t, db, call.ID)
			if err := calls.MarkCallRunning(call.ID); err != nil {
				t.Fatal(err)
			}
			stageAPICallProjectionInput(t, call.ID)

			observation := observeAPICallTerminalProjection(t, db, call.ID)
			projection := &ConversationProjectionOutputRequest{
				OutputItems: []canonical.Item{{
					Type: "message", Role: canonical.RoleAssistant,
					Content: []canonical.Content{{Type: "output_text", Text: "output-" + testCase.name}},
				}},
				RequestLogID: 77, ProviderResponseID: "provider-" + testCase.name, FinishReason: "stop",
			}
			if err := testCase.finish(calls, call.ID, projection); err != nil {
				t.Fatal(err)
			}

			entry := observation.require(t, testCase.status)
			if !entry.OutputReady || entry.RequestLogID != 77 || entry.ProviderResponseID != "provider-"+testCase.name || entry.FinishReason != "stop" {
				t.Fatalf("terminal projection = %#v", entry)
			}
			if !strings.Contains(string(entry.CanonicalOutput), "output-"+testCase.name) {
				t.Fatalf("terminal canonical output = %s", entry.CanonicalOutput)
			}
		})
	}
}

func TestAPICallTerminalTransitionsRollBackWhenConversationOutputCannotStage(t *testing.T) {
	testCases := []struct {
		name   string
		finish func(*gorm.DB, *APICallService, string, *ConversationProjectionOutputRequest) error
	}{
		{
			name: "completed",
			finish: func(tx *gorm.DB, calls *APICallService, callID string, projection *ConversationProjectionOutputRequest) error {
				return calls.CompleteCallTx(tx, callID, &CompleteCallRequest{ConversationProjection: projection})
			},
		},
		{
			name: "failed",
			finish: func(tx *gorm.DB, calls *APICallService, callID string, projection *ConversationProjectionOutputRequest) error {
				return calls.FailCallTx(tx, callID, &FailCallRequest{ErrorCode: "failed", ConversationProjection: projection})
			},
		},
		{
			name: "cancelled",
			finish: func(tx *gorm.DB, calls *APICallService, callID string, projection *ConversationProjectionOutputRequest) error {
				return calls.CancelCallTx(tx, callID, &CancelCallRequest{ErrorCode: "cancelled", ConversationProjection: projection})
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			db := setupAPICallProjectionTerminalTestDB(t)
			calls := NewAPICallService()
			call := startAPICallForTest(t, calls, 51, false, "request-stage-failure-"+testCase.name, "model-a")
			enableAPICallConversationProjection(t, db, call.ID)
			if err := calls.MarkCallRunning(call.ID); err != nil {
				t.Fatal(err)
			}
			stageAPICallProjectionInput(t, call.ID)

			injectedErr := errors.New("injected conversation projection stage failure")
			callbackName := "test:fail-conversation-output:" + testCase.name
			if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "conversation_projection_outbox" {
					tx.AddError(injectedErr)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })

			err := db.Transaction(func(tx *gorm.DB) error {
				return testCase.finish(tx, calls, call.ID, &ConversationProjectionOutputRequest{
					OutputItems: []canonical.Item{{Type: "message", Role: canonical.RoleAssistant}},
				})
			})
			if !errors.Is(err, injectedErr) {
				t.Fatalf("terminal error = %v, want injected stage failure", err)
			}
			if err := db.Callback().Update().Remove(callbackName); err != nil {
				t.Fatal(err)
			}

			var storedCall model.APICall
			if err := db.First(&storedCall, "id = ?", call.ID).Error; err != nil {
				t.Fatal(err)
			}
			if storedCall.Status != model.APICallStatusInProgress {
				t.Fatalf("call status after rollback = %s", storedCall.Status)
			}
			var entry model.ConversationProjectionOutbox
			if err := db.First(&entry, "call_id = ?", call.ID).Error; err != nil {
				t.Fatal(err)
			}
			if entry.OutputReady {
				t.Fatalf("projection output committed despite rollback: %#v", entry)
			}
		})
	}
}

func TestPublicConversationCallStagesEmptyOutputWhenTerminalRequestOmitsProjection(t *testing.T) {
	db := setupAPICallProjectionTerminalTestDB(t)
	calls := NewAPICallService()
	call, err := calls.StartCall(&StartCallRequest{
		RequestID: "request-public-empty-projection", UserID: 55, TokenID: 66,
		Endpoint: "/v1/chat/completions", Operation: "chat", Model: "model-a",
		ProjectConversation: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := calls.MarkCallRunning(call.ID); err != nil {
		t.Fatal(err)
	}
	stageAPICallProjectionInput(t, call.ID)

	if err := db.Transaction(func(tx *gorm.DB) error {
		return calls.FailCallTx(tx, call.ID, &FailCallRequest{ErrorCode: "pre_execution_failure"})
	}); err != nil {
		t.Fatal(err)
	}
	var entry model.ConversationProjectionOutbox
	if err := db.First(&entry, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !entry.OutputReady || string(entry.CanonicalOutput) != "[]" {
		t.Fatalf("automatic terminal projection = %#v", entry)
	}
}

func TestNonConversationCallRejectsProjectionOutputWithoutStorageQuery(t *testing.T) {
	setupAPICallServiceTestDB(t)
	calls := NewAPICallService()
	call := startAPICallForTest(t, calls, 56, false, "request-non-conversation", "model-a")
	if err := calls.MarkCallRunning(call.ID); err != nil {
		t.Fatal(err)
	}
	err := calls.FailCall(call.ID, &FailCallRequest{
		ErrorCode: "ordinary_failure",
		ConversationProjection: &ConversationProjectionOutputRequest{
			OutputItems: []canonical.Item{{Type: "message", Role: canonical.RoleAssistant}},
		},
	})
	if !errors.Is(err, ErrAPICallInvalidInput) {
		t.Fatalf("non-conversation projection error = %v", err)
	}
	var stored model.APICall
	if err := model.DB().First(&stored, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.APICallStatusInProgress {
		t.Fatalf("non-conversation call = %#v", stored)
	}
}

func TestCompletionIntentRecoveryRestagesConversationOutputAtomically(t *testing.T) {
	db := setupAPICallProjectionTerminalTestDB(t)
	calls := NewAPICallService()
	call := startAPICallForTest(t, calls, 61, false, "request-completion-intent-projection", "model-a")
	enableAPICallConversationProjection(t, db, call.ID)
	if err := calls.MarkCallRunning(call.ID); err != nil {
		t.Fatal(err)
	}
	attempt, err := calls.StartAttempt(&StartAttemptRequest{CallID: call.ID})
	if err != nil {
		t.Fatal(err)
	}
	stageAPICallProjectionInput(t, call.ID)

	injectedErr := errors.New("injected terminal call update failure")
	callbackName := "test:fail-terminal-completion"
	if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if status, ok := apiCallTerminalStatusFromUpdate(tx); ok && status == model.APICallStatusCompleted {
			tx.AddError(injectedErr)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })

	err = calls.CompleteCall(call.ID, &CompleteCallRequest{
		FinalAttemptID: attempt.ID, CompleteStartedAttempt: true, HTTPStatus: 200,
		ProviderResponseID: "provider-recovered",
		ConversationProjection: &ConversationProjectionOutputRequest{
			OutputItems: []canonical.Item{
				{
					Type: "message", Role: canonical.RoleAssistant,
					Content: []canonical.Content{{Type: "output_text", Text: "recovered output"}},
				},
				{
					Type: "function_call", CallID: "tool_recovered", Name: "weather",
					Arguments: []byte(`{"city":`),
				},
			},
			RequestLogID: 81, ProviderResponseID: "provider-recovered", FinishReason: "stop",
		},
	})
	if !errors.Is(err, injectedErr) {
		t.Fatalf("initial completion error = %v, want injected failure", err)
	}
	if err := db.Callback().Update().Remove(callbackName); err != nil {
		t.Fatal(err)
	}

	var pending model.APICall
	if err := db.First(&pending, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if pending.Status != model.APICallStatusInProgress || pending.ErrorCode != callCompletionPendingCode {
		t.Fatalf("pending completion call = %#v", pending)
	}
	var pendingEntry model.ConversationProjectionOutbox
	if err := db.First(&pendingEntry, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if pendingEntry.OutputReady {
		t.Fatalf("rolled-back output remained ready: %#v", pendingEntry)
	}

	claimed, err := claimStaleForegroundCall(context.Background(), call.ID, time.Now().Add(time.Hour))
	if err != nil || !claimed {
		t.Fatalf("recover completion intent: claimed=%v err=%v", claimed, err)
	}
	var completed model.APICall
	if err := db.First(&completed, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.APICallStatusCompleted {
		t.Fatalf("recovered call = %#v", completed)
	}
	var completedAttempt model.APICallAttempt
	if err := db.First(&completedAttempt, attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if completedAttempt.Status != model.APICallAttemptStatusCompleted || completedAttempt.ProviderResponseID != "provider-recovered" {
		t.Fatalf("recovered attempt = %#v", completedAttempt)
	}
	var entry model.ConversationProjectionOutbox
	if err := db.First(&entry, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !entry.OutputReady || entry.RequestLogID != 81 || entry.ProviderResponseID != "provider-recovered" ||
		!strings.Contains(string(entry.CanonicalOutput), "recovered output") || !strings.Contains(string(entry.CanonicalOutput), `city`) {
		t.Fatalf("recovered projection = %#v", entry)
	}
	var output []canonical.Item
	if err := json.Unmarshal(entry.CanonicalOutput, &output); err != nil {
		t.Fatal(err)
	}
	if len(output) != 2 || !json.Valid(output[1].Arguments) || !strings.Contains(string(output[1].Arguments), `city`) {
		t.Fatalf("recovered normalized output = %#v", output)
	}
}

type apiCallTerminalProjectionObservation struct {
	mu     sync.Mutex
	seen   bool
	status model.APICallStatus
	entry  model.ConversationProjectionOutbox
	err    error
}

func observeAPICallTerminalProjection(t *testing.T, db *gorm.DB, callID string) *apiCallTerminalProjectionObservation {
	t.Helper()
	observation := &apiCallTerminalProjectionObservation{}
	callbackName := "test:observe-terminal-projection:" + t.Name()
	if err := db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		status, terminal := apiCallTerminalStatusFromUpdate(tx)
		if !terminal {
			return
		}
		var entry model.ConversationProjectionOutbox
		queryErr := tx.Session(&gorm.Session{NewDB: true}).First(&entry, "call_id = ?", callID).Error
		observation.mu.Lock()
		if !observation.seen {
			observation.seen = true
			observation.status = status
			observation.entry = entry
			observation.err = queryErr
		}
		observation.mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })
	return observation
}

func (o *apiCallTerminalProjectionObservation) require(t *testing.T, status model.APICallStatus) model.ConversationProjectionOutbox {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.seen || o.status != status || o.err != nil {
		t.Fatalf("terminal observation: seen=%v status=%s err=%v", o.seen, o.status, o.err)
	}
	return o.entry
}

func apiCallTerminalStatusFromUpdate(tx *gorm.DB) (model.APICallStatus, bool) {
	if tx == nil || tx.Statement == nil || tx.Statement.Schema == nil || tx.Statement.Schema.Table != "api_calls" {
		return "", false
	}
	updates, ok := tx.Statement.Dest.(map[string]any)
	if !ok {
		return "", false
	}
	status := model.APICallStatus(fmt.Sprint(updates["status"]))
	switch status {
	case model.APICallStatusCompleted, model.APICallStatusFailed, model.APICallStatusCancelled:
		return status, true
	default:
		return "", false
	}
}

func setupAPICallProjectionTerminalTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	setupAPICallServiceTestDB(t)
	db := model.DB()
	if err := db.AutoMigrate(&model.ConversationProjectionOutbox{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func stageAPICallProjectionInput(t *testing.T, callID string) {
	t.Helper()
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: callID,
		InputItems: []canonical.Item{{
			Type: "message", Role: canonical.RoleUser,
			Content: []canonical.Content{{Type: "input_text", Text: "input"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
}

func enableAPICallConversationProjection(t *testing.T, db *gorm.DB, callID string) {
	t.Helper()
	if err := db.Model(&model.APICall{}).Where("id = ?", callID).
		Update("project_conversation", true).Error; err != nil {
		t.Fatal(err)
	}
}
