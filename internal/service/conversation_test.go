package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestSaveConversationTurnBindsRequestLog(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	call := createConversationTestCall(t, db, "call_conversation", 1, 2, decimal.RequireFromString("0.125"))
	requestLogID := createConversationTestRequestLog(t, db, call.ID, 9, "openai_chat", 321)

	conversationID, err := SaveConversationTurn(
		&ConversationContext{},
		1,
		2,
		"test-model",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "hello"}},
		chat.ChatMessage{Role: model.RoleAssistant, Content: "world"},
		nil,
		"stop",
		"",
		call.ID,
		requestLogID,
	)
	if err != nil {
		t.Fatalf("SaveConversationTurn failed: %v", err)
	}
	if conversationID == 0 {
		t.Fatal("SaveConversationTurn returned no conversation ID")
	}

	var conversation model.Conversation
	if err := db.First(&conversation, conversationID).Error; err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	if conversation.CallID != call.ID {
		t.Fatalf("conversation call_id = %q, want %q", conversation.CallID, call.ID)
	}
	var turn model.ConversationTurn
	if err := db.Where("conversation_id = ?", conversationID).First(&turn).Error; err != nil {
		t.Fatalf("reload conversation turn: %v", err)
	}
	if turn.Sequence != 1 || turn.CallID != call.ID || turn.RequestLogID != requestLogID ||
		!turn.Cost.Equal(call.FinalCost) || turn.LatencyMs != 321 {
		t.Fatalf("conversation turn = %#v", turn)
	}
	var itemCount int64
	if err := db.Model(&model.ConversationItem{}).Where("conversation_id = ?", conversationID).Count(&itemCount).Error; err != nil || itemCount != 2 {
		t.Fatalf("canonical item count = %d, err = %v", itemCount, err)
	}
}

func TestSaveConversationTurnRollsBackWhenAssociationFails(t *testing.T) {
	db := setupConversationDomainTestDB(t)

	conversationID, err := SaveConversationTurn(
		&ConversationContext{}, 1, 2, "test-model",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "hello"}},
		chat.ChatMessage{Role: model.RoleAssistant, Content: "world"},
		nil, "stop", "", "missing-call", 999,
	)
	if err == nil || conversationID != 0 {
		t.Fatalf("save result: conversation=%d err=%v", conversationID, err)
	}
	var conversations, turns, items int64
	if err := db.Model(&model.Conversation{}).Count(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ConversationTurn{}).Count(&turns).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ConversationItem{}).Count(&items).Error; err != nil {
		t.Fatal(err)
	}
	if conversations != 0 || turns != 0 || items != 0 {
		t.Fatalf("partial conversation persisted: conversations=%d turns=%d items=%d", conversations, turns, items)
	}
}

