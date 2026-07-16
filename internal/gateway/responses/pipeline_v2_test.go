package responses

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

type pipelineV2Selector struct{ route *routing.RouteResult }

type failingV2ResponseWriter struct{ header http.Header }

func (w *failingV2ResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingV2ResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("client connection closed")
}

func (w *failingV2ResponseWriter) WriteHeader(int) {}

func (s *pipelineV2Selector) SelectTransport(_ string, _ routing.RouteRequirements, _ routing.RouteOptions) (*routing.RouteResult, error) {
	copy := *s.route
	return &copy, nil
}
func (s *pipelineV2Selector) Release(uint) {}

type pipelineV2Transport struct {
	id        transport.ID
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
func (t *pipelineV2Transport) captured() canonical.Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.request
}

func (t *pipelineV2Transport) callCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

func TestPipelineV2CreatesStableCallLedger(t *testing.T) {
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
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusInProgress || call.RequestID != "request-response-ledger" {
		t.Fatalf("call status=%s request_id=%q", call.Status, call.RequestID)
	}
	if call.Endpoint != "/v1/responses" || call.Operation != "responses" || call.ResourceType != "response" ||
		call.ResourceID != result.Record.ID || !call.ProjectConversation {
		t.Fatalf("unexpected call correlation: %#v", call)
	}
	if !call.Store || call.Background || call.IsStream || call.AttemptCount != 1 || upstream.callCount() != 1 {
		t.Fatalf("unexpected call lifecycle: %#v upstream_calls=%d", call, upstream.callCount())
	}
	if err := result.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	if err := model.DB().First(&call, "id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCompleted || call.FinalAttemptID != result.AttemptID {
		t.Fatalf("delivered call=%#v", call)
	}
	requireResponseConversationTurn(t, result.Record.CallID, model.ConversationTurnCompleted)
}

func TestPipelineV2StreamUsesStableCallLedger(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	upstream.stream = []canonical.Event{
		{Type: canonical.EventResponseCreated, Response: &canonical.Response{ID: "provider_stream", ProviderResponseID: "provider_stream", Model: "vendor", Status: "in_progress"}},
		{Type: canonical.EventOutputTextDelta, Delta: "done"},
		{Type: canonical.EventUsage, Usage: &canonical.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}},
		{Type: canonical.EventCompleted, Response: &canonical.Response{ID: "provider_stream", Status: "completed"}},
	}
	request := &protocol.Request{Model: "public", Input: json.RawMessage(`"hello"`), Stream: true}
	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, "", "request-response-stream")
	if err != nil {
		t.Fatal(err)
	}
	if result.Record.CallID == "" || result.V2Stream == nil {
		t.Fatalf("stream result=%#v", result)
	}
	var pendingProjection model.ConversationProjectionOutbox
	if err := model.DB().First(&pendingProjection, "call_id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if !pendingProjection.InputReady || pendingProjection.OutputReady {
		t.Fatalf("stream projection was ready before output: %#v", pendingProjection)
	}
	recorder := httptest.NewRecorder()
	if err := pipeline.ProxyV2Stream(context.Background(), recorder, result, request); err != nil {
		t.Fatal(err)
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCompleted || !call.IsStream || call.RequestID != "request-response-stream" || call.AttemptCount != 1 {
		t.Fatalf("stream call=%#v", call)
	}
	turn := requireResponseConversationTurn(t, result.Record.CallID, model.ConversationTurnCompleted)
	requireResponseConversationItemText(t, turn.ID, model.ConversationItemOutput, "done")
}

