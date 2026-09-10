package responses

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

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
	if result.Record.Status != "completed" || upstream.callCount() != 1 {
		t.Fatalf("response=%#v upstream_calls=%d", result.Record, upstream.callCount())
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

func TestResponseIdempotencyIntentIncludesExplicitConversation(t *testing.T) {
	request, err := json.Marshal(&protocol.Request{Model: "public", Input: json.RawMessage(`"idempotent"`)})
	if err != nil {
		t.Fatal(err)
	}
	first := responseIdempotencyRequestJSON(request, 11)
	repeated := responseIdempotencyRequestJSON(request, 11)
	second := responseIdempotencyRequestJSON(request, 12)
	if string(first) != string(repeated) {
		t.Fatalf("same conversation produced different intents: %s != %s", first, repeated)
	}
	if hashResponseRequest(first) == hashResponseRequest(second) {
		t.Fatalf("different conversations produced the same intent: %s", first)
	}
}
