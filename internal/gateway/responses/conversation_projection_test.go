package responses

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
)

func TestPipelineV2PreviousResponseProjectsSameConversation(t *testing.T) {
	pipeline, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	first, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"first"`),
	}, "", "request-conversation-first")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	firstTurn := requireResponseConversationTurn(t, first.Record.CallID, model.ConversationTurnCompleted)

	second, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"second"`), PreviousResponseID: first.Record.ID,
	}, "", "request-conversation-second")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	secondTurn := requireResponseConversationTurn(t, second.Record.CallID, model.ConversationTurnCompleted)
	if secondTurn.ConversationID != firstTurn.ConversationID || secondTurn.Sequence != firstTurn.Sequence+1 {
		t.Fatalf("first_turn=%#v second_turn=%#v", firstTurn, secondTurn)
	}
	requireResponseConversationItemText(t, secondTurn.ID, model.ConversationItemInput, "second")
	var conversationCount int64
	if err := model.DB().Model(&model.Conversation{}).Count(&conversationCount).Error; err != nil {
		t.Fatal(err)
	}
	if conversationCount != 1 {
		t.Fatalf("conversation_count=%d", conversationCount)
	}
}

func TestPipelineV2ValidatesExplicitConversationBeforeExecution(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	conversation := model.Conversation{
		UserID: token.UserID, TokenID: token.ID, Model: "public", Title: "existing", LastStatus: "completed", Status: 1,
	}
	if err := model.DB().Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	result, err := pipeline.CreateWithOptions(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"attached"`), Conversation: json.RawMessage(`"conv_upstream"`),
	}, "", CreateOptions{RequestID: "request-explicit-conversation", ConversationID: conversation.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	turn := requireResponseConversationTurn(t, result.Record.CallID, model.ConversationTurnCompleted)
	if turn.ConversationID != conversation.ID {
		t.Fatalf("conversation_id=%d want=%d", turn.ConversationID, conversation.ID)
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.ConversationID != conversation.ID {
		t.Fatalf("call conversation_id=%d want=%d", call.ConversationID, conversation.ID)
	}

	before := upstream.callCount()
	_, err = pipeline.CreateWithOptions(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"invalid"`), Conversation: json.RawMessage(`"conv_native"`),
	}, "", CreateOptions{RequestID: "request-invalid-conversation", ConversationID: 999999})
	if err == nil || !strings.Contains(err.Error(), "conversation") {
		t.Fatalf("invalid conversation error=%v", err)
	}
	if upstream.callCount() != before {
		t.Fatalf("invalid conversation reached upstream: before=%d after=%d", before, upstream.callCount())
	}
}

