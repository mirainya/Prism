package stream

import (
	"strings"
	"testing"
)

type testWriter struct{ strings.Builder }

func (w *testWriter) Write(data []byte) (int, error) { return w.Builder.Write(data) }
func (w *testWriter) Flush()                         {}

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
