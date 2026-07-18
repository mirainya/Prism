package console

import (
	"encoding/json"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/datatypes"
)

func TestConversationTurnResponsesHideInternalFields(t *testing.T) {
	turns := []service.ConversationTurnItem{{
		ConversationTurn: model.ConversationTurn{
			ID: 3, ConversationID: 5, Sequence: 2, CallID: "call-test",
			RequestLogID: 7, ProviderResponseID: "provider-secret",
			Status: model.ConversationTurnCompleted, ContextMode: model.ConversationTurnContextSnapshot,
		},
		Items: []model.ConversationItem{{
			ID: 11, Direction: model.ConversationItemInput, Ordinal: 0,
			CanonicalJSON: datatypes.JSON(`{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}`),
		}},
	}}

	publicJSON, err := json.Marshal(conversationTurnResponses(turns, false))
	if err != nil {
		t.Fatal(err)
	}
	var public []map[string]any
	if err := json.Unmarshal(publicJSON, &public); err != nil {
		t.Fatal(err)
	}
	if _, exists := public[0]["request_log_id"]; exists {
		t.Fatal("public response exposed request_log_id")
	}
	if _, exists := public[0]["provider_response_id"]; exists {
		t.Fatal("public response exposed provider_response_id")
	}
	if public[0]["id"] != "3" || public[0]["sequence"] != "2" {
		t.Fatalf("64-bit identifiers were not serialized as strings: %#v", public[0])
	}
	if public[0]["context_mode"] != string(model.ConversationTurnContextSnapshot) {
		t.Fatalf("context mode = %#v", public[0]["context_mode"])
	}
	items := public[0]["items"].([]any)
	item := items[0].(map[string]any)
	if item["id"] != "11" {
		t.Fatalf("item id = %#v", item["id"])
	}
	canonical := item["canonical"].(map[string]any)
	if canonical["type"] != "message" {
		t.Fatalf("canonical item = %#v", canonical)
	}

	internal := conversationTurnResponses(turns, true)
	if internal[0]["request_log_id"] != uint(7) || internal[0]["provider_response_id"] != "provider-secret" {
		t.Fatalf("internal response = %#v", internal[0])
	}
}