func TestPipelineV2StreamCancelsCallWhenClientWriteFails(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	upstream.stream = []canonical.Event{
		{Type: canonical.EventResponseCreated, Response: &canonical.Response{ID: "provider_stream", Status: "in_progress"}},
		{Type: canonical.EventCompleted, Response: &canonical.Response{ID: "provider_stream", Status: "completed"}},
	}
	request := &protocol.Request{Model: "public", Input: json.RawMessage(`"hello"`), Stream: true}
	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, "stream-write-failure", "request-response-write-failure")
	if err != nil {
		t.Fatal(err)
	}

	err = pipeline.ProxyV2Stream(context.Background(), &failingV2ResponseWriter{}, result, request)
	if err == nil {
		t.Fatal("expected downstream write failure")
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCancelled || !call.ClientDisconnected || call.FinalAttemptID == 0 {
		t.Fatalf("call after stream write failure: %#v", call)
	}
	var attempt model.APICallAttempt
	if err := model.DB().First(&attempt, call.FinalAttemptID).Error; err != nil {
		t.Fatal(err)
	}
	if attempt.Status != model.APICallAttemptStatusCancelled {
		t.Fatalf("attempt after stream write failure: %#v", attempt)
	}
	var cacheRows int64
	if err := model.DB().Model(&model.AIResponseIdempotencyCache{}).
		Where("token_id = ? AND idempotency_key = ?", token.ID, "stream-write-failure").
		Count(&cacheRows).Error; err != nil {
		t.Fatal(err)
	}
	if cacheRows != 0 {
		t.Fatalf("stream write failure retained %d pending idempotency rows", cacheRows)
	}
	requireResponseConversationTurn(t, result.Record.CallID, model.ConversationTurnAborted)
}

func TestPipelineV2StoreFalseStreamReplaysCachedTerminalEvent(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	upstream.stream = []canonical.Event{
		{Type: canonical.EventResponseCreated, Response: &canonical.Response{ID: "provider_stream", Model: "vendor", Status: "in_progress"}},
		{Type: canonical.EventOutputTextDelta, Delta: "done"},
		{Type: canonical.EventCompleted, Response: &canonical.Response{ID: "provider_stream", Status: "completed"}},
	}
	store := false
	request := &protocol.Request{Model: "public", Input: json.RawMessage(`"hello"`), Stream: true, Store: &store}
	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, "nostore-stream", "request-stream-first")
	if err != nil {
		t.Fatal(err)
	}
	first := httptest.NewRecorder()
	if err := pipeline.ProxyV2Stream(context.Background(), first, result, request); err != nil {
		t.Fatal(err)
	}

	replayed, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, "nostore-stream", "request-stream-replay")
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.IdempotentReplay || replayed.V2Stream != nil || replayed.Response == nil || upstream.callCount() != 1 {
		t.Fatalf("stream replay=%#v upstream_calls=%d", replayed, upstream.callCount())
	}
	second := httptest.NewRecorder()
	if err := ProxyIdempotentReplay(second, replayed.Response); err != nil {
		t.Fatal(err)
	}
	if second.Header().Get("Content-Type") != "text/event-stream" ||
		!strings.Contains(second.Body.String(), "event: response.completed") ||
		!strings.Contains(second.Body.String(), `"text":"done"`) {
		t.Fatalf("cached stream replay headers=%v body=%s", second.Header(), second.Body.String())
	}
}

