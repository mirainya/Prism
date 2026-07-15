// Package engine is the single Gateway V2 execution path.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var (
	ErrNoTransportPlan             = errors.New("no upstream transport can execute this request")
	ErrStreamEndedWithoutTerminal  = errors.New("upstream stream ended without a terminal event")
	ErrStreamClosedWithoutTerminal = errors.New("upstream stream was closed without a terminal event")
)

type Selector interface {
	SelectTransport(string, routing.RouteRequirements, routing.RouteOptions) (*routing.RouteResult, error)
	Release(uint)
}

type Engine struct {
	selector   Selector
	transports *transport.Registry
	billing    *service.BillingService
	apiCalls   *service.APICallService
	circuit    *routing.Circuit
}

func New(
	selector Selector,
	transports *transport.Registry,
	billing *service.BillingService,
	apiCalls ...*service.APICallService,
) (*Engine, error) {
	if selector == nil || transports == nil || billing == nil {
		return nil, errors.New("selector, transport registry, and billing service are required")
	}
	var callService *service.APICallService
	if len(apiCalls) > 0 {
		callService = apiCalls[0]
	}
	return &Engine{
		selector: selector, transports: transports, billing: billing,
		apiCalls: callService, circuit: routing.NewCircuit(),
	}, nil
}

type RoutePreparer func(context.Context, canonical.Request, *routing.RouteResult) (canonical.Request, error)
type TransportPreparer func(context.Context, canonical.Request, transport.ID) (canonical.Request, error)

type ExecuteOptions struct {
	UserID              uint
	TokenID             uint
	CallID              string
	RequestID           string
	DownstreamEndpoint  string
	DownstreamRequest   []byte
	ResourceType        string
	ResourceID          string
	ConversationID      uint
	ProjectConversation bool
	KeepCallOpenOnError bool
	DeferCallCompletion bool
	BillingKey          string
	MaxAttempts         int
	PrepareRoute        RoutePreparer
	PrepareTransport    TransportPreparer
}

type Result struct {
	Response     *canonical.Response
	Prepared     transport.PreparedRequest
	Route        *routing.RouteResult
	RequestLogID uint
	CallID       string
	AttemptID    uint
	Stream       *StreamResult

	ledger                 *callLifecycle
	usage                  *canonical.Usage
	conversationProjection *service.ConversationProjectionOutputRequest
}

type StreamResult struct {
	Prepared  transport.PreparedRequest
	Route     *routing.RouteResult
	CallID    string
	AttemptID uint

	stream              transport.EventStream
	reservation         *Reservation
	requestLog          *RequestLog
	ledger              *callLifecycle
	keepCallOpenOnError bool
	deferCallCompletion bool
	projectConversation bool
	release             func()

	nextMu                 sync.Mutex
	prefetched             *canonical.Event
	stateMu                sync.Mutex
	produced               bool
	terminal               bool
	usage                  *canonical.Usage
	providerResponseID     string
	transcript             *canonical.EventAccumulator
	done                   bool
	finishErr              error
	ledgerActive           bool
	ledgerOutcome          *streamLedgerOutcome
	conversationProjection *service.ConversationProjectionOutputRequest

	finishOnce    sync.Once
	firstByteOnce sync.Once
	closeOnce     sync.Once
	closeErr      error
}

type callLifecycle struct {
	service         *service.APICallService
	callID          string
	leaseOwner      string
	leaseStop       chan struct{}
	leaseDone       chan struct{}
	leaseOnce       sync.Once
	executionCtx    context.Context
	cancelExecution context.CancelCauseFunc
	leaseDuration   time.Duration
	leaseHeartbeat  time.Duration

	mu                     sync.Mutex
	finalAttemptID         uint
	providerResponseID     string
	conversationProjection *service.ConversationProjectionOutputRequest
	leaseErr               error
	finished               bool
}

const (
	callLeaseDuration  = 5 * time.Minute
	callLeaseHeartbeat = time.Minute
)

type streamLedgerOutcome struct {
	usage                  *canonical.Usage
	requestErr             error
	attemptCompleted       bool
	cancelled              bool
	clientDisconnected     bool
	conversationProjection *service.ConversationProjectionOutputRequest
}

func newCallLifecycle(ctx context.Context, callService *service.APICallService, callID string) (*callLifecycle, error) {
	return newCallLifecycleWithOptions(ctx, callService, callID, callLeaseDuration, callLeaseHeartbeat)
}

func newCallLifecycleWithOptions(
	ctx context.Context,
	callService *service.APICallService,
	callID string,
	duration time.Duration,
	heartbeat time.Duration,
) (*callLifecycle, error) {
	if callService == nil || strings.TrimSpace(callID) == "" {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if duration <= 0 || heartbeat <= 0 || heartbeat >= duration {
		return nil, errors.New("invalid API call lease options")
	}
	executionCtx, cancelExecution := context.WithCancelCause(ctx)
	lifecycle := &callLifecycle{
		service: callService, callID: callID,
		leaseOwner: service.GenerateRequestID(),
		leaseStop:  make(chan struct{}), leaseDone: make(chan struct{}),
		executionCtx: executionCtx, cancelExecution: cancelExecution,
		leaseDuration: duration, leaseHeartbeat: heartbeat,
	}
	if err := callService.AcquireCallLease(callID, lifecycle.leaseOwner, time.Now().Add(duration)); err != nil {
		cancelExecution(err)
		return nil, err
	}
	go lifecycle.heartbeatLease()
	return lifecycle, nil
}

func (l *callLifecycle) Context() context.Context {
	if l == nil || l.executionCtx == nil {
		return context.Background()
	}
	return l.executionCtx
}

func (l *callLifecycle) leaseFailure() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.leaseErr
}

func (l *callLifecycle) heartbeatLease() {
	if l == nil || l.service == nil || l.leaseStop == nil || l.leaseDone == nil {
		return
	}
	defer close(l.leaseDone)
	ticker := time.NewTicker(l.leaseHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := l.service.RenewCallLease(l.callID, l.leaseOwner, time.Now().Add(l.leaseDuration)); err != nil {
				logLedgerError("renew API call execution lease", l.callID, 0, err)
				l.mu.Lock()
				if l.leaseErr == nil {
					l.leaseErr = err
				}
				l.mu.Unlock()
				l.cancelExecution(err)
				return
			}
		case <-l.leaseStop:
			return
		case <-l.executionCtx.Done():
			return
		}
	}
}

func (l *callLifecycle) releaseLease() {
	if l == nil || l.service == nil {
		return
	}
	l.leaseOnce.Do(func() {
		if l.leaseStop != nil {
			close(l.leaseStop)
		}
		if l.leaseDone != nil {
			<-l.leaseDone
		}
		if err := l.service.ReleaseCallLease(l.callID, l.leaseOwner); err != nil {
			logLedgerError("release API call execution lease", l.callID, 0, err)
		}
		if l.cancelExecution != nil {
			l.cancelExecution(context.Canceled)
		}
	})
}

