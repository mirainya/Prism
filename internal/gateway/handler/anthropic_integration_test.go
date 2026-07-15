package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewaytransport "github.com/mirainya/Prism/internal/gateway/transport"
	googletransport "github.com/mirainya/Prism/internal/gateway/transport/google"
	opentransport "github.com/mirainya/Prism/internal/gateway/transport/openai"
	volcenginetransport "github.com/mirainya/Prism/internal/gateway/transport/volcengine"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var anthropicHandlerIntegrationID atomic.Uint64

type anthropicHandlerSelector struct {
	route    routing.RouteResult
	options  routing.RouteOptions
	releases int
}

func (s *anthropicHandlerSelector) SelectTransport(modelName string, _ routing.RouteRequirements, options routing.RouteOptions) (*routing.RouteResult, error) {
	if modelName != s.route.ModelName {
		return nil, fmt.Errorf("unexpected model %q", modelName)
	}
	s.options = options
	route := s.route
	return &route, nil
}

func (s *anthropicHandlerSelector) Release(uint) { s.releases++ }

func TestAnthropicMessagesConvertsAcrossUpstreamTransports(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name             string
		transportID      gatewaytransport.ID
		vendorModel      string
		upstreamPath     string
		upstreamResponse string
		responseID       string
		responseText     string
		inputTokens      int
		outputTokens     int
		newTransport     func(*http.Client) gatewaytransport.Transport
		assertRequest    func(*testing.T, *http.Request, []byte)
	}{
		{
			name:             "OpenAI Chat",
			transportID:      gatewaytransport.OpenAIChat,
			vendorModel:      "vendor-openai",
			upstreamPath:     "/v1/chat/completions",
			upstreamResponse: `{"id":"chatcmpl_1","object":"chat.completion","created":123,"model":"vendor-openai","choices":[{"index":0,"message":{"role":"assistant","content":"openai ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
			responseID:       "chatcmpl_1",
			responseText:     "openai ok",
			inputTokens:      3,
			outputTokens:     2,
			newTransport:     opentransport.NewChat,
			assertRequest: func(t *testing.T, request *http.Request, raw []byte) {
				t.Helper()
				if request.Header.Get("Authorization") != "Bearer provider-key" {
					t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
				}
				var body struct {
					Model               string `json:"model"`
					MaxCompletionTokens int    `json:"max_completion_tokens"`
					Messages            []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				decodeIntegrationJSON(t, raw, &body)
				if body.Model != "vendor-openai" || body.MaxCompletionTokens != 64 || len(body.Messages) != 1 || body.Messages[0].Role != "user" {
					t.Fatalf("unexpected OpenAI request: %s", raw)
				}
				var content string
				decodeIntegrationJSON(t, body.Messages[0].Content, &content)
				if content != "hello from anthropic" {
					t.Fatalf("OpenAI message content = %q", content)
				}
			},
		},
		{
			name:             "Google GenerateContent",
			transportID:      gatewaytransport.GoogleGenerateContent,
			vendorModel:      "vendor-google",
			upstreamPath:     "/v1beta/models/vendor-google:generateContent",
			upstreamResponse: `{"responseId":"gemini_1","modelVersion":"gemini-test","candidates":[{"content":{"role":"model","parts":[{"text":"google ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":3,"totalTokenCount":7}}`,
			responseID:       "gemini_1",
			responseText:     "google ok",
			inputTokens:      4,
			outputTokens:     3,
			newTransport:     googletransport.NewGenerateContent,
			assertRequest: func(t *testing.T, request *http.Request, raw []byte) {
				t.Helper()
				if request.URL.Query().Get("key") != "provider-key" {
					t.Fatalf("Google key query = %q", request.URL.Query().Get("key"))
				}
				var body struct {
					Contents []struct {
						Role  string `json:"role"`
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"contents"`
					GenerationConfig struct {
						MaxOutputTokens int `json:"maxOutputTokens"`
					} `json:"generationConfig"`
				}
				decodeIntegrationJSON(t, raw, &body)
				if len(body.Contents) != 1 || body.Contents[0].Role != "user" || len(body.Contents[0].Parts) != 1 || body.Contents[0].Parts[0].Text != "hello from anthropic" || body.GenerationConfig.MaxOutputTokens != 64 {
					t.Fatalf("unexpected Google request: %s", raw)
				}
			},
		},
		{
			name:             "Volcengine Responses v3",
			transportID:      gatewaytransport.VolcengineResponsesV3,
			vendorModel:      "vendor-volcengine",
			upstreamPath:     "/api/v3/responses",
			upstreamResponse: `{"id":"resp_volcengine_1","object":"response","status":"completed","model":"vendor-volcengine","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"volcengine ok"}]}],"usage":{"input_tokens":5,"output_tokens":4,"total_tokens":9}}`,
			responseID:       "resp_volcengine_1",
			responseText:     "volcengine ok",
			inputTokens:      5,
			outputTokens:     4,
			newTransport: func(client *http.Client) gatewaytransport.Transport {
				return volcenginetransport.New(gatewaytransport.HTTPClient{Client: client})
			},
			assertRequest: func(t *testing.T, request *http.Request, raw []byte) {
				t.Helper()
				if request.Header.Get("Authorization") != "Bearer provider-key" {
					t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
				}
				var body struct {
					Model           string `json:"model"`
					MaxOutputTokens int    `json:"max_output_tokens"`
					Input           []struct {
						Type    string `json:"type"`
						Role    string `json:"role"`
						Content []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					} `json:"input"`
				}
				decodeIntegrationJSON(t, raw, &body)
				if body.Model != "vendor-volcengine" || body.MaxOutputTokens != 64 || len(body.Input) != 1 || body.Input[0].Type != "message" || body.Input[0].Role != "user" || len(body.Input[0].Content) != 1 || body.Input[0].Content[0].Type != "input_text" || body.Input[0].Content[0].Text != "hello from anthropic" {
					t.Fatalf("unexpected Volcengine request: %s", raw)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				upstreamCalls.Add(1)
				raw, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read upstream request: %v", err)
					http.Error(writer, "invalid request", http.StatusBadRequest)
					return
				}
				if request.Method != http.MethodPost || request.URL.Path != test.upstreamPath {
					t.Errorf("upstream request = %s %s, want POST %s", request.Method, request.URL.Path, test.upstreamPath)
				}
				test.assertRequest(t, request, raw)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.upstreamResponse)
			}))
			defer upstream.Close()

			db, token := setupAnthropicHandlerIntegrationDB(t)
			registry := gatewaytransport.NewRegistry()
			if err := registry.Register(test.newTransport(upstream.Client())); err != nil {
				t.Fatal(err)
			}
			registry.Freeze()
			selector := &anthropicHandlerSelector{route: routing.RouteResult{
				AbilityID: 1, ChannelID: 2, KeyID: 3,
				Transport: test.transportID, BaseURL: upstream.URL, APIKey: "provider-key",
				ModelName: "public-model", VendorModel: test.vendorModel,
				PriceMode: "token", InputPrice: decimal.Zero, OutputPrice: decimal.Zero,
			}}
			executionEngine, err := engine.New(selector, registry, service.NewBillingService(), service.NewAPICallService())
			if err != nil {
				t.Fatal(err)
			}

			router := gin.New()
			router.Use(middleware.RequestID())
			router.Use(func(context *gin.Context) {
				context.Set(middleware.ContextKeyTokenID, token.ID)
				context.Set(middleware.ContextKeyToken, token)
				context.Next()
			})
			router.POST("/v1/messages", NewAnthropicHandler(executionEngine).Messages)

			request := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"public-model","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"hello from anthropic"}]}]}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Request-ID", "messages-request")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
			}
			if calls := upstreamCalls.Load(); calls != 1 || selector.releases != 1 || !containsTransport(selector.options.AllowedTransports, test.transportID) {
				t.Fatalf("calls=%d releases=%d allowed=%v", calls, selector.releases, selector.options.AllowedTransports)
			}
			if response.Header().Get("X-Prism-Request-Log-ID") == "" {
				t.Fatal("X-Prism-Request-Log-ID is missing")
			}
			callID := response.Header().Get("X-Prism-Call-ID")
			if !strings.HasPrefix(callID, "call_") {
				t.Fatalf("X-Prism-Call-ID = %q", callID)
			}

			var message struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				Role    string `json:"role"`
				Model   string `json:"model"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			decodeIntegrationJSON(t, response.Body.Bytes(), &message)
			if message.ID != test.responseID || message.Type != "message" || message.Role != "assistant" || message.Model != "public-model" || len(message.Content) != 1 || message.Content[0].Type != "text" || message.Content[0].Text != test.responseText || message.Usage.InputTokens != test.inputTokens || message.Usage.OutputTokens != test.outputTokens {
				t.Fatalf("unexpected Anthropic response: %s", response.Body.String())
			}

			var requestLog model.ChannelRequestLog
			if err := db.Order("id DESC").First(&requestLog).Error; err != nil {
				t.Fatal(err)
			}
			if requestLog.UpstreamTransport != test.transportID || requestLog.RequestPath != test.upstreamPath || requestLog.StatusCode != http.StatusOK {
				t.Fatalf("unexpected request log: %#v", requestLog)
			}

			var call model.APICall
			if err := db.First(&call, "id = ?", callID).Error; err != nil {
				t.Fatal(err)
			}
			if call.RequestID != "messages-request" || call.Endpoint != "/v1/messages" ||
				call.UserID != token.UserID || call.TokenID != token.ID || call.Model != "public-model" ||
				call.Status != model.APICallStatusCompleted || call.AttemptCount != 1 || call.FinalAttemptID == 0 {
				t.Fatalf("unexpected API call: %#v", call)
			}
			var attempt model.APICallAttempt
			if err := db.First(&attempt, call.FinalAttemptID).Error; err != nil {
				t.Fatal(err)
			}
			if attempt.CallID != call.ID || attempt.Transport != test.transportID || attempt.Status != model.APICallAttemptStatusCompleted {
				t.Fatalf("unexpected API call attempt: %#v", attempt)
			}
		})
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
		&model.User{}, &model.Token{}, &model.BillingLog{}, &model.ChannelRequestLog{},
		&model.APICall{}, &model.APICallAttempt{}, &model.BalanceEntry{},
	); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	balance := decimal.NewFromInt(1_000_000)
	user := model.User{Username: fmt.Sprintf("messages-user-%d", testID), Balance: balance, Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := &model.Token{UserID: user.ID, Key: fmt.Sprintf("messages-token-%d", testID), Balance: balance, Status: 1}
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

func containsTransport(transports []model.UpstreamTransport, expected model.UpstreamTransport) bool {
	for _, item := range transports {
		if item == expected {
			return true
		}
	}
	return false
}
