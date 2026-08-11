package open

import (
	"bytes"
	"strings"
	"testing"
)

type imageSSEFlushRecorder struct {
	bytes.Buffer
	flushCount int
}

func (r *imageSSEFlushRecorder) Flush() {
	r.flushCount++
}

func TestForwardImageSSEEventsFlushesClientVisibleFrames(t *testing.T) {
	events := make(chan []byte, 3)
	events <- []byte(`{"type":"image_generation.partial_image","b64_json":"cGFydGlhbA=="}`)
	events <- []byte(`{"type":"image_generation.completed","b64_json":"ZmluYWw="}`)
	events <- []byte(`{"type":"error","error":{"message":"upstream failed"}}`)
	close(events)

	recorder := &imageSSEFlushRecorder{}
	errorForwarded := forwardImageSSEEvents(recorder, events)

	if !errorForwarded {
		t.Fatal("expected forwarded error")
	}
	if recorder.flushCount != 2 {
		t.Fatalf("flush count = %d, want 2", recorder.flushCount)
	}
	body := recorder.String()
	if !strings.Contains(body, "image_generation.partial_image") || !strings.Contains(body, "upstream failed") {
		t.Fatalf("SSE body = %s", body)
	}
	if !strings.Contains(body, `"type":"image_generation.failed"`) {
		t.Fatalf("SSE error frame has no failure type: %s", body)
	}
	if strings.Contains(body, `"type":"image_generation.completed"`) {
		t.Fatalf("raw completed event was forwarded: %s", body)
	}
}

func TestForwardImageSSEEventsAcceptsPartialImageB64(t *testing.T) {
	events := make(chan []byte, 1)
	events <- []byte(`{"type":"image_generation.partial_image","partial_image_b64":"cGFydGlhbA=="}`)
	close(events)

	recorder := &imageSSEFlushRecorder{}
	if forwarded := forwardImageSSEEvents(recorder, events); forwarded {
		t.Fatal("partial image must not be treated as an error")
	}
	if !strings.Contains(recorder.String(), `"partial_image_b64":"cGFydGlhbA=="`) {
		t.Fatalf("SSE body = %s", recorder.String())
	}
}

func TestForwardImageSSEEventsRecognizesUntypedAPIError(t *testing.T) {
	events := make(chan []byte, 1)
	events <- []byte(`{"type":"api_error","message":"no available account"}`)
	close(events)

	recorder := &imageSSEFlushRecorder{}
	if !forwardImageSSEEvents(recorder, events) {
		t.Fatal("expected API error to be forwarded")
	}
	body := recorder.String()
	if !strings.Contains(body, `"type":"image_generation.failed"`) ||
		!strings.Contains(body, "no available account") {
		t.Fatalf("SSE body = %s", body)
	}
}