func TestPipelineV2StoreFalseKeepsOnlyRequestDigest(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	store := false
	request := &protocol.Request{
		Model: "public", Input: json.RawMessage(`"private"`), Store: &store,
		Metadata: map[string]string{"private": "metadata"},
	}
	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, "nostore-key", "request-nostore")
	if err != nil {
		t.Fatal(err)
	}
	if err := result.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	var stored model.AIResponse
	if err := model.DB().First(&stored, "id = ?", result.Record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.RequestHash == "" {
		t.Fatal("request digest is empty")
	}
	if len(stored.RequestJSON) != 0 || len(stored.InputItems) != 0 || len(stored.ResponseJSON) != 0 || len(stored.OutputItems) != 0 || len(stored.Metadata) != 0 {
		t.Fatalf("store=false retained payloads: request=%d input=%d response=%d output=%d", len(stored.RequestJSON), len(stored.InputItems), len(stored.ResponseJSON), len(stored.OutputItems))
	}
	if !strings.HasPrefix(stored.IdempotencyKey, "internal:") {
		t.Fatalf("store=false response retained external idempotency key %q", stored.IdempotencyKey)
	}
	var cached model.AIResponseIdempotencyCache
	if err := model.DB().First(&cached, "token_id = ? AND idempotency_key = ?", token.ID, "nostore-key").Error; err != nil {
		t.Fatal(err)
	}
	if cached.Status != model.ResponseIdempotencyCompleted || cached.ResponseID != stored.ID || len(cached.ResponseJSON) == 0 || !cached.ExpiresAt.After(time.Now()) {
		t.Fatalf("idempotency cache=%#v", cached)
	}
	replayed, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, "nostore-key", "request-replay")
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Record.ID != stored.ID || replayed.Record.CallID != stored.CallID ||
		replayed.CallID == "" || replayed.CallID == stored.CallID || upstream.callCount() != 1 {
		t.Fatalf("idempotent replay ledger mismatch: response=%s resource_call=%s replay_call=%s upstream_calls=%d", replayed.Record.ID, replayed.Record.CallID, replayed.CallID, upstream.callCount())
	}
	if replayed.Response == nil || replayed.Response.ID != result.Response.ID || string(replayed.Response.Output) != string(result.Response.Output) {
		t.Fatalf("idempotent replay lost response body: first=%#v replay=%#v", result.Response, replayed.Response)
	}
	if err := replayed.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	var replayCall model.APICall
	if err := model.DB().First(&replayCall, "id = ?", replayed.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if replayCall.Status != model.APICallStatusCompleted || replayCall.AttemptCount != 0 ||
		replayCall.Operation != "responses.replay" || replayCall.ResourceID != stored.ID || !replayCall.FinalCost.IsZero() {
		t.Fatalf("replay call=%#v", replayCall)
	}
	_, err = pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"different"`), Store: &store,
	}, "nostore-key", "request-conflict")
	if err == nil || !strings.Contains(err.Error(), "different request") {
		t.Fatalf("idempotency conflict error=%v", err)
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", stored.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Store {
		t.Fatal("store=false call was marked retained")
	}
	turn := requireResponseConversationTurn(t, stored.CallID, model.ConversationTurnCompleted)
	requireResponseConversationItemText(t, turn.ID, model.ConversationItemInput, "private")
	var turnCount int64
	if err := model.DB().Model(&model.ConversationTurn{}).Count(&turnCount).Error; err != nil {
		t.Fatal(err)
	}
	if turnCount != 1 || replayCall.ConversationID != turn.ConversationID {
		t.Fatalf("turn_count=%d replay_conversation=%d original_conversation=%d", turnCount, replayCall.ConversationID, turn.ConversationID)
	}
}

func TestPipelineV2IdempotencyCacheCoversStoreAndStreamModes(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	cases := []struct {
		name   string
		store  bool
		stream bool
	}{
		{name: "stored_nonstream", store: true},
		{name: "private_nonstream", store: false},
		{name: "stored_stream", store: true, stream: true},
		{name: "private_stream", store: false, stream: true},
	}
	for index, testCase := range cases {
		store := testCase.store
		request := &protocol.Request{
			Model: "public", Input: json.RawMessage(`"` + testCase.name + `"`),
			Store: &store, Stream: testCase.stream,
		}
		if testCase.stream {
			upstream.mu.Lock()
			upstream.stream = []canonical.Event{
				{Type: canonical.EventResponseCreated, Response: &canonical.Response{ID: "provider_" + testCase.name, Model: "vendor", Status: "in_progress"}},
				{Type: canonical.EventOutputTextDelta, Delta: "done"},
				{Type: canonical.EventCompleted, Response: &canonical.Response{ID: "provider_" + testCase.name, Status: "completed"}},
			}
			upstream.mu.Unlock()
		}
		key := "lease-" + testCase.name
		beforeCalls := upstream.callCount()
		result, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, key, "request-"+testCase.name)
		if err != nil {
			t.Fatalf("%s create: %v", testCase.name, err)
		}
		if testCase.stream {
			if err := pipeline.ProxyV2Stream(context.Background(), httptest.NewRecorder(), result, request); err != nil {
				t.Fatalf("%s stream: %v", testCase.name, err)
			}
		} else if err := result.CompleteDelivery(); err != nil {
			t.Fatalf("%s delivery: %v", testCase.name, err)
		}

		var cached model.AIResponseIdempotencyCache
		if err := model.DB().First(&cached, "token_id = ? AND idempotency_key = ?", token.ID, key).Error; err != nil {
			t.Fatalf("%s cache: %v", testCase.name, err)
		}
		now := time.Now()
		if cached.Status != model.ResponseIdempotencyCompleted || cached.Owner != "" || cached.ResponseID != result.Record.ID ||
			len(cached.ResponseJSON) == 0 || cached.ExpiresAt.Before(now.Add(23*time.Hour)) || cached.ExpiresAt.After(now.Add(25*time.Hour)) {
			t.Fatalf("%s cache=%#v", testCase.name, cached)
		}

		replayed, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, key, "replay-"+testCase.name)
		if err != nil {
			t.Fatalf("%s replay: %v", testCase.name, err)
		}
		if !replayed.IdempotentReplay || replayed.Record.ID != result.Record.ID || upstream.callCount() != beforeCalls+1 {
			t.Fatalf("%s replay=%#v upstream_calls=%d", testCase.name, replayed, upstream.callCount())
		}
		if testCase.stream {
			if err := ProxyIdempotentReplay(httptest.NewRecorder(), replayed.Response); err != nil {
				t.Fatalf("%s replay stream: %v", testCase.name, err)
			}
		}
		if err := replayed.CompleteDelivery(); err != nil {
			t.Fatalf("%s replay delivery: %v", testCase.name, err)
		}
		var cacheCount int64
		if err := model.DB().Model(&model.AIResponseIdempotencyCache{}).Count(&cacheCount).Error; err != nil {
			t.Fatal(err)
		}
		if cacheCount != int64(index+1) {
			t.Fatalf("%s cache count=%d", testCase.name, cacheCount)
		}
	}

	cacheCount := int64(len(cases))
	withoutKey, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"without-key"`),
	}, "", "request-without-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := withoutKey.CompleteDelivery(); err != nil {
		t.Fatal(err)
	}
	var finalCacheCount int64
	if err := model.DB().Model(&model.AIResponseIdempotencyCache{}).Count(&finalCacheCount).Error; err != nil {
		t.Fatal(err)
	}
	if finalCacheCount != cacheCount {
		t.Fatalf("request without Idempotency-Key created a cache row: before=%d after=%d", cacheCount, finalCacheCount)
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
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", callID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusFailed || call.RequestID != "request-failed" {
		t.Fatalf("failed call=%#v", call)
	}
	requireResponseConversationTurn(t, callID, model.ConversationTurnFailed)
}

