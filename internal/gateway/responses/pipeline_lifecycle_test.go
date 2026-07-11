package responses

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestPrepareContinuationRebuildsThreeRoundHistory(t *testing.T) {
	db := setupResponsesLifecycleDB(t, &model.AIResponse{})
	chain := []model.AIResponse{
		responseHistoryRecord("resp_1", "", `"first input"`, "first output"),
		responseHistoryRecord("resp_2", "resp_1", `"second input"`, "second output"),
		responseHistoryRecord("resp_3", "resp_2", `"third input"`, "third output"),
	}
	for index := range chain {
		if err := db.Create(&chain[index]).Error; err != nil {
			t.Fatalf("create history record %d: %v", index, err)
		}
	}

	req := &protocol.Request{Input: json.RawMessage(`"current input"`), PreviousResponseID: "resp_3"}
	route := &routing.RouteResult{Protocol: model.ProtocolAnthropic, ChannelID: 99, KeyID: 99}
	if err := prepareContinuation(req, &chain[2], route); err != nil {
		t.Fatalf("prepare continuation: %v", err)
	}
	if req.PreviousResponseID != "" {
		t.Fatalf("previous_response_id = %q, want empty after history rebuild", req.PreviousResponseID)
	}

	var items []map[string]any
	if err := json.Unmarshal(req.Input, &items); err != nil {
		t.Fatalf("decode rebuilt input: %v", err)
	}
	if len(items) != 7 {
		t.Fatalf("rebuilt item count = %d, want 7: %s", len(items), req.Input)
	}
	for index, expected := range []string{"first input", "first output", "second input", "second output", "third input", "third output", "current input"} {
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
	db := setupResponsesLifecycleDB(t, &model.AIResponse{})
	record := model.AIResponse{ID: "resp_string", UserID: 1, TokenID: 10, Model: "m", Status: "completed", Store: true, InputItems: datatypes.JSON(`"hello"`), IdempotencyKey: "string-input", CreatedAt: time.Now()}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	list, err := (&Pipeline{}).InputItems(10, record.ID)
	if err != nil {
		t.Fatalf("get input items: %v", err)
	}
	if len(list.Data) != 1 || !strings.Contains(string(list.Data[0]), `"type":"input_text"`) || !strings.Contains(string(list.Data[0]), `"text":"hello"`) {
		t.Fatalf("unexpected string input items: %s", list.Data)
	}
}

func TestResolveImageFileIDUsesDataURLWithoutPersistingExpandedInput(t *testing.T) {
	db := setupResponsesLifecycleDB(t, &model.AIFile{}, &model.AIResponse{})
	file := model.AIFile{ID: "file_image", UserID: 1, TokenID: 10, Filename: "pixel.png", Purpose: "vision", Bytes: 3, MimeType: "image/png", Content: []byte{1, 2, 3}, Status: "processed"}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	original := datatypes.JSON(`[{
		"type":"message","role":"user","content":[{"type":"input_image","file_id":"file_image"}]
	}]`)
	record := model.AIResponse{ID: "resp_image", UserID: 1, TokenID: 10, Model: "m", Status: "in_progress", Store: true, InputItems: original, RequestJSON: mustJSON(map[string]any{"model": "m", "input": json.RawMessage(original)}), IdempotencyKey: "image-input", CreatedAt: time.Now()}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}

	req := &protocol.Request{Input: append(json.RawMessage(nil), original...)}
	if err := resolveInputFiles(10, req); err != nil {
		t.Fatalf("resolve image file: %v", err)
	}
	if !strings.Contains(string(req.Input), `"image_url":"data:image/png;base64,AQID"`) || strings.Contains(string(req.Input), `"file_id"`) {
		t.Fatalf("unexpected resolved image input: %s", req.Input)
	}

	var stored model.AIResponse
	if err := db.First(&stored, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored.InputItems), `"file_id":"file_image"`) || strings.Contains(string(stored.InputItems), "data:image/png") {
		t.Fatalf("persisted input was expanded: %s", stored.InputItems)
	}
	if !strings.Contains(string(stored.RequestJSON), `"file_id":"file_image"`) || strings.Contains(string(stored.RequestJSON), "data:image/png") {
		t.Fatalf("persisted request was expanded: %s", stored.RequestJSON)
	}
}

func TestCancelOnlyAllowsBackgroundAndCancelledCannotBeCompleted(t *testing.T) {
	db := setupResponsesLifecycleDB(t, &model.AIResponse{}, &model.BillingLog{})
	pipeline := &Pipeline{billing: service.NewBillingService()}
	now := time.Now()
	foreground := model.AIResponse{ID: "resp_foreground", UserID: 1, TokenID: 10, Model: "m", Status: "in_progress", Store: true, IdempotencyKey: "foreground", CreatedAt: now}
	background := model.AIResponse{ID: "resp_background", UserID: 1, TokenID: 10, Model: "m", Status: "queued", Background: true, Store: true, IdempotencyKey: "background", CreatedAt: now}
	if err := db.Create(&foreground).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&background).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := pipeline.Cancel(10, foreground.ID); err == nil || !errors.Is(err, domain.ErrBadRequest("only background responses can be cancelled")) && !strings.Contains(err.Error(), "only background") {
		t.Fatalf("foreground cancel error = %v", err)
	}
	cancelled, err := pipeline.Cancel(10, background.ID)
	if err != nil {
		t.Fatalf("cancel background: %v", err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("cancelled status = %q", cancelled.Status)
	}
	err = completeRecord(&background, &protocol.Response{ID: background.ID, Status: "completed", Output: json.RawMessage(`[]`)})
	if err == nil {
		t.Fatal("completed response overwrote cancellation")
	}
	var stored model.AIResponse
	if err := db.First(&stored, "id = ?", background.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "cancelled" {
		t.Fatalf("stored status = %q, want cancelled", stored.Status)
	}
}

func setupResponsesLifecycleDB(t *testing.T, tables ...any) *gorm.DB {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(tables...); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	return db
}

func responseHistoryRecord(id, previousID, input, output string) model.AIResponse {
	outputJSON, _ := json.Marshal([]map[string]any{{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": output}}}})
	return model.AIResponse{
		ID: id, UserID: 1, TokenID: 10, Model: "m", Status: "completed", Store: true,
		PreviousResponseID: previousID, InputItems: datatypes.JSON(input), OutputItems: outputJSON,
		IdempotencyKey: "history-" + id, CreatedAt: time.Now(),
	}
}
