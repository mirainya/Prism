// Package transport defines upstream wire protocol contracts for Gateway V2.
package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/model"
)

type ID = model.UpstreamTransport

const (
	OpenAIChat            ID = model.UpstreamTransportOpenAIChat
	OpenAIResponses       ID = model.UpstreamTransportOpenAIResponses
	AnthropicMessages     ID = model.UpstreamTransportAnthropic
	GoogleGenerateContent ID = model.UpstreamTransportGoogle
	VolcengineResponsesV3 ID = model.UpstreamTransportVolcengineV3
)

// Operation is the data exchange shape a transport will execute. It is not a
// semantic model capability.
type Operation string

const (
	OperationChat      Operation = "chat"
	OperationResponses Operation = "responses"
	OperationMessages  Operation = "messages"
)

type PlanKind uint8

const (
	PlanUnsupported PlanKind = iota
	PlanConverted
	PlanExact
)

type Plan struct {
	Kind         PlanKind
	Requested    Operation
	Upstream     Operation
	Requirements canonical.FeatureSet
	Reason       string
}

func Exact(operation Operation, requirements canonical.FeatureSet) Plan {
	return Plan{Kind: PlanExact, Requested: operation, Upstream: operation, Requirements: cloneFeatures(requirements)}
}

func Converted(requested, upstream Operation, requirements canonical.FeatureSet) Plan {
	return Plan{Kind: PlanConverted, Requested: requested, Upstream: upstream, Requirements: cloneFeatures(requirements)}
}

func Unsupported(operation Operation, reason string) Plan {
	return Plan{Kind: PlanUnsupported, Requested: operation, Reason: reason}
}

func (p Plan) Supported() bool { return p.Kind == PlanExact || p.Kind == PlanConverted }

type Route struct {
	AbilityID    uint
	ChannelID    uint
	KeyID        uint
	BaseURL      string
	APIKey       string
	VendorModel  string
	PublicModel  string
	ExtraHeaders map[string]string
	Config       map[string]any
}

type Invocation struct {
	Route     Route
	Request   canonical.Request
	Operation Operation
}

// PreparedRequest records the exact upstream call. The engine owns retries,
// billing, persistence, and circuit state; transports own only wire behavior.
type PreparedRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
	Stream  bool
}

func (p PreparedRequest) Clone() PreparedRequest {
	clone := p
	clone.Headers = p.Headers.Clone()
	clone.Body = append([]byte(nil), p.Body...)
	return clone
}

type EventStream interface {
	Next(context.Context) (canonical.Event, error)
	Close() error
}

type Transport interface {
	ID() ID
	Plan(Operation, canonical.Request, canonical.FeatureSet) Plan
	Prepare(context.Context, Invocation) (PreparedRequest, error)
	ExecutePrepared(context.Context, Invocation, PreparedRequest) (canonical.Response, error)
	StreamPrepared(context.Context, Invocation, PreparedRequest) (EventStream, error)
}

// Execute prepares once, then sends that exact request. The engine calls the
// prepared methods directly so reservation and request logging happen between
// these two phases.
func Execute(ctx context.Context, item Transport, invocation Invocation) (canonical.Response, PreparedRequest, error) {
	prepared, err := item.Prepare(ctx, invocation)
	if err != nil {
		return canonical.Response{}, prepared, err
	}
	response, err := item.ExecutePrepared(ctx, invocation, prepared)
	return response, prepared, err
}

func Stream(ctx context.Context, item Transport, invocation Invocation) (EventStream, PreparedRequest, error) {
	invocation.Request.Stream = true
	prepared, err := item.Prepare(ctx, invocation)
	if err != nil {
		return nil, prepared, err
	}
	stream, err := item.StreamPrepared(ctx, invocation, prepared)
	return stream, prepared, err
}

var (
	ErrDuplicateTransport = errors.New("transport is already registered")
	ErrRegistryFrozen     = errors.New("transport registry is frozen")
)

// Registry is constructed at composition time, frozen, and injected into the
// HTTP server and workers. It intentionally has no package-level singleton.
type Registry struct {
	mu     sync.RWMutex
	items  map[ID]Transport
	frozen bool
}

func NewRegistry() *Registry { return &Registry{items: make(map[ID]Transport)} }

func (r *Registry) Register(item Transport) error {
	if r == nil || item == nil || strings.TrimSpace(string(item.ID())) == "" {
		return errors.New("transport id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrRegistryFrozen
	}
	if _, exists := r.items[item.ID()]; exists {
		return ErrDuplicateTransport
	}
	r.items[item.ID()] = item
	return nil
}

func (r *Registry) Freeze() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func (r *Registry) Get(id ID) (Transport, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, ok := r.items[ID(strings.TrimSpace(string(id)))]
	return item, ok
}

func (r *Registry) IDs() []ID {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]ID, 0, len(r.items))
	for id := range r.items {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func ReadAllEvents(ctx context.Context, stream EventStream) ([]canonical.Event, error) {
	if stream == nil {
		return nil, errors.New("event stream is required")
	}
	defer stream.Close()
	var events []canonical.Event
	for {
		event, err := stream.Next(ctx)
		if errors.Is(err, io.EOF) {
			return events, nil
		}
		if err != nil {
			return events, err
		}
		events = append(events, event)
	}
}

func cloneFeatures(source canonical.FeatureSet) canonical.FeatureSet {
	if source == nil {
		return nil
	}
	clone := make(canonical.FeatureSet, len(source))
	for feature, enabled := range source {
		clone[feature] = enabled
	}
	return clone
}
