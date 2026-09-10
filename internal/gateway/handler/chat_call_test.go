package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/pipeline"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

type chatCallSelector struct {
	route routing.RouteResult
	err   error
	calls int
}

func (s *chatCallSelector) SelectTransport(_ context.Context, _ string, _ routing.RouteRequirements, _ routing.RouteOptions) (*routing.RouteResult, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	result := s.route
	return &result, nil
}

func (s *chatCallSelector) Release(uint) {}

type chatCallTransport struct{}

func (chatCallTransport) ID() transport.ID { return transport.OpenAIChat }

func (chatCallTransport) Plan(operation transport.Operation, _ canonical.Request, features canonical.FeatureSet) transport.Plan {
	return transport.Exact(operation, features)
}

func (chatCallTransport) Prepare(_ context.Context, _ transport.Invocation) (transport.PreparedRequest, error) {
	return transport.PreparedRequest{
		Method: http.MethodPost, URL: "https://upstream.test/v1/chat/completions",
		Headers: http.Header{}, Body: []byte(`{}`),
	}, nil
}

func (chatCallTransport) ExecutePrepared(_ context.Context, _ transport.Invocation, _ transport.PreparedRequest) (canonical.Response, error) {
	return canonical.Response{
		ID: "chatcmpl_test", Status: "completed", FinishReason: "stop",
		Output: []canonical.Item{{
			Type: "message", Role: canonical.RoleAssistant,
			Content: []canonical.Content{{Type: "output_text", Text: "hello"}},
		}},
		Usage: &canonical.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3},
	}, nil
}

func (chatCallTransport) StreamPrepared(context.Context, transport.Invocation, transport.PreparedRequest) (transport.EventStream, error) {
	panic("unexpected stream")
}

func TestChatCompletionsReportsRoutingFailureWithCallID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, token := setupAnthropicHandlerIntegrationDB(t)
	registry := transport.NewRegistry()
	if err := registry.Register(chatCallTransport{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	selector := &chatCallSelector{err: routing.ErrNoRoute}
	executionEngine, err := engine.New(selector, registry)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(middleware.RequestID())
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyTokenID, token.ID)
		c.Set(middleware.ContextKeyToken, token)
		c.Next()
	})
	router.POST("/v1/chat/completions", NewChatHandler(pipeline.New(executionEngine)).Completions)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public-model","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "chat-routing-failure")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	callID := response.Header().Get("X-Prism-Call-ID")
	if len(callID) != 36 || strings.HasPrefix(callID, "call_") {
		t.Fatalf("X-Prism-Call-ID = %q", callID)
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls=%d", selector.calls)
	}
}