func (e *Engine) beginCall(
	ctx context.Context,
	request canonical.Request,
	operation transport.Operation,
	options ExecuteOptions,
) (*callLifecycle, error) {
	if e.apiCalls == nil {
		return nil, nil
	}

	callID := strings.TrimSpace(options.CallID)
	if options.ProjectConversation && callID == "" {
		return nil, fmt.Errorf("%w: projected API calls must be created with their conversation input before execution", service.ErrAPICallInvalidInput)
	}
	if callID != "" {
		var existing model.APICall
		projectionErr := model.DB().Select("id", "project_conversation").First(&existing, "id = ?", callID).Error
		if projectionErr == nil && existing.ProjectConversation != options.ProjectConversation {
			return nil, fmt.Errorf(
				"%w: call %s conversation projection setting does not match execution options",
				service.ErrAPICallInvalidInput,
				callID,
			)
		}
		if options.ProjectConversation {
			if errors.Is(projectionErr, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("%w: projected call %s must be created with its conversation input before execution", service.ErrAPICallNotFound, callID)
			}
		}
		if projectionErr != nil && !errors.Is(projectionErr, gorm.ErrRecordNotFound) {
			return nil, projectionErr
		}
		err := e.apiCalls.MarkCallRunning(callID)
		if err == nil {
			return newCallLifecycle(ctx, e.apiCalls, callID)
		}
		if !errors.Is(err, service.ErrAPICallNotFound) {
			return nil, err
		}
		if options.ProjectConversation {
			return nil, fmt.Errorf("%w: projected call %s disappeared before execution", service.ErrAPICallNotFound, callID)
		}
	}

	store := request.Store != nil && *request.Store
	call, err := e.apiCalls.StartCall(&service.StartCallRequest{
		ID:                  callID,
		RequestID:           options.RequestID,
		UserID:              options.UserID,
		TokenID:             options.TokenID,
		Endpoint:            downstreamEndpoint(options.DownstreamEndpoint, request.Endpoint),
		Operation:           string(operation),
		Model:               request.Model,
		IsStream:            request.Stream,
		Background:          request.Background,
		Store:               store,
		ResourceType:        options.ResourceType,
		ResourceID:          options.ResourceID,
		ConversationID:      options.ConversationID,
		ProjectConversation: options.ProjectConversation,
	})
	if err != nil {
		return nil, err
	}
	if err := e.apiCalls.MarkCallRunning(call.ID); err != nil {
		return nil, err
	}
	return newCallLifecycle(ctx, e.apiCalls, call.ID)
}

func downstreamEndpoint(explicit string, endpoint canonical.Endpoint) string {
	if value := strings.TrimSpace(explicit); value != "" {
		return value
	}
	switch endpoint {
	case canonical.EndpointOpenAIChat:
		return "/v1/chat/completions"
	case canonical.EndpointAnthropic:
		return "/v1/messages"
	case canonical.EndpointOpenAIResponses:
		return "/v1/responses"
	default:
		return string(endpoint)
	}
}

func (l *callLifecycle) startAttempt(
	route *routing.RouteResult,
	prepared transport.PreparedRequest,
) (*model.APICallAttempt, error) {
	if l == nil || l.service == nil {
		return nil, nil
	}
	path, _ := logURL(prepared.URL)
	attempt, err := l.service.StartAttempt(&service.StartAttemptRequest{
		CallID:      l.callID,
		AbilityID:   route.AbilityID,
		ChannelID:   route.ChannelID,
		KeyID:       route.KeyID,
		Protocol:    route.Protocol,
		VendorModel: route.VendorModel,
		Transport:   route.Transport,
		RequestPath: path,
	})
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.finalAttemptID = attempt.ID
	l.mu.Unlock()
	return attempt, nil
}

func (l *callLifecycle) markFirstByte(attemptID uint) {
	if l == nil || l.service == nil || attemptID == 0 {
		return
	}
	if err := l.service.MarkAttemptFirstByte(attemptID); err != nil {
		logLedgerError("mark API call attempt first byte", l.callID, attemptID, err)
	}
}

func (l *callLifecycle) recordPayload(attemptID uint, kind string, data []byte) {
	if l == nil || l.service == nil || len(data) == 0 {
		return
	}
	err := l.service.RecordPayload(&model.APICallPayload{
		CallID: l.callID, AttemptID: attemptID, Kind: kind,
		ContentType: "application/json", Data: append([]byte(nil), data...),
	})
	if err != nil {
		logLedgerError("record API call payload", l.callID, attemptID, err)
	}
}

func (l *callLifecycle) completeAttempt(attemptID uint, usage *canonical.Usage, providerResponseID string) {
	if l == nil || l.service == nil || attemptID == 0 {
		return
	}
	l.mu.Lock()
	if providerResponseID != "" {
		l.providerResponseID = providerResponseID
	}
	l.mu.Unlock()
	values := usageValues(usage)
	err := l.service.CompleteAttempt(attemptID, &service.CompleteAttemptRequest{
		HTTPStatus:            http.StatusOK,
		InputTokens:           values.input,
		OutputTokens:          values.output,
		TotalTokens:           values.total,
		CachedInputTokens:     values.cached,
		ReasoningOutputTokens: values.reasoning,
		UsageJSON:             values.raw,
		ProviderResponseID:    providerResponseID,
	})
	if err != nil {
		logLedgerError("complete API call attempt", l.callID, attemptID, err)
	}
}

func (l *callLifecycle) failAttempt(attemptID uint, requestErr error, usage *canonical.Usage, providerResponseID string) {
	if l == nil || l.service == nil || attemptID == 0 {
		return
	}
	detail := ledgerErrorDetail(requestErr)
	values := usageValues(usage)
	err := l.service.FailAttempt(attemptID, &service.FailAttemptRequest{
		HTTPStatus:            detail.status,
		ErrorType:             detail.errorType,
		ErrorCode:             detail.code,
		ErrorMessage:          detail.message,
		ErrorRetryable:        detail.retryable,
		InputTokens:           values.input,
		OutputTokens:          values.output,
		TotalTokens:           values.total,
		CachedInputTokens:     values.cached,
		ReasoningOutputTokens: values.reasoning,
		UsageJSON:             values.raw,
		ProviderResponseID:    providerResponseID,
	})
	if err != nil {
		logLedgerError("fail API call attempt", l.callID, attemptID, err)
	}
}