func TestPipelineV2BackgroundRetryReusesCall(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	upstream.failures = []error{errors.New("temporary one"), errors.New("temporary two"), errors.New("temporary three")}
	record := createPipelineV2Background(t, token, "resp_background_retry", "request-background-retry")

	err := pipeline.ExecuteBackground(context.Background(), record.ID, false, 0)
	if err == nil {
		t.Fatal("first background attempt unexpectedly succeeded")
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusInProgress || call.AttemptCount != 3 {
		t.Fatalf("call after retryable failure=%#v", call)
	}
	var queued model.AIResponse
	if err := model.DB().First(&queued, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if queued.Status != "queued" || queued.CallID != record.CallID {
		t.Fatalf("queued response=%#v", queued)
	}

	if err := pipeline.ExecuteBackground(context.Background(), record.ID, true, 1); err != nil {
		t.Fatal(err)
	}
	if err := model.DB().First(&call, "id = ?", record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCompleted || call.AttemptCount != 4 || upstream.callCount() != 4 {
		t.Fatalf("completed retried call=%#v upstream_calls=%d", call, upstream.callCount())
	}
	requireResponseConversationTurn(t, record.CallID, model.ConversationTurnCompleted)
}

func TestBackgroundResponseLeasePreventsConcurrentExecution(t *testing.T) {
	_, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	record := createPipelineV2Background(t, token, "resp_background_lease", "request-background-lease")

	first, acquired, err := acquireBackgroundResponseLease(context.Background(), record.ID, 0)
	if err != nil || !acquired || first.LeaseOwner == "" {
		t.Fatalf("first lease: record=%#v acquired=%v err=%v", first, acquired, err)
	}
	second, acquired, err := acquireBackgroundResponseLease(context.Background(), record.ID, 0)
	if err != nil || acquired || second.LeaseOwner != first.LeaseOwner {
		t.Fatalf("second lease: record=%#v acquired=%v err=%v", second, acquired, err)
	}

	expired := time.Now().Add(-time.Minute)
	if err := model.DB().Model(&model.AIResponse{}).Where("id = ?", record.ID).
		Update("lease_expires_at", &expired).Error; err != nil {
		t.Fatal(err)
	}
	third, acquired, err := acquireBackgroundResponseLease(context.Background(), record.ID, 1)
	if err != nil || !acquired || third.LeaseOwner == first.LeaseOwner || third.ExecutionAttempt != 2 {
		t.Fatalf("replacement lease: record=%#v acquired=%v err=%v", third, acquired, err)
	}
}

func TestBackgroundCancellationDoesNotLeaveOutboxWhenWorkerReturnsLate(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	upstream.started = make(chan struct{})
	upstream.release = make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-upstream.release:
		default:
			close(upstream.release)
		}
	})
	previousEnqueue := enqueueResponseBackground
	enqueueResponseBackground = func(string) error { return nil }
	t.Cleanup(func() { enqueueResponseBackground = previousEnqueue })

	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, &protocol.Request{
		Model: "public", Input: json.RawMessage(`"cancel race"`), Background: true,
	}, "", "request-background-cancel-race")
	if err != nil {
		t.Fatal(err)
	}
	workerResult := make(chan error, 1)
	go func() {
		workerResult <- pipeline.ExecuteBackground(context.Background(), result.Record.ID, true, 0)
	}()
	select {
	case <-upstream.started:
	case <-time.After(5 * time.Second):
		t.Fatal("background worker did not reach upstream")
	}

	cancelled, err := pipeline.Cancel(token.ID, result.Record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatalf("cancel response status=%s", cancelled.Status)
	}
	close(upstream.release)
	select {
	case workerErr := <-workerResult:
		if workerErr != nil {
			t.Fatalf("late background worker returned error: %v", workerErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background worker did not finish after cancellation")
	}

	var stored model.AIResponse
	if err := model.DB().First(&stored, "id = ?", result.Record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "cancelled" {
		t.Fatalf("stored response status=%s", stored.Status)
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCancelled {
		t.Fatalf("call status=%s", call.Status)
	}
	turn := requireResponseConversationTurn(t, result.Record.CallID, model.ConversationTurnAborted)
	var turnCount, outboxCount int64
	if err := model.DB().Model(&model.ConversationTurn{}).Where("call_id = ?", result.Record.CallID).Count(&turnCount).Error; err != nil {
		t.Fatal(err)
	}
	if err := model.DB().Model(&model.ConversationProjectionOutbox{}).Where("call_id = ?", result.Record.CallID).Count(&outboxCount).Error; err != nil {
		t.Fatal(err)
	}
	if turn.ID == 0 || turnCount != 1 || outboxCount != 0 {
		t.Fatalf("turn=%#v turn_count=%d outbox_count=%d", turn, turnCount, outboxCount)
	}
}

func TestBackgroundResponseResumesPersistedCheckpointWithoutUpstreamReplay(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	record := createPipelineV2Background(t, token, "resp_background_checkpoint", "request-background-checkpoint")
	leased, acquired, err := acquireBackgroundResponseLease(context.Background(), record.ID, 0)
	if err != nil || !acquired {
		t.Fatalf("acquire lease: acquired=%v err=%v", acquired, err)
	}
	checkpoint := &V2Result{
		Response: &protocol.Response{
			ID: record.ID, Object: "response", Status: "completed", Model: record.Model,
			Background: true, Store: true, Output: json.RawMessage(`[]`),
		},
		Route: &routing.RouteResult{
			ChannelID: 1, KeyID: 7, Transport: transport.OpenAIResponses,
		},
		ProviderResponseID: "provider-checkpoint",
	}
	if err := pipeline.checkpointBackgroundResponse(leased, leased.LeaseOwner, checkpoint); err != nil {
		t.Fatal(err)
	}
	expired := time.Now().Add(-time.Minute)
	if err := model.DB().Model(&model.AIResponse{}).Where("id = ?", record.ID).
		Update("lease_expires_at", &expired).Error; err != nil {
		t.Fatal(err)
	}
	if err := pipeline.ExecuteBackground(context.Background(), record.ID, false, 1); err != nil {
		t.Fatal(err)
	}
	if upstream.callCount() != 0 {
		t.Fatalf("upstream replayed %d times", upstream.callCount())
	}
	var stored model.AIResponse
	if err := model.DB().First(&stored, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "completed" || stored.ProviderResponseID != "provider-checkpoint" ||
		stored.LeaseOwner != "" || stored.LeaseExpiresAt != nil {
		t.Fatalf("checkpointed response=%#v", stored)
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusCompleted {
		t.Fatalf("checkpointed call status=%s", call.Status)
	}
}

func TestPipelineV2BackgroundEnqueueCreatesReceivedCall(t *testing.T) {
	pipeline, _, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	previousEnqueue := enqueueResponseBackground
	var queuedID string
	queuedCount := 0
	enqueueResponseBackground = func(responseID string) error {
		queuedID = responseID
		queuedCount++
		return nil
	}
	t.Cleanup(func() { enqueueResponseBackground = previousEnqueue })

	request := &protocol.Request{
		Model: "public", Input: json.RawMessage(`"background"`), Background: true,
	}
	result, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, "background-enqueue-key", "request-background-enqueue")
	if err != nil {
		t.Fatal(err)
	}
	if queuedID != result.Record.ID || result.Record.CallID == "" {
		t.Fatalf("queued_id=%q response=%#v", queuedID, result.Record)
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", result.Record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusReceived || !call.Background || call.AttemptCount != 0 || call.RequestID != "request-background-enqueue" {
		t.Fatalf("queued call=%#v", call)
	}
	var cached model.AIResponseIdempotencyCache
	if err := model.DB().First(&cached, "token_id = ? AND idempotency_key = ?", token.ID, "background-enqueue-key").Error; err != nil {
		t.Fatal(err)
	}
	if cached.Status != model.ResponseIdempotencyCompleted || cached.ResponseID != result.Record.ID || cached.Owner != "" {
		t.Fatalf("background cache=%#v", cached)
	}
	replayed, err := pipeline.Create(context.Background(), token.UserID, token.ID, request, "background-enqueue-key", "request-background-replay")
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.IdempotentReplay || replayed.Record.ID != result.Record.ID || queuedCount != 1 {
		t.Fatalf("background replay=%#v queued_count=%d", replayed, queuedCount)
	}
}

func TestPipelineV2FinalBackgroundFailureFailsCall(t *testing.T) {
	pipeline, upstream, token := setupPipelineV2Test(t, transport.OpenAIResponses, 7)
	upstream.failures = []error{errors.New("temporary one"), errors.New("temporary two"), errors.New("temporary three")}
	record := createPipelineV2Background(t, token, "resp_background_failed", "request-background-failed")

	if err := pipeline.ExecuteBackground(context.Background(), record.ID, true, 0); err == nil {
		t.Fatal("final background attempt unexpectedly succeeded")
	}
	var call model.APICall
	if err := model.DB().First(&call, "id = ?", record.CallID).Error; err != nil {
		t.Fatal(err)
	}
	if call.Status != model.APICallStatusFailed || call.AttemptCount != 3 || call.FinalAttemptID == 0 {
		t.Fatalf("failed call=%#v", call)
	}
	requireResponseConversationTurn(t, record.CallID, model.ConversationTurnFailed)
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
	db := openResponsesTestDB(t)
	if err := db.AutoMigrate(
		&model.User{}, &model.Token{}, &model.BillingLog{}, &model.ChannelRequestLog{}, &model.AIResponse{},
		&model.AIResponseIdempotencyCache{},
		&model.APICall{}, &model.APICallAttempt{}, &model.APICallPayload{}, &model.BalanceEntry{},
		&model.Conversation{}, &model.ConversationTurn{}, &model.ConversationItem{}, &model.ConversationProjectionOutbox{}, &model.Message{},
	); err != nil {
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
	executionEngine, err := engine.New(
		&pipelineV2Selector{route: route}, registry, service.NewBillingService(), service.NewAPICallService(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return New(executionEngine), upstream, token
}

func createPipelineV2Background(t *testing.T, token model.Token, responseID, requestID string) *model.AIResponse {
	t.Helper()
	request := &protocol.Request{Model: "public", Input: json.RawMessage(`"background"`), Background: true}
	requestJSON, _ := json.Marshal(request)
	record := &model.AIResponse{
		ID: responseID, UserID: token.UserID, TokenID: token.ID, Model: request.Model,
		Status: "queued", Background: true, Store: true,
		RequestJSON: requestJSON, RequestHash: hashResponseRequest(requestJSON), InputItems: datatypes.JSON(request.Input),
		ResponseJSON:   datatypes.JSON(`{"id":"` + responseID + `","status":"queued"}`),
		IdempotencyKey: "background-" + responseID,
	}
	projection, err := newResponseConversationProjection(request, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := createResponseWithCall(record, requestID, false, projection); err != nil {
		t.Fatal(err)
	}
	return record
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
