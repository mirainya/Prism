package engine

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

type deferredSelector struct {
	route    *routing.RouteResult
	selected int
	released int
}

func (s *deferredSelector) SelectTransport(context.Context, string, routing.RouteRequirements, routing.RouteOptions) (*routing.RouteResult, error) {
	s.selected++
	clone := *s.route
	return &clone, nil
}

func (s *deferredSelector) Release(uint) { s.released++ }

type deferredTransport struct {
	prepareCalls int
	executeCalls int
	invocation   transport.Invocation
	prepared     transport.PreparedRequest
}

func (t *deferredTransport) ID() transport.ID { return transport.OpenAIResponses }

func (t *deferredTransport) Plan(operation transport.Operation, _ canonical.Request, features canonical.FeatureSet) transport.Plan {
	return transport.Exact(operation, features)
}

func (t *deferredTransport) Prepare(_ context.Context, invocation transport.Invocation) (transport.PreparedRequest, error) {
	t.prepareCalls++
	t.invocation = invocation
	return t.prepared.Clone(), nil
}

func (t *deferredTransport) ExecutePrepared(_ context.Context, invocation transport.Invocation, prepared transport.PreparedRequest) (canonical.Response, error) {
	t.executeCalls++
	t.invocation = invocation
	t.prepared = prepared.Clone()
	return canonical.Response{ID: "provider-response", Status: "completed"}, nil
}

func (t *deferredTransport) StreamPrepared(context.Context, transport.Invocation, transport.PreparedRequest) (transport.EventStream, error) {
	return nil, errors.New("unexpected stream")
}

func TestDeferredPlanDoesNotPrepareOrExecuteBeforePersistence(t *testing.T) {
	route := deferredUnifiedRoute()
	selector := &deferredSelector{route: route}
	upstream := &deferredTransport{prepared: transport.PreparedRequest{
		Method: http.MethodPost, URL: "https://provider.example/v1/responses", Body: []byte(`{"model":"vendor"}`),
	}}
	registry := transport.NewRegistry()
	if err := registry.Register(upstream); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	executionEngine, err := New(selector, registry)
	if err != nil {
		t.Fatal(err)
	}

	request := canonical.Request{Endpoint: canonical.EndpointOpenAIResponses, Model: "public"}
	plan, err := executionEngine.PlanDeferred(context.Background(), request, ExecuteOptions{CallID: "4cd84a6d-f6c2-4efd-a91d-682fa3e4d164"})
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.Route == nil || plan.Route.RouteID != route.RouteID {
		t.Fatalf("plan=%#v", plan)
	}
	if selector.selected != 1 || selector.released != 1 || upstream.prepareCalls != 0 || upstream.executeCalls != 0 {
		t.Fatalf("selected=%d released=%d prepared=%d executed=%d", selector.selected, selector.released, upstream.prepareCalls, upstream.executeCalls)
	}
}

func TestPrepareFixedExecutesPersistedRouteExactlyOnce(t *testing.T) {
	route := deferredUnifiedRoute()
	selector := &deferredSelector{route: route}
	upstream := &deferredTransport{prepared: transport.PreparedRequest{
		Method: http.MethodPost, URL: "https://provider.example/v1/responses", Body: []byte(`{"model":"vendor"}`),
	}}
	registry := transport.NewRegistry()
	if err := registry.Register(upstream); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	executionEngine, err := New(selector, registry)
	if err != nil {
		t.Fatal(err)
	}

	request := canonical.Request{Endpoint: canonical.EndpointOpenAIResponses, Model: "public"}
	fixed, err := executionEngine.PrepareFixed(context.Background(), route, request)
	if err != nil {
		t.Fatal(err)
	}
	if upstream.prepareCalls != 1 || selector.selected != 0 {
		t.Fatalf("prepared=%d selected=%d", upstream.prepareCalls, selector.selected)
	}
	response, err := fixed.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if response.ID != "provider-response" || upstream.executeCalls != 1 || upstream.invocation.Route.APIKey != route.APIKey {
		t.Fatalf("response=%#v calls=%d route=%#v", response, upstream.executeCalls, upstream.invocation.Route)
	}
	if _, err := fixed.Execute(context.Background()); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("second execute error=%v", err)
	}
	if upstream.executeCalls != 1 {
		t.Fatalf("execute calls=%d", upstream.executeCalls)
	}
}

func deferredUnifiedRoute() *routing.RouteResult {
	return &routing.RouteResult{
		ReleaseID: 1, OperationContractID: 2, ModelOperationID: 3, SKUID: 4,
		RouteID: 5, OfferingID: 6, CostPlanID: 15, ProductTransportID: 7, CredentialPoolID: 8,
		CredentialID: 9, CredentialVersionID: 10, PurposeGrantID: 11,
		AbilityID: 12, ChannelID: 13, KeyID: 14, BaseURL: "https://provider.example",
		APIKey: "secret", VendorModel: "vendor", ModelName: "public", Transport: transport.OpenAIResponses,
	}
}
