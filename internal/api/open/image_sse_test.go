package open

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

type imageSSEFlushRecorder struct {
	bytes.Buffer
	flushCount int
	flushed    chan struct{}
}

func (r *imageSSEFlushRecorder) Flush() {
	r.flushCount++
	if r.flushed != nil {
		select {
		case r.flushed <- struct{}{}:
		default:
		}
	}
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

func TestForwardImageSSEEventsSendsHeartbeatWhileUpstreamIsSilent(t *testing.T) {
	events := make(chan []byte)
	heartbeats := make(chan time.Time, 1)
	recorder := &imageSSEFlushRecorder{flushed: make(chan struct{}, 1)}
	done := make(chan bool, 1)
	go func() {
		done <- forwardImageSSEEventsWithHeartbeat(recorder, events, heartbeats)
	}()
	heartbeats <- time.Now()
	<-recorder.flushed
	close(events)
	<-done

	if recorder.String() != ": keep-alive\n\n" || recorder.flushCount != 1 {
		t.Fatalf("heartbeat output = %q, flush count = %d", recorder.String(), recorder.flushCount)
	}
}

func TestOpenAIImageExecutionContextSurvivesClientCancellation(t *testing.T) {
	requestCtx, cancel := context.WithCancel(context.Background())
	executionCtx := imageExecutionContext(requestCtx)
	cancel()

	if err := executionCtx.Err(); err != nil {
		t.Fatalf("execution context was canceled with client: %v", err)
	}
}
