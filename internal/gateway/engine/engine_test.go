package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type testSelector struct {
	route        *routing.RouteResult
	mu           sync.Mutex
	requirements []routing.RouteRequirements
	releases     []uint
}

type retrySelector struct {
	routes   []*routing.RouteResult
	mu       sync.Mutex
	options  []routing.RouteOptions
	releases []uint
}

func (s *retrySelector) SelectTransport(_ string, _ routing.RouteRequirements, options routing.RouteOptions) (*routing.RouteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.options = append(s.options, options)
	for _, route := range s.routes {
		excluded := false
		for _, attempt := range options.ExcludeAttempts {
			if attempt.KeyID == route.KeyID && attempt.Transport == route.Transport {
				excluded = true
				break
			}
		}
		if !excluded {
			copy := *route
			return &copy, nil
		}
	}
	return nil, routing.ErrNoRoute
}

func (s *retrySelector) Release(keyID uint) {
	s.mu.Lock()
	s.releases = append(s.releases, keyID)
	s.mu.Unlock()
}

type retryStatusError struct{ status int }

func (e *retryStatusError) Error() string   { return http.StatusText(e.status) }
func (e *retryStatusError) HTTPStatus() int { return e.status }

func (s *testSelector) SelectTransport(_ string, requirements routing.RouteRequirements, options routing.RouteOptions) (*routing.RouteResult, error) {
	s.mu.Lock()
	copyRequirements := make(routing.RouteRequirements, len(requirements))
	for capability, required := range requirements {
		copyRequirements[capability] = required
	}
	s.requirements = append(s.requirements, copyRequirements)
	route := *s.route
	if route.Transport == "" {
		route.Transport = options.PreferredTransports[0]
	}
	s.mu.Unlock()
	return &route, nil
}

func (s *testSelector) Release(keyID uint) {
	s.mu.Lock()
	s.releases = append(s.releases, keyID)
	s.mu.Unlock()
}

func (s *testSelector) releaseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.releases)
}

func (s *testSelector) lastRequirements() routing.RouteRequirements {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requirements) == 0 {
		return nil
	}
	return s.requirements[len(s.requirements)-1]
}

type scriptedTransport struct {
	id              transport.ID
	plan            func(transport.Operation, canonical.Request, canonical.FeatureSet) transport.Plan
	prepared        transport.PreparedRequest
	prepareErr      error
	executeResponse canonical.Response
	executeErr      error
	stream          transport.EventStream
	streamErr       error
	onExecute       func()

	mu    sync.Mutex
	calls []string
}

func (t *scriptedTransport) ID() transport.ID { return t.id }

func (t *scriptedTransport) Plan(operation transport.Operation, request canonical.Request, features canonical.FeatureSet) transport.Plan {
	if t.plan != nil {
		return t.plan(operation, request, features)
	}
	return transport.Exact(operation, features)
}

func (t *scriptedTransport) Prepare(context.Context, transport.Invocation) (transport.PreparedRequest, error) {
	t.record("prepare")
	return t.prepared.Clone(), t.prepareErr
}

func (t *scriptedTransport) ExecutePrepared(_ context.Context, _ transport.Invocation, prepared transport.PreparedRequest) (canonical.Response, error) {
	t.record("execute")
	if t.onExecute != nil {
		t.onExecute()
	}
	if string(prepared.Body) != string(t.prepared.Body) || prepared.URL != t.prepared.URL {
		return canonical.Response{}, errors.New("engine changed the prepared request before execution")
	}
	return t.executeResponse, t.executeErr
}

func (t *scriptedTransport) StreamPrepared(_ context.Context, _ transport.Invocation, prepared transport.PreparedRequest) (transport.EventStream, error) {
	t.record("stream")
	if string(prepared.Body) != string(t.prepared.Body) || prepared.URL != t.prepared.URL {
		return nil, errors.New("engine changed the prepared stream request before execution")
	}
	return t.stream, t.streamErr
}

func (t *scriptedTransport) record(call string) {
	t.mu.Lock()
	t.calls = append(t.calls, call)
	t.mu.Unlock()
}

func (t *scriptedTransport) callSequence() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.calls...)
}

type streamStep struct {
	event canonical.Event
	err   error
}

type scriptedEventStream struct {
	mu     sync.Mutex
	steps  []streamStep
	index  int
	closes int
}

func (s *scriptedEventStream) Next(context.Context) (canonical.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index >= len(s.steps) {
		return canonical.Event{}, io.EOF
	}
	step := s.steps[s.index]
	s.index++
	return step.event, step.err
}

func (s *scriptedEventStream) Close() error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	return nil
}

func (s *scriptedEventStream) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}

func TestNewRequiresBillingService(t *testing.T) {
	registry := transport.NewRegistry()
	selector := &testSelector{route: testRoute(decimal.Zero, decimal.Zero)}
	if _, err := New(selector, registry, nil); err == nil {
		t.Fatal("New accepted a nil billing service")
	}
}