func (l *callLifecycle) cancelAttempt(attemptID uint, requestErr error) {
	if l == nil || l.service == nil || attemptID == 0 {
		return
	}
	detail := ledgerErrorDetail(requestErr)
	err := l.service.CancelAttempt(attemptID, &service.CancelAttemptRequest{
		HTTPStatus:     detail.status,
		ErrorType:      detail.errorType,
		ErrorCode:      detail.code,
		ErrorMessage:   detail.message,
		ErrorRetryable: false,
	})
	if err != nil {
		logLedgerError("cancel API call attempt", l.callID, attemptID, err)
	}
}

func (l *callLifecycle) completeCall(
	attemptID uint,
	usage *canonical.Usage,
	projection *service.ConversationProjectionOutputRequest,
) error {
	_, ok := l.beginFinish(attemptID)
	if !ok {
		return nil
	}
	l.mu.Lock()
	providerResponseID := l.providerResponseID
	l.mu.Unlock()
	values := usageValues(usage)
	err := l.service.CompleteCall(l.callID, &service.CompleteCallRequest{
		LeaseOwner:             l.leaseOwner,
		FinalAttemptID:         attemptID,
		InputTokens:            values.input,
		OutputTokens:           values.output,
		TotalTokens:            values.total,
		CachedInputTokens:      values.cached,
		ReasoningOutputTokens:  values.reasoning,
		UsageJSON:              values.raw,
		ProviderResponseID:     providerResponseID,
		HTTPStatus:             http.StatusOK,
		CompleteStartedAttempt: true,
		ConversationProjection: projection,
	})
	if err != nil {
		logLedgerError("complete API call", l.callID, attemptID, err)
	}
	l.releaseLease()
	return err
}

func (l *callLifecycle) failCall(
	requestErr error,
	usage *canonical.Usage,
	clientDisconnected bool,
	projection *service.ConversationProjectionOutputRequest,
) error {
	attemptID, ok := l.beginFinish(0)
	if !ok {
		return nil
	}
	detail := ledgerErrorDetail(requestErr)
	values := usageValues(usage)
	err := l.service.FailCall(l.callID, &service.FailCallRequest{
		LeaseOwner:             l.leaseOwner,
		FinalAttemptID:         attemptID,
		HTTPStatus:             detail.status,
		ErrorType:              detail.errorType,
		ErrorCode:              detail.code,
		ErrorMessage:           detail.message,
		ErrorRetryable:         detail.retryable,
		InputTokens:            values.input,
		OutputTokens:           values.output,
		TotalTokens:            values.total,
		CachedInputTokens:      values.cached,
		ReasoningOutputTokens:  values.reasoning,
		UsageJSON:              values.raw,
		ClientDisconnected:     clientDisconnected,
		FailStartedAttempt:     true,
		ConversationProjection: projection,
	})
	if err != nil {
		logLedgerError("fail API call", l.callID, attemptID, err)
	}
	l.releaseLease()
	return err
}

func (l *callLifecycle) cancelCall(
	requestErr error,
	clientDisconnected bool,
	projection *service.ConversationProjectionOutputRequest,
) error {
	attemptID, ok := l.beginFinish(0)
	if !ok {
		return nil
	}
	detail := ledgerErrorDetail(requestErr)
	err := l.service.CancelCall(l.callID, &service.CancelCallRequest{
		LeaseOwner:             l.leaseOwner,
		FinalAttemptID:         attemptID,
		HTTPStatus:             detail.status,
		ErrorType:              detail.errorType,
		ErrorCode:              detail.code,
		ErrorMessage:           detail.message,
		ClientDisconnected:     clientDisconnected,
		ConversationProjection: projection,
	})
	if err != nil {
		logLedgerError("cancel API call", l.callID, 0, err)
	}
	l.releaseLease()
	return err
}

// CompleteDelivery marks a deferred call successful after its response has
// been encoded and accepted by the downstream writer.
func (r *Result) CompleteDelivery() error {
	if r == nil || r.ledger == nil {
		return nil
	}
	return r.ledger.completeCall(r.AttemptID, r.usage, r.conversationProjection)
}

// FailDelivery terminates a deferred call when downstream encoding or writing
// fails. Writer failures should set clientDisconnected so the call is cancelled.
func (r *Result) FailDelivery(err error, clientDisconnected bool) error {
	if r == nil || r.ledger == nil {
		return nil
	}
	if err == nil {
		err = errors.New("downstream response delivery failed")
	}
	if clientDisconnected {
		return r.ledger.cancelCall(err, true, r.conversationProjection)
	}
	return r.ledger.failCall(err, r.usage, false, r.conversationProjection)
}

// CancelDelivery terminates a deferred call without treating an explicit
// application cancellation as a disconnected downstream client.
func (r *Result) CancelDelivery(err error, clientDisconnected bool) error {
	if r == nil || r.ledger == nil {
		return nil
	}
	if err == nil {
		err = context.Canceled
	}
	return r.ledger.cancelCall(err, clientDisconnected, r.conversationProjection)
}

func (l *callLifecycle) beginFinish(preferredAttemptID uint) (uint, bool) {
	if l == nil || l.service == nil {
		return 0, false
	}
	l.mu.Lock()
	if l.finished {
		attemptID := l.finalAttemptID
		l.mu.Unlock()
		return attemptID, false
	}
	if preferredAttemptID > 0 {
		l.finalAttemptID = preferredAttemptID
	}
	l.finished = true
	attemptID := l.finalAttemptID
	l.mu.Unlock()
	return attemptID, true
}

func (l *callLifecycle) isFinished() bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.finished
}

func (l *callLifecycle) setConversationProjection(request *service.ConversationProjectionOutputRequest) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.conversationProjection = cloneConversationProjectionOutputRequest(request)
	l.mu.Unlock()
}

func (l *callLifecycle) currentConversationProjection() *service.ConversationProjectionOutputRequest {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return cloneConversationProjectionOutputRequest(l.conversationProjection)
}

type canonicalUsageValues struct {
	input, output, total, cached, reasoning int
	raw                                     datatypes.JSON
}

func usageValues(usage *canonical.Usage) canonicalUsageValues {
	if usage == nil {
		return canonicalUsageValues{}
	}
	raw, _ := json.Marshal(usage)
	return canonicalUsageValues{
		input: usage.InputTokens, output: usage.OutputTokens, total: usage.TotalTokens,
		cached: usage.CachedInputTokens, reasoning: usage.ReasoningOutputTokens,
		raw: datatypes.JSON(raw),
	}
}

type ledgerError struct {
	status    int
	errorType string
	code      string
	message   string
	retryable bool
}

