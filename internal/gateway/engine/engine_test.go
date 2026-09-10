package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

type engineTestSelector struct {
	route    *routing.RouteResult
	releases []uint
}

func (s *engineTestSelector) SelectTransport(_ context.Context, _ string, _ routing.RouteRequirements, options routing.RouteOptions) (*routing.RouteResult, error) {
	if s.route == nil {
		return nil, routing.ErrNoRoute
	}
	result := *s.route
	if result.Transport == "" && len(options.PreferredTransports) != 0 {
		result.Transport = options.PreferredTransports[0]
	}
	return &result, nil
}

func (s *engineTestSelector) Release(keyID uint) { s.releases = append(s.releases, keyID) }

type engineTestTransport struct {
	id          transport.ID
	plan        func(transport.Operation, canonical.Request, canonical.FeatureSet) transport.Plan
	prepareCall int
}

func (t *engineTestTransport) ID() transport.ID { return t.id }

func (t *engineTestTransport) Plan(operation transport.Operation, request canonical.Request, requirements canonical.FeatureSet) transport.Plan {
	if t.plan != nil {
		return t.plan(operation, request, requirements)
	}
	return transport.Exact(operation, requirements)
}

func (t *engineTestTransport) Prepare(context.Context, transport.Invocation) (transport.PreparedRequest, error) {
	t.prepareCall++
	return transport.PreparedRequest{}, errors.New("unexpected upstream preparation")
}

func (*engineTestTransport) ExecutePrepared(context.Context, transport.Invocation, transport.PreparedRequest) (canonical.Response, error) {
	return canonical.Response{}, errors.New("unexpected upstream execution")
}

func (*engineTestTransport) StreamPrepared(context.Context, transport.Invocation, transport.PreparedRequest) (transport.EventStream, error) {
	return nil, errors.New("unexpected upstream stream")
}

func TestNewRequiresSelectorAndRegistry(t *testing.T) {
	registry := transport.NewRegistry()
	selector := &engineTestSelector{}
	if _, err := New(nil, registry); err == nil {
		t.Fatal("nil selector accepted")
	}
	if _, err := New(selector, nil); err == nil {
		t.Fatal("nil registry accepted")
	}
	if _, err := New(selector, registry); err != nil {
		t.Fatalf("valid engine rejected: %v", err)
	}
}

func TestExecuteRejectsRouteOutsideUnifiedCatalogBeforeUpstream(t *testing.T) {
	item := &engineTestTransport{id: transport.OpenAIChat}
	registry := transport.NewRegistry()
	if err := registry.Register(item); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	selector := &engineTestSelector{route: &routing.RouteResult{
		KeyID: 7, ModelName: "public", VendorModel: "vendor", Transport: transport.OpenAIChat,
	}}
	executionEngine, err := New(selector, registry)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executionEngine.Execute(t.Context(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Model:    "public",
	}, ExecuteOptions{UserID: 1, TokenID: 1})
	if result != nil || !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if item.prepareCall != 0 {
		t.Fatalf("prepared %d upstream requests before catalog validation", item.prepareCall)
	}
	if len(selector.releases) != 1 || selector.releases[0] != 7 {
		t.Fatalf("released credentials=%v", selector.releases)
	}
}

