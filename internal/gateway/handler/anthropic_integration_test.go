package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewaytransport "github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var anthropicHandlerIntegrationID atomic.Uint64

type anthropicExecutionStub struct {
	request canonical.Request
	options engine.ExecuteOptions
	route   routing.RouteResult
}

func (stub *anthropicExecutionStub) Execute(ctx context.Context, request canonical.Request, options engine.ExecuteOptions) (*engine.Result, error) {
	if options.PrepareRoute != nil {
		var err error
		request, err = options.PrepareRoute(ctx, request, &stub.route)
		if err != nil {
			return nil, err
		}
	}
	stub.request = request.Clone()
	stub.options = options
	return &engine.Result{
		Response: &canonical.Response{
			ID: "msg_provider", ProviderResponseID: "msg_provider", Model: stub.route.VendorModel,
			Status: "completed", FinishReason: "stop",
			Output: []canonical.Item{{
				Type: "message", Role: canonical.RoleAssistant,
				Content: []canonical.Content{{Type: "output_text", Text: "canonical ok"}},
			}},
			Usage: &canonical.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5},
		},
		Route: &stub.route, CallID: options.CallID, RequestLogID: 17, AttemptID: 23,
	}, nil
}

func TestAnthropicMessagesDecodesAndEncodesCanonicalProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, token := setupAnthropicHandlerIntegrationDB(t)
	conversation := model.Conversation{UserID: token.UserID, TokenID: token.ID, Title: "messages", Status: 1}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	stub := &anthropicExecutionStub{route: routing.RouteResult{
		Transport: gatewaytransport.OpenAIChat, ModelName: "public-model", VendorModel: "vendor-model",
	}}
	router := gin.New()
	router.Use(middleware.RequestID())
	router.Use(func(context *gin.Context) {
		context.Set(middleware.ContextKeyTokenID, token.ID)
		context.Set(middleware.ContextKeyToken, token)
		context.Next()
	})
	router.POST("/v1/messages", (&AnthropicHandler{engine: stub}).Messages)

	request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"public-model","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"hello from anthropic"}]}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "messages-request")
	request.Header.Set(prismConversationIDHeader, fmt.Sprint(conversation.ID))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("X-Prism-Request-Log-ID") != "17" || response.Header().Get(prismConversationIDHeader) != fmt.Sprint(conversation.ID) {
		t.Fatalf("headers=%v", response.Header())
	}
	callID := response.Header().Get("X-Prism-Call-ID")
	if len(callID) != 36 || strings.HasPrefix(callID, "call_") {
		t.Fatalf("X-Prism-Call-ID = %q", callID)
	}
	var message struct {
		ID, Type, Role, Model string
		Content               []struct{ Type, Text string }
		Usage                 struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		}
	}
	decodeIntegrationJSON(t, response.Body.Bytes(), &message)
	if message.ID != "msg_provider" || message.Type != "message" || message.Role != "assistant" || message.Model != "public-model" ||
		len(message.Content) != 1 || message.Content[0].Type != "text" || message.Content[0].Text != "canonical ok" ||
		message.Usage.InputTokens != 3 || message.Usage.OutputTokens != 2 {
		t.Fatalf("unexpected Anthropic response: %s", response.Body.String())
	}
	if stub.request.Endpoint != canonical.EndpointAnthropic || stub.request.Model != "public-model" || stub.request.MaxOutputTokens == nil || *stub.request.MaxOutputTokens != 64 ||
		len(stub.request.Items) != 1 || stub.request.Items[0].Role != canonical.RoleUser || stub.request.Items[0].Content[0].Text != "hello from anthropic" {
		t.Fatalf("canonical request=%#v", stub.request)
	}
	if stub.options.CallID != callID || stub.options.RequestID != "messages-request" || stub.options.ConversationID != conversation.ID ||
		stub.options.ConversationInput == nil || stub.options.ConversationInput.ConversationID != conversation.ID {
		t.Fatalf("execute options=%#v", stub.options)
	}
}

func setupAnthropicHandlerIntegrationDB(t *testing.T) (*gorm.DB, *model.Token) {
	t.Helper()
	testID := anthropicHandlerIntegrationID.Add(1)
	dsn := fmt.Sprintf("file:anthropic_handler_integration_%d?mode=memory&cache=shared", testID)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{}, &model.Token{},
		&model.Conversation{}, &model.ConversationTurn{}, &model.ConversationItem{}, &model.Message{},
		&model.ConversationProjectionOutbox{},
	); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	balance := decimal.NewFromInt(1_000_000)
	user := model.User{Username: fmt.Sprintf("messages-user-%d", testID), Balance: balance, Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := &model.Token{UserID: user.ID, Selector: fmt.Sprintf("messages-token-%d", testID), Balance: balance, Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatal(err)
	}
	return db, token
}

func decodeIntegrationJSON(t *testing.T, raw []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode JSON %s: %v", raw, err)
	}
}