func ledgerErrorDetail(err error) ledgerError {
	if err == nil {
		return ledgerError{}
	}
	result := ledgerError{message: err.Error(), errorType: "execution_error", code: "execution_failed"}
	if errors.Is(err, context.Canceled) {
		result.errorType = "cancelled"
		result.code = "client_cancelled"
		return result
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.status = http.StatusGatewayTimeout
		result.errorType = "timeout_error"
		result.code = "deadline_exceeded"
		result.retryable = true
		return result
	}
	switch {
	case errors.Is(err, routing.ErrModelNotFound):
		result.status = http.StatusNotFound
		result.errorType = "invalid_request_error"
		result.code = "model_not_found"
		return result
	case errors.Is(err, routing.ErrCapabilityUnavailable),
		errors.Is(err, routing.ErrNoCompatibleTransport),
		errors.Is(err, ErrNoTransportPlan):
		result.status = http.StatusBadRequest
		result.errorType = "invalid_request_error"
		result.code = "unsupported_model_capability"
		return result
	case errors.Is(err, routing.ErrNoRoute):
		result.status = http.StatusServiceUnavailable
		result.errorType = "server_error"
		result.code = "model_unavailable"
		result.retryable = true
		return result
	case errors.Is(err, service.ErrInsufficientTokenBalance),
		errors.Is(err, service.ErrInsufficientUserBalance):
		result.status = http.StatusTooManyRequests
		result.errorType = "insufficient_quota"
		result.code = "insufficient_quota"
		return result
	}
	if appErr, ok := domain.IsAppError(err); ok {
		result.status = appErr.HTTPStatus
		result.code = appErr.Code
		result.errorType = "api_error"
		return result
	}
	result.status = domain.UpstreamStatusCode(err)
	if result.status > 0 {
		result.errorType = "upstream_error"
		result.code = "upstream_http_error"
		result.retryable = result.status == http.StatusRequestTimeout ||
			result.status == http.StatusConflict || result.status == http.StatusTooManyRequests || result.status >= 500
	}
	return result
}

func logLedgerError(action, callID string, attemptID uint, err error) {
	if err == nil || logger.L == nil {
		return
	}
	logger.Error(action,
		zap.String("call_id", callID),
		zap.Uint("attempt_id", attemptID),
		zap.Error(err),
	)
}

func conversationProjectionOutputRequest(
	callID string,
	requestLogID uint,
	response canonical.Response,
) *service.ConversationProjectionOutputRequest {
	providerResponseID := response.ProviderResponseID
	if providerResponseID == "" {
		providerResponseID = response.ID
	}
	return &service.ConversationProjectionOutputRequest{
		CallID: callID, OutputItems: canonical.CloneItems(response.Output),
		RequestLogID: requestLogID, ProviderResponseID: providerResponseID,
		FinishReason: response.FinishReason,
	}
}

func stageConversationProjectionOutputBestEffort(request *service.ConversationProjectionOutputRequest) {
	if request == nil {
		return
	}
	_, err := service.StageAPIConversationProjectionOutputIfPresent(*request)
	logLedgerError("stage conversation projection output", request.CallID, 0, err)
}

func cloneConversationProjectionOutputRequest(
	request *service.ConversationProjectionOutputRequest,
) *service.ConversationProjectionOutputRequest {
	if request == nil {
		return nil
	}
	clone := *request
	clone.OutputItems = canonical.CloneItems(request.OutputItems)
	return &clone
}

func (e *Engine) Execute(
	ctx context.Context,
	request canonical.Request,
	options ExecuteOptions,
) (result *Result, executeErr error) {
	operation, err := operationFor(request.Endpoint)
	if err != nil {
		return nil, err
	}
	requestCtx := ctx
	ledger, err := e.beginCall(ctx, request, operation, options)
	if err != nil {
		return nil, err
	}
	if ledger != nil {
		ctx = ledger.Context()
		options.CallID = ledger.callID
		ledger.recordPayload(0, model.APICallPayloadRequest, options.DownstreamRequest)
	}
	streamHandedOff := false
	defer func() {
		if ledger == nil || streamHandedOff || ledger.isFinished() {
			return
		}
		if options.DeferCallCompletion && executeErr == nil && result != nil {
			return
		}
		if options.KeepCallOpenOnError && executeErr != nil {
			ledger.releaseLease()
			return
		}
		failure := executeErr
		if failure == nil {
			failure = errors.New("Gateway V2 execution ended without a result")
		}
		projection := ledger.currentConversationProjection()
		if options.ProjectConversation && projection == nil {
			projection = conversationProjectionOutputRequest(options.CallID, 0, canonical.Response{})
		}
		if leaseErr := ledger.leaseFailure(); leaseErr != nil {
			ledger.failCall(errors.Join(failure, leaseErr), nil, false, projection)
			return
		}
		if errors.Is(failure, context.Canceled) || errors.Is(requestCtx.Err(), context.Canceled) {
			ledger.cancelCall(failure, true, projection)
			return
		}
		ledger.failCall(failure, nil, false, projection)
	}()

	requirements := request.RequiredFeatures()
	plans, err := e.plans(ctx, operation, request, requirements, options.PrepareTransport)
	if err != nil {
		return nil, err
	}
	if len(plans) == 0 {
		return nil, ErrNoTransportPlan
	}

	maxAttempts := options.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if maxAttempts > 3 {
		maxAttempts = 3
	}
	attempts := make([]routing.TransportAttempt, 0, maxAttempts)
	transportHints := append([]string(nil), request.TransportHints...)
	var attemptErrors []error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptPlans := filterHintedPlans(plans, transportHints)
		if len(attemptPlans) == 0 {
			return nil, ErrNoTransportPlan
		}
		selectionRequirements := requirementsForPlans(requirements, attemptPlans)
		route, selectErr := e.selector.SelectTransport(request.Model, routingRequirements(selectionRequirements), routing.RouteOptions{
			AllowedTransports: planIDs(attemptPlans), PreferredTransports: preferredPlanIDs(attemptPlans),
			ExcludeAttempts: attempts, ResponsesRequest: operation == transport.OperationResponses,
		})
		if selectErr != nil {
			if len(attemptErrors) > 0 && errors.Is(selectErr, routing.ErrNoRoute) {
				return nil, errors.Join(attemptErrors...)
			}
			return nil, selectErr
		}
		if route == nil {
			return nil, errors.New("selector returned a nil route")
		}

		attemptRequest := request.Clone()
		if options.PrepareRoute != nil {
			attemptRequest, err = options.PrepareRoute(ctx, request.Clone(), route)
			if err != nil {
				e.selector.Release(route.KeyID)
				return nil, err
			}
			if len(attemptRequest.TransportHints) > 0 {
				transportHints = append([]string(nil), attemptRequest.TransportHints...)
			}
		}
		if options.PrepareTransport != nil {
			attemptRequest, err = options.PrepareTransport(ctx, attemptRequest.Clone(), route.Transport)
			if err != nil {
				e.selector.Release(route.KeyID)
				return nil, err
			}
		}
		attemptRequirements := mergeFeatures(requirements, attemptRequest.RequiredFeatures())
		if !routeSupportsFeatures(route, attemptRequirements) || !e.transportSupports(route.Transport, operation, attemptRequest, attemptRequirements) {
			e.selector.Release(route.KeyID)
			attempts = append(attempts, routing.TransportAttempt{KeyID: route.KeyID, Transport: route.Transport})
			attemptErrors = append(attemptErrors, routing.ErrCapabilityUnavailable)
			requirements = mergeFeatures(requirements, attemptRequirements)
			plans, err = e.plans(ctx, operation, request, requirements, options.PrepareTransport)
			if err != nil {
				return nil, errors.Join(append(attemptErrors, err)...)
			}
			continue
		}
		selectedResult, upstreamErr, err := e.executeSelected(
			ctx,
			attemptRequest,
			operation,
			attemptRequirements,
			route,
			attemptBillingOptions(options, attempt),
			ledger,
		)
		if err == nil && selectedResult != nil && selectedResult.Stream != nil && maxAttempts > 1 {
			if _, prefetchErr := selectedResult.Stream.prefetch(ctx); prefetchErr != nil {
				upstreamErr, err = true, prefetchErr
			} else {
				selectedResult.Stream.activateLedger()
				streamHandedOff = true
				return selectedResult, nil
			}
		}
		if err == nil {
			if selectedResult != nil && selectedResult.Stream != nil {
				selectedResult.Stream.activateLedger()
				streamHandedOff = true
			}
			return selectedResult, nil
		}
		if !upstreamErr || !retryableUpstreamError(ctx, err) || attempt+1 >= maxAttempts {
			return nil, errors.Join(append(attemptErrors, err)...)
		}
		e.circuit.MarkTransportUnavailable(route.KeyID, request.Model, route.Transport, err)
		attempts = append(attempts, routing.TransportAttempt{KeyID: route.KeyID, Transport: route.Transport})
		attemptErrors = append(attemptErrors, err)
	}
	return nil, errors.Join(attemptErrors...)
}

