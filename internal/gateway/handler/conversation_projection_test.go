package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/pipeline"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
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
	handler := NewChatHandler(pipeline.New(executionEngine))
	router := projectionRouter(token, func(router *gin.Engine) {
		router.POST("/v1/chat/completions", handler.Completions)
	})

	owned := model.Conversation{UserID: token.UserID, TokenID: token.ID, Title: "owned", Status: 1}
	if err := db.Create(&owned).Error; err != nil {
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
	otherToken := model.Token{UserID: otherUser.ID, Selector: fmt.Sprintf("other-token-%d", owned.ID), Balance: decimal.NewFromInt(100), Status: 1}
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
	if selector.calls != 0 {
		t.Fatalf("invalid conversation IDs reached selector %d times", selector.calls)
	}
}

func TestAnthropicRejectsBodyConversationIDAndSkipsLegacyProjectionOnRoutingFailure(t *testing.T) {
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
	if selector.calls != 0 {
		t.Fatalf("body conversation_id reached selector %d times", selector.calls)
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
	if len(callID) != 36 || strings.HasPrefix(callID, "call_") {
		t.Fatalf("X-Prism-Call-ID = %q", callID)
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls=%d", selector.calls)
	}
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
