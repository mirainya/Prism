package responses

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type pipelineV2Selector struct{ route *routing.RouteResult }

func (s *pipelineV2Selector) SelectTransport(_ string, _ routing.RouteRequirements, _ routing.RouteOptions) (*routing.RouteResult, error) {
	copy := *s.route
	return &copy, nil
}
func (s *pipelineV2Selector) Release(uint) {}

type pipelineV2Transport struct {
	id       transport.ID
	response canonical.Response
	mu       sync.Mutex
	request  canonical.Request
}

func (t *pipelineV2Transport) ID() transport.ID { return t.id }
func (t *pipelineV2Transport) Plan(operation transport.Operation, _ canonical.Request, features canonical.FeatureSet) transport.Plan {
	return transport.Exact(operation, features)
}
func (t *pipelineV2Transport) Prepare(_ context.Context, invocation transport.Invocation) (transport.PreparedRequest, error) {
	return transport.PreparedRequest{Method: http.MethodPost, URL: invocation.Route.BaseURL + "/execute", Headers: http.Header{}, Body: []byte(`{}`)}, nil
}
func (t *pipelineV2Transport) ExecutePrepared(_ context.Context, invocation transport.Invocation, _ transport.PreparedRequest) (canonical.Response, error) {
	t.mu.Lock()
	t.request = invocation.Request
	t.mu.Unlock()
	return t.response, nil
}
func (t *pipelineV2Transport) StreamPrepared(context.Context, transport.Invocation, transport.PreparedRequest) (transport.EventStream, error) {
	panic("unexpected stream")
}
func (t *pipelineV2Transport) captured() canonical.Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.request
}

func TestPipelineV2ReusesProviderStateOnlyForSameKeyAndTransport(t *testing.T) {
	t.Run("same state", func(t *testing.T) {
		pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
		previous := createPipelineV2Previous(t, token.ID, 7, transport.OpenAIResponses)
		result, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
			Model: "public", Input: json.RawMessage(`"current"`), PreviousResponseID: previous.ID,
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		captured := upstream.captured()
		if captured.PreviousResponseID != "provider_previous" || len(captured.Items) != 1 || len(captured.TransportHints) != 1 || captured.TransportHints[0] != string(transport.OpenAIResponses) {
			t.Fatalf("captured request=%#v", captured)
		}
		assertPipelineV2Result(t, result, transport.OpenAIResponses, 7)
	})

	t.Run("different transport expands history", func(t *testing.T) {
		pipeline, upstream, token := setupPipelineV2Test(t, transport.AnthropicMessages, 8)
		previous := createPipelineV2Previous(t, token.ID, 7, transport.OpenAIResponses)
		result, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
			Model: "public", Input: json.RawMessage(`"current"`), PreviousResponseID: previous.ID,
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		captured := upstream.captured()
		if captured.PreviousResponseID != "" || len(captured.Items) != 3 || len(captured.TransportHints) != 0 {
			t.Fatalf("captured request=%#v", captured)
		}
		assertPipelineV2Result(t, result, transport.AnthropicMessages, 8)
	})
}

func TestReconcileV2BackgroundReservationsRefundsInterruptedAndLegacyAttempts(t *testing.T) {
	pipeline, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	var user model.User
	if err := model.DB().First(&user, token.UserID).Error; err != nil {
		t.Fatal(err)
	}
	record := &model.AIResponse{ID: "resp_background", UserID: user.ID, TokenID: token.ID}
	keys := []string{record.ID + ":background:1:attempt:2", record.ID}
	for _, key := range keys {
		if err := pipeline.billing.DeductWithKey(token.ID, user.ID, decimal.NewFromInt(5), key+":reserve"); err != nil {
			t.Fatal(err)
		}
	}
	if err := pipeline.reconcileV2BackgroundReservations(record); err != nil {
		t.Fatal(err)
	}
	var refreshed model.Token
	if err := model.DB().First(&refreshed, token.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !refreshed.Balance.Equal(token.Balance) {
		t.Fatalf("balance=%s want=%s", refreshed.Balance, token.Balance)
	}
	for _, key := range keys {
		var settlements int64
		if err := model.DB().Model(&model.BillingLog{}).Where("idempotent_key = ?", key+":settle").Count(&settlements).Error; err != nil {
			t.Fatal(err)
		}
		if settlements != 1 {
			t.Fatalf("key=%s settlements=%d", key, settlements)
		}
	}
}

func setupPipelineV2Test(t *testing.T, transportID transport.ID, keyID uint) (*Pipeline, *pipelineV2Transport, model.Token) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.BillingLog{}, &model.ChannelRequestLog{}, &model.AIResponse{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	user := model.User{Username: "user", Balance: decimal.NewFromInt(1000), Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := model.Token{UserID: user.ID, Key: "key", Balance: decimal.NewFromInt(1000), Status: 1}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	upstream := &pipelineV2Transport{id: transportID, response: canonical.Response{
		ID: "provider_new", ProviderResponseID: "provider_new", Model: "vendor", Status: "completed",
		Output: []canonical.Item{{Type: "message", Role: canonical.RoleAssistant, Content: []canonical.Content{{Type: "output_text", Text: "done"}}}},
		Usage:  &canonical.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3},
	}}
	registry := transport.NewRegistry()
	if err := registry.Register(upstream); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	route := &routing.RouteResult{
		AbilityID: 1, ChannelID: 2, KeyID: keyID, Transport: transportID,
		ModelName: "public", VendorModel: "vendor", BaseURL: "https://example.test", PriceMode: "request",
	}
	executionEngine, err := engine.New(&pipelineV2Selector{route: route}, registry, service.NewBillingService())
	if err != nil {
		t.Fatal(err)
	}
	return New(executionEngine), upstream, token
}

func createPipelineV2Previous(t *testing.T, tokenID, keyID uint, transportID transport.ID) *model.AIResponse {
	t.Helper()
	input := datatypes.JSON(`[ {"type":"message","role":"user","content":[{"type":"input_text","text":"before"}]} ]`)
	output := datatypes.JSON(`[ {"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]} ]`)
	response := &model.AIResponse{
		ID: "resp_previous", UserID: 1, TokenID: tokenID, Model: "public", Status: "completed", Store: true,
		ProviderResponseID: "provider_previous", KeyID: keyID, UpstreamTransport: transportID,
		InputItems: input, OutputItems: output, IdempotencyKey: "previous", RequestJSON: datatypes.JSON(`{}`),
	}
	if err := model.DB().Create(response).Error; err != nil {
		t.Fatal(err)
	}
	return response
}

func assertPipelineV2Result(t *testing.T, result *Result, transportID transport.ID, keyID uint) {
	t.Helper()
	if result == nil || result.Response == nil || result.Record == nil {
		t.Fatalf("result=%#v", result)
	}
	if result.Response.ID != result.Record.ID || result.Record.ProviderResponseID != "provider_new" {
		t.Fatalf("response=%#v record=%#v", result.Response, result.Record)
	}
	if result.Record.UpstreamTransport != transportID || result.Record.KeyID != keyID || result.Record.RequestLogID == 0 {
		t.Fatalf("record route=%#v", result.Record)
	}
}