func routeSupportsFeatures(route *routing.RouteResult, requirements canonical.FeatureSet) bool {
	if route == nil {
		return false
	}
	// Nil is retained for selector implementations outside the database router.
	if route.Capabilities == nil {
		return true
	}
	for feature, required := range requirements {
		if required && !route.Capabilities[routing.Capability(feature)] {
			return false
		}
	}
	return true
}

func (e *Engine) transportSupports(id transport.ID, operation transport.Operation, request canonical.Request, requirements canonical.FeatureSet) bool {
	selected, ok := e.transports.Get(id)
	return ok && selected.Plan(operation, request.Clone(), requirements).Supported()
}

func mergeFeatures(left, right canonical.FeatureSet) canonical.FeatureSet {
	merged := make(canonical.FeatureSet, len(left)+len(right))
	for feature, enabled := range left {
		if enabled {
			merged[feature] = true
		}
	}
	for feature, enabled := range right {
		if enabled {
			merged[feature] = true
		}
	}
	return merged
}

func filterHintedPlans(plans []plannedTransport, hints []string) []plannedTransport {
	if len(hints) == 0 {
		return plans
	}
	allowed := make(map[string]struct{}, len(hints))
	for _, hint := range hints {
		allowed[hint] = struct{}{}
	}
	filtered := make([]plannedTransport, 0, len(plans))
	for _, plan := range plans {
		if _, ok := allowed[string(plan.id)]; ok {
			filtered = append(filtered, plan)
		}
	}
	return filtered
}

