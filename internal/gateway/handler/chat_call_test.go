package handler

import (
	"context"
	"errors"
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
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
)

type chatCallSelector struct {
	route routing.RouteResult
	err   error
}

func (s *chatCallSelector) SelectTransport(_ string, _ routing.RouteRequirements, _ routing.RouteOptions) (*routing.RouteResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	result := s.route
	return &result, nil
}

func (s *chatCallSelector) Release(uint) {}

type chatCallTransport struct{}

var errChatDownstreamWrite = errors.New("downstream writer failed")

type failingChatResponseWriter struct{ gin.ResponseWriter }

func (w *failingChatResponseWriter) Write([]byte) (int, error) {
	return 0, errChatDownstreamWrite
}

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

func TestChatCompletionsRecordsCallContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, token := setupAnthropicHandlerIntegrationDB(t)
	registry := transport.NewRegistry()
	if err := registry.Register(chatCallTransport{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	executionEngine, err := engine.New(
		&chatCallSelector{route: routing.RouteResult{
			AbilityID: 1, ChannelID: 2, KeyID: 3, Protocol: model.ProtocolOpenAI,
			Transport: transport.OpenAIChat, ModelName: "public-model", VendorModel: "vendor-model",
			BaseURL: "https://upstream.test", PriceMode: "token",
			InputPrice: decimal.Zero, OutputPrice: decimal.Zero,
		}},
		registry,
		service.NewBillingService(),
		service.NewAPICallService(),
	)
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
	request.Header.Set("X-Request-ID", "chat-request")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	callID := response.Header().Get("X-Prism-Call-ID")
	if !strings.HasPrefix(callID, "call_") {
		t.Fatalf("X-Prism-Call-ID = %q", callID)
	}
	var call model.APICall
	if err := db.First(&call, "id = ?", callID).Error; err != nil {
		t.Fatal(err)
	}
	if call.RequestID != "chat-request" || call.Endpoint != "/v1/chat/completions" ||
		call.UserID != token.UserID || call.TokenID != token.ID || call.Model != "public-model" ||
		call.Status != model.APICallStatusCompleted || call.AttemptCount != 1 || call.TotalTokens != 3 {
		t.Fatalf("unexpected API call: %#v", call)
	}
	var attempt model.APICallAttempt
	if err := db.First(&attempt, call.FinalAttemptID).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.CallID != call.ID || attempt.Status != model.APICallAttemptStatusCompleted || attempt.TotalTokens != 3 {
		t.Fatalf("unexpected API call attempt: %#v", attempt)
	}
}

func TestChatCompletionsCancelsCallWhenClientWriteFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, token := setupAnthropicHandlerIntegrationDB(t)
	registry := transport.NewRegistry()
	if err := registry.Register(chatCallTransport{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	executionEngine, err := engine.New(
		&chatCallSelector{route: routing.RouteResult{
			AbilityID: 1, ChannelID: 2, KeyID: 3, Protocol: model.ProtocolOpenAI,
			Transport: transport.OpenAIChat, ModelName: "public-model", VendorModel: "vendor-model",
			BaseURL: "https://upstream.test", PriceMode: "token",
			InputPrice: decimal.Zero, OutputPrice: decimal.Zero,
		}},
		registry,
		service.NewBillingService(),
		service.NewAPICallService(),
	)
	if err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	router.Use(middleware.RequestID())
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyTokenID, token.ID)
		c.Set(middleware.ContextKeyToken, token)
		c.Writer = &failingChatResponseWriter{ResponseWriter: c.Writer}
		c.Next()
	})
	router.POST("/v1/chat/completions", NewChatHandler(pipeline.New(executionEngine)).Completions)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"public-model","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	callID := response.Header().Get("X-Prism-Call-ID")
	var call model.APICall
	if err := db.First(&call, "id = ?", callID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCancelled || !call.ClientDisconnected || call.FinalAttemptID == 0 {
		t.Fatalf("call after downstream write failure: %#v", call)
	}
	var attempt model.APICallAttempt
	if err := db.First(&attempt, call.FinalAttemptID).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.Status != model.APICallAttemptStatusCompleted {
		t.Fatalf("upstream attempt changed after downstream write failure: %#v", attempt)
	}
}

func TestChatCompletionsKeepsCallIDOnRoutingFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, token := setupAnthropicHandlerIntegrationDB(t)
	registry := transport.NewRegistry()
	if err := registry.Register(chatCallTransport{}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	executionEngine, err := engine.New(
		&chatCallSelector{err: routing.ErrNoRoute}, registry,
		service.NewBillingService(), service.NewAPICallService(),
	)
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
	if !strings.HasPrefix(callID, "call_") {
		t.Fatalf("X-Prism-Call-ID = %q", callID)
	}
	var call model.APICall
	if err := db.First(&call, "id = ?", callID).Error; err != nil {
		t.Fatal(err)
	}
	if call.RequestID != "chat-routing-failure" || call.Status != model.APICallStatusFailed ||
		call.ErrorCode != "model_unavailable" || call.AttemptCount != 0 {
		t.Fatalf("unexpected failed API call: %#v", call)
	}
}
