package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"gorm.io/gorm"
)

func TestParsePrismConversationID(t *testing.T) {
	tests := []struct {
		name       string
		body       json.RawMessage
		header     string
		expected   uint
		shouldFail bool
	}{
		{name: "numeric body", body: json.RawMessage(`12`), expected: 12},
		{name: "numeric string body", body: json.RawMessage(`"12"`), expected: 12},
		{name: "matching header", body: json.RawMessage(`12`), header: "012", expected: 12},
		{name: "header only", header: "13", expected: 13},
		{name: "conflict", body: json.RawMessage(`12`), header: "13", shouldFail: true},
		{name: "zero", body: json.RawMessage(`0`), shouldFail: true},
		{name: "object", body: json.RawMessage(`{}`), shouldFail: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := parseJSONConversationID(test.body)
			if err == nil {
				var conversationID uint
				conversationID, err = parsePrismConversationID(body, test.header)
				if err == nil && conversationID != test.expected {
					t.Fatalf("conversation ID = %d, want %d", conversationID, test.expected)
				}
			}
			if test.shouldFail != (err != nil) {
				t.Fatalf("error = %v, shouldFail=%v", err, test.shouldFail)
			}
		})
	}
}

func TestChatConversationIDValidationPrecedesExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, token := setupAnthropicHandlerIntegrationDB(t)
	handler := newProjectionChatHandler(t, chatCallTransport{})
	router := projectionRouter(token, func(router *gin.Engine) {
		router.POST("/v1/chat/completions", handler.Completions)
	})

	owned := model.Conversation{UserID: token.UserID, TokenID: token.ID, Title: "owned", Status: 1}
	if err := db.Create(&owned).Error; err != nil {
		t.Fatal(err)
	}
	valid := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"conversation_id":%d}`, owned.ID)))
	valid.Header.Set("Content-Type", "application/json")
	valid.Header.Set(prismConversationIDHeader, fmt.Sprintf("%d", owned.ID))
	validResponse := httptest.NewRecorder()
	router.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusOK || validResponse.Header().Get(prismConversationIDHeader) != fmt.Sprint(owned.ID) {
		t.Fatalf("valid response: status=%d headers=%v body=%s", validResponse.Code, validResponse.Header(), validResponse.Body.String())
	}
	var validCall model.APICall
	if err := db.First(&validCall, "id = ?", validResponse.Header().Get("X-Prism-Call-ID")).Error; err != nil {
		t.Fatal(err)
	}
	if validCall.ConversationID != owned.ID {
		t.Fatalf("call conversation_id = %d, want %d", validCall.ConversationID, owned.ID)
	}

	var callsBefore int64
	if err := db.Model(&model.APICall{}).Count(&callsBefore).Error; err != nil {
		t.Fatal(err)
	}
	conflict := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}],"conversation_id":"%d"}`, owned.ID)))
	conflict.Header.Set("Content-Type", "application/json")
	conflict.Header.Set(prismConversationIDHeader, fmt.Sprintf("%d", owned.ID+1))
	conflictResponse := httptest.NewRecorder()
	router.ServeHTTP(conflictResponse, conflict)
	if conflictResponse.Code != http.StatusBadRequest {
		t.Fatalf("conflict status=%d body=%s", conflictResponse.Code, conflictResponse.Body.String())
	}

	otherUser := model.User{Username: fmt.Sprintf("other-%d", owned.ID), Balance: decimal.NewFromInt(100), Status: 1}
	if err := db.Create(&otherUser).Error; err != nil {
		t.Fatal(err)
	}
	otherToken := model.Token{UserID: otherUser.ID, Key: fmt.Sprintf("other-token-%d", owned.ID), Balance: decimal.NewFromInt(100), Status: 1}
	if err := db.Create(&otherToken).Error; err != nil {
		t.Fatal(err)
	}
	foreign := model.Conversation{UserID: otherUser.ID, TokenID: otherToken.ID, Title: "foreign", Status: 1}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	foreignRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"public-model","messages":[{"role":"user","content":"hi"}]}`))
	foreignRequest.Header.Set("Content-Type", "application/json")
	foreignRequest.Header.Set(prismConversationIDHeader, fmt.Sprint(foreign.ID))
	foreignResponse := httptest.NewRecorder()
	router.ServeHTTP(foreignResponse, foreignRequest)
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign status=%d body=%s", foreignResponse.Code, foreignResponse.Body.String())
	}
	var callsAfter int64
	if err := db.Model(&model.APICall{}).Count(&callsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if callsAfter != callsBefore {
		t.Fatalf("invalid conversation IDs executed calls: before=%d after=%d", callsBefore, callsAfter)
	}
}

func TestAnthropicRejectsBodyConversationIDAndProjectsExecutionFailure(t *testing.T) {
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
	router := projectionRouter(token, func(router *gin.Engine) {
		router.POST("/v1/messages", NewAnthropicHandler(executionEngine).Messages)
	})

	bodyID := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"public-model","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"conversation_id":1}`))
	bodyID.Header.Set("Content-Type", "application/json")
	bodyIDResponse := httptest.NewRecorder()
	router.ServeHTTP(bodyIDResponse, bodyID)
	if bodyIDResponse.Code != http.StatusBadRequest {
		t.Fatalf("body conversation_id status=%d body=%s", bodyIDResponse.Code, bodyIDResponse.Body.String())
	}
	var count int64
	if err := db.Model(&model.APICall{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("calls after rejected body conversation_id = %d, err=%v", count, err)
	}

	failed := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(
		`{"model":"public-model","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	failed.Header.Set("Content-Type", "application/json")
	failedResponse := httptest.NewRecorder()
	router.ServeHTTP(failedResponse, failed)
	if failedResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed status=%d body=%s", failedResponse.Code, failedResponse.Body.String())
	}
	callID := failedResponse.Header().Get("X-Prism-Call-ID")
	var call model.APICall
	if err := db.First(&call, "id = ?", callID).Error; err != nil {
		t.Fatal(err)
	}
	turn, items := loadProjectedTurn(t, db, callID)
	if call.Status != model.APICallStatusFailed || turn.Status != model.ConversationTurnFailed ||
		!containsCanonicalText(items, model.ConversationItemInput, "hi") {
		t.Fatalf("unexpected Anthropic failure projection: call=%#v turn=%#v items=%#v", call, turn, items)
	}
}

func TestStreamingConversationProjectionMarksClientDisconnect(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		bind func(*gin.Engine, *engine.Engine)
	}{
		{
			name: "Chat", path: "/v1/chat/completions",
			body: `{"model":"public-model","messages":[{"role":"user","content":"hi"}],"stream":true}`,
			bind: func(router *gin.Engine, executionEngine *engine.Engine) {
				router.POST("/v1/chat/completions", NewChatHandler(pipeline.New(executionEngine)).Completions)
			},
		},
		{
			name: "Anthropic", path: "/v1/messages",
			body: `{"model":"public-model","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"stream":true}`,
			bind: func(router *gin.Engine, executionEngine *engine.Engine) {
				router.POST("/v1/messages", NewAnthropicHandler(executionEngine).Messages)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			db, token := setupAnthropicHandlerIntegrationDB(t)
			executionEngine := newProjectionEngine(t, projectionStreamTransport{events: []canonical.Event{
				{Type: canonical.EventTextDelta, OutputIndex: 0, ContentIndex: 0, Delta: "partial"},
				{Type: canonical.EventCompleted, ProviderResponseID: "provider-stream", Response: &canonical.Response{ID: "provider-stream", Status: "completed", FinishReason: "stop"}, Usage: &canonical.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}},
			}})
			router := gin.New()
			router.Use(middleware.RequestID())
			router.Use(func(c *gin.Context) {
				c.Set(middleware.ContextKeyTokenID, token.ID)
				c.Set(middleware.ContextKeyToken, token)
				c.Writer = &failingChatResponseWriter{ResponseWriter: c.Writer}
				c.Next()
			})
			test.bind(router, executionEngine)
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			callID := response.Header().Get("X-Prism-Call-ID")
			var call model.APICall
			if err := db.First(&call, "id = ?", callID).Error; err != nil {
				t.Fatal(err)
			}
			if call.Status != model.APICallStatusCancelled || !call.ClientDisconnected {
				t.Fatalf("call after disconnect: %#v", call)
			}
			turn, items := loadProjectedTurn(t, db, callID)
			if turn.Status != model.ConversationTurnAborted {
				t.Fatalf("turn status = %s, want aborted", turn.Status)
			}
			if !containsCanonicalText(items, model.ConversationItemOutput, "partial") {
				t.Fatalf("partial canonical output was not retained: %#v", items)
			}
		})
	}
}

