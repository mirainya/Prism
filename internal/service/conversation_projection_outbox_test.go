package service

import (
	"bytes"
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
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestConversationProjectionOutboxStagesLargeCanonicalDataByUniqueCallID(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	call := createConversationTestCall(t, db, "call_outbox_large", 11, 22, decimal.Zero)
	conversation := model.Conversation{UserID: 11, TokenID: 22, Status: 1}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	setConversationProjectionTestCallID(t, db, call, conversation.ID)
	largeData := strings.Repeat("A", 2<<20)

	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: "call_outbox_large", ConversationID: conversation.ID, PreviousResponseID: "previous-1",
		InputItems: []canonical.Item{{
			Type: "message", Role: canonical.RoleUser,
			Content: []canonical.Content{{Type: "input_file", Data: largeData, MediaType: "application/octet-stream"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: "call_outbox_large",
		OutputItems: []canonical.Item{{
			Type: "message", Role: canonical.RoleAssistant,
			Content: []canonical.Content{{Type: "output_text", Text: "first"}},
		}},
		RequestLogID: 19, ProviderResponseID: "response-1", FinishReason: "stop",
	})
	if err != nil || !updated {
		t.Fatalf("stage output: updated=%v err=%v", updated, err)
	}

	var entries []model.ConversationProjectionOutbox
	if err := db.Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("outbox entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if !entry.InputReady || !entry.InputPrepared || !entry.OutputReady || entry.ConversationID != conversation.ID || entry.PreviousResponseID != "previous-1" {
		t.Fatalf("staged entry = %#v", entry)
	}
	if len(entry.CanonicalInput) <= 2<<20 {
		t.Fatalf("canonical input bytes = %d, want more than 2 MiB", len(entry.CanonicalInput))
	}
	if entry.RequestLogID != 19 || entry.ProviderResponseID != "response-1" || entry.FinishReason != "stop" {
		t.Fatalf("output metadata = %#v", entry)
	}

	updated, err = StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: "call_outbox_large",
		OutputItems: []canonical.Item{{
			Type: "message", Role: canonical.RoleAssistant,
			Content: []canonical.Content{{Type: "output_text", Text: "replacement"}},
		}},
	})
	if err != nil || !updated {
		t.Fatalf("replace output: updated=%v err=%v", updated, err)
	}
	if err := db.First(&entry, "call_id = ?", "call_outbox_large").Error; err != nil {
		t.Fatal(err)
	}
	output, err := unmarshalConversationProjectionItems(entry.CanonicalOutput, "output")
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 1 || output[0].Content[0].Text != "replacement" {
		t.Fatalf("replacement output = %#v", output)
	}
}

func TestConversationProjectionItemsMarshalNormalizesMalformedRawWithoutMutatingSource(t *testing.T) {
	items := []canonical.Item{{
		Type:      "function_call",
		Name:      "partial_tool",
		Arguments: json.RawMessage(`{"partial":`),
		Output:    json.RawMessage(`[1,`),
		Extra: map[string]json.RawMessage{
			"valid":   json.RawMessage(`{"ok":true}`),
			"partial": json.RawMessage(`{"nested":`),
			"empty":   json.RawMessage{},
		},
		Content: []canonical.Content{{
			Type: "custom_content",
			Extra: map[string]json.RawMessage{
				"valid":   json.RawMessage(`[1,2]`),
				"partial": json.RawMessage(`{"content":`),
			},
		}},
	}}
	original := canonical.CloneItems(items)
	encoded, err := marshalConversationProjectionItems(items)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := unmarshalConversationProjectionItems(encoded, "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 {
		t.Fatalf("decoded items = %#v", decoded)
	}
	assertProjectionJSONString(t, decoded[0].Arguments, string(original[0].Arguments))
	assertProjectionJSONString(t, decoded[0].Output, string(original[0].Output))
	assertProjectionJSONString(t, decoded[0].Extra["partial"], string(original[0].Extra["partial"]))
	assertProjectionJSONString(t, decoded[0].Extra["empty"], "")
	assertProjectionJSONString(t, decoded[0].Content[0].Extra["partial"], string(original[0].Content[0].Extra["partial"]))
	if string(decoded[0].Extra["valid"]) != string(original[0].Extra["valid"]) ||
		string(decoded[0].Content[0].Extra["valid"]) != string(original[0].Content[0].Extra["valid"]) {
		t.Fatalf("valid raw JSON changed: %#v", decoded[0])
	}
	if !bytes.Equal(items[0].Arguments, original[0].Arguments) ||
		!bytes.Equal(items[0].Output, original[0].Output) ||
		!bytes.Equal(items[0].Extra["partial"], original[0].Extra["partial"]) ||
		!bytes.Equal(items[0].Content[0].Extra["partial"], original[0].Content[0].Extra["partial"]) {
		t.Fatal("marshal mutated the canonical source items")
	}
	originalFingerprint, err := canonicalConversationFingerprint(original[0])
	if err != nil {
		t.Fatal(err)
	}
	decodedFingerprint, err := canonicalConversationFingerprint(decoded[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalFingerprint, decodedFingerprint) {
		t.Fatal("normalized malformed raw JSON changed the conversation fingerprint")
	}
}

func TestConversationProjectionOutboxProjectsTerminalCallAndDeletesIdempotently(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	call := createConversationTestCall(t, db, "call_outbox_project", 31, 41, decimal.RequireFromString("0.25"))
	call.Model = "model-a"
	if err := db.Model(call).Update("model", call.Model).Error; err != nil {
		t.Fatal(err)
	}
	stageCompletedConversationProjection(t, call.ID, "hello", "world")

	service := NewConversationProjectionOutboxService()
	conversationID, err := service.Project(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if conversationID == 0 {
		t.Fatal("conversation id is zero")
	}
	var count int64
	if err := db.Model(&model.ConversationProjectionOutbox{}).Where("call_id = ?", call.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("outbox count = %d, want 0", count)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.ConversationID != conversationID || turn.Status != model.ConversationTurnCompleted {
		t.Fatalf("turn = %#v", turn)
	}

	repeatedID, err := service.Project(call.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedID != conversationID {
		t.Fatalf("repeated conversation id = %d, want %d", repeatedID, conversationID)
	}
}

func TestConversationProjectionOutboxProjectsLegacyUnpreparedExplicitInput(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	baseInput := []canonical.Item{canonicalMessage(canonical.RoleUser, "legacy base", "input_text")}
	baseOutput := []canonical.Item{canonicalMessage(canonical.RoleAssistant, "legacy answer", "output_text")}
	conversationID := projectConversationTestTurn(t, db, "call_outbox_legacy_base", baseInput, baseOutput)
	call := createConversationTestCall(t, db, "call_outbox_legacy_unprepared", 1, 2, decimal.Zero)
	setConversationProjectionTestCallID(t, db, call, conversationID)
	input := append(canonical.CloneItems(baseInput), canonical.CloneItems(baseOutput)...)
	input = append(input, canonicalMessage(canonical.RoleUser, "legacy next", "input_text"))
	encodedInput, err := marshalConversationProjectionItems(input)
	if err != nil {
		t.Fatal(err)
	}
	encodedOutput, err := marshalConversationProjectionItems([]canonical.Item{
		canonicalMessage(canonical.RoleAssistant, "legacy done", "output_text"),
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := &model.ConversationProjectionOutbox{
		CallID: call.ID, ConversationID: conversationID,
		CanonicalInput: encodedInput, CanonicalOutput: encodedOutput,
		InputReady: true, InputPrepared: false, OutputReady: true,
	}
	if err := db.Create(entry).Error; err != nil {
		t.Fatal(err)
	}

	returnedID, err := ProjectPendingAPIConversation(call.ID)
	if err != nil || returnedID != conversationID {
		t.Fatalf("legacy unprepared projection = %d, %v", returnedID, err)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	var items []model.ConversationItem
	if err := db.Where("turn_id = ?", turn.ID).Order("ordinal ASC").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("legacy unprepared turn items = %d, want only delta and output", len(items))
	}
	var storedInput canonical.Item
	if err := json.Unmarshal(items[0].CanonicalJSON, &storedInput); err != nil {
		t.Fatal(err)
	}
	if canonicalConversationItemText(storedInput) != "legacy next" {
		t.Fatalf("legacy unprepared stored input = %#v", storedInput)
	}
}

func TestConversationProjectionOutboxRetainsSanitizedFailureAndRecovers(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	call := createConversationTestCall(t, db, "call_outbox_recover", 51, 61, decimal.Zero)
	call.Model = "model-recover"
	if err := db.Model(call).Update("model", call.Model).Error; err != nil {
		t.Fatal(err)
	}
	stageCompletedConversationProjection(t, call.ID, "recover me", "recovered")

	now := time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)
	service := NewConversationProjectionOutboxService()
	service.now = func() time.Time { return now }
	realProject := service.project
	failed := false
	service.project = func(request ConversationProjectionRequest) (uint, error) {
		if !failed {
			failed = true
			return 0, errors.New("api_key=supersecret https://example.test/file?X-Amz-Signature=private")
		}
		return realProject(request)
	}

	if _, err := service.Project(call.ID); !errors.Is(err, ErrConversationProjectionFailed) {
		t.Fatalf("first projection error = %v", err)
	} else if strings.Contains(err.Error(), "supersecret") || strings.Contains(err.Error(), "private") {
		t.Fatalf("returned error contains secret: %v", err)
	}
	var entry model.ConversationProjectionOutbox
	if err := db.First(&entry, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if entry.RetryCount != 1 || entry.LastAttemptAt == nil || entry.NextAttemptAt == nil {
		t.Fatalf("retry metadata = %#v", entry)
	}
	if strings.Contains(entry.LastError, "supersecret") || strings.Contains(entry.LastError, "private") || !strings.Contains(entry.LastError, "[REDACTED]") {
		t.Fatalf("stored error was not sanitized: %q", entry.LastError)
	}
	if !entry.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("next attempt = %s", entry.NextAttemptAt)
	}

	attempted, err := service.Reconcile(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if attempted != 0 {
		t.Fatalf("attempted before retry time = %d, want 0", attempted)
	}
	now = now.Add(2 * time.Minute)
	attempted, err = service.Reconcile(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if attempted != 1 {
		t.Fatalf("recovered attempts = %d, want 1", attempted)
	}
	if err := db.First(&entry, "call_id = ?", call.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("outbox remains after recovery: %v", err)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
}

func TestConversationProjectionOutboxRequiresExplicitOutputForEveryTerminalStatus(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	completed := createConversationTestCall(t, db, "call_outbox_missing_output", 71, 81, decimal.Zero)
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID:     completed.ID,
		InputItems: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "input"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectPendingAPIConversation(completed.ID); !errors.Is(err, ErrConversationProjectionFailed) {
		t.Fatalf("missing output error = %v", err)
	}
	if updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: completed.ID, OutputItems: []canonical.Item{},
	}); err != nil || !updated {
		t.Fatalf("stage completed output: updated=%v err=%v", updated, err)
	}
	if _, err := ProjectPendingAPIConversation(completed.ID); err != nil {
		t.Fatal(err)
	}

	failed := createConversationTestCall(t, db, "call_outbox_failed", 71, 81, decimal.Zero)
	if err := db.Model(failed).Updates(map[string]any{
		"status": model.APICallStatusFailed, "error_type": "server_error",
		"error_code": "upstream_error", "error_message": "failed safely",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID:     failed.ID,
		InputItems: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_text", Text: "failed input"}}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectPendingAPIConversation(failed.ID); !errors.Is(err, ErrConversationProjectionFailed) {
		t.Fatalf("failed call missing output error = %v", err)
	}
	if updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: failed.ID, OutputItems: []canonical.Item{},
	}); err != nil || !updated {
		t.Fatalf("stage failed output: updated=%v err=%v", updated, err)
	}
	if _, err := ProjectPendingAPIConversation(failed.ID); err != nil {
		t.Fatal(err)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", failed.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status != model.ConversationTurnFailed || turn.ErrorCode != "upstream_error" {
		t.Fatalf("failed turn = %#v", turn)
	}
}

func TestConversationProjectionOutboxReconcileSelectsOnlyTerminalCalls(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	terminal := createConversationTestCall(t, db, "call_outbox_terminal", 91, 92, decimal.Zero)
	running := &model.APICall{
		ID: "call_outbox_running", RequestID: "call_outbox_running",
		UserID: 91, TokenID: 92, Model: "model-a", Status: model.APICallStatusInProgress,
		ProjectConversation: true,
		StartedAt:           time.Now(),
	}
	if err := db.Create(running).Error; err != nil {
		t.Fatal(err)
	}
	stageCompletedConversationProjection(t, terminal.ID, "terminal", "done")
	stageCompletedConversationProjection(t, running.ID, "running", "not yet")

	attempted, err := ReconcilePendingAPIConversations(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if attempted != 1 {
		t.Fatalf("attempted terminal calls = %d, want 1", attempted)
	}
	var runningEntry model.ConversationProjectionOutbox
	if err := db.First(&runningEntry, "call_id = ?", running.ID).Error; err != nil {
		t.Fatal(err)
	}
	if runningEntry.RetryCount != 0 {
		t.Fatalf("running retry count = %d, want 0", runningEntry.RetryCount)
	}
	if _, err := ProjectPendingAPIConversation(running.ID); !errors.Is(err, ErrConversationProjectionNotFinal) {
		t.Fatalf("running projection error = %v", err)
	}
}

func TestConversationProjectionOutboxReconcileWaitsForStaleCallRefund(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	call := createConversationTestCall(t, db, "call_outbox_stale_refund", 93, 94, decimal.Zero)
	if err := db.Model(call).Updates(map[string]any{
		"status":        model.APICallStatusFailed,
		"error_type":    "server_error",
		"error_code":    staleCallPendingCode,
		"error_message": "billing reconciliation is pending",
	}).Error; err != nil {
		t.Fatal(err)
	}
	stageCompletedConversationProjection(t, call.ID, "stale input", "")

	attempted, err := ReconcilePendingAPIConversations(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if attempted != 0 {
		t.Fatalf("attempted stale pending calls = %d, want 0", attempted)
	}
	var pendingCount int64
	if err := db.Model(&model.ConversationProjectionOutbox{}).Where("call_id = ?", call.ID).Count(&pendingCount).Error; err != nil {
		t.Fatal(err)
	}
	if pendingCount != 1 {
		t.Fatalf("pending outbox count = %d, want 1", pendingCount)
	}
	var turnCount int64
	if err := db.Model(&model.ConversationTurn{}).Where("call_id = ?", call.ID).Count(&turnCount).Error; err != nil {
		t.Fatal(err)
	}
	if turnCount != 0 {
		t.Fatalf("stale pending turn count = %d, want 0", turnCount)
	}

	if err := db.Model(call).
		Where("status = ? AND error_code = ?", model.APICallStatusFailed, staleCallPendingCode).
		Updates(map[string]any{
			"error_code":    staleCallFinalCode,
			"error_message": "Execution stopped before a terminal result was persisted",
		}).Error; err != nil {
		t.Fatal(err)
	}
	attempted, err = ReconcilePendingAPIConversations(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if attempted != 1 {
		t.Fatalf("attempted finalized stale calls = %d, want 1", attempted)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status != model.ConversationTurnFailed || turn.ErrorCode != staleCallFinalCode {
		t.Fatalf("finalized stale turn = %#v", turn)
	}
	if err := db.First(&model.ConversationProjectionOutbox{}, "call_id = ?", call.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("outbox after finalized projection error = %v", err)
	}
}

func TestConversationProjectionOutboxTxStagesWithCallAndRollsBackAtomically(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	rollbackErr := errors.New("rollback transaction")
	err := db.Transaction(func(tx *gorm.DB) error {
		call := model.APICall{
			ID: "call_outbox_tx_rollback", RequestID: "call_outbox_tx_rollback",
			UserID: 101, TokenID: 102, Model: "model-a",
			ProjectConversation: true,
			Status:              model.APICallStatusInProgress, StartedAt: time.Now(),
		}
		if err := tx.Create(&call).Error; err != nil {
			return err
		}
		if err := StageAPIConversationProjectionInputTx(tx, ConversationProjectionInputRequest{
			CallID: call.ID, InputItems: []canonical.Item{},
		}); err != nil {
			return err
		}
		if updated, err := StageAPIConversationProjectionOutputIfPresentTx(tx, ConversationProjectionOutputRequest{
			CallID: call.ID, OutputItems: []canonical.Item{},
		}); err != nil || !updated {
			return fmt.Errorf("stage rollback output: updated=%v: %w", updated, err)
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("rollback error = %v", err)
	}
	var count int64
	if err := db.Model(&model.APICall{}).Where("id = ?", "call_outbox_tx_rollback").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rolled back call count=%d err=%v", count, err)
	}
	if err := db.Model(&model.ConversationProjectionOutbox{}).Where("call_id = ?", "call_outbox_tx_rollback").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rolled back outbox count=%d err=%v", count, err)
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		call := model.APICall{
			ID: "call_outbox_tx_commit", RequestID: "call_outbox_tx_commit",
			UserID: 101, TokenID: 102, Model: "model-a",
			ProjectConversation: true,
			Status:              model.APICallStatusInProgress, StartedAt: time.Now(),
		}
		if err := tx.Create(&call).Error; err != nil {
			return err
		}
		if err := StageAPIConversationProjectionInputTx(tx, ConversationProjectionInputRequest{
			CallID: call.ID, InputItems: []canonical.Item{},
		}); err != nil {
			return err
		}
		updated, err := StageAPIConversationProjectionOutputIfPresentTx(tx, ConversationProjectionOutputRequest{
			CallID: call.ID, OutputItems: []canonical.Item{},
		})
		if err != nil || !updated {
			return fmt.Errorf("stage committed output: updated=%v: %w", updated, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var entry model.ConversationProjectionOutbox
	if err := db.First(&entry, "call_id = ?", "call_outbox_tx_commit").Error; err != nil {
		t.Fatal(err)
	}
	if !entry.InputReady || !entry.OutputReady || string(entry.CanonicalInput) != "[]" || string(entry.CanonicalOutput) != "[]" {
		t.Fatalf("committed outbox = %#v", entry)
	}
}

func TestConversationProjectionOutputIfPresentNeverCreatesOutputOnlyEntry(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	call := createConversationTestCall(t, db, "call_outbox_if_present", 111, 112, decimal.Zero)
	request := ConversationProjectionOutputRequest{
		CallID: call.ID,
		OutputItems: []canonical.Item{{
			Type: "message", Role: canonical.RoleAssistant,
			Content: []canonical.Content{{Type: "output_text", Text: "engine output"}},
		}},
		FinishReason: "stop",
	}
	updated, err := StageAPIConversationProjectionOutputIfPresent(request)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("missing outbox was reported as updated")
	}
	var count int64
	if err := db.Model(&model.ConversationProjectionOutbox{}).Where("call_id = ?", call.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("output-only outbox count = %d, want 0", count)
	}

	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: call.ID, InputItems: []canonical.Item{},
	}); err != nil {
		t.Fatal(err)
	}
	updated, err = StageAPIConversationProjectionOutputIfPresent(request)
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("input-ready outbox was not updated")
	}
	var entry model.ConversationProjectionOutbox
	if err := db.First(&entry, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !entry.OutputReady || entry.FinishReason != "stop" {
		t.Fatalf("updated outbox = %#v", entry)
	}
}

func TestConversationProjectionInputRejectsUnmarkedAPICall(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	call := &model.APICall{
		ID: "call_outbox_unmarked", RequestID: "request-outbox-unmarked",
		UserID: 121, TokenID: 122, Model: "model-a",
		Status: model.APICallStatusInProgress, StartedAt: time.Now(),
	}
	if err := db.Create(call).Error; err != nil {
		t.Fatal(err)
	}
	err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: call.ID, InputItems: []canonical.Item{},
	})
	if !errors.Is(err, ErrAPICallInvalidInput) {
		t.Fatalf("stage unmarked call error = %v", err)
	}
	var count int64
	if err := db.Model(&model.ConversationProjectionOutbox{}).Where("call_id = ?", call.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unmarked call created %d outbox rows", count)
	}
}

func TestConversationProjectionInputRejectsMismatchedConversationID(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	call := createConversationTestCall(t, db, "call_outbox_conversation_mismatch", 131, 132, decimal.Zero)
	conversation := &model.Conversation{UserID: 131, TokenID: 132, Model: "model-a", Status: 1}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: call.ID, ConversationID: conversation.ID, InputItems: []canonical.Item{},
	})
	if !errors.Is(err, ErrAPICallInvalidInput) {
		t.Fatalf("stage mismatched conversation error = %v", err)
	}
	var count int64
	if err := db.Model(&model.ConversationProjectionOutbox{}).Where("call_id = ?", call.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("mismatched conversation created %d outbox rows", count)
	}
}

func TestConversationProjectionInputResolvesPreviousResponseBeforeStaging(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	baseInput := canonicalMessage(canonical.RoleUser, "base input", "input_text")
	baseOutput := canonicalMessage(canonical.RoleAssistant, "base output", "output_text")
	conversationID := projectConversationTestTurn(
		t, db, "call_outbox_previous_base", []canonical.Item{baseInput}, []canonical.Item{baseOutput},
	)
	cutoff := time.Now().AddDate(0, 0, -90)
	old := cutoff.AddDate(0, 0, -1)
	if err := db.Model(&model.Conversation{}).Where("id = ?", conversationID).UpdateColumns(map[string]any{
		"provider_response_id": "provider-outbox-previous",
		"updated_at":           old,
	}).Error; err != nil {
		t.Fatal(err)
	}
	pendingCall := &model.APICall{
		ID: "call_outbox_previous_pending", RequestID: "request-outbox-previous-pending",
		UserID: 1, TokenID: 2, Model: "model-a", Status: model.APICallStatusReceived,
		ProjectConversation: true, StartedAt: time.Now(),
	}
	if err := db.Create(pendingCall).Error; err != nil {
		t.Fatal(err)
	}
	newInput := canonicalMessage(canonical.RoleUser, "new input", "input_text")
	stageRequest := ConversationProjectionInputRequest{
		CallID: pendingCall.ID, PreviousResponseID: " provider-outbox-previous ",
		InputItems: []canonical.Item{baseInput, baseOutput, newInput},
	}
	if err := StageAPIConversationProjectionInput(stageRequest); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Conversation{}).Where("id = ?", conversationID).
		UpdateColumn("provider_response_id", "provider-outbox-reassigned").Error; err != nil {
		t.Fatal(err)
	}
	reassigned := model.Conversation{
		UserID: 1, TokenID: 2, Status: 1, ProviderResponseID: "provider-outbox-previous",
	}
	if err := db.Create(&reassigned).Error; err != nil {
		t.Fatal(err)
	}
	if err := StageAPIConversationProjectionInput(stageRequest); err != nil {
		t.Fatalf("repeat previous-response stage after mapping changed: %v", err)
	}

	var stagedCall model.APICall
	if err := db.First(&stagedCall, "id = ?", pendingCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	var entry model.ConversationProjectionOutbox
	if err := db.First(&entry, "call_id = ?", pendingCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stagedCall.ConversationID != conversationID || entry.ConversationID != conversationID || !entry.InputPrepared {
		t.Fatalf("resolved call=%#v entry=%#v conversation=%d", stagedCall, entry, conversationID)
	}
	if entry.PreviousResponseID != "provider-outbox-previous" {
		t.Fatalf("stored previous_response_id = %q", entry.PreviousResponseID)
	}
	input, err := unmarshalConversationProjectionItems(entry.CanonicalInput, "input")
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 1 || canonicalConversationItemText(input[0]) != "new input" {
		t.Fatalf("prepared previous-response input = %#v", input)
	}

	// The cleanup candidate was selected before staging. Its transactional
	// recheck must now observe the direct outbox reference and preserve it.
	deleted, err := deleteExpiredConversationCandidates(db, cutoff, []uint{conversationID}, true)
	if err != nil || deleted != 0 {
		t.Fatalf("deleted resolved previous-response conversation=%d err=%v", deleted, err)
	}
	var conversationCount int64
	if err := db.Model(&model.Conversation{}).Where("id = ?", conversationID).Count(&conversationCount).Error; err != nil || conversationCount != 1 {
		t.Fatalf("resolved conversation count=%d err=%v", conversationCount, err)
	}
}

func TestConversationProjectionInputLeavesUnmatchedPreviousResponseImplicit(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	call := &model.APICall{
		ID: "call_outbox_previous_unmatched", RequestID: "request-outbox-previous-unmatched",
		UserID: 7, TokenID: 8, Model: "model-a", Status: model.APICallStatusReceived,
		ProjectConversation: true, StartedAt: time.Now(),
	}
	if err := db.Create(call).Error; err != nil {
		t.Fatal(err)
	}
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: call.ID, PreviousResponseID: "provider-not-found",
		InputItems: []canonical.Item{canonicalMessage(canonical.RoleUser, "unmatched input", "input_text")},
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.First(call, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	var entry model.ConversationProjectionOutbox
	if err := db.First(&entry, "call_id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if call.ConversationID != 0 || entry.ConversationID != 0 || entry.InputPrepared {
		t.Fatalf("unmatched call=%#v entry=%#v", call, entry)
	}
	input, err := unmarshalConversationProjectionItems(entry.CanonicalInput, "input")
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 1 || canonicalConversationItemText(input[0]) != "unmatched input" {
		t.Fatalf("unmatched previous-response input = %#v", input)
	}
	if err := db.Model(call).Update("status", model.APICallStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	if updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: call.ID,
		OutputItems: []canonical.Item{
			canonicalMessage(canonical.RoleAssistant, "unmatched output", "output_text"),
		},
	}); err != nil || !updated {
		t.Fatalf("stage unmatched output: updated=%v err=%v", updated, err)
	}
	if conversationID, err := ProjectPendingAPIConversation(call.ID); err != nil || conversationID == 0 {
		t.Fatalf("project unmatched previous response: conversation=%d err=%v", conversationID, err)
	}

	ambiguous := []model.Conversation{
		{UserID: 7, TokenID: 8, Status: 1, ProviderResponseID: "provider-ambiguous"},
		{UserID: 7, TokenID: 8, Status: 1, ProviderResponseID: "provider-ambiguous"},
	}
	if err := db.Create(&ambiguous).Error; err != nil {
		t.Fatal(err)
	}
	ambiguousCall := &model.APICall{
		ID: "call_outbox_previous_ambiguous", RequestID: "request-outbox-previous-ambiguous",
		UserID: 7, TokenID: 8, Model: "model-a", Status: model.APICallStatusReceived,
		ProjectConversation: true, StartedAt: time.Now(),
	}
	if err := db.Create(ambiguousCall).Error; err != nil {
		t.Fatal(err)
	}
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: ambiguousCall.ID, PreviousResponseID: "provider-ambiguous",
		InputItems: []canonical.Item{canonicalMessage(canonical.RoleUser, "ambiguous input", "input_text")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.First(ambiguousCall, "id = ?", ambiguousCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	entry = model.ConversationProjectionOutbox{}
	if err := db.First(&entry, "call_id = ?", ambiguousCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ambiguousCall.ConversationID != 0 || entry.ConversationID != 0 || entry.InputPrepared {
		t.Fatalf("ambiguous call=%#v entry=%#v", ambiguousCall, entry)
	}
}

func TestConversationProjectionWaitsForPreviousPublicResponseProjection(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	now := time.Now()
	previousCall := model.APICall{
		ID: "call_outbox_dependency_previous", RequestID: "request-outbox-dependency-previous",
		UserID: 1, TokenID: 2, Model: "model-a", Status: model.APICallStatusCompleted,
		ProjectConversation: true, ResourceType: "response", ResourceID: "resp_dependency_previous",
		StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	continuationCall := model.APICall{
		ID: "call_outbox_dependency_continuation", RequestID: "request-outbox-dependency-continuation",
		UserID: 1, TokenID: 2, Model: "model-a", Status: model.APICallStatusCompleted,
		ProjectConversation: true, ResourceType: "response", ResourceID: "resp_dependency_continuation",
		StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&[]model.APICall{previousCall, continuationCall}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AIResponse{
		ID: "resp_dependency_previous", UserID: 1, TokenID: 2, CallID: previousCall.ID,
		Model: "model-a", Status: "completed", Store: true,
		IdempotencyKey: "internal:resp_dependency_previous", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: previousCall.ID,
		InputItems: []canonical.Item{
			canonicalMessage(canonical.RoleUser, "previous input", "input_text"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: previousCall.ID,
		OutputItems: []canonical.Item{
			canonicalMessage(canonical.RoleAssistant, "previous output", "output_text"),
		},
	}); err != nil || !updated {
		t.Fatalf("stage previous output: updated=%v err=%v", updated, err)
	}
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: continuationCall.ID, PreviousResponseID: "resp_dependency_previous",
		InputItems: []canonical.Item{
			canonicalMessage(canonical.RoleUser, "continuation input", "input_text"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: continuationCall.ID,
		OutputItems: []canonical.Item{
			canonicalMessage(canonical.RoleAssistant, "continuation output", "output_text"),
		},
	}); err != nil || !updated {
		t.Fatalf("stage continuation output: updated=%v err=%v", updated, err)
	}

	if _, err := ProjectPendingAPIConversation(continuationCall.ID); !errors.Is(err, ErrConversationProjectionFailed) ||
		!errors.Is(err, ErrConversationProjectionDependencyPending) {
		t.Fatalf("continuation projected before dependency: %v", err)
	}
	var pending model.ConversationProjectionOutbox
	if err := db.First(&pending, "call_id = ?", continuationCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pending.LastError, ErrConversationProjectionDependencyPending.Error()) {
		t.Fatalf("dependency error = %q", pending.LastError)
	}
	var prematureTurns int64
	if err := db.Model(&model.ConversationTurn{}).Where("call_id = ?", continuationCall.ID).Count(&prematureTurns).Error; err != nil || prematureTurns != 0 {
		t.Fatalf("premature continuation turns=%d err=%v", prematureTurns, err)
	}

	previousConversationID, err := ProjectPendingAPIConversation(previousCall.ID)
	if err != nil {
		t.Fatal(err)
	}
	continuationConversationID, err := ProjectPendingAPIConversation(continuationCall.ID)
	if err != nil {
		t.Fatal(err)
	}
	if continuationConversationID != previousConversationID {
		t.Fatalf("continuation conversation=%d, want %d", continuationConversationID, previousConversationID)
	}
	var remaining int64
	if err := db.Model(&model.ConversationProjectionOutbox{}).Where("call_id IN ?", []string{previousCall.ID, continuationCall.ID}).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("remaining dependency outbox rows=%d err=%v", remaining, err)
	}
}

func TestConversationProjectionOutputIfMissingPreservesRealOutputAcrossInterleavings(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	realOutput := ConversationProjectionOutputRequest{
		OutputItems: []canonical.Item{{
			Type: "message", Role: canonical.RoleAssistant,
			Content: []canonical.Content{{Type: "output_text", Text: "real output"}},
		}},
		FinishReason: "stop",
	}
	for _, fallbackFirst := range []bool{false, true} {
		callID := "call_outbox_output_race_real_first"
		if fallbackFirst {
			callID = "call_outbox_output_race_fallback_first"
		}
		createConversationTestCall(t, db, callID, 121, 122, decimal.Zero)
		if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
			CallID: callID, InputItems: []canonical.Item{},
		}); err != nil {
			t.Fatal(err)
		}
		realOutput.CallID = callID
		if fallbackFirst {
			updated, err := StageAPIConversationProjectionOutputIfMissing(ConversationProjectionOutputRequest{CallID: callID})
			if err != nil || !updated {
				t.Fatalf("stage fallback first: updated=%v err=%v", updated, err)
			}
			if _, err := StageAPIConversationProjectionOutputIfPresent(realOutput); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := StageAPIConversationProjectionOutputIfPresent(realOutput); err != nil {
				t.Fatal(err)
			}
			updated, err := StageAPIConversationProjectionOutputIfMissing(ConversationProjectionOutputRequest{CallID: callID})
			if err != nil || updated {
				t.Fatalf("stage fallback after real output: updated=%v err=%v", updated, err)
			}
		}
		var entry model.ConversationProjectionOutbox
		if err := db.First(&entry, "call_id = ?", callID).Error; err != nil {
			t.Fatal(err)
		}
		output, err := unmarshalConversationProjectionItems(entry.CanonicalOutput, "output")
		if err != nil {
			t.Fatal(err)
		}
		if len(output) != 1 || output[0].Content[0].Text != "real output" || entry.FinishReason != "stop" {
			t.Fatalf("fallbackFirst=%v output=%#v entry=%#v", fallbackFirst, output, entry)
		}
	}
}

func TestConversationProjectionOutputIfMissingDoesNotRaceWithRealOutput(t *testing.T) {
	db := setupConversationProjectionOutboxTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for iteration := 0; iteration < 20; iteration++ {
		callID := fmt.Sprintf("call_outbox_output_concurrent_%d", iteration)
		createConversationTestCall(t, db, callID, 131, 132, decimal.Zero)
		if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
			CallID: callID, InputItems: []canonical.Item{},
		}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errorsChannel := make(chan error, 2)
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			_, stageErr := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
				CallID: callID,
				OutputItems: []canonical.Item{{
					Type: "message", Role: canonical.RoleAssistant,
					Content: []canonical.Content{{Type: "output_text", Text: "real output"}},
				}},
			})
			errorsChannel <- stageErr
		}()
		go func() {
			defer wait.Done()
			<-start
			_, stageErr := StageAPIConversationProjectionOutputIfMissing(ConversationProjectionOutputRequest{CallID: callID})
			errorsChannel <- stageErr
		}()
		close(start)
		wait.Wait()
		close(errorsChannel)
		for stageErr := range errorsChannel {
			if stageErr != nil {
				t.Fatal(stageErr)
			}
		}
		var entry model.ConversationProjectionOutbox
		if err := db.First(&entry, "call_id = ?", callID).Error; err != nil {
			t.Fatal(err)
		}
		output, err := unmarshalConversationProjectionItems(entry.CanonicalOutput, "output")
		if err != nil {
			t.Fatal(err)
		}
		if len(output) != 1 || output[0].Content[0].Text != "real output" {
			t.Fatalf("iteration=%d output=%#v", iteration, output)
		}
	}
}

func setupConversationProjectionOutboxTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupConversationDomainTestDB(t)
	if err := db.AutoMigrate(&model.ConversationProjectionOutbox{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertProjectionJSONString(t *testing.T, raw json.RawMessage, expected string) {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode normalized raw JSON %q: %v", string(raw), err)
	}
	if value != expected {
		t.Fatalf("normalized raw JSON = %q, want %q", value, expected)
	}
}

func stageCompletedConversationProjection(t *testing.T, callID, input, output string) {
	t.Helper()
	if err := StageAPIConversationProjectionInput(ConversationProjectionInputRequest{
		CallID: callID,
		InputItems: []canonical.Item{{
			Type: "message", Role: canonical.RoleUser,
			Content: []canonical.Content{{Type: "input_text", Text: input}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if updated, err := StageAPIConversationProjectionOutputIfPresent(ConversationProjectionOutputRequest{
		CallID: callID,
		OutputItems: []canonical.Item{{
			Type: "message", Role: canonical.RoleAssistant,
			Content: []canonical.Content{{Type: "output_text", Text: output}},
		}},
		FinishReason: "stop",
	}); err != nil || !updated {
		t.Fatalf("stage completed projection: updated=%v err=%v", updated, err)
	}
}