func TestPlansIsolateMutableRequestsAndMergePreparedRequirements(t *testing.T) {
	seen := map[transport.ID]canonical.Request{}
	registry := transport.NewRegistry()
	for _, id := range []transport.ID{transport.OpenAIChat, transport.OpenAIResponses} {
		currentID := id
		item := &engineTestTransport{id: currentID, plan: func(operation transport.Operation, request canonical.Request, requirements canonical.FeatureSet) transport.Plan {
			seen[currentID] = request.Clone()
			return transport.Exact(operation, requirements)
		}}
		if err := registry.Register(item); err != nil {
			t.Fatal(err)
		}
	}
	registry.Freeze()
	executionEngine := &Engine{transports: registry}
	request := canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Items: []canonical.Item{{
			Role: canonical.RoleUser,
			Content: []canonical.Content{{
				Text:  "original",
				Extra: map[string]json.RawMessage{"content": json.RawMessage(`"original"`)},
			}},
			Extra: map[string]json.RawMessage{"item": json.RawMessage(`"original"`)},
		}},
		Metadata:         map[string]string{"trace": "original"},
		ClientExtensions: map[string]json.RawMessage{"client": json.RawMessage(`{"value":"original"}`)},
	}
	plans, err := executionEngine.plans(t.Context(), transport.OperationChat, request, request.RequiredFeatures(), func(_ context.Context, candidate canonical.Request, id transport.ID) (canonical.Request, error) {
		if id == transport.OpenAIChat {
			candidate.Items[0].Content[0].Text = "chat-only"
			candidate.Items[0].Extra["item"] = json.RawMessage(`"chat-only"`)
			candidate.Items[0].Content[0].Extra["content"] = json.RawMessage(`"chat-only"`)
			candidate.Metadata["trace"] = "chat-only"
			candidate.ClientExtensions["client"] = json.RawMessage(`"chat-only"`)
		} else {
			candidate.Reasoning = &canonical.Reasoning{Effort: "high"}
		}
		return candidate, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 {
		t.Fatalf("plans=%#v", plans)
	}
	if seen[transport.OpenAIResponses].Items[0].Content[0].Text != "original" || seen[transport.OpenAIResponses].Metadata["trace"] != "original" {
		t.Fatalf("responses plan was polluted: %#v", seen[transport.OpenAIResponses])
	}
	reasoning := false
	for _, plan := range plans {
		reasoning = reasoning || plan.requirements[canonical.FeatureReasoning]
	}
	if !reasoning {
		t.Fatalf("prepared reasoning requirement missing: %#v", plans)
	}
	if request.Items[0].Content[0].Text != "original" || request.Metadata["trace"] != "original" || string(request.ClientExtensions["client"]) != `{"value":"original"}` {
		t.Fatalf("source request mutated: %#v", request)
	}
}

func TestPlansPreferExactThenStableTransportID(t *testing.T) {
	registry := transport.NewRegistry()
	items := []*engineTestTransport{
		{id: transport.OpenAIResponses, plan: func(operation transport.Operation, _ canonical.Request, requirements canonical.FeatureSet) transport.Plan {
			return transport.Converted(operation, transport.OperationResponses, requirements)
		}},
		{id: transport.AnthropicMessages},
		{id: transport.OpenAIChat},
	}
	for _, item := range items {
		if err := registry.Register(item); err != nil {
			t.Fatal(err)
		}
	}
	registry.Freeze()
	plans, err := (&Engine{transports: registry}).plans(t.Context(), transport.OperationChat, canonical.Request{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := planIDs(plans)
	want := []transport.ID{transport.AnthropicMessages, transport.OpenAIChat, transport.OpenAIResponses}
	if len(got) != len(want) {
		t.Fatalf("plan IDs=%v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("plan IDs=%v want=%v", got, want)
		}
	}
	if preferred := preferredPlanIDs(plans); len(preferred) != 3 || preferred[2] != transport.OpenAIResponses {
		t.Fatalf("preferred plans=%v", preferred)
	}
}

func TestPlansPropagateTransportPreparationError(t *testing.T) {
	registry := transport.NewRegistry()
	if err := registry.Register(&engineTestTransport{id: transport.OpenAIChat}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	want := errors.New("normalize failed")
	plans, err := (&Engine{transports: registry}).plans(t.Context(), transport.OperationChat, canonical.Request{}, nil, func(context.Context, canonical.Request, transport.ID) (canonical.Request, error) {
		return canonical.Request{}, want
	})
	if plans != nil || !errors.Is(err, want) {
		t.Fatalf("plans=%#v err=%v", plans, err)
	}
}

func TestPlanFiltersAndRequirements(t *testing.T) {
	plans := []plannedTransport{
		{id: transport.OpenAIChat, requirements: canonical.FeatureSet{canonical.FeatureVision: true}},
		{id: transport.OpenAIResponses, requirements: canonical.FeatureSet{canonical.FeatureReasoning: true}},
	}
	filtered := filterHintedPlans(plans, []string{string(transport.OpenAIResponses), "unknown"})
	if len(filtered) != 1 || filtered[0].id != transport.OpenAIResponses {
		t.Fatalf("filtered=%#v", filtered)
	}
	requirements := requirementsForPlans(canonical.FeatureSet{canonical.FeatureTools: true}, filtered)
	if !requirements[canonical.FeatureTools] || !requirements[canonical.FeatureReasoning] || requirements[canonical.FeatureVision] {
		t.Fatalf("requirements=%#v", requirements)
	}
}

func TestRouteFeatureValidation(t *testing.T) {
	if !routeSupportsFeatures(&routing.RouteResult{Capabilities: nil}, canonical.FeatureSet{canonical.FeatureVision: true}) {
		t.Fatal("nil capability projection must remain compatible with external selectors")
	}
	configured := &routing.RouteResult{Capabilities: map[routing.Capability]bool{routing.CapabilityVision: true}}
	if !routeSupportsFeatures(configured, canonical.FeatureSet{canonical.FeatureVision: true}) {
		t.Fatal("declared feature rejected")
	}
	if routeSupportsFeatures(configured, canonical.FeatureSet{canonical.FeatureTools: true}) {
		t.Fatal("undeclared feature accepted")
	}
}

type engineStatusError int

func (e engineStatusError) Error() string   { return http.StatusText(int(e)) }
func (e engineStatusError) HTTPStatus() int { return int(e) }

func TestRetryableUpstreamErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests, http.StatusInternalServerError} {
		if !retryableUpstreamError(t.Context(), engineStatusError(status)) {
			t.Fatalf("status %d was not retryable", status)
		}
	}
	if retryableUpstreamError(t.Context(), engineStatusError(http.StatusBadRequest)) {
		t.Fatal("400 was retryable")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if retryableUpstreamError(cancelled, errors.New("network error")) {
		t.Fatal("cancelled context was retryable")
	}
}

func TestOperationForPublicEndpoints(t *testing.T) {
	tests := map[canonical.Endpoint]transport.Operation{
		canonical.EndpointOpenAIChat:      transport.OperationChat,
		canonical.EndpointOpenAIResponses: transport.OperationResponses,
		canonical.EndpointAnthropic:       transport.OperationMessages,
	}
	for endpoint, want := range tests {
		got, err := operationFor(endpoint)
		if err != nil || got != want {
			t.Fatalf("endpoint=%q operation=%q err=%v", endpoint, got, err)
		}
	}
	if _, err := operationFor(canonical.Endpoint("unsupported")); err == nil {
		t.Fatal("unsupported endpoint accepted")
	}
}
