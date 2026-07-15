package stream

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type testWriter struct{ strings.Builder }

func (w *testWriter) Write(data []byte) (int, error) { return w.Builder.Write(data) }
func (w *testWriter) Flush()                         {}

type failedWriter struct{ short bool }

func (w *failedWriter) Write(data []byte) (int, error) {
	if w.short {
		return len(data) - 1, nil
	}
	return 0, errors.New("client disconnected")
}

func (w *failedWriter) Flush() {}

func TestProxyStreamReturnsErrorEvent(t *testing.T) {
	w := &testWriter{}
	agg, err := ProxyStream(w, strings.NewReader("data: {\"error\":{\"message\":\"bad request\",\"code\":\"bad\"}}\n\n"))
	if err == nil || agg == nil || agg.ErrorMessage != "bad request" {
		t.Fatalf("agg=%+v err=%v", agg, err)
	}
}

func TestProxyStreamRejectsMalformedJSON(t *testing.T) {
	w := &testWriter{}
	_, err := ProxyStream(w, strings.NewReader("data: {bad}\n\n"))
	if err == nil {
		t.Fatal("malformed stream event was accepted")
	}
}

func TestProxyStreamRequiresDoneMarker(t *testing.T) {
	w := &testWriter{}
	agg, err := ProxyStream(w, strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
	if err == nil || agg == nil || agg.AssistantContent != "partial" || agg.ErrorMessage == "" {
		t.Fatalf("agg=%+v err=%v", agg, err)
	}
}

func TestProxyStreamAcceptsDoneMarker(t *testing.T) {
	w := &testWriter{}
	_, err := ProxyStream(w, strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestProxyStreamPropagatesDownstreamWriteFailure(t *testing.T) {
	for _, writer := range []*failedWriter{{}, {short: true}} {
		_, err := ProxyStream(writer, strings.NewReader("data: [DONE]\n\n"))
		if err == nil {
			t.Fatal("downstream write failure was ignored")
		}
		if writer.short && !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("short write error = %v", err)
		}
	}
}
