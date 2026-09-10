package responses

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

type pipelineV2Selector struct{ route *routing.RouteResult }

func (s *pipelineV2Selector) SelectTransport(_ context.Context, _ string, _ routing.RouteRequirements, _ routing.RouteOptions) (*routing.RouteResult, error) {
	copy := *s.route
	return &copy, nil
}
func (s *pipelineV2Selector) Release(uint) {}

type pipelineV2Transport struct {
	id        transport.ID
	route     *routing.RouteResult
	response  canonical.Response
	mu        sync.Mutex
	request   canonical.Request
	failures  []error
	calls     int
	stream    []canonical.Event
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
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
	t.calls++
	started, release := t.started, t.release
	t.mu.Unlock()
	if started != nil {
		t.startOnce.Do(func() { close(started) })
	}
	if release != nil {
		<-release
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.failures) > 0 {
		err := t.failures[0]
		t.failures = t.failures[1:]
		return canonical.Response{}, err
	}
	return t.response, nil
}
func (t *pipelineV2Transport) StreamPrepared(_ context.Context, invocation transport.Invocation, _ transport.PreparedRequest) (transport.EventStream, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.request = invocation.Request
	t.calls++
	events := append([]canonical.Event(nil), t.stream...)
	return &v2StreamEvents{events: events}, nil
}
func (t *pipelineV2Transport) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

func (t *pipelineV2Transport) Execute(ctx context.Context, request canonical.Request, options engine.ExecuteOptions) (*engine.Result, error) {
	attempts := options.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	if attempts > 3 {
		attempts = 3
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		route := *t.route
		attemptRequest := request.Clone()
		var err error
		if options.PrepareRoute != nil {
			attemptRequest, err = options.PrepareRoute(ctx, attemptRequest, &route)
			if err != nil {
				return nil, err
			}
		}
		if options.PrepareTransport != nil {
			attemptRequest, err = options.PrepareTransport(ctx, attemptRequest, route.Transport)
			if err != nil {
				return nil, err
			}
		}

		t.mu.Lock()
		t.request = attemptRequest
		t.calls++
		attemptID := uint(t.calls)
		started, release := t.started, t.release
		var failure error
		if len(t.failures) > 0 {
			failure = t.failures[0]
			t.failures = t.failures[1:]
		}
		response := t.response
		response.Output = canonical.CloneItems(t.response.Output)
		t.mu.Unlock()

		if started != nil {
			t.startOnce.Do(func() { close(started) })
		}
		if release != nil {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if failure != nil {
			lastErr = failure
			continue
		}
		return &engine.Result{
			Response: &response,
			Prepared: transport.PreparedRequest{Method: http.MethodPost, URL: route.BaseURL + "/execute"},
			Route:    &route, RequestLogID: 1, CallID: options.CallID, AttemptID: attemptID,
		}, nil
	}
	return nil, lastErr
}

func TestPipelineV2ReturnsPublicResponseIdentity(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"hello"`),
	}, "", "request-response-ledger")
	if err != nil {
		t.Fatal(err)
	}
	if result.Record.CallID == "" {
		t.Fatal("response call id is empty")
	}
	if len(result.Record.CallID) != 36 || strings.HasPrefix(result.Record.CallID, "call_") {
		t.Fatalf("response call id=%q", result.Record.CallID)
	}
	if result.Record.Status != "completed" || result.Record.ProviderResponseID != "provider_new" ||
		result.Record.RequestLogID != 1 || result.Response == nil || result.Response.ID != result.Record.ID || upstream.callCount() != 1 {
		t.Fatalf("response=%#v record=%#v upstream_calls=%d", result.Response, result.Record, upstream.callCount())
	}
	if err := result.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineV2UnifiedExecutionDoesNotWriteLegacyResponse(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"hello"`),
	}, "", "request-unified-response")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Record == nil || result.Response == nil {
		t.Fatalf("result=%#v", result)
	}
	if result.Record.ID == result.Record.CallID || !strings.HasPrefix(result.Record.ID, "resp_") || len(result.Record.CallID) != 36 {
		t.Fatalf("resource_id=%q call_id=%q", result.Record.ID, result.Record.CallID)
	}
	var count int64
	if err := model.DB().Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ai_responses'`).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || upstream.callCount() != 1 {
		t.Fatalf("legacy_responses=%d upstream_calls=%d", count, upstream.callCount())
	}
}

func TestPipelineV2UnifiedFailureDoesNotWriteLegacyResponse(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	upstream.failures = []error{errors.New("one"), errors.New("two"), errors.New("three")}
	_, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"hello"`),
	}, "", "request-unified-failure")
	if err == nil || CallIDFromError(err) == "" {
		t.Fatalf("error=%v", err)
	}
	var count int64
	if err := model.DB().Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ai_responses'`).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("legacy_responses=%d", count)
	}
}