type projectionStreamTransport struct {
	events []canonical.Event
}

func (projectionStreamTransport) ID() transport.ID { return transport.OpenAIChat }

func (projectionStreamTransport) Plan(operation transport.Operation, _ canonical.Request, features canonical.FeatureSet) transport.Plan {
	return transport.Exact(operation, features)
}

func (projectionStreamTransport) Prepare(_ context.Context, _ transport.Invocation) (transport.PreparedRequest, error) {
	return transport.PreparedRequest{Method: http.MethodPost, URL: "https://upstream.test/v1/chat/completions", Headers: http.Header{}, Body: []byte(`{}`)}, nil
}

func (projectionStreamTransport) ExecutePrepared(context.Context, transport.Invocation, transport.PreparedRequest) (canonical.Response, error) {
	panic("unexpected non-stream execution")
}

func (transportImpl projectionStreamTransport) StreamPrepared(context.Context, transport.Invocation, transport.PreparedRequest) (transport.EventStream, error) {
	return &projectionEventStream{events: append([]canonical.Event(nil), transportImpl.events...)}, nil
}

type projectionEventStream struct {
	events []canonical.Event
	index  int
}

func (stream *projectionEventStream) Next(context.Context) (canonical.Event, error) {
	if stream.index >= len(stream.events) {
		return canonical.Event{}, io.EOF
	}
	event := stream.events[stream.index]
	stream.index++
	return event, nil
}

