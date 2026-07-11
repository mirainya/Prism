package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
)

type contractTransport struct{ id string }

func (t contractTransport) ID() ID { return ID(t.id) }

func (contractTransport) Plan(operation Operation, _ canonical.Request, features canonical.FeatureSet) Plan {
	return Exact(operation, features)
}

func (contractTransport) Prepare(context.Context, Invocation) (PreparedRequest, error) {
	return PreparedRequest{Method: http.MethodPost}, nil
}

func (contractTransport) ExecutePrepared(context.Context, Invocation, PreparedRequest) (canonical.Response, error) {
	return canonical.Response{}, nil
}

func (contractTransport) StreamPrepared(context.Context, Invocation, PreparedRequest) (EventStream, error) {
	return nil, nil
}

type testEventStream struct {
	events []canonical.Event
	closed bool
}

func (s *testEventStream) Next(context.Context) (canonical.Event, error) {
	if len(s.events) == 0 {
		return canonical.Event{}, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *testEventStream) Close() error {
	s.closed = true
	return nil
}

func TestRegistryRejectsDuplicatesAndFreezes(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(contractTransport{id: "openai"}); err != nil {
		t.Fatalf("register transport: %v", err)
	}
	if err := registry.Register(contractTransport{id: "openai"}); !errors.Is(err, ErrDuplicateTransport) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, ok := registry.Get(" openai "); !ok {
		t.Fatal("registered transport was not found")
	}
	registry.Freeze()
	if err := registry.Register(contractTransport{id: "anthropic"}); !errors.Is(err, ErrRegistryFrozen) {
		t.Fatalf("frozen registry error = %v", err)
	}
}

func TestPlanRetainsIndependentRequirements(t *testing.T) {
	requirements := canonical.NewFeatureSet(canonical.FeatureTools)
	plan := Converted(OperationResponses, OperationChat, requirements)
	requirements[canonical.FeatureTools] = false
	if !plan.Supported() || !plan.Requirements.Has(canonical.FeatureTools) {
		t.Fatal("converted plan lost its requirements")
	}
	if Unsupported(OperationResponses, "unsupported").Supported() {
		t.Fatal("unsupported plan must not be executable")
	}
}

func TestPreparedRequestCloneIsIndependent(t *testing.T) {
	original := PreparedRequest{Headers: http.Header{"X-Test": {"one"}}, Body: []byte("body")}
	clone := original.Clone()
	clone.Headers.Set("X-Test", "two")
	clone.Body[0] = 'B'
	if original.Headers.Get("X-Test") != "one" {
		t.Fatal("original headers were mutated")
	}
	if string(original.Body) != "body" {
		t.Fatal("original body was mutated")
	}
}

func TestReadAllEventsClosesStream(t *testing.T) {
	stream := &testEventStream{events: []canonical.Event{{Type: canonical.EventOutputTextDelta, Delta: "ok"}}}
	events, err := ReadAllEvents(context.Background(), stream)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(events) != 1 || events[0].Delta != "ok" {
		t.Fatalf("events = %#v", events)
	}
	if !stream.closed {
		t.Fatal("stream was not closed")
	}
}
