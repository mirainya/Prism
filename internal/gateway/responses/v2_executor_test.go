package responses

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

type v2ExecutorEngine struct {
	result  *engine.Result
	err     error
	request canonical.Request
}

func (e *v2ExecutorEngine) Execute(_ context.Context, request canonical.Request, _ engine.ExecuteOptions) (*engine.Result, error) {
	e.request = request
	return e.result, e.err
}

func TestV2ExecutorNormalizesPublicResponse(t *testing.T) {
	route := &routing.RouteResult{KeyID: 7, ModelName: "public", VendorModel: "vendor", Transport: transport.OpenAIResponses}
	prepared := transport.PreparedRequest{Method: "POST", URL: "https://upstream.test/v1/responses"}
	backend := &v2ExecutorEngine{result: &engine.Result{
		Response: &canonical.Response{
			ID: "provider_id", ProviderResponseID: "provider_id", Model: "vendor", Status: "completed", CreatedAt: 123,
			Output: []canonical.Item{{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "output_text", Text: "ok"}}}},
			Usage:  &canonical.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		},
		Route: route, Prepared: prepared, RequestLogID: 9, AttemptID: 11,
	}}
	executor := &V2Executor{engine: backend}
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
	if result.Route != route || result.Prepared.Method != prepared.Method || result.Prepared.URL != prepared.URL || result.RequestLogID != 9 || result.AttemptID != 11 {
		t.Fatalf("execution metadata = %#v", result)
	}
	if backend.request.Endpoint != canonical.EndpointOpenAIResponses || backend.request.Model != "public" || backend.request.Stream {
		t.Fatalf("canonical request = %#v", backend.request)
	}
}

func TestV2ExecutorRejectsStreaming(t *testing.T) {
	backend := &v2ExecutorEngine{err: errors.New("must not execute")}
	executor := &V2Executor{engine: backend}
	_, err := executor.Execute(context.Background(), &protocol.Request{Model: "m", Input: json.RawMessage(`"hello"`), Stream: true}, "resp_stream", engine.ExecuteOptions{})
	if err == nil {
		t.Fatal("streaming request was accepted")
	}
	if backend.request.Model != "" {
		t.Fatal("streaming request reached Gateway Engine")
	}
}
