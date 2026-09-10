package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/execution"
)

func TestAsyncProjectionStateUsesPublicVocabulary(t *testing.T) {
	tests := []struct {
		state    execution.AsyncState
		status   string
		progress uint8
	}{
		{execution.AsyncAllocated, "queued", 0},
		{execution.AsyncSubmitting, "queued", 0},
		{execution.AsyncAccepted, "submitted", 0},
		{execution.AsyncRunning, "tracking", 0},
		{execution.AsyncSucceeded, "completed", 100},
		{execution.AsyncFailed, "failed", 0},
		{execution.AsyncCancelled, "cancelled", 0},
		{execution.AsyncSubmissionUnknown, "submission_unknown", 0},
		{execution.AsyncManualReview, "manual_review", 0},
		{execution.AsyncCancelRequested, "cancel_requested", 0},
		{execution.AsyncCancelUnknown, "cancel_unknown", 0},
		{execution.AsyncNotCreated, "not_created", 0},
		{execution.AsyncTerminatedUnknown, "terminated_unknown", 0},
	}
	for _, test := range tests {
		status, progress := asyncProjectionState(test.state)
		if status != test.status || progress != test.progress {
			t.Errorf("state=%s got %s/%d, want %s/%d", test.state, status, progress, test.status, test.progress)
		}
	}
}

func TestBuildVideoResourceSummaryDoesNotPersistPromptOrParameters(t *testing.T) {
	prompt := "private prompt text"
	payload := []byte(`{"model":"public","prompt":"` + prompt + `","task_mode":"references","resolution":"2k","ratio":"16:9","duration":5,"generate_audio":true,"content":[{"type":"image_url","image_url":{"url":"https://private.example/image"}}],"params":{"seed":7,"watermark":false}}`)
	summary, err := buildVideoResourceSummary(payload, []byte(strings.Repeat("k", 32)), "public", "vendor", "standard")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, sensitive := range []string{prompt, "https://private.example/image", `"seed":7`, `"watermark":false`} {
		if strings.Contains(text, sensitive) {
			t.Fatalf("summary contains sensitive value %q: %s", sensitive, text)
		}
	}
	if summary["resolution"] != "2k" || summary["ratio"] != "16:9" || summary["duration"] != 5 || summary["task_mode"] != "references" {
		t.Fatalf("summary omitted video specification: %#v", summary)
	}
	if summary["content_count"] != 1 {
		t.Fatalf("content count=%v, want 1", summary["content_count"])
	}
	keys, ok := summary["param_keys"].([]string)
	if !ok || len(keys) != 2 || keys[0] != "seed" || keys[1] != "watermark" {
		t.Fatalf("param keys=%#v", summary["param_keys"])
	}
}