func TestExecutePreparesReservesLogsSettlesAndReleases(t *testing.T) {
	db, user, token := executionTestDB(t)
	initial := token.Balance
	var observedBeforeExecute bool
	item := &scriptedTransport{
		id:       transport.OpenAIChat,
		prepared: testPrepared(false),
		executeResponse: canonical.Response{
			ID:    "ok",
			Usage: &canonical.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		},
		onExecute: func() {
			var billingLogs, requestLogs int64
			db.Model(&model.BillingLog{}).Count(&billingLogs)
			db.Model(&model.ChannelRequestLog{}).Count(&requestLogs)
			observedBeforeExecute = billingLogs == 1 && requestLogs == 1
		},
	}
	selector, executionEngine := newExecutionTestEngine(t, item, testRoute(decimal.NewFromInt(1_000_000), decimal.NewFromInt(1_000_000)))
	maxOutput := 8
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint:        canonical.EndpointOpenAIChat,
		Model:           "public",
		MaxOutputTokens: &maxOutput,
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if !observedBeforeExecute {
		t.Fatal("transport executed before reservation and request log were created")
	}
	if result.Response.ID != "ok" || result.Prepared.URL != item.prepared.URL {
		t.Fatalf("unexpected result: %#v", result)
	}
	if calls := item.callSequence(); len(calls) != 2 || calls[0] != "prepare" || calls[1] != "execute" {
		t.Fatalf("calls=%v", calls)
	}
	if selector.releaseCount() != 1 {
		t.Fatalf("release count=%d", selector.releaseCount())
	}
	assertTokenBalance(t, db, token.ID, initial.Sub(decimal.NewFromInt(3)))
	stored := latestRequestLog(t, db)
	if stored.StatusCode != http.StatusOK || stored.UsageTotalTokens != 3 || stored.RequestPath != "/v1/chat/completions" {
		t.Fatalf("request log=%#v", stored)
	}
}