func (e *Engine) executeSelected(
	ctx context.Context,
	request canonical.Request,
	operation transport.Operation,
	requirements canonical.FeatureSet,
	route *routing.RouteResult,
	options ExecuteOptions,
	ledger *callLifecycle,
) (*Result, bool, error) {
	var terminalProjection *service.ConversationProjectionOutputRequest
	selected, ok := e.transports.Get(route.Transport)
	if !ok {
		e.selector.Release(route.KeyID)
		return nil, false, fmt.Errorf("selected transport %q is not registered", route.Transport)
	}
	if !selected.Plan(operation, request, requirements).Supported() {
		e.selector.Release(route.KeyID)
		return nil, false, ErrNoTransportPlan
	}
	invocation := transport.Invocation{Route: routeToTransport(route), Request: request, Operation: operation}
	prepared, err := selected.Prepare(ctx, invocation)
	if err != nil {
		e.selector.Release(route.KeyID)
		return nil, false, err
	}
	attempt, err := ledger.startAttempt(route, prepared)
	if err != nil {
		e.selector.Release(route.KeyID)
		return nil, false, err
	}
	attemptID := uint(0)
	if attempt != nil {
		attemptID = attempt.ID
	}
	ledger.recordPayload(attemptID, model.APICallPayloadUpstreamRequest, prepared.Body)
	billingContext := callBillingContext(options.CallID, attemptID, route)
	reservation, err := reserveWithBillingContext(
		e.billing,
		options.TokenID,
		options.UserID,
		route,
		request,
		options.BillingKey,
		billingContext,
	)
	if err != nil {
		ledger.failAttempt(attemptID, err, nil, "")
		e.selector.Release(route.KeyID)
		return nil, false, err
	}
	requestLog, err := StartRequestLog(route, prepared, operation, RequestLogLink{
		CallID: options.CallID, AttemptID: attemptID,
	})
	if err != nil {
		cancelErr := reservation.Cancel()
		ledger.failAttempt(attemptID, errors.Join(err, cancelErr), nil, "")
		e.selector.Release(route.KeyID)
		return nil, false, errors.Join(err, cancelErr)
	}
	if request.Stream {
		stream, streamErr := selected.StreamPrepared(ctx, invocation, prepared)
		if streamErr != nil || stream == nil {
			if streamErr == nil {
				streamErr = errors.New("transport returned a nil event stream")
			}
			cancelErr := reservation.Cancel()
			combinedErr := errors.Join(streamErr, cancelErr)
			logErr := requestLog.CompleteStream(0, combinedErr)
			if errors.Is(streamErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				ledger.cancelAttempt(attemptID, streamErr)
			} else {
				ledger.failAttempt(attemptID, combinedErr, nil, "")
			}
			e.selector.Release(route.KeyID)
			return nil, true, errors.Join(streamErr, cancelErr, logErr)
		}
		return &Result{
			Prepared: prepared, Route: route, RequestLogID: requestLog.Record().ID,
			CallID: options.CallID, AttemptID: attemptID,
			Stream: &StreamResult{
				stream: stream, Prepared: prepared, Route: route, CallID: options.CallID, AttemptID: attemptID,
				reservation: reservation, requestLog: requestLog, ledger: ledger,
				transcript:          canonical.NewEventAccumulator(),
				keepCallOpenOnError: options.KeepCallOpenOnError,
				deferCallCompletion: options.DeferCallCompletion,
				projectConversation: options.ProjectConversation,
				release:             func() { e.selector.Release(route.KeyID) },
			}}, false, nil
	}

	response, executeErr := selected.ExecutePrepared(ctx, invocation, prepared)
	e.selector.Release(route.KeyID)
	if executeErr != nil {
		leaseErr := ledger.leaseFailure()
		if leaseErr != nil {
			executeErr = errors.Join(leaseErr, executeErr)
		}
		cancelErr := reservation.Cancel()
		combinedErr := errors.Join(executeErr, cancelErr)
		logErr := requestLog.CompleteResponse(nil, 0, combinedErr)
		if leaseErr != nil {
			ledger.failAttempt(attemptID, combinedErr, nil, "")
		} else if errors.Is(executeErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			ledger.cancelAttempt(attemptID, executeErr)
		} else {
			ledger.failAttempt(attemptID, combinedErr, nil, "")
		}
		return nil, true, errors.Join(executeErr, cancelErr, logErr)
	}
	if leaseErr := ledger.leaseFailure(); leaseErr != nil {
		cancelErr := reservation.Cancel()
		combinedErr := errors.Join(leaseErr, cancelErr)
		logErr := requestLog.CompleteResponse(nil, 0, combinedErr)
		ledger.failAttempt(attemptID, combinedErr, nil, "")
		return nil, false, errors.Join(combinedErr, logErr)
	}
	ledger.markFirstByte(attemptID)
	if responseBody, marshalErr := json.Marshal(response); marshalErr == nil {
		ledger.recordPayload(attemptID, model.APICallPayloadUpstreamResponse, responseBody)
	}
	providerResponseID := response.ProviderResponseID
	if providerResponseID == "" {
		providerResponseID = response.ID
	}
	settleErr := reservation.Settle(response.Usage)
	logErr := requestLog.CompleteResponse(&response, http.StatusOK, settleErr)
	if options.ProjectConversation {
		terminalProjection = conversationProjectionOutputRequest(options.CallID, requestLog.Record().ID, response)
		ledger.setConversationProjection(terminalProjection)
		stageConversationProjectionOutputBestEffort(terminalProjection)
	}
	ledger.completeAttempt(attemptID, response.Usage, providerResponseID)
	if settleErr != nil || logErr != nil {
		return nil, false, errors.Join(settleErr, logErr)
	}
	if !options.DeferCallCompletion {
		if completeErr := ledger.completeCall(attemptID, response.Usage, terminalProjection); errors.Is(completeErr, service.ErrAPICallLeaseUnavailable) {
			return nil, false, completeErr
		}
	}
	return &Result{
		Response: &response, Prepared: prepared, Route: route, RequestLogID: requestLog.Record().ID,
		CallID: options.CallID, AttemptID: attemptID, ledger: ledger, usage: cloneUsage(response.Usage),
		conversationProjection: cloneConversationProjectionOutputRequest(terminalProjection),
	}, false, nil
}

func callBillingContext(callID string, attemptID uint, route *routing.RouteResult) service.BillingContext {
	if callID == "" || route == nil {
		return service.BillingContext{}
	}
	snapshot, _ := json.Marshal(map[string]any{
		"price_mode":   route.PriceMode,
		"input_price":  route.InputPrice,
		"output_price": route.OutputPrice,
		"public_model": route.ModelName,
		"vendor_model": route.VendorModel,
		"transport":    route.Transport,
	})
	return service.BillingContext{
		CallID: callID, AttemptID: attemptID,
		PricingSnapshot: datatypes.JSON(snapshot),
	}
}

func attemptBillingOptions(options ExecuteOptions, attempt int) ExecuteOptions {
	if attempt > 0 && options.BillingKey != "" {
		options.BillingKey = fmt.Sprintf("%s:attempt:%d", options.BillingKey, attempt+1)
	}
	return options
}

func retryableUpstreamError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	status := domain.UpstreamStatusCode(err)
	return status == 0 || status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound ||
		status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || status >= 500
}

type streamDisposition uint8

const (
	streamSettle streamDisposition = iota
	streamCancel
	streamRetain
)

func (s *StreamResult) Next(ctx context.Context) (canonical.Event, error) {
	if s == nil || s.stream == nil {
		return canonical.Event{}, errors.New("event stream is not initialized")
	}
	s.nextMu.Lock()
	defer s.nextMu.Unlock()
	if s.prefetched != nil {
		event := *s.prefetched
		s.prefetched = nil
		return event, nil
	}
	return s.nextLocked(ctx)
}

func (s *StreamResult) prefetch(ctx context.Context) (canonical.Event, error) {
	if s == nil || s.stream == nil {
		return canonical.Event{}, errors.New("event stream is not initialized")
	}
	s.nextMu.Lock()
	defer s.nextMu.Unlock()
	if s.prefetched != nil {
		return *s.prefetched, nil
	}
	event, err := s.nextLocked(ctx)
	if err == nil {
		copy := event
		s.prefetched = &copy
	}
	return event, err
}

func (s *StreamResult) nextLocked(ctx context.Context) (canonical.Event, error) {
	if s.isDone() {
		return canonical.Event{}, io.EOF
	}

	event, err := s.stream.Next(ctx)
	if err != nil {
		cause := err
		leaseErr := s.ledger.leaseFailure()
		if leaseErr != nil {
			cause = errors.Join(leaseErr, err)
		}
		if errors.Is(err, io.EOF) {
			cause = ErrStreamEndedWithoutTerminal
			if leaseErr != nil {
				cause = errors.Join(leaseErr, cause)
			}
		}
		disposition := streamCancel
		if s.hasProduced() {
			disposition = streamRetain
		}
		cancelled := leaseErr == nil && (errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled))
		return canonical.Event{}, s.finish(disposition, nil, cause, true, cancelled, cancelled)
	}

	s.observe(event)
	if leaseErr := s.ledger.leaseFailure(); leaseErr != nil {
		disposition := streamCancel
		if s.hasProduced() {
			disposition = streamRetain
		}
		return canonical.Event{}, s.finish(disposition, nil, leaseErr, true, false, false)
	}
	if isTerminalEvent(event.Type) {
		s.markTerminal()
		return event, s.finish(streamSettle, s.currentUsage(), terminalEventError(event), false, false, false)
	}
	return event, nil
}