func TestPipelineV2StoreFalseKeepsOnlyRequestDigest(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	store := false
	request := &protocol.Request{
		Model: "public", Input: json.RawMessage(`"private"`), Store: &store,
		Metadata: map[string]string{"private": "metadata"},
	}
	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, "", "request-nostore")
	if err != nil {
		t.Fatal(err)
	}
	if err := result.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	stored := result.Record
	if stored.RequestHash == "" {
		t.Fatal("request digest is empty")
	}
	if len(stored.RequestJSON) != 0 || len(stored.InputItems) != 0 || len(stored.Metadata) != 0 {
		t.Fatalf("store=false retained public request fields: request=%d input=%d metadata=%d", len(stored.RequestJSON), len(stored.InputItems), len(stored.Metadata))
	}
	if result.Response == nil || len(stored.ResponseJSON) == 0 || len(stored.OutputItems) == 0 {
		t.Fatalf("store=false execution result was unavailable: response=%#v record=%#v", result.Response, stored)
	}
	if upstream.callCount() != 1 {
		t.Fatalf("upstream calls=%d", upstream.callCount())
	}
}

func TestPipelineV2FailureReturnsPersistedCallID(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 0)
	upstream.failures = []error{errors.New("temporary one"), errors.New("temporary two"), errors.New("temporary three")}
	_, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"hello"`),
	}, "", "request-failed")
	if err == nil {
		t.Fatal("expected upstream failure")
	}
	callID := CallIDFromError(err)
	if callID == "" {
		t.Fatalf("failure did not expose call id: %v", err)
	}
	if len(callID) != 36 || upstream.callCount() != 3 {
		t.Fatalf("call_id=%q upstream_calls=%d", callID, upstream.callCount())
	}
}

func TestPipelineV2ReusesProviderStateOnlyForSameKeyAndTransport(t *testing.T) {
	t.Run("same state", func(t *testing.T) {
		previous := createPipelineV2Previous(7, transport.OpenAIResponses)
		request := &protocol.Request{Model: "public", Input: json.RawMessage(`"current"`), PreviousResponseID: previous.ID}
		route := &routing.RouteResult{KeyID: 7, Transport: transport.OpenAIResponses}
		if err := prepareContinuation(request, previous, route); err != nil {
			t.Fatal(err)
		}
		captured, err := openairesponses.DecodeRequest(*request)
		if err != nil {
			t.Fatal(err)
		}
		if captured.PreviousResponseID != "provider_previous" || len(captured.Items) != 1 {
			t.Fatalf("captured request=%#v", captured)
		}
	})

	t.Run("different transport expands history", func(t *testing.T) {
		previous := createPipelineV2Previous(7, transport.OpenAIResponses)
		request := &protocol.Request{Model: "public", Input: json.RawMessage(`"current"`), PreviousResponseID: previous.ID}
		route := &routing.RouteResult{KeyID: 8, Transport: transport.AnthropicMessages}
		if err := prepareContinuation(request, previous, route); err != nil {
			t.Fatal(err)
		}
		captured, err := openairesponses.DecodeRequest(*request)
		if err != nil {
			t.Fatal(err)
		}
		if captured.PreviousResponseID != "" || len(captured.Items) != 3 || len(captured.TransportHints) != 0 {
			t.Fatalf("captured request=%#v", captured)
		}
	})
}

func setupPipelineV2Test(t *testing.T, transportID transport.ID, keyID uint) (*Pipeline, *pipelineV2Transport, model.Token) {
	return setupPipelineV2Fixture(t, transportID, keyID)
}

func setupPipelineV2Fixture(t *testing.T, transportID transport.ID, keyID uint) (*Pipeline, *pipelineV2Transport, model.Token) {
	t.Helper()
	db := openResponsesTestDB(t)
	if err := db.AutoMigrate(
		&model.User{}, &model.Token{},
		&model.Conversation{}, &model.ConversationTurn{}, &model.ConversationItem{}, &model.ConversationProjectionOutbox{}, &model.Message{},
	); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	user := model.User{Username: "user", Balance: decimal.NewFromInt(1000), Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := model.Token{UserID: user.ID, Selector: "key", Balance: decimal.NewFromInt(1000), Status: 1}
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
	upstream.route = route
	executionEngine, err := engine.New(&pipelineV2Selector{route: route}, registry)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := New(executionEngine)
	pipeline.v2 = &V2Executor{engine: upstream}
	return pipeline, upstream, token
}

func createPipelineV2Previous(keyID uint, transportID transport.ID) *model.AIResponse {
	input := datatypes.JSON(`[ {"type":"message","role":"user","content":[{"type":"input_text","text":"before"}]} ]`)
	output := datatypes.JSON(`[ {"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]} ]`)
	return &model.AIResponse{
		ID: "resp_previous", UserID: 1, TokenID: 1, Model: "public", Status: "completed", Store: true,
		ProviderResponseID: "provider_previous", KeyID: keyID, UpstreamTransport: transportID,
		InputItems: input, OutputItems: output,
	}
}