func TestPipelineV2CancellationProjectsAbortedTurn(t *testing.T) {
	pipeline, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	record := createPipelineV2Background(t, token, "resp_background_cancel_projection", "request-background-cancel-projection")
	response, err := pipeline.Cancel(token.ID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "cancelled" {
		t.Fatalf("response_status=%s", response.Status)
	}
	turn := requireResponseConversationTurn(t, record.CallID, model.ConversationTurnAborted)
	requireResponseConversationItemText(t, turn.ID, model.ConversationItemInput, "background")
}

func TestPipelineV2IdempotencyIncludesExplicitConversation(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	firstConversation := model.Conversation{UserID: token.UserID, TokenID: token.ID, Model: "public", Title: "first", Status: 1}
	secondConversation := model.Conversation{UserID: token.UserID, TokenID: token.ID, Model: "public", Title: "second", Status: 1}
	if err := model.DB().Create(&firstConversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB().Create(&secondConversation).Error; err != nil {
		t.Fatal(err)
	}
	request := &protocol.Request{Model: "public", Input: json.RawMessage(`"idempotent"`)}
	first, err := pipeline.CreateWithOptions(context.Background(), token.UserID, token.ID, request, "conversation-key", CreateOptions{
		RequestID: "request-conversation-key-first", ConversationID: firstConversation.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	replayed, err := pipeline.CreateWithOptions(context.Background(), token.UserID, token.ID, request, "conversation-key", CreateOptions{
		RequestID: "request-conversation-key-replay", ConversationID: firstConversation.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.IdempotentReplay {
		t.Fatalf("replay=%#v", replayed)
	}
	if err := replayed.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	var replayCall model.APICall
	if err := model.DB().First(&replayCall, "id = ?", replayed.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if replayCall.ConversationID != firstConversation.ID {
		t.Fatalf("replay conversation_id=%d want=%d", replayCall.ConversationID, firstConversation.ID)
	}
	var turns int64
	if err := model.DB().Model(&model.ConversationTurn{}).Count(&turns).Error; err != nil {
		t.Fatal(err)
	}
	if turns != 1 {
		t.Fatalf("replay created %d turns", turns)
	}

	before := upstream.callCount()
	_, err = pipeline.CreateWithOptions(context.Background(), token.UserID, token.ID, request, "conversation-key", CreateOptions{
		RequestID: "request-conversation-key-conflict", ConversationID: secondConversation.ID,
	})
	if err == nil || !strings.Contains(err.Error(), "different request") {
		t.Fatalf("conversation idempotency conflict=%v", err)
	}
	if upstream.callCount() != before {
		t.Fatalf("conversation conflict reached upstream: before=%d after=%d", before, upstream.callCount())
	}
}

func TestPipelineV2BackgroundRetainsExplicitConversationForRecovery(t *testing.T) {
	pipeline, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	conversation := model.Conversation{UserID: token.UserID, TokenID: token.ID, Model: "public", Title: "background", Status: 1}
	if err := model.DB().Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	previousEnqueue := enqueueResponseBackground
	enqueueResponseBackground = func(string) error { return nil }
	t.Cleanup(func() { enqueueResponseBackground = previousEnqueue })
	result, err := pipeline.CreateWithOptions(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"background explicit"`), Background: true,
	}, "background-conversation-key", CreateOptions{
		RequestID: "request-background-explicit-conversation", ConversationID: conversation.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.ConversationID != conversation.ID {
		t.Fatalf("queued call conversation_id=%d want=%d", call.ConversationID, conversation.ID)
	}
	var pending model.ConversationProjectionOutbox
	if err := model.DB().First(&pending, "call_id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if !pending.InputReady || pending.OutputReady {
		t.Fatalf("background projection was ready before execution: %#v", pending)
	}
	if err := pipeline.ExecuteBackground(context.Background(), result.Record.ID, true); err != nil {
		t.Fatal(err)
	}
	turn := requireResponseConversationTurn(t, result.Record.CallID, model.ConversationTurnCompleted)
	if turn.ConversationID != conversation.ID {
		t.Fatalf("background turn conversation_id=%d want=%d", turn.ConversationID, conversation.ID)
	}
}

func TestReconcilePendingResponseConversationRecoversStoreFalse(t *testing.T) {
	pipeline, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	store := false
	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"recover private"`), Store: &store,
	}, "", "request-recover-private")
	if err != nil {
		t.Fatal(err)
	}
	if result.execution == nil {
		t.Fatal("missing deferred execution lifecycle")
	}
	var staged model.ConversationProjectionOutbox
	if err := model.DB().First(&staged, "call_id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if !staged.InputReady || !staged.OutputReady || !strings.Contains(string(staged.CanonicalOutput), "done") {
		t.Fatalf("incomplete projection outbox: %#v", staged)
	}
	stageResponseConversationReadyBestEffort(result.Record, nil)
	if err := model.DB().First(&staged, "call_id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(staged.CanonicalOutput), "done") {
		t.Fatalf("staged output was overwritten: %s", staged.CanonicalOutput)
	}
	replayCall, err := service.NewAPICallService().StartCall(&service.StartCallRequest{
		ID: "call_replay_before_projection", RequestID: "request-replay-before-projection",
		UserID: token.UserID, TokenID: token.ID, Endpoint: "/v1/responses",
		Operation: "responses.replay", Model: result.Record.Model,
		ResourceType: "response", ResourceID: result.Record.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a process stopping after the APICall terminal transition but
	// before Result.CompleteDelivery can invoke the projector.
	if err := result.execution.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := model.DB().Model(&model.ConversationTurn{}).Where("call_id = ?", result.Record.CallID).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("turn existed before reconciliation: %d", before)
	}
	projected, err := service.ReconcilePendingAPIConversations(context.Background(), 100)
	if err != nil || projected != 1 {
		t.Fatalf("projected=%d err=%v", projected, err)
	}
	turn := requireResponseConversationTurn(t, result.Record.CallID, model.ConversationTurnCompleted)
	requireResponseConversationItemText(t, turn.ID, model.ConversationItemInput, "recover private")
	requireResponseConversationItemText(t, turn.ID, model.ConversationItemOutput, "done")
	repeatedConversationID, err := service.ProjectPendingAPIConversation(result.Record.CallID)
	if err != nil || repeatedConversationID != turn.ConversationID {
		t.Fatalf("repeated conversation_id=%d want=%d err=%v", repeatedConversationID, turn.ConversationID, err)
	}
	var turnCount int64
	if err := model.DB().Model(&model.ConversationTurn{}).Where("call_id = ?", result.Record.CallID).Count(&turnCount).Error; err != nil {
		t.Fatal(err)
	}
	if turnCount != 1 {
		t.Fatalf("repeated projection created %d turns", turnCount)
	}
	if err := model.DB().First(replayCall, "id = ?", replayCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if replayCall.ConversationID != turn.ConversationID {
		t.Fatalf("replay conversation_id=%d want=%d", replayCall.ConversationID, turn.ConversationID)
	}
	var stored model.AIResponse
	if err := model.DB().First(&stored, "id = ?", result.Record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored.RequestJSON) != 0 || len(stored.InputItems) != 0 || len(stored.ResponseJSON) != 0 || len(stored.OutputItems) != 0 {
		t.Fatalf("private projection source was retained: %#v", stored)
	}
}

func TestCreateResponseRollsBackCallWhenProjectionInputCannotBeStaged(t *testing.T) {
	_, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	if err := model.DB().Migrator().DropTable(&model.ConversationProjectionOutbox{}); err != nil {
		t.Fatal(err)
	}
	request := &protocol.Request{Model: "public", Input: json.RawMessage(`"atomic input"`)}
	projection, err := newResponseConversationProjection(request, 0)
	if err != nil {
		t.Fatal(err)
	}
	record := &model.AIResponse{
		ID: "resp_atomic_projection", UserID: token.UserID, TokenID: token.ID,
		Model: request.Model, Status: "in_progress", Store: true,
		RequestJSON: []byte(`{"model":"public","input":"atomic input"}`),
		InputItems:  []byte(request.Input), IdempotencyKey: "atomic-projection",
	}
	err = createResponseWithCall(record, "request-atomic-projection", false, projection)
	if err == nil {
		t.Fatal("missing projection table did not fail response creation")
	}
	var responses, calls int64
	if countErr := model.DB().Model(&model.AIResponse{}).Where("id = ?", record.ID).Count(&responses).Error; countErr != nil {
		t.Fatal(countErr)
	}
	if countErr := model.DB().Model(&model.APICall{}).Where("id = ?", record.CallID).Count(&calls).Error; countErr != nil {
		t.Fatal(countErr)
	}
	if responses != 0 || calls != 0 {
		t.Fatalf("response=%d call=%d after projection rollback", responses, calls)
	}
}

func TestEnsureResponseCallRebuildsLedgerAndProjectionInputAtomically(t *testing.T) {
	_, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	request := &protocol.Request{Model: "public", Input: json.RawMessage(`"rebuild input"`)}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	record := &model.AIResponse{
		ID: "resp_rebuild_projection", CallID: "call_rebuild_projection",
		UserID: token.UserID, TokenID: token.ID, Model: request.Model,
		Status: "in_progress", Store: true,
		RequestJSON: requestJSON, InputItems: append([]byte(nil), request.Input...),
		IdempotencyKey: "rebuild-projection",
	}
	if err := model.DB().Create(record).Error; err != nil {
		t.Fatal(err)
	}
	requestID, err := ensureResponseCall(record)
	if err != nil || requestID == "" {
		t.Fatalf("ensure response call: request_id=%q err=%v", requestID, err)
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if !call.ProjectConversation || call.Status != model.APICallStatusReceived {
		t.Fatalf("rebuilt call = %#v", call)
	}
	var entry model.ConversationProjectionOutbox
	if err := model.DB().First(&entry, "call_id = ?", record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if !entry.InputReady || entry.OutputReady || !strings.Contains(string(entry.CanonicalInput), "rebuild input") {
		t.Fatalf("rebuilt projection input = %#v", entry)
	}
}

func TestEnsureResponseCallUpgradesActiveLegacyLedgerWithProjectionInput(t *testing.T) {
	_, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	request := &protocol.Request{Model: "public", Input: json.RawMessage(`"legacy queued input"`), Background: true}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	record := &model.AIResponse{
		ID: "resp_upgrade_legacy_projection", CallID: "call_upgrade_legacy_projection",
		UserID: token.UserID, TokenID: token.ID, Model: request.Model,
		Status: "queued", Background: true, Store: true,
		RequestJSON: requestJSON, InputItems: append([]byte(nil), request.Input...),
		IdempotencyKey: "upgrade-legacy-projection",
	}
	if err := model.DB().Create(record).Error; err != nil {
		t.Fatal(err)
	}
	legacyCall := &model.APICall{
		ID: record.CallID, RequestID: "request-legacy-projection",
		UserID: record.UserID, TokenID: record.TokenID,
		Endpoint: "/v1/responses", Operation: "responses", Model: record.Model,
		Status: model.APICallStatusReceived, Background: true,
		ResourceType: "response", ResourceID: record.ID, StartedAt: time.Now(),
	}
	if err := model.DB().Create(legacyCall).Error; err != nil {
		t.Fatal(err)
	}

	requestID, err := ensureResponseCall(record)
	if err != nil || requestID != legacyCall.RequestID {
		t.Fatalf("upgrade legacy response call: request_id=%q err=%v", requestID, err)
	}
	if err := model.DB().First(legacyCall, "id = ?", legacyCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !legacyCall.ProjectConversation {
		t.Fatalf("legacy call was not marked for projection: %#v", legacyCall)
	}
	var entry model.ConversationProjectionOutbox
	if err := model.DB().First(&entry, "call_id = ?", legacyCall.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !entry.InputReady || entry.OutputReady || !strings.Contains(string(entry.CanonicalInput), "legacy queued input") {
		t.Fatalf("legacy projection input = %#v", entry)
	}
}

func requireResponseConversationTurn(t *testing.T, callID string, status model.ConversationTurnStatus) model.ConversationTurn {
	t.Helper()
	var turn model.ConversationTurn
	if err := model.DB().First(&turn, "call_id = ?", callID).Error; err != nil {
		t.Fatalf("load conversation turn for %s: %v", callID, err)
	}
	if turn.Status != status {
		t.Fatalf("turn status=%s want=%s turn=%#v", turn.Status, status, turn)
	}
	return turn
}

func requireResponseConversationItemText(t *testing.T, turnID uint64, direction, expected string) {
	t.Helper()
	var records []model.ConversationItem
	if err := model.DB().Where("turn_id = ? AND direction = ?", turnID, direction).Order("ordinal ASC").Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		var item canonical.Item
		if json.Unmarshal(record.CanonicalJSON, &item) != nil {
			continue
		}
		for _, content := range item.Content {
			if strings.Contains(content.Text, expected) {
				return
			}
		}
	}
	t.Fatalf("turn %d direction %s missing text %q: %#v", turnID, direction, expected, records)
}
