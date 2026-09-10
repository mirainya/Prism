package responses

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"gorm.io/datatypes"
)

func TestPrepareContinuationRebuildsStoredRound(t *testing.T) {
	previous := responseHistoryRecord("resp_previous", "", `"previous input"`, "previous output")
	req := &protocol.Request{Input: json.RawMessage(`"current input"`), PreviousResponseID: "resp_3"}
	route := &routing.RouteResult{Protocol: model.ProtocolAnthropic, ChannelID: 99, KeyID: 99}
	if err := prepareContinuation(req, &previous, route); err != nil {
		t.Fatalf("prepare continuation: %v", err)
	}
	if req.PreviousResponseID != "" {
		t.Fatalf("previous_response_id = %q, want empty after history rebuild", req.PreviousResponseID)
	}

	var items []map[string]any
	if err := json.Unmarshal(req.Input, &items); err != nil {
		t.Fatalf("decode rebuilt input: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("rebuilt item count = %d, want 3: %s", len(items), req.Input)
	}
	for index, expected := range []string{"previous input", "previous output", "current input"} {
		if !strings.Contains(string(req.Input), expected) {
			t.Fatalf("rebuilt input missing %q: %s", expected, req.Input)
		}
		encoded, _ := json.Marshal(items[index])
		if !strings.Contains(string(encoded), expected) {
			t.Fatalf("item %d = %s, want %q", index, encoded, expected)
		}
	}
}

func TestInputItemsExpandsStoredStringInput(t *testing.T) {
	items, err := decodeInputItems(json.RawMessage(`"hello"`))
	if err != nil {
		t.Fatal(err)
	}
	list, err := paginateResponseInput("resp_string", items)
	if err != nil {
		t.Fatalf("get input items: %v", err)
	}
	if len(list.Data) != 1 || !strings.Contains(string(list.Data[0]), `"type":"input_text"`) || !strings.Contains(string(list.Data[0]), `"text":"hello"`) {
		t.Fatalf("unexpected string input items: %s", list.Data)
	}
}

func TestResolveImageFileIDUsesDataURLWithoutPersistingExpandedInput(t *testing.T) {
	db := openResponsesTestDB(t)
	model.SetDB(db)
	file := model.AIFile{ID: "file_image", UserID: 1, TokenID: 10, Filename: "pixel.png", Purpose: "vision", Bytes: 3, MimeType: "image/png", Content: []byte{1, 2, 3}, Status: "processed"}
	storeResponseTestFiles(t, db, file)
	original := datatypes.JSON(`[{
		"type":"message","role":"user","content":[{"type":"input_image","file_id":"file_image"}]
	}]`)
	req := &protocol.Request{Input: append(json.RawMessage(nil), original...)}
	if err := resolveInputFiles(context.Background(), 10, req); err != nil {
		t.Fatalf("resolve image file: %v", err)
	}
	if !strings.Contains(string(req.Input), `"image_url":"data:image/png;base64,AQID"`) || strings.Contains(string(req.Input), `"file_id"`) {
		t.Fatalf("unexpected resolved image input: %s", req.Input)
	}

	if !strings.Contains(string(original), `"file_id":"file_image"`) || strings.Contains(string(original), "data:image/png") {
		t.Fatalf("source input was expanded: %s", original)
	}
}

func responseHistoryRecord(id, previousID, input, output string) model.AIResponse {
	outputJSON, _ := json.Marshal([]map[string]any{{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": output}}}})
	return model.AIResponse{
		ID: id, UserID: 1, TokenID: 10, Model: "m", Status: "completed", Store: true,
		PreviousResponseID: previousID, InputItems: datatypes.JSON(input), OutputItems: outputJSON,
	}
}
