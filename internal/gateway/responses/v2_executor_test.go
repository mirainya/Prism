package responses

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
)

type v2ExecutorSelector struct {
	route    *routing.RouteResult
	released uint
}

func (s *v2ExecutorSelector) SelectTransport(_ string, _ routing.RouteRequirements, _ routing.RouteOptions) (*routing.RouteResult, error) {
	return s.route, nil
}
func (s *v2ExecutorSelector) Release(keyID uint) { s.released = keyID }

type v2ExecutorTransport struct{}

func (v2ExecutorTransport) ID() transport.ID { return model.UpstreamTransportOpenAIResponses }
func (v2ExecutorTransport) Plan(operation transport.Operation, _ canonical.Request, features canonical.FeatureSet) transport.Plan {
	return transport.Exact(operation, features)
}
func (v2ExecutorTransport) Prepare(context.Context, transport.Invocation) (transport.PreparedRequest, error) {
	return transport.PreparedRequest{Method: "POST", URL: "https://upstream.test/v1/responses"}, nil
}
func (v2ExecutorTransport) ExecutePrepared(_ context.Context, invocation transport.Invocation, _ transport.PreparedRequest) (canonical.Response, error) {
	return canonical.Response{
		ID: "provider_id", Model: invocation.Route.VendorModel, Status: "completed", CreatedAt: 123,
		Output: []canonical.Item{{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "output_text", Text: "ok"}}}},
		Usage:  &canonical.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
	}, nil
}
func (v2ExecutorTransport) StreamPrepared(context.Context, transport.Invocation, transport.PreparedRequest) (transport.EventStream, error) {
	return nil, nil
}

func TestV2ExecutorNormalizesPublicResponse(t *testing.T) {
	setupV2ExecutorDB(t)
	selector := &v2ExecutorSelector{route: &routing.RouteResult{KeyID: 7, ModelName: "public", VendorModel: "vendor", Transport: model.UpstreamTransportOpenAIResponses}}
	registry := transport.NewRegistry()
	if err := registry.Register(v2ExecutorTransport{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	executionEngine, err := engine.New(selector, registry, service.NewBillingService())
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewV2Executor(executionEngine)
	if err != nil {
		t.Fatal(err)
	}
	store := false
	request := &protocol.Request{Model: "public", Input: json.RawMessage(`"hello"`), PreviousResponseID: "resp_previous", Store: &store}
	result, err := executor.Execute(context.Background(), request, "resp_public", engine.ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Response.ID != "resp_public" || result.Response.Model != "public" || result.ProviderResponseID != "provider_id" {
		t.Fatalf("normalized response = %#v", result)
	}
	if result.Response.PreviousResponseID == nil || *result.Response.PreviousResponseID != "resp_previous" || result.Response.Store {
		t.Fatalf("request fields were not normalized: %#v", result.Response)
	}
	if result.Route != selector.route || result.Prepared.URL != "https://upstream.test/v1/responses" || selector.released != 7 {
		t.Fatalf("execution metadata = %#v released=%d", result, selector.released)
	}
}

func TestV2ExecutorRejectsStreaming(t *testing.T) {
	setupV2ExecutorDB(t)
	registry := transport.NewRegistry()
	registry.Freeze()
	executionEngine, err := engine.New(&v2ExecutorSelector{}, registry, service.NewBillingService())
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewV2Executor(executionEngine)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), &protocol.Request{Model: "m", Input: json.RawMessage(`"hello"`), Stream: true}, "resp_stream", engine.ExecuteOptions{})
	if err == nil {
		t.Fatal("streaming request was accepted")
	}
}

func setupV2ExecutorDB(t *testing.T) {
	t.Helper()
	database := openResponsesTestDB(t)
	if err := database.AutoMigrate(&model.ChannelRequestLog{}, &model.BillingLog{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(database)
}