func (s *StreamResult) Close() error {
	if s == nil {
		return nil
	}
	s.closeUnderlying()
	s.nextMu.Lock()
	defer s.nextMu.Unlock()
	if s.isDone() {
		return s.finishedError()
	}
	if s.isTerminal() {
		return s.finish(streamSettle, s.currentUsage(), nil, false, false, false)
	}
	disposition := streamCancel
	if s.hasProduced() {
		disposition = streamRetain
	}
	return s.finish(disposition, nil, ErrStreamClosedWithoutTerminal, true, true, true)
}

// Abort stops a stream with an explicit downstream outcome. It is used when
// protocol encoding fails or the client writer rejects a frame.
func (s *StreamResult) Abort(err error, clientDisconnected bool) error {
	if s == nil {
		return nil
	}
	if err == nil {
		err = errors.New("downstream stream delivery failed")
	}
	s.closeUnderlying()
	s.nextMu.Lock()
	defer s.nextMu.Unlock()
	if s.isDone() {
		return s.FailDelivery(err, clientDisconnected)
	}
	disposition := streamCancel
	if s.hasProduced() {
		disposition = streamRetain
	}
	return s.finish(disposition, nil, err, true, clientDisconnected, clientDisconnected)
}

// CompleteDelivery completes a deferred streaming call after its terminal
// event has been written to the downstream response.
func (s *StreamResult) CompleteDelivery() error {
	if s == nil || s.ledger == nil {
		return nil
	}
	return s.ledger.completeCall(s.AttemptID, s.currentUsage(), s.currentConversationProjection())
}

// FailDelivery records a deferred streaming response that could not be sent.
func (s *StreamResult) FailDelivery(err error, clientDisconnected bool) error {
	if s == nil || s.ledger == nil {
		return nil
	}
	if err == nil {
		err = errors.New("downstream stream delivery failed")
	}
	projection := s.currentConversationProjection()
	if clientDisconnected {
		return s.ledger.cancelCall(err, true, projection)
	}
	return s.ledger.failCall(err, s.currentUsage(), false, projection)
}

func (s *StreamResult) observe(event canonical.Event) {
	s.requestLog.Observe(event)
	s.firstByteOnce.Do(func() {
		s.ledger.markFirstByte(s.AttemptID)
	})
	s.stateMu.Lock()
	s.produced = true
	if s.transcript == nil {
		s.transcript = canonical.NewEventAccumulator()
	}
	s.transcript.Observe(event)
	if usage := usageFromEvent(event); usage != nil {
		copy := *usage
		s.usage = &copy
	}
	if event.ProviderResponseID != "" {
		s.providerResponseID = event.ProviderResponseID
	}
	if event.Response != nil {
		if event.Response.ProviderResponseID != "" {
			s.providerResponseID = event.Response.ProviderResponseID
		} else if event.Response.ID != "" && s.providerResponseID == "" {
			s.providerResponseID = event.Response.ID
		}
	}
	s.stateMu.Unlock()
}

// CanonicalResponse returns an immutable snapshot of stream output observed
// before any downstream protocol encoder flattened the events.
func (s *StreamResult) CanonicalResponse() canonical.Response {
	if s == nil {
		return canonical.Response{}
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.transcript == nil {
		return canonical.Response{}
	}
	return s.transcript.Snapshot()
}

func (s *StreamResult) finish(
	disposition streamDisposition,
	usage *canonical.Usage,
	requestErr error,
	exposeRequestErr bool,
	cancelled bool,
	clientDisconnected bool,
) error {
	s.finishOnce.Do(func() {
		closeErr := s.closeUnderlying()
		var billingErr error
		switch disposition {
		case streamSettle:
			billingErr = s.reservation.Settle(usage)
		case streamCancel:
			billingErr = s.reservation.Cancel()
		case streamRetain:
			billingErr = s.reservation.Retain()
		}
		recordedErr := errors.Join(requestErr, billingErr, closeErr)
		s.ledger.recordPayload(s.AttemptID, model.APICallPayloadUpstreamResponse, s.requestLog.StreamPayload())
		logErr := s.requestLog.CompleteStream(0, recordedErr)
		var projection *service.ConversationProjectionOutputRequest
		if s.projectConversation {
			projectionResponse := s.CanonicalResponse()
			if projectionResponse.ProviderResponseID == "" {
				projectionResponse.ProviderResponseID = s.currentProviderResponseID()
			}
			projection = conversationProjectionOutputRequest(s.CallID, s.requestLog.Record().ID, projectionResponse)
			s.ledger.setConversationProjection(projection)
			stageConversationProjectionOutputBestEffort(projection)
		}
		if s.release != nil {
			s.release()
		}
		resultErr := errors.Join(billingErr, closeErr, logErr)
		if exposeRequestErr {
			resultErr = errors.Join(requestErr, resultErr)
		}
		ledgerErr := requestErr
		attemptCompleted := disposition == streamSettle && requestErr == nil
		if ledgerErr == nil {
			ledgerErr = errors.Join(billingErr, closeErr, logErr)
		}
		outcome := &streamLedgerOutcome{
			usage: cloneUsage(usage), requestErr: ledgerErr,
			attemptCompleted: attemptCompleted, cancelled: cancelled,
			clientDisconnected:     clientDisconnected,
			conversationProjection: cloneConversationProjectionOutputRequest(projection),
		}
		s.finalizeLedgerAttempt(outcome)
		s.stateMu.Lock()
		s.done = true
		s.finishErr = resultErr
		s.conversationProjection = cloneConversationProjectionOutputRequest(projection)
		ledgerActive := s.ledgerActive
		if !ledgerActive {
			s.ledgerOutcome = outcome
		}
		s.stateMu.Unlock()
		if ledgerActive {
			s.finalizeLedgerCall(outcome)
		}
	})
	return s.finishedError()
}

func (s *StreamResult) activateLedger() {
	if s == nil {
		return
	}
	s.stateMu.Lock()
	s.ledgerActive = true
	outcome := s.ledgerOutcome
	s.ledgerOutcome = nil
	s.stateMu.Unlock()
	if outcome != nil {
		s.finalizeLedgerCall(outcome)
	}
}

func (s *StreamResult) finalizeLedgerAttempt(outcome *streamLedgerOutcome) {
	if s == nil || outcome == nil || s.ledger == nil {
		return
	}
	providerResponseID := s.currentProviderResponseID()
	if outcome.cancelled {
		s.ledger.cancelAttempt(s.AttemptID, outcome.requestErr)
		return
	}
	if outcome.attemptCompleted {
		s.ledger.completeAttempt(s.AttemptID, outcome.usage, providerResponseID)
		return
	}
	s.ledger.failAttempt(s.AttemptID, outcome.requestErr, outcome.usage, providerResponseID)
}

func (s *StreamResult) finalizeLedgerCall(outcome *streamLedgerOutcome) {
	if s == nil || outcome == nil || s.ledger == nil {
		return
	}
	if s.keepCallOpenOnError && (outcome.cancelled || outcome.requestErr != nil) {
		s.ledger.releaseLease()
		return
	}
	if outcome.cancelled {
		s.ledger.cancelCall(outcome.requestErr, outcome.clientDisconnected, outcome.conversationProjection)
		return
	}
	if outcome.attemptCompleted && outcome.requestErr == nil {
		if s.deferCallCompletion {
			return
		}
		s.ledger.completeCall(s.AttemptID, outcome.usage, outcome.conversationProjection)
		return
	}
	s.ledger.failCall(outcome.requestErr, outcome.usage, outcome.clientDisconnected, outcome.conversationProjection)
}

func cloneUsage(usage *canonical.Usage) *canonical.Usage {
	if usage == nil {
		return nil
	}
	copy := *usage
	if usage.Extra != nil {
		copy.Extra = make(map[string]json.RawMessage, len(usage.Extra))
		for key, value := range usage.Extra {
			copy.Extra[key] = append(json.RawMessage(nil), value...)
		}
	}
	return &copy
}

func (s *StreamResult) currentProviderResponseID() string {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.providerResponseID
}

func (s *StreamResult) currentConversationProjection() *service.ConversationProjectionOutputRequest {
	if s == nil {
		return nil
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return cloneConversationProjectionOutputRequest(s.conversationProjection)
}

func (s *StreamResult) closeUnderlying() error {
	s.closeOnce.Do(func() {
		if s.stream != nil {
			s.closeErr = s.stream.Close()
		}
	})
	return s.closeErr
}

func (s *StreamResult) markTerminal() {
	s.stateMu.Lock()
	s.terminal = true
	s.stateMu.Unlock()
}

func (s *StreamResult) isTerminal() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.terminal
}

func (s *StreamResult) hasProduced() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.produced
}