func TestConversationCanonicalHistoryPreservesToolsMultimodalAndBranches(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	callOne := createConversationTestCall(t, db, "call_canonical_1", 1, 2, decimal.RequireFromString("0.25"))
	refusal := "policy refusal"
	input := []chat.ChatMessage{{
		Role: model.RoleUser,
		Content: []any{
			map[string]any{"type": "text", "text": "inspect"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/image.png", "detail": "high"}},
			map[string]any{"type": "file_url", "file_url": map[string]any{"url": "https://example.test/file.pdf", "content_type": "application/pdf"}},
		},
	}}
	assistant := chat.ChatMessage{
		Role: model.RoleAssistant, Content: nil, Name: "worker", ReasoningContent: "checking",
		Refusal: &refusal, Annotations: json.RawMessage(`[{"type":"citation"}]`), Audio: json.RawMessage(`{"id":"audio_1"}`),
		ToolCalls: []chat.ToolCall{{ID: "tool_1", Type: "function", Function: chat.FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`}}},
	}
	conversationID, err := SaveConversationTurn(&ConversationContext{}, 1, 2, "model-a", input, assistant, nil, "tool_calls", "provider-1", callOne.ID, 0)
	if err != nil {
		t.Fatal(err)
	}

	contextOne, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(contextOne.History) != 2 || len(contextOne.History[1].ToolCalls) != 1 {
		t.Fatalf("canonical history = %#v", contextOne.History)
	}
	parts, ok := contextOne.History[0].Content.([]any)
	if !ok || len(parts) != 3 || contextOne.History[1].Name != "worker" || contextOne.History[1].Refusal == nil ||
		string(contextOne.History[1].Annotations) != `[{"type":"citation"}]` || string(contextOne.History[1].Audio) != `{"id":"audio_1"}` {
		t.Fatalf("canonical fields were not preserved: %#v", contextOne.History)
	}

	callTwo := createConversationTestCall(t, db, "call_canonical_2", 1, 2, decimal.RequireFromString("0.5"))
	toolResult := []chat.ChatMessage{{Role: "tool", ToolCallID: "tool_1", Content: map[string]any{"value": 42}}}
	finalAssistant := chat.ChatMessage{Role: model.RoleAssistant, Content: "done"}
	if _, err := SaveConversationTurn(contextOne, 1, 2, "model-a", toolResult, finalAssistant, nil, "stop", "provider-2", callTwo.ID, 0); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.History) != 4 || loaded.History[2].Role != "tool" || loaded.History[2].ToolCallID != "tool_1" || loaded.History[3].Content != "done" {
		t.Fatalf("tool branch history = %#v", loaded.History)
	}

	branchCall := createConversationTestCall(t, db, "call_canonical_branch", 1, 2, decimal.Zero)
	branchID, err := SaveConversationTurn(&ConversationContext{}, 1, 2, "model-a", input, chat.ChatMessage{Role: model.RoleAssistant, Content: "branch"}, nil, "stop", "", branchCall.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if branchID == conversationID {
		t.Fatal("a new conversation reused a title-matching session")
	}
}

func TestConversationFailureTurnIsRecordedButExcludedFromContext(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	completedCall := createConversationTestCall(t, db, "call_failure_base", 1, 2, decimal.RequireFromString("0.1"))
	conversationID, err := SaveConversationTurn(&ConversationContext{}, 1, 2, "model-a",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "start"}}, chat.ChatMessage{Role: model.RoleAssistant, Content: "ok"},
		nil, "stop", "", completedCall.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	failedCall := createConversationTestCall(t, db, "call_failure_abort", 1, 2, decimal.RequireFromString("0.2"))
	updateConversationTestCallStatus(t, db, failedCall, model.APICallStatusCancelled)
	partial := chat.ChatMessage{Role: model.RoleAssistant, Content: "partial"}
	if _, err := RecordConversationTurnFailure(cc, ConversationTurnRecord{
		UserID: 1, TokenID: 2, Model: "model-a", NewMessages: []chat.ChatMessage{{Role: model.RoleUser, Content: "abort me"}},
		Assistant: &partial, Status: model.ConversationTurnAborted, CallID: failedCall.ID,
		ErrorType: "cancelled", ErrorCode: "client_cancelled", ErrorMessage: "client disconnected",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.History) != 2 {
		t.Fatalf("failed turn leaked into context: %#v", loaded.History)
	}
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", failedCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if turn.Status != model.ConversationTurnAborted || turn.ErrorCode != "client_cancelled" || !turn.Cost.Equal(failedCall.FinalCost) {
		t.Fatalf("failed turn = %#v", turn)
	}
	list, err := NewConversationService().ListConversations(&ListConversationsRequest{UserID: 1})
	if err != nil || len(list.Items) != 1 || !list.Items[0].TotalCost.Equal(decimal.RequireFromString("0.3")) {
		t.Fatalf("turn cost aggregate = %#v, err = %v", list, err)
	}
	if _, err := LoadConversationContextStrict("999999", 2, "model-a"); !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("missing conversation error = %v", err)
	}
}

func TestLoadConversationContextMarksConversationActive(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	conversation := &model.Conversation{UserID: 1, TokenID: 2, Title: "old", Model: "model-a", Status: 1}
	if err := db.Create(conversation).Error; err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -100)
	if err := db.Model(&model.Conversation{}).Where("id = ?", conversation.ID).UpdateColumn("updated_at", old).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConversationContextStrict(fmt.Sprint(conversation.ID), conversation.TokenID, conversation.Model); err != nil {
		t.Fatal(err)
	}
	var current model.Conversation
	if err := db.First(&current, conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !current.UpdatedAt.After(old) {
		t.Fatalf("updated_at = %v, want after %v", current.UpdatedAt, old)
	}
}

func TestConversationTurnSequenceAndCallIdempotency(t *testing.T) {
	db := setupConversationDomainTestDB(t)
	baseCall := createConversationTestCall(t, db, "call_sequence_base", 1, 2, decimal.Zero)
	conversationID, err := SaveConversationTurn(&ConversationContext{}, 1, 2, "model-a",
		[]chat.ChatMessage{{Role: model.RoleUser, Content: "base"}}, chat.ChatMessage{Role: model.RoleAssistant, Content: "ok"}, nil, "stop", "", baseCall.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := LoadConversationContextStrict(fmt.Sprint(conversationID), 2, "model-a")
	if err != nil {
		t.Fatal(err)
	}

	const writers = 4
	calls := make([]*model.APICall, writers)
	for index := 0; index < writers; index++ {
		calls[index] = createConversationTestCall(t, db, fmt.Sprintf("call_sequence_%d", index), 1, 2, decimal.Zero)
	}
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for index := 0; index < writers; index++ {
		wg.Add(1)
		go func(index int, callID string) {
			defer wg.Done()
			_, saveErr := RecordConversationTurn(cc, ConversationTurnRecord{
				UserID: 1, TokenID: 2, Model: "model-a", CallID: callID,
				NewMessages: []chat.ChatMessage{{Role: model.RoleUser, Content: fmt.Sprintf("input-%d", index)}},
				Assistant:   &chat.ChatMessage{Role: model.RoleAssistant, Content: "ok"}, Status: model.ConversationTurnCompleted,
			})
			errs <- saveErr
		}(index, calls[index].ID)
	}
	wg.Wait()
	close(errs)
	for saveErr := range errs {
		if saveErr != nil {
			t.Fatal(saveErr)
		}
	}
	var turns []model.ConversationTurn
	if err := db.Where("conversation_id = ?", conversationID).Order("turn_sequence ASC").Find(&turns).Error; err != nil {
		t.Fatal(err)
	}
	if len(turns) != writers+1 {
		t.Fatalf("turn count = %d", len(turns))
	}
	for index, turn := range turns {
		if turn.Sequence != uint64(index+1) {
			t.Fatalf("turn sequences = %#v", turns)
		}
	}
	duplicate := turns[len(turns)-1]
	returnedID, err := RecordConversationTurn(cc, ConversationTurnRecord{
		UserID: 1, TokenID: 2, Model: "model-a", CallID: duplicate.CallID,
		NewMessages: []chat.ChatMessage{{Role: model.RoleUser, Content: "duplicate"}},
		Assistant:   &chat.ChatMessage{Role: model.RoleAssistant, Content: "duplicate"}, Status: model.ConversationTurnCompleted,
	})
	if err != nil || returnedID != conversationID {
		t.Fatalf("idempotent result = %d, %v", returnedID, err)
	}
	var count int64
	if err := db.Model(&model.ConversationTurn{}).Where("call_id = ?", duplicate.CallID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("duplicate turn count = %d, err = %v", count, err)
	}
}

func setupConversationDomainTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupTestDB(t)
	if err := db.AutoMigrate(
		&model.Conversation{}, &model.ConversationTurn{}, &model.ConversationItem{},
	); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE gw_models(id INTEGER PRIMARY KEY, model_code TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE gw_catalog_models(id INTEGER PRIMARY KEY, release_id INTEGER NOT NULL, model_id INTEGER NOT NULL)`,
		`CREATE TABLE gw_model_operations(id INTEGER PRIMARY KEY, release_id INTEGER NOT NULL, catalog_model_id INTEGER NOT NULL)`,
		`CREATE TABLE gw_api_calls(id INTEGER PRIMARY KEY AUTOINCREMENT, public_id TEXT NOT NULL UNIQUE, user_id INTEGER NOT NULL, token_id INTEGER NOT NULL, catalog_release_id INTEGER NOT NULL, model_operation_id INTEGER NOT NULL, status TEXT NOT NULL, current_attempt_id INTEGER, final_attempt_id INTEGER, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE gw_api_call_attempts(id INTEGER PRIMARY KEY AUTOINCREMENT, call_id INTEGER NOT NULL, attempt_no INTEGER NOT NULL, catalog_release_id INTEGER NOT NULL, product_transport_id INTEGER NOT NULL DEFAULT 0, credential_id INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE gw_channel_request_logs(id INTEGER PRIMARY KEY AUTOINCREMENT, attempt_id INTEGER, request_seq INTEGER NOT NULL DEFAULT 1, duration_ms INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE gw_channel_transports(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER NOT NULL, transport_code TEXT NOT NULL)`,
		`CREATE TABLE gw_product_transports(id INTEGER PRIMARY KEY AUTOINCREMENT, release_id INTEGER NOT NULL, channel_transport_id INTEGER NOT NULL)`,
		`CREATE TABLE gw_api_resources(id INTEGER PRIMARY KEY AUTOINCREMENT, public_id TEXT NOT NULL UNIQUE, resource_kind TEXT NOT NULL, call_id INTEGER NOT NULL, user_id INTEGER NOT NULL, token_id INTEGER NOT NULL, deleted_at DATETIME)`,
		`CREATE TABLE billing_reservations(id INTEGER PRIMARY KEY AUTOINCREMENT, call_id INTEGER NOT NULL UNIQUE)`,
		`CREATE TABLE billing_settlements(reservation_id INTEGER PRIMARY KEY, actual_amount TEXT NOT NULL)`,
		`INSERT INTO gw_models(id,model_code) VALUES (1,'model-a'),(2,'model-b'),(3,'model-recover')`,
		`INSERT INTO gw_catalog_models(id,release_id,model_id) VALUES (1,1,1),(2,1,2),(3,1,3)`,
		`INSERT INTO gw_model_operations(id,release_id,catalog_model_id) VALUES (1,1,1),(2,1,2),(3,1,3)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("create unified conversation fixture: %v", err)
		}
	}
	return db
}

func createConversationTestCall(t *testing.T, db *gorm.DB, id string, userID, tokenID uint, cost decimal.Decimal) *model.APICall {
	t.Helper()
	call := &model.APICall{
		ID: id, RequestID: id, UserID: userID, TokenID: tokenID, Status: model.APICallStatusCompleted,
		ProjectConversation: true,
		FinalCost:           cost, DurationMs: 25, Model: "model-a",
	}
	if err := insertConversationTestCall(db, call); err != nil {
		t.Fatal(err)
	}
	return call
}

func insertConversationTestCall(db *gorm.DB, call *model.APICall) error {
	modelCode := call.Model
	if modelCode == "" {
		modelCode = "model-a"
		call.Model = modelCode
	}
	var operationID uint64
	if err := db.Table("gw_model_operations AS operation").
		Select("operation.id").
		Joins("JOIN gw_catalog_models AS catalog_model ON catalog_model.id = operation.catalog_model_id AND catalog_model.release_id = operation.release_id").
		Joins("JOIN gw_models AS gateway_model ON gateway_model.id = catalog_model.model_id").
		Where("operation.release_id = 1 AND gateway_model.model_code = ?", modelCode).
		Take(&operationID).Error; err != nil {
		return err
	}
	createdAt := call.StartedAt
	if createdAt.IsZero() {
		createdAt = time.Now().Add(-time.Duration(call.DurationMs) * time.Millisecond)
	}
	updatedAt := call.UpdatedAt
	if updatedAt.IsZero() || updatedAt.Before(createdAt) {
		updatedAt = createdAt.Add(time.Duration(call.DurationMs) * time.Millisecond)
	}
	if err := db.Exec(`INSERT INTO gw_api_calls(public_id,user_id,token_id,catalog_release_id,model_operation_id,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		call.ID, call.UserID, call.TokenID, 1, operationID, string(call.Status), createdAt, updatedAt).Error; err != nil {
		return err
	}
	var internalCallID uint64
	if err := db.Table("gw_api_calls").Where("public_id = ?", call.ID).Pluck("id", &internalCallID).Error; err != nil {
		return err
	}
	if err := db.Exec(`INSERT INTO billing_reservations(call_id) VALUES (?)`, internalCallID).Error; err != nil {
		return err
	}
	var reservationID uint64
	if err := db.Table("billing_reservations").Where("call_id = ?", internalCallID).Pluck("id", &reservationID).Error; err != nil {
		return err
	}
	return db.Exec(`INSERT INTO billing_settlements(reservation_id,actual_amount) VALUES (?,?)`, reservationID, call.FinalCost.String()).Error
}

func updateConversationTestCallStatus(t *testing.T, db *gorm.DB, call *model.APICall, status model.APICallStatus) {
	t.Helper()
	if err := db.Table("gw_api_calls").Where("public_id = ?", call.ID).Update("status", string(status)).Error; err != nil {
		t.Fatal(err)
	}
	call.Status = status
}

func updateConversationTestCallModel(t *testing.T, db *gorm.DB, call *model.APICall, modelCode string) {
	t.Helper()
	var operationID uint64
	if err := db.Table("gw_model_operations AS operation").
		Select("operation.id").
		Joins("JOIN gw_catalog_models AS catalog_model ON catalog_model.id = operation.catalog_model_id AND catalog_model.release_id = operation.release_id").
		Joins("JOIN gw_models AS gateway_model ON gateway_model.id = catalog_model.model_id").
		Where("operation.release_id = 1 AND gateway_model.model_code = ?", modelCode).
		Take(&operationID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("gw_api_calls").Where("public_id = ?", call.ID).Update("model_operation_id", operationID).Error; err != nil {
		t.Fatal(err)
	}
	call.Model = modelCode
}

func createConversationTestRequestLog(t *testing.T, db *gorm.DB, callID string, credentialID uint64, transport string, durationMS uint64) uint {
	t.Helper()
	var internalCallID uint64
	if err := db.Table("gw_api_calls").Where("public_id = ?", callID).Pluck("id", &internalCallID).Error; err != nil || internalCallID == 0 {
		t.Fatalf("load unified call %s: id=%d err=%v", callID, internalCallID, err)
	}
	result := db.Exec(`INSERT INTO gw_channel_transports(release_id,transport_code) VALUES (1,?)`, transport)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	var channelTransportID uint64
	if err := db.Raw(`SELECT last_insert_rowid()`).Scan(&channelTransportID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_product_transports(release_id,channel_transport_id) VALUES (1,?)`, channelTransportID).Error; err != nil {
		t.Fatal(err)
	}
	var productTransportID uint64
	if err := db.Raw(`SELECT last_insert_rowid()`).Scan(&productTransportID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_api_call_attempts(call_id,attempt_no,catalog_release_id,product_transport_id,credential_id) VALUES (?,1,1,?,?)`, internalCallID, productTransportID, credentialID).Error; err != nil {
		t.Fatal(err)
	}
	var attemptID uint64
	if err := db.Raw(`SELECT last_insert_rowid()`).Scan(&attemptID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("gw_api_calls").Where("id = ?", internalCallID).Updates(map[string]any{"current_attempt_id": attemptID, "final_attempt_id": attemptID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_channel_request_logs(attempt_id,request_seq,duration_ms) VALUES (?,1,?)`, attemptID, durationMS).Error; err != nil {
		t.Fatal(err)
	}
	var requestLogID uint
	if err := db.Raw(`SELECT last_insert_rowid()`).Scan(&requestLogID).Error; err != nil {
		t.Fatal(err)
	}
	return requestLogID
}

func createConversationTestResource(t *testing.T, db *gorm.DB, callID, publicID, kind string, userID, tokenID uint) {
	t.Helper()
	var internalCallID uint64
	if err := db.Table("gw_api_calls").Where("public_id = ?", callID).Pluck("id", &internalCallID).Error; err != nil || internalCallID == 0 {
		t.Fatalf("load unified call %s: id=%d err=%v", callID, internalCallID, err)
	}
	if err := db.Exec(`INSERT INTO gw_api_resources(public_id,resource_kind,call_id,user_id,token_id) VALUES (?,?,?,?,?)`, publicID, kind, internalCallID, userID, tokenID).Error; err != nil {
		t.Fatal(err)
	}
}