func (*projectionEventStream) Close() error { return nil }

func newProjectionChatHandler(t *testing.T, transportImpl transport.Transport) *ChatHandler {
	t.Helper()
	return NewChatHandler(pipeline.New(newProjectionEngine(t, transportImpl)))
}

func newProjectionEngine(t *testing.T, transportImpl transport.Transport) *engine.Engine {
	t.Helper()
	registry := transport.NewRegistry()
	if err := registry.Register(transportImpl); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	executionEngine, err := engine.New(&chatCallSelector{route: routing.RouteResult{
		AbilityID: 1, ChannelID: 2, KeyID: 3, Protocol: model.ProtocolOpenAI,
		Transport: transport.OpenAIChat, ModelName: "public-model", VendorModel: "vendor-model",
		BaseURL: "https://upstream.test", PriceMode: "token", InputPrice: decimal.Zero, OutputPrice: decimal.Zero,
	}}, registry, service.NewBillingService(), service.NewAPICallService())
	if err != nil {
		t.Fatal(err)
	}
	return executionEngine
}

func projectionRouter(token *model.Token, bind func(*gin.Engine)) *gin.Engine {
	router := gin.New()
	router.Use(middleware.RequestID())
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyTokenID, token.ID)
		c.Set(middleware.ContextKeyToken, token)
		c.Next()
	})
	bind(router)
	return router
}

func loadProjectedTurn(t *testing.T, db *gorm.DB, callID string) (model.ConversationTurn, []model.ConversationItem) {
	t.Helper()
	var turn model.ConversationTurn
	if err := db.First(&turn, "call_id = ?", callID).Error; err != nil {
		t.Fatal(err)
	}
	var items []model.ConversationItem
	if err := db.Where("turn_id = ?", turn.ID).Order("ordinal ASC").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	return turn, items
}

func containsCanonicalText(items []model.ConversationItem, direction, expected string) bool {
	for _, record := range items {
		if record.Direction != direction {
			continue
		}
		var item canonical.Item
		if json.Unmarshal(record.CanonicalJSON, &item) != nil {
			continue
		}
		for _, content := range item.Content {
			if content.Text == expected {
				return true
			}
		}
	}
	return false
}