func TestExecuteNormalizesForTransportBeforePlanningAndPreparesOnce(t *testing.T) {
	_, user, token := executionTestDB(t)
	item := &scriptedTransport{
		id:       transport.OpenAIResponses,
		prepared: testPrepared(false),
		plan: func(operation transport.Operation, request canonical.Request, features canonical.FeatureSet) transport.Plan {
			if request.Reasoning == nil || request.Reasoning.Effort != "high" {
				return transport.Unsupported(operation, "reasoning extension was not normalized")
			}
			return transport.Converted(operation, transport.OperationResponses, features)
		},
		executeResponse: canonical.Response{ID: "ok", Usage: &canonical.Usage{}},
	}
	route := testRoute(decimal.Zero, decimal.Zero)
	route.Transport = transport.OpenAIResponses
	route.Capabilities = map[routing.Capability]bool{routing.CapabilityReasoning: true}
	selector, executionEngine := newExecutionTestEngine(t, item, route)
	prepareCalls := 0
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Model:    "public",
	}, ExecuteOptions{
		UserID: user.ID, TokenID: token.ID, BillingKey: t.Name(),
		PrepareTransport: func(_ context.Context, request canonical.Request, id transport.ID) (canonical.Request, error) {
			prepareCalls++
			if id == transport.OpenAIResponses {
				request.Reasoning = &canonical.Reasoning{Effort: "high"}
			}
			return request, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Response == nil || result.Response.ID != "ok" {
		t.Fatalf("result = %#v", result)
	}
	if prepareCalls != 2 {
		t.Fatalf("transport normalization calls = %d, want planning and selected-route calls", prepareCalls)
	}
	if !selector.lastRequirements()[routing.CapabilityReasoning] {
		t.Fatalf("normalized reasoning requirement was not passed to selector: %#v", selector.lastRequirements())
	}
	if calls := item.callSequence(); len(calls) != 2 || calls[0] != "prepare" || calls[1] != "execute" {
		t.Fatalf("wire calls = %v", calls)
	}
}

func TestPlansIsolateMutableRequestFieldsBetweenTransports(t *testing.T) {
	registry := transport.NewRegistry()
	if err := registry.Register(&scriptedTransport{id: transport.OpenAIChat}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&scriptedTransport{id: transport.OpenAIResponses}); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	executionEngine := &Engine{transports: registry}
	request := canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Items: []canonical.Item{{
			Role:    canonical.RoleUser,
			Content: []canonical.Content{{Text: "original", Extra: map[string]json.RawMessage{"content": json.RawMessage(`"original"`)}}},
			Extra:   map[string]json.RawMessage{"item": json.RawMessage(`"original"`)},
		}},
		Metadata:         map[string]string{"trace": "original"},
		ClientExtensions: map[string]json.RawMessage{"client": json.RawMessage(`{"value":"original"}`)},
		ProviderOptions: canonical.ProviderOptions{Volcengine: &canonical.VolcengineOptions{
			Unknown: map[string]json.RawMessage{"provider": json.RawMessage(`"original"`)},
		}},
	}
	prepareCalls := 0
	_, err := executionEngine.plans(t.Context(), transport.OperationChat, request, request.RequiredFeatures(), func(_ context.Context, candidate canonical.Request, _ transport.ID) (canonical.Request, error) {
		if prepareCalls == 0 {
			candidate.Items[0].Content[0].Text = "mutated"
			candidate.Items[0].Extra["item"] = json.RawMessage(`"mutated"`)
			candidate.Items[0].Content[0].Extra["content"] = json.RawMessage(`"mutated"`)
			candidate.Metadata["trace"] = "mutated"
			candidate.ClientExtensions["client"] = json.RawMessage(`"mutated"`)
			candidate.ProviderOptions.Volcengine.Unknown["provider"] = json.RawMessage(`"mutated"`)
		} else if candidate.Items[0].Content[0].Text != "original" || candidate.Metadata["trace"] != "original" || string(candidate.ClientExtensions["client"]) != `{"value":"original"}` {
			t.Fatalf("candidate was polluted by another transport: %#v", candidate)
		}
		prepareCalls++
		return candidate, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepareCalls != 2 || request.Items[0].Content[0].Text != "original" || request.Metadata["trace"] != "original" || string(request.ProviderOptions.Volcengine.Unknown["provider"]) != `"original"` {
		t.Fatalf("source request was mutated: calls=%d request=%#v", prepareCalls, request)
	}
}

func TestExecuteErrorCancelsReservationAndLogsFailure(t *testing.T) {
	db, user, token := executionTestDB(t)
	upstreamErr := errors.New("upstream failed")
	item := &scriptedTransport{id: transport.OpenAIChat, prepared: testPrepared(false), executeErr: upstreamErr}
	selector, executionEngine := newExecutionTestEngine(t, item, requestPriceRoute(7))
	_, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Model:    "public",
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if !errors.Is(err, upstreamErr) {
		t.Fatalf("error=%v", err)
	}
	assertTokenBalance(t, db, token.ID, token.Balance)
	stored := latestRequestLog(t, db)
	if stored.StatusCode != http.StatusBadGateway || stored.ErrorMessage == "" {
		t.Fatalf("request log=%#v", stored)
	}
	if selector.releaseCount() != 1 {
		t.Fatalf("release count=%d", selector.releaseCount())
	}
}

func TestPrepareErrorDoesNotReserveOrLog(t *testing.T) {
	db, user, token := executionTestDB(t)
	prepareErr := errors.New("cannot prepare")
	item := &scriptedTransport{id: transport.OpenAIChat, prepareErr: prepareErr}
	selector, executionEngine := newExecutionTestEngine(t, item, requestPriceRoute(7))
	_, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Model:    "public",
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if !errors.Is(err, prepareErr) {
		t.Fatalf("error=%v", err)
	}
	var billingLogs, requestLogs int64
	db.Model(&model.BillingLog{}).Count(&billingLogs)
	db.Model(&model.ChannelRequestLog{}).Count(&requestLogs)
	if billingLogs != 0 || requestLogs != 0 || selector.releaseCount() != 1 {
		t.Fatalf("billing=%d requests=%d releases=%d", billingLogs, requestLogs, selector.releaseCount())
	}
}

func TestExecuteRetriesAnotherKeyTransportAttempt(t *testing.T) {
	_, user, token := executionTestDB(t)
	first := &scriptedTransport{id: transport.OpenAIChat, prepared: testPrepared(false), executeErr: &retryStatusError{status: http.StatusServiceUnavailable}}
	secondPrepared := testPrepared(false)
	secondPrepared.URL = "https://second.example/v1/responses"
	second := &scriptedTransport{id: transport.OpenAIResponses, prepared: secondPrepared, executeResponse: canonical.Response{ID: "ok"}}
	registry := transport.NewRegistry()
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	firstRoute := requestPriceRoute(0)
	secondRoute := requestPriceRoute(0)
	secondRoute.KeyID = 5
	secondRoute.Transport = transport.OpenAIResponses
	selector := &retrySelector{routes: []*routing.RouteResult{firstRoute, secondRoute}}
	executionEngine, err := New(selector, registry, service.NewBillingService(), service.NewAPICallService())
	if err != nil {
		t.Fatal(err)
	}
	result, err := executionEngine.Execute(context.Background(), canonical.Request{Endpoint: canonical.EndpointOpenAIChat, Model: "public"}, ExecuteOptions{
		UserID: user.ID, TokenID: token.ID, BillingKey: t.Name(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Route.KeyID != secondRoute.KeyID || result.Response.ID != "ok" {
		t.Fatalf("result=%#v", result)
	}
	if len(selector.options) != 2 || len(selector.options[1].ExcludeAttempts) != 1 {
		t.Fatalf("selection options=%#v", selector.options)
	}
	if len(selector.releases) != 2 {
		t.Fatalf("releases=%v", selector.releases)
	}
}

func TestStreamRetriesBeforeFirstEvent(t *testing.T) {
	_, user, token := executionTestDB(t)
	firstStream := &scriptedEventStream{steps: []streamStep{{err: &retryStatusError{status: http.StatusServiceUnavailable}}}}
	secondStream := &scriptedEventStream{steps: []streamStep{
		{event: canonical.Event{Type: canonical.EventTextDelta, Delta: "ready"}},
		{event: canonical.Event{Type: canonical.EventCompleted}},
	}}
	first := &scriptedTransport{id: transport.OpenAIChat, prepared: testPrepared(true), stream: firstStream}
	second := &scriptedTransport{id: transport.OpenAIResponses, prepared: testPrepared(true), stream: secondStream}
	registry := transport.NewRegistry()
	_ = registry.Register(first)
	_ = registry.Register(second)
	registry.Freeze()
	firstRoute := requestPriceRoute(0)
	secondRoute := requestPriceRoute(0)
	secondRoute.KeyID = 5
	secondRoute.Transport = transport.OpenAIResponses
	selector := &retrySelector{routes: []*routing.RouteResult{firstRoute, secondRoute}}
	executionEngine, err := New(selector, registry, service.NewBillingService(), service.NewAPICallService())
	if err != nil {
		t.Fatal(err)
	}
	result, err := executionEngine.Execute(context.Background(), canonical.Request{Endpoint: canonical.EndpointOpenAIChat, Model: "public", Stream: true}, ExecuteOptions{
		UserID: user.ID, TokenID: token.ID, BillingKey: t.Name(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := result.Stream.Next(context.Background())
	if err != nil || event.Delta != "ready" {
		t.Fatalf("event=%#v err=%v", event, err)
	}
	terminal, err := result.Stream.Next(context.Background())
	if err != nil || terminal.Type != canonical.EventCompleted {
		t.Fatalf("terminal=%#v err=%v", terminal, err)
	}
	if firstStream.closeCount() != 1 || secondStream.closeCount() != 1 {
		t.Fatalf("stream closes=%d,%d", firstStream.closeCount(), secondStream.closeCount())
	}
}

func TestExecuteRechecksCapabilitiesAfterRoutePreparation(t *testing.T) {
	_, user, token := executionTestDB(t)
	item := &scriptedTransport{
		id:       transport.OpenAIChat,
		prepared: testPrepared(false),
		executeResponse: canonical.Response{
			ID: "ok",
		},
	}
	registry := transport.NewRegistry()
	if err := registry.Register(item); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	first := requestPriceRoute(0)
	first.Capabilities = map[routing.Capability]bool{}
	second := requestPriceRoute(0)
	second.KeyID = 5
	second.Capabilities = map[routing.Capability]bool{routing.CapabilityVision: true}
	selector := &retrySelector{routes: []*routing.RouteResult{first, second}}
	executionEngine, err := New(selector, registry, service.NewBillingService(), service.NewAPICallService())
	if err != nil {
		t.Fatal(err)
	}
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Model:    "public",
	}, ExecuteOptions{
		UserID: user.ID, TokenID: token.ID, BillingKey: t.Name(), MaxAttempts: 3,
		PrepareRoute: func(_ context.Context, request canonical.Request, _ *routing.RouteResult) (canonical.Request, error) {
			request.Items = append(request.Items, canonical.Item{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{Type: "input_image", URL: "https://example.test/image.png"}}})
			return request, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Route.KeyID != second.KeyID {
		t.Fatalf("selected key=%d, want %d", result.Route.KeyID, second.KeyID)
	}
	if calls := item.callSequence(); len(calls) != 2 || calls[0] != "prepare" || calls[1] != "execute" {
		t.Fatalf("calls=%v", calls)
	}
	if len(selector.releases) != 2 {
		t.Fatalf("releases=%v", selector.releases)
	}
}

func TestStreamTerminalEventSettlesObservedUsageAndFinalizesOnce(t *testing.T) {
	db, user, token := executionTestDB(t)
	initial := token.Balance
	upstream := &scriptedEventStream{steps: []streamStep{
		{event: canonical.Event{Type: canonical.EventTextDelta, Delta: "part"}},
		{event: canonical.Event{Type: canonical.EventUsage, Usage: &canonical.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}}},
		{event: canonical.Event{Type: canonical.EventCompleted}},
	}}
	item := &scriptedTransport{id: transport.OpenAIChat, prepared: testPrepared(true), stream: upstream}
	selector, executionEngine := newExecutionTestEngine(t, item, testRoute(decimal.NewFromInt(1_000_000), decimal.NewFromInt(1_000_000)))
	maxOutput := 8
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint:        canonical.EndpointOpenAIChat,
		Model:           "public",
		Stream:          true,
		MaxOutputTokens: &maxOutput,
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		event, nextErr := result.Stream.Next(context.Background())
		if nextErr != nil {
			t.Fatalf("event %d: %v", index, nextErr)
		}
		if index == 2 && event.Type != canonical.EventCompleted {
			t.Fatalf("terminal event=%#v", event)
		}
	}
	assertTokenBalance(t, db, token.ID, initial.Sub(decimal.NewFromInt(3)))
	stored := latestRequestLog(t, db)
	if stored.StatusCode != http.StatusOK || stored.UsageTotalTokens != 3 {
		t.Fatalf("request log=%#v", stored)
	}
	if err := result.Stream.Close(); err != nil {
		t.Fatal(err)
	}
	if err := result.Stream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := result.Stream.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("next after terminal=%v", err)
	}
	if selector.releaseCount() != 1 || upstream.closeCount() != 1 {
		t.Fatalf("releases=%d closes=%d", selector.releaseCount(), upstream.closeCount())
	}
}

func TestStreamFailedTerminalSettlesAndRecordsUpstreamStatus(t *testing.T) {
	db, user, token := executionTestDB(t)
	initial := token.Balance
	upstream := &scriptedEventStream{steps: []streamStep{{event: canonical.Event{
		Type:  canonical.EventFailed,
		Usage: &canonical.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3},
		Error: &canonical.Error{Status: http.StatusTooManyRequests, Message: "limited"},
	}}}}
	item := &scriptedTransport{id: transport.OpenAIChat, prepared: testPrepared(true), stream: upstream}
	selector, executionEngine := newExecutionTestEngine(t, item, testRoute(decimal.NewFromInt(1_000_000), decimal.NewFromInt(1_000_000)))
	maxOutput := 8
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint:        canonical.EndpointOpenAIChat,
		Model:           "public",
		Stream:          true,
		MaxOutputTokens: &maxOutput,
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	event, err := result.Stream.Next(context.Background())
	if err != nil || event.Type != canonical.EventFailed {
		t.Fatalf("event=%#v error=%v", event, err)
	}
	assertTokenBalance(t, db, token.ID, initial.Sub(decimal.NewFromInt(3)))
	stored := latestRequestLog(t, db)
	if stored.StatusCode != http.StatusTooManyRequests || stored.ErrorMessage != "limited" {
		t.Fatalf("request log=%#v", stored)
	}
	if selector.releaseCount() != 1 || upstream.closeCount() != 1 {
		t.Fatalf("releases=%d closes=%d", selector.releaseCount(), upstream.closeCount())
	}
}

func TestStreamFailureBeforeOutputCancelsReservation(t *testing.T) {
	db, user, token := executionTestDB(t)
	upstreamErr := errors.New("read failed")
	upstream := &scriptedEventStream{steps: []streamStep{{err: upstreamErr}}}
	item := &scriptedTransport{id: transport.OpenAIChat, prepared: testPrepared(true), stream: upstream}
	selector, executionEngine := newExecutionTestEngine(t, item, requestPriceRoute(7))
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Model:    "public",
		Stream:   true,
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = result.Stream.Next(context.Background()); !errors.Is(err, upstreamErr) {
		t.Fatalf("stream error=%v", err)
	}
	assertTokenBalance(t, db, token.ID, token.Balance)
	stored := latestRequestLog(t, db)
	if stored.StatusCode != http.StatusBadGateway || stored.ErrorMessage == "" {
		t.Fatalf("request log=%#v", stored)
	}
	_ = result.Stream.Close()
	if selector.releaseCount() != 1 || upstream.closeCount() != 1 {
		t.Fatalf("releases=%d closes=%d", selector.releaseCount(), upstream.closeCount())
	}
}

func TestStreamFailureAfterOutputRetainsReservation(t *testing.T) {
	db, user, token := executionTestDB(t)
	upstreamErr := errors.New("truncated")
	upstream := &scriptedEventStream{steps: []streamStep{
		{event: canonical.Event{Type: canonical.EventTextDelta, Delta: "partial"}},
		{err: upstreamErr},
	}}
	item := &scriptedTransport{id: transport.OpenAIChat, prepared: testPrepared(true), stream: upstream}
	selector, executionEngine := newExecutionTestEngine(t, item, requestPriceRoute(7))
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Model:    "public",
		Stream:   true,
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = result.Stream.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = result.Stream.Next(context.Background()); !errors.Is(err, upstreamErr) {
		t.Fatalf("stream error=%v", err)
	}
	assertTokenBalance(t, db, token.ID, token.Balance.Sub(decimal.NewFromInt(7)))
	var billingLogs int64
	db.Model(&model.BillingLog{}).Count(&billingLogs)
	if billingLogs != 2 {
		t.Fatalf("billing log count=%d, want reserve and retained settlement", billingLogs)
	}
	stored := latestRequestLog(t, db)
	if stored.ErrorMessage == "" {
		t.Fatalf("request log=%#v", stored)
	}
	if selector.releaseCount() != 1 || upstream.closeCount() != 1 {
		t.Fatalf("releases=%d closes=%d", selector.releaseCount(), upstream.closeCount())
	}
}

func TestStreamEOFWithoutTerminalIsFailureAndCancelsWhenEmpty(t *testing.T) {
	db, user, token := executionTestDB(t)
	upstream := &scriptedEventStream{}
	item := &scriptedTransport{id: transport.OpenAIChat, prepared: testPrepared(true), stream: upstream}
	selector, executionEngine := newExecutionTestEngine(t, item, requestPriceRoute(7))
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Model:    "public",
		Stream:   true,
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = result.Stream.Next(context.Background()); !errors.Is(err, ErrStreamEndedWithoutTerminal) {
		t.Fatalf("stream error=%v", err)
	}
	assertTokenBalance(t, db, token.ID, token.Balance)
	if selector.releaseCount() != 1 || upstream.closeCount() != 1 {
		t.Fatalf("releases=%d closes=%d", selector.releaseCount(), upstream.closeCount())
	}
}

func TestCloseBeforeTerminalCancelsAndFinalizesOnce(t *testing.T) {
	db, user, token := executionTestDB(t)
	upstream := &scriptedEventStream{steps: []streamStep{{event: canonical.Event{Type: canonical.EventTextDelta, Delta: "unused"}}}}
	item := &scriptedTransport{id: transport.OpenAIChat, prepared: testPrepared(true), stream: upstream}
	selector, executionEngine := newExecutionTestEngine(t, item, requestPriceRoute(7))
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat,
		Model:    "public",
		Stream:   true,
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if err = result.Stream.Close(); !errors.Is(err, ErrStreamClosedWithoutTerminal) {
		t.Fatalf("close error=%v", err)
	}
	_ = result.Stream.Close()
	assertTokenBalance(t, db, token.ID, token.Balance)
	if selector.releaseCount() != 1 || upstream.closeCount() != 1 {
		t.Fatalf("releases=%d closes=%d", selector.releaseCount(), upstream.closeCount())
	}
	var call model.APICall
	if err := db.First(&call, "id = ?", result.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCancelled || !call.ClientDisconnected || call.FinalAttemptID != result.AttemptID {
		t.Fatalf("cancelled call=%#v", call)
	}
	var attempt model.APICallAttempt
	if err := db.First(&attempt, result.AttemptID).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.Status != model.APICallAttemptStatusCancelled {
		t.Fatalf("cancelled attempt=%#v", attempt)
	}
}

func TestExecuteRecordsUnifiedCallLedgerAcrossRetries(t *testing.T) {
	db, user, token := executionTestDB(t)
	initial := token.Balance
	first := &scriptedTransport{
		id: transport.OpenAIChat, prepared: testPrepared(false),
		executeErr: &retryStatusError{status: http.StatusServiceUnavailable},
	}
	secondPrepared := testPrepared(false)
	secondPrepared.URL = "https://second.example/v1/responses"
	second := &scriptedTransport{
		id: transport.OpenAIResponses, prepared: secondPrepared,
		executeResponse: canonical.Response{
			ID: "provider-ok", Usage: &canonical.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
		},
	}
	registry := transport.NewRegistry()
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	firstRoute := requestPriceRoute(7)
	secondRoute := requestPriceRoute(7)
	secondRoute.KeyID = 5
	secondRoute.ChannelID = 3
	secondRoute.Transport = transport.OpenAIResponses
	selector := &retrySelector{routes: []*routing.RouteResult{firstRoute, secondRoute}}
	executionEngine, err := New(
		selector, registry, service.NewBillingService(), service.NewAPICallService(),
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIResponses, Model: "public",
	}, ExecuteOptions{
		UserID: user.ID, TokenID: token.ID, BillingKey: t.Name(), MaxAttempts: 3,
		RequestID: "request-ledger", ResourceType: "response", ResourceID: "resp_public",
		ConversationID: 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CallID == "" || result.AttemptID == 0 {
		t.Fatalf("missing ledger identifiers: %#v", result)
	}

	var call model.APICall
	if err := db.First(&call, "id = ?", result.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCompleted || call.RequestID != "request-ledger" ||
		call.AttemptCount != 2 || call.FinalAttemptID != result.AttemptID || call.TotalTokens != 3 ||
		call.Endpoint != "/v1/responses" || call.ResourceType != "response" ||
		call.ResourceID != "resp_public" || call.ConversationID != 42 {
		t.Fatalf("call=%#v", call)
	}
	if call.FirstByteAt == nil {
		t.Fatal("non-streaming call did not record first-byte time")
	}
	if !call.ReservedAmount.Equal(decimal.NewFromInt(14)) ||
		!call.FinalCost.Equal(decimal.NewFromInt(7)) ||
		!call.RefundedAmount.Equal(decimal.NewFromInt(7)) {
		t.Fatalf("call amounts reserved=%s final=%s refunded=%s", call.ReservedAmount, call.FinalCost, call.RefundedAmount)
	}
	assertTokenBalance(t, db, token.ID, initial.Sub(decimal.NewFromInt(7)))

	var attempts []model.APICallAttempt
	if err := db.Where("call_id = ?", call.ID).Order("attempt_no ASC").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Status != model.APICallAttemptStatusFailed ||
		attempts[1].Status != model.APICallAttemptStatusCompleted || attempts[1].TotalTokens != 3 ||
		attempts[1].ProviderResponseID != "provider-ok" || attempts[1].FirstByteAt == nil {
		t.Fatalf("attempts=%#v", attempts)
	}

	var billingLogs []model.BillingLog
	if err := db.Where("call_id = ?", call.ID).Order("id ASC").Find(&billingLogs).Error; err != nil {
		t.Fatal(err)
	}
	if len(billingLogs) != 4 {
		t.Fatalf("billing logs=%#v", billingLogs)
	}
	for _, billingLog := range billingLogs {
		if billingLog.AttemptID == 0 || len(billingLog.PricingSnapshot) == 0 || billingLog.Phase == "" {
			t.Fatalf("incomplete billing log=%#v", billingLog)
		}
	}
}

func TestKeepCallOpenOnErrorAllowsRetryWithSameCallID(t *testing.T) {
	db, user, token := executionTestDB(t)
	callService := service.NewAPICallService()
	call, err := callService.StartCall(&service.StartCallRequest{
		ID: "call_background_retry", RequestID: "request-background",
		UserID: user.ID, TokenID: token.ID, Endpoint: string(canonical.EndpointOpenAIResponses),
		Operation: string(transport.OperationResponses), Model: "public", Background: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	upstreamErr := &retryStatusError{status: http.StatusServiceUnavailable}
	item := &scriptedTransport{
		id: transport.OpenAIResponses, prepared: testPrepared(false), executeErr: upstreamErr,
	}
	route := requestPriceRoute(0)
	route.Transport = transport.OpenAIResponses
	selector, executionEngine := newExecutionTestEngineWithCallService(t, item, route, callService)
	options := ExecuteOptions{
		UserID: user.ID, TokenID: token.ID, CallID: call.ID,
		BillingKey: t.Name() + ":first", KeepCallOpenOnError: true,
	}
	if _, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIResponses, Model: "public",
	}, options); !errors.Is(err, upstreamErr) {
		t.Fatalf("first error=%v", err)
	}
	var stored model.APICall
	if err := db.First(&stored, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.APICallStatusInProgress || stored.AttemptCount != 1 {
		t.Fatalf("call after retryable failure=%#v", stored)
	}

	item.executeErr = nil
	item.executeResponse = canonical.Response{ID: "retry-ok", Usage: &canonical.Usage{TotalTokens: 2}}
	options.BillingKey = t.Name() + ":second"
	options.KeepCallOpenOnError = false
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIResponses, Model: "public",
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	if result.CallID != call.ID || selector.releaseCount() != 2 {
		t.Fatalf("result=%#v releases=%d", result, selector.releaseCount())
	}
	if err := db.First(&stored, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.APICallStatusCompleted || stored.AttemptCount != 2 || stored.TotalTokens != 2 {
		t.Fatalf("completed retried call=%#v", stored)
	}
}

func TestDeferCallCompletionLeavesSuccessfulCallInProgress(t *testing.T) {
	db, user, token := executionTestDB(t)
	callService := service.NewAPICallService()
	item := &scriptedTransport{
		id: transport.OpenAIResponses, prepared: testPrepared(false),
		executeResponse: canonical.Response{ID: "deferred", Usage: &canonical.Usage{TotalTokens: 3}},
	}
	route := requestPriceRoute(0)
	route.Transport = transport.OpenAIResponses
	_, executionEngine := newExecutionTestEngineWithCallService(t, item, route, callService)
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIResponses, Model: "public",
	}, ExecuteOptions{
		UserID: user.ID, TokenID: token.ID, BillingKey: t.Name(), DeferCallCompletion: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var call model.APICall
	if err := db.First(&call, "id = ?", result.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusInProgress || call.FinalAttemptID != 0 {
		t.Fatalf("deferred call=%#v", call)
	}
	var attempt model.APICallAttempt
	if err := db.First(&attempt, result.AttemptID).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.Status != model.APICallAttemptStatusCompleted || attempt.TotalTokens != 3 {
		t.Fatalf("deferred attempt=%#v", attempt)
	}
	if err := result.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&call, "id = ?", result.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCompleted || call.FinalAttemptID != result.AttemptID || call.TotalTokens != 3 {
		t.Fatalf("delivered call=%#v", call)
	}
}

func TestAttemptLedgerFailureStopsBeforeUpstream(t *testing.T) {
	db, user, token := executionTestDB(t)
	if err := db.Migrator().DropTable(&model.APICallAttempt{}); err != nil {
		t.Fatal(err)
	}
	item := &scriptedTransport{
		id: transport.OpenAIChat, prepared: testPrepared(false),
		executeResponse: canonical.Response{ID: "must-not-run"},
	}
	_, executionEngine := newExecutionTestEngine(t, item, requestPriceRoute(0))
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat, Model: "public",
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if err == nil || result != nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if calls := item.callSequence(); len(calls) != 1 || calls[0] != "prepare" {
		t.Fatalf("upstream was reached after attempt ledger failure: %v", calls)
	}
}

func TestCallLeaseLossCancelsExecutionContext(t *testing.T) {
	db, user, token := executionTestDB(t)
	calls := service.NewAPICallService()
	call, err := calls.StartCall(&service.StartCallRequest{
		UserID: user.ID, TokenID: token.ID, Endpoint: "/v1/chat/completions",
		Operation: "chat.completions", Model: "public",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := calls.MarkCallRunning(call.ID); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := newCallLifecycleWithOptions(
		context.Background(), calls, call.ID, 120*time.Millisecond, 20*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.APICall{}).Where("id = ?", call.ID).Updates(map[string]any{
		"lease_owner": "replacement-owner", "lease_expires_at": time.Now().Add(time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	select {
	case <-lifecycle.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("execution context was not cancelled after lease loss")
	}
	if !errors.Is(lifecycle.leaseFailure(), service.ErrAPICallLeaseUnavailable) {
		t.Fatalf("lease failure = %v", lifecycle.leaseFailure())
	}
	lifecycle.releaseLease()
	var stored model.APICall
	if err := db.First(&stored, "id = ?", call.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.LeaseOwner != "replacement-owner" {
		t.Fatalf("replacement lease owner was cleared: %q", stored.LeaseOwner)
	}
}

func TestLedgerCompletionFailureDoesNotReplaceSuccessfulResponse(t *testing.T) {
	db, user, token := executionTestDB(t)
	item := &scriptedTransport{
		id:       transport.OpenAIChat,
		prepared: testPrepared(false),
		executeResponse: canonical.Response{
			ID: "upstream-success", Usage: &canonical.Usage{TotalTokens: 1},
		},
		onExecute: func() {
			if err := db.Exec("DELETE FROM api_call_attempts").Error; err != nil {
				t.Errorf("delete attempts: %v", err)
			}
		},
	}
	_, executionEngine := newExecutionTestEngine(t, item, requestPriceRoute(0))
	result, err := executionEngine.Execute(context.Background(), canonical.Request{
		Endpoint: canonical.EndpointOpenAIChat, Model: "public",
	}, ExecuteOptions{UserID: user.ID, TokenID: token.ID, BillingKey: t.Name()})
	if err != nil {
		t.Fatalf("ledger completion replaced upstream success: %v", err)
	}
	if result == nil || result.Response == nil || result.Response.ID != "upstream-success" {
		t.Fatalf("result=%#v", result)
	}
}

func newExecutionTestEngine(t *testing.T, item transport.Transport, route *routing.RouteResult) (*testSelector, *Engine) {
	return newExecutionTestEngineWithCallService(t, item, route, service.NewAPICallService())
}

func newExecutionTestEngineWithCallService(
	t *testing.T,
	item transport.Transport,
	route *routing.RouteResult,
	callService *service.APICallService,
) (*testSelector, *Engine) {
	t.Helper()
	registry := transport.NewRegistry()
	if err := registry.Register(item); err != nil {
		t.Fatal(err)
	}
	registry.Freeze()
	selector := &testSelector{route: route}
	executionEngine, err := New(selector, registry, service.NewBillingService(), callService)
	if err != nil {
		t.Fatal(err)
	}
	return selector, executionEngine
}

func executionTestDB(t *testing.T) (*gorm.DB, model.User, model.Token) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.BillingLog{},
		&model.ChannelRequestLog{},
		&model.APICall{},
		&model.APICallAttempt{},
		&model.BalanceEntry{},
	); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	user := model.User{Username: "user", Balance: decimal.NewFromInt(1_000_000), Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	token := model.Token{UserID: user.ID, Key: "token", Balance: decimal.NewFromInt(1_000_000), Status: 1}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	return db, user, token
}

func testRoute(inputPrice, outputPrice decimal.Decimal) *routing.RouteResult {
	return &routing.RouteResult{
		AbilityID:   1,
		KeyID:       4,
		ChannelID:   2,
		Transport:   transport.OpenAIChat,
		ModelName:   "public",
		VendorModel: "vendor",
		BaseURL:     "https://example.test",
		PriceMode:   "token",
		InputPrice:  inputPrice,
		OutputPrice: outputPrice,
	}
}

func requestPriceRoute(price int64) *routing.RouteResult {
	route := testRoute(decimal.NewFromInt(price), decimal.Zero)
	route.PriceMode = "request"
	return route
}

func testPrepared(stream bool) transport.PreparedRequest {
	return transport.PreparedRequest{
		Method:  http.MethodPost,
		URL:     "https://example.test/v1/chat/completions",
		Headers: http.Header{"Authorization": []string{"Bearer secret"}},
		Body:    []byte(`{"model":"vendor"}`),
		Stream:  stream,
	}
}

func assertTokenBalance(t *testing.T, db *gorm.DB, tokenID uint, expected decimal.Decimal) {
	t.Helper()
	var token model.Token
	if err := db.First(&token, tokenID).Error; err != nil {
		t.Fatal(err)
	}
	if !token.Balance.Equal(expected) {
		t.Fatalf("balance=%s expected=%s", token.Balance, expected)
	}
}

func latestRequestLog(t *testing.T, db *gorm.DB) model.ChannelRequestLog {
	t.Helper()
	var stored model.ChannelRequestLog
	if err := db.Order("id DESC").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	return stored
}
