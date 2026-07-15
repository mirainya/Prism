package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
)

type responsesConversationCaptureTransport struct {
	mu       sync.Mutex
	requests []canonical.Request
}

func (*responsesConversationCaptureTransport) ID() transport.ID { return transport.OpenAIChat }

func (*responsesConversationCaptureTransport) Plan(operation transport.Operation, _ canonical.Request, features canonical.FeatureSet) transport.Plan {
	return transport.Exact(operation, features)
}

func (*responsesConversationCaptureTransport) Prepare(_ context.Context, _ transport.Invocation) (transport.PreparedRequest, error) {
	return transport.PreparedRequest{
		Method: http.MethodPost, URL: "https://upstream.test/v1/chat/completions",
		Headers: http.Header{}, Body: []byte(`{}`),
	}, nil
}

func (transportImpl *responsesConversationCaptureTransport) ExecutePrepared(_ context.Context, invocation transport.Invocation, _ transport.PreparedRequest) (canonical.Response, error) {
	transportImpl.mu.Lock()
	transportImpl.requests = append(transportImpl.requests, invocation.Request.Clone())
	transportImpl.mu.Unlock()
	return canonical.Response{
		ID: "provider-response", ProviderResponseID: "provider-response", Status: "completed", FinishReason: "stop",
		Output: []canonical.Item{{
			Type: "message", Role: canonical.RoleAssistant,
			Content: []canonical.Content{{Type: "output_text", Text: "done"}},
		}},
		Usage: &canonical.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
	}, nil
}

func (*responsesConversationCaptureTransport) StreamPrepared(context.Context, transport.Invocation, transport.PreparedRequest) (transport.EventStream, error) {
	panic("unexpected stream")
}

func (transportImpl *responsesConversationCaptureTransport) captured() []canonical.Request {
	transportImpl.mu.Lock()
	defer transportImpl.mu.Unlock()
	result := make([]canonical.Request, len(transportImpl.requests))
	for index := range transportImpl.requests {
		result[index] = transportImpl.requests[index].Clone()
	}
	return result
}

func TestResponsesConversationHeaderIsSeparateFromUpstreamConversation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, token := setupAnthropicHandlerIntegrationDB(t)
	if err := db.AutoMigrate(&model.AIResponse{}, &model.AIResponseIdempotencyCache{}, &model.APICallPayload{}); err != nil {
		t.Fatal(err)
	}
	owned := model.Conversation{UserID: token.UserID, TokenID: token.ID, Title: "owned", Status: 1}
	if err := db.Create(&owned).Error; err != nil {
		t.Fatal(err)
	}
	upstream := &responsesConversationCaptureTransport{}
	handler := NewResponsesHandler(responsepipeline.New(newProjectionEngine(t, upstream)))
	router := projectionRouter(token, func(router *gin.Engine) {
		router.POST("/v1/responses", handler.Create)
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"public-model","input":"hello","conversation":"conv_upstream"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(prismConversationIDHeader, fmt.Sprint(owned.ID))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get(prismConversationIDHeader) != fmt.Sprint(owned.ID) {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	callID := response.Header().Get("X-Prism-Call-ID")
	var call model.APICall
	if err := db.First(&call, "id = ?", callID).Error; err != nil {
		t.Fatal(err)
	}
	if call.ConversationID != owned.ID {
		t.Fatalf("call conversation_id=%d want=%d", call.ConversationID, owned.ID)
	}
	turn, _ := loadProjectedTurn(t, db, callID)
	if turn.ConversationID != owned.ID {
		t.Fatalf("turn conversation_id=%d want=%d", turn.ConversationID, owned.ID)
	}
	captured := upstream.captured()
	if len(captured) != 1 {
		t.Fatalf("upstream calls=%d", len(captured))
	}
	extras := captured[0].ClientExtensions["openai_responses.request_extras"]
	if !strings.Contains(string(extras), `"conversation":"conv_upstream"`) {
		t.Fatalf("native conversation was not preserved: %s", extras)
	}

	nativeNumeric := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"public-model","input":"native numeric","conversation":999999}`))
	nativeNumeric.Header.Set("Content-Type", "application/json")
	nativeResponse := httptest.NewRecorder()
	router.ServeHTTP(nativeResponse, nativeNumeric)
	if nativeResponse.Code != http.StatusOK {
		t.Fatalf("native numeric status=%d body=%s", nativeResponse.Code, nativeResponse.Body.String())
	}

	otherUser := model.User{Username: "responses-other", Status: 1}
	if err := db.Create(&otherUser).Error; err != nil {
		t.Fatal(err)
	}
	otherToken := model.Token{UserID: otherUser.ID, Key: "responses-other-token", Status: 1}
	if err := db.Create(&otherToken).Error; err != nil {
		t.Fatal(err)
	}
	foreign := model.Conversation{UserID: otherUser.ID, TokenID: otherToken.ID, Title: "foreign", Status: 1}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	before := len(upstream.captured())
	invalid := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"public-model","input":"invalid"}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid.Header.Set(prismConversationIDHeader, fmt.Sprint(foreign.ID))
	invalidResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
	if len(upstream.captured()) != before {
		t.Fatal("foreign conversation reached upstream")
	}
}

func TestResponsesConversationHeaderRejectsInvalidValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, token := setupAnthropicHandlerIntegrationDB(t)
	if err := db.AutoMigrate(&model.AIResponse{}, &model.AIResponseIdempotencyCache{}, &model.APICallPayload{}); err != nil {
		t.Fatal(err)
	}
	upstream := &responsesConversationCaptureTransport{}
	handler := NewResponsesHandler(responsepipeline.New(newProjectionEngine(t, upstream)))
	router := projectionRouter(token, func(router *gin.Engine) {
		router.POST("/v1/responses", handler.Create)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"public-model","input":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(prismConversationIDHeader, "not-a-number")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || len(upstream.captured()) != 0 {
		t.Fatalf("status=%d upstream=%d body=%s", response.Code, len(upstream.captured()), response.Body.String())
	}
	var calls int64
	if err := db.Model(&model.APICall{}).Count(&calls).Error; err != nil || calls != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