func (s *StreamResult) currentUsage() *canonical.Usage {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.usage == nil {
		return nil
	}
	copy := *s.usage
	return &copy
}

func (s *StreamResult) isDone() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.done
}

func (s *StreamResult) finishedError() error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.finishErr
}

func isTerminalEvent(eventType canonical.EventType) bool {
	switch eventType {
	case canonical.EventCompleted, canonical.EventIncomplete, canonical.EventFailed, canonical.EventError:
		return true
	default:
		return false
	}
}

func usageFromEvent(event canonical.Event) *canonical.Usage {
	if event.Usage != nil {
		return event.Usage
	}
	if event.Response != nil {
		return event.Response.Usage
	}
	return nil
}

type upstreamStreamError struct {
	status  int
	message string
}

func (e *upstreamStreamError) Error() string {
	if e.message != "" {
		return e.message
	}
	return "upstream stream failed"
}

func (e *upstreamStreamError) HTTPStatus() int { return e.status }

func terminalEventError(event canonical.Event) error {
	if event.Type != canonical.EventFailed && event.Type != canonical.EventError {
		return nil
	}
	detail := event.Error
	if detail == nil && event.Response != nil {
		detail = event.Response.Error
	}
	if detail == nil {
		return &upstreamStreamError{message: string(event.Type)}
	}
	return &upstreamStreamError{status: detail.Status, message: detail.Message}
}

type plannedTransport struct {
	id           transport.ID
	plan         transport.Plan
	requirements canonical.FeatureSet
}

func (e *Engine) plans(ctx context.Context, operation transport.Operation, request canonical.Request, requirements canonical.FeatureSet, prepare TransportPreparer) ([]plannedTransport, error) {
	plans := make([]plannedTransport, 0, len(e.transports.IDs()))
	for _, id := range e.transports.IDs() {
		item, ok := e.transports.Get(id)
		if !ok {
			continue
		}
		plannedRequest := request.Clone()
		if prepare != nil {
			var err error
			plannedRequest, err = prepare(ctx, plannedRequest, id)
			if err != nil {
				return nil, err
			}
		}
		planRequirements := mergeFeatures(requirements, plannedRequest.RequiredFeatures())
		plan := item.Plan(operation, plannedRequest.Clone(), planRequirements)
		if plan.Supported() {
			plans = append(plans, plannedTransport{id: id, plan: plan, requirements: planRequirements})
		}
	}
	sort.SliceStable(plans, func(i, j int) bool {
		if plans[i].plan.Kind != plans[j].plan.Kind {
			return plans[i].plan.Kind > plans[j].plan.Kind
		}
		return plans[i].id < plans[j].id
	})
	return plans, nil
}

func requirementsForPlans(base canonical.FeatureSet, plans []plannedTransport) canonical.FeatureSet {
	requirements := mergeFeatures(nil, base)
	for _, plan := range plans {
		requirements = mergeFeatures(requirements, plan.requirements)
	}
	return requirements
}

func operationFor(endpoint canonical.Endpoint) (transport.Operation, error) {
	switch endpoint {
	case canonical.EndpointOpenAIChat:
		return transport.OperationChat, nil
	case canonical.EndpointOpenAIResponses:
		return transport.OperationResponses, nil
	case canonical.EndpointAnthropic:
		return transport.OperationMessages, nil
	default:
		return "", fmt.Errorf("unsupported downstream endpoint %q", endpoint)
	}
}

func routingRequirements(features canonical.FeatureSet) routing.RouteRequirements {
	result := make(routing.RouteRequirements, len(features))
	for feature, enabled := range features {
		if enabled {
			result.Require(routing.Capability(feature))
		}
	}
	return result
}

func planIDs(plans []plannedTransport) []transport.ID {
	result := make([]transport.ID, 0, len(plans))
	for _, plan := range plans {
		result = append(result, plan.id)
	}
	return result
}

func preferredPlanIDs(plans []plannedTransport) []transport.ID {
	result := make([]transport.ID, 0, len(plans))
	for _, plan := range plans {
		if plan.plan.Kind == transport.PlanExact {
			result = append(result, plan.id)
		}
	}
	for _, plan := range plans {
		if plan.plan.Kind == transport.PlanConverted {
			result = append(result, plan.id)
		}
	}
	return result
}

func routeToTransport(route *routing.RouteResult) transport.Route {
	config := make(map[string]any, len(route.ChannelConfig)+len(route.TransportConfig))
	for key, value := range route.ChannelConfig {
		config[key] = value
	}
	for key, value := range route.TransportConfig {
		config[key] = value
	}
	return transport.Route{
		AbilityID: route.AbilityID, ChannelID: route.ChannelID, KeyID: route.KeyID,
		BaseURL: route.BaseURL, APIKey: route.APIKey, VendorModel: route.VendorModel,
		PublicModel: route.ModelName, ExtraHeaders: route.ExtraHeaders, Config: config,
	}
}
