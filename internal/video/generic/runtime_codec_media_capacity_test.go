package generic

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mirainya/Prism/internal/video"
)

func TestPublishedRuntimeRejectsPartialAudioProjection(t *testing.T) {
	var catalog map[string]any
	if err := json.Unmarshal(runtimeH3Config(), &catalog); err != nil {
		t.Fatal(err)
	}
	catalog["task_types"] = []string{"multimodal"}
	request := catalog["adapter"].(map[string]any)["request"].(map[string]any)
	request["content_projections"] = []any{
		map[string]any{"source": "url", "target": "reference_images", "output": "array", "types": []string{"image_url"}},
		map[string]any{"source": "url", "target": "audio_reference", "output": "scalar", "index": 0, "types": []string{"audio_url"}},
	}
	validate := func() error {
		t.Helper()
		raw, err := json.Marshal(catalog)
		if err != nil {
			t.Fatal(err)
		}
		return ValidatePublishedRuntimeCatalog("https://www.aicost.me", "POST", "/api/v1/workflows/h3", "minimax-h3", raw)
	}
	if err := validate(); err == nil {
		t.Fatal("one scalar audio projection accepted a three-audio capability")
	}

	request["content_projections"] = []any{
		map[string]any{"source": "url", "target": "reference_images", "output": "array", "types": []string{"image_url"}},
		map[string]any{"source": "url", "target": "audios", "output": "array", "types": []string{"audio_url"}},
	}
	if err := validate(); err != nil {
		t.Fatalf("complete audio projection rejected: %v", err)
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := NewRuntimeCodec("https://www.aicost.me", raw)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := codec.PrepareSubmit(context.Background(), &video.GenerateRequest{
		Model: "minimax-h3", Prompt: "animate", Duration: 5, Resolution: "768p", TaskMode: "multimodal",
		Content: []video.ContentItem{
			{Type: "image_url", URL: "https://assets.example/frame.png"},
			{Type: "audio_url", URL: "https://assets.example/one.mp3"},
			{Type: "audio_url", URL: "https://assets.example/two.mp3"},
			{Type: "audio_url", URL: "https://assets.example/three.mp3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(upstream.Body, &body); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body["audios"], []any{
		"https://assets.example/one.mp3", "https://assets.example/two.mp3", "https://assets.example/three.mp3",
	}) || body["audio_reference"] != nil {
		t.Fatalf("audio payload = %#v, scalar = %#v", body["audios"], body["audio_reference"])
	}
}

func TestProjectionCapacityRequiresContiguousScalarIndexes(t *testing.T) {
	projections := []contentProjection{
		{Source: "url", Target: "audio_0", Output: "scalar", Types: []string{"audio_url"}, Index: 0},
		{Source: "url", Target: "audio_2", Output: "scalar", Types: []string{"audio_url"}, Index: 2},
		{Source: "url", Target: "audio_3", Output: "scalar", Types: []string{"audio_url"}, Index: 3},
	}
	if hasProjectionCapacity(projections, "minimax-h3", "audio_url", 3) {
		t.Fatal("a gap in scalar audio indexes was accepted")
	}
	projections[2].Index = 1
	if !hasProjectionCapacity(projections, "minimax-h3", "audio_url", 3) {
		t.Fatal("three contiguous scalar audio fields were rejected")
	}
}
