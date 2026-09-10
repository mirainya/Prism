package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/engine"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

func TestResponsesConversationHeaderRejectsInvalidValue(t *testing.T) {
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
	handler := NewResponsesHandler(responsepipeline.New(executionEngine))
	router := projectionRouter(token, func(router *gin.Engine) {
		router.POST("/v1/responses", handler.Create)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(
		`{"model":"public-model","input":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(prismConversationIDHeader, "not-a-number")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || selector.calls != 0 {
		t.Fatalf("status=%d selector_calls=%d body=%s", response.Code, selector.calls, response.Body.String())
	}
}
