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

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

var (
	ErrNoTransportPlan             = errors.New("no upstream transport can execute this request")
	ErrStreamEndedWithoutTerminal  = errors.New("upstream stream ended without a terminal event")
	ErrStreamClosedWithoutTerminal = errors.New("upstream stream was closed without a terminal event")
)

type Selector interface {
	SelectTransport(context.Context, string, routing.RouteRequirements, routing.RouteOptions) (*routing.RouteResult, error)
	Release(uint)
}

// Engine 编排一次网关调用的完整生命周期：能力规划、选路、重试、计费、
// 调用台账、会话投影以及流式资源释放。具体协议转换仍由 transport 负责。
type Engine struct {
	selector   Selector
	transports *transport.Registry
	circuit    *routing.Circuit
}

func New(selector Selector, transports *transport.Registry) (*Engine, error) {
	if selector == nil || transports == nil {
		return nil, errors.New("selector and transport registry are required")
	}
	return &Engine{selector: selector, transports: transports, circuit: routing.NewCircuit()}, nil
}

type RoutePreparer func(context.Context, canonical.Request, *routing.RouteResult) (canonical.Request, error)
type TransportPreparer func(context.Context, canonical.Request, transport.ID) (canonical.Request, error)

// ExecuteOptions 补充 canonical.Request 中不属于协议正文的执行上下文。
// KeepCallOpenOnError 用于由外层工作流决定终态的后台调用；
// DeferCallCompletion 用于等响应确实写给下游后再完成调用台账。
type ExecuteOptions struct {
	UserID                   uint
	TokenID                  uint
	CallID                   string
	RequestID                string
	DownstreamEndpoint       string
	DownstreamRequest        []byte
	ResourceType             string
	ResourceID               string
	ResourceSummary          any
	PreviousResourceID       *uint64
	Idempotency              *repository.IdempotencyInput
	EnforceIdempotencyPolicy bool
	ConversationID           uint
	ProjectConversation      bool
	ConversationInput        *service.ConversationProjectionInputRequest
	KeepCallOpenOnError      bool
	DeferCallCompletion      bool
	BillingKey               string
	MaxAttempts              int
	PrepareRoute             RoutePreparer
	PrepareTransport         TransportPreparer
}

// IdempotentReplayError stops execution after the existing Call has been
// resolved. The protocol layer reads the immutable result from unified storage.
type IdempotentReplayError struct {
	CallID, ResourceID             uint64
	CallPublicID, ResourcePublicID string
}

func (e *IdempotentReplayError) Error() string {
	return "gateway engine: idempotent replay resolved to an existing call"
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
	reservation         reservationLifecycle
	requestLog          *RequestLog
	ledger              *callLifecycle
	keepCallOpenOnError bool
	deferCallCompletion bool
	projectConversation bool
	release             func()

	// nextMu 保证预读与下游消费不会并发推进同一个上游流；stateMu 保护聚合结果和终态。
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

	finishOnce sync.Once
	closeOnce  sync.Once
	closeErr   error
}

type callLifecycle struct {
	unified         *unifiedLifecycle
	callID          string
	executionCtx    context.Context
	cancelExecution context.CancelCauseFunc

	mu                     sync.Mutex
	finalAttemptID         uint
	providerResponseID     string
	conversationProjection *service.ConversationProjectionOutputRequest
	finished               bool
}

func (l *callLifecycle) startUnified(ctx context.Context, route *routing.RouteResult, request canonical.Request, options ExecuteOptions) error {
	if l == nil || !unifiedRoute(route) || l.unified != nil {
		return nil
	}
	u, err := newUnifiedLifecycle(ctx, route, request, l.callID, options)
	if err != nil {
		logLedgerError("create unified gateway call", l.callID, 0, err)
		return err
	}
	l.mu.Lock()
	l.unified = u
	l.mu.Unlock()
	return nil
}

func (l *callLifecycle) startUnifiedAttempt(ctx context.Context, route *routing.RouteResult, asynchronous bool, scopeKey string) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	u := l.unified
	l.mu.Unlock()
	if u == nil {
		return nil
	}
	if err := u.startAttempt(ctx, route, asynchronous, scopeKey); err != nil {
		logLedgerError("start unified gateway attempt", l.callID, 0, err)
		return unifiedAdmissionError(err)
	}
	return nil
}

func (l *callLifecycle) currentAttemptID() uint {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unified != nil {
		return uint(l.unified.attemptID)
	}
	return l.finalAttemptID
}

func (l *callLifecycle) finishUnified(ctx context.Context, attemptState execution.AttemptState, callState execution.CallState, reason string) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	u := l.unified
	l.mu.Unlock()
	if u == nil {
		return nil
	}
	if err := u.finish(ctx, attemptState, callState, reason); err != nil {
		logLedgerError("finish unified gateway lifecycle", l.callID, 0, err)
		return err
	}
	return nil
}

func (l *callLifecycle) reserveUnified(ctx context.Context, userID, tokenID uint, route *routing.RouteResult, request canonical.Request) (reservationLifecycle, error) {
	if l == nil {
		return nil, nil
	}
	l.mu.Lock()
	u := l.unified
	l.mu.Unlock()
	if u == nil {
		return nil, nil
	}
	return u.reserve(ctx, userID, tokenID, route, request)
}

type streamLedgerOutcome struct {
	usage                  *canonical.Usage
	requestErr             error
	attemptCompleted       bool
	cancelled              bool
	clientDisconnected     bool
	conversationProjection *service.ConversationProjectionOutputRequest
}

// newUnifiedCallLifecycle creates the in-process execution owner before the
// selected unified route is activated. It deliberately has no legacy lease;
// the gw_* attempt and credential slot are the only ownership records.
func newUnifiedCallLifecycle(ctx context.Context, callID string) *callLifecycle {
	if strings.TrimSpace(callID) == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	executionCtx, cancelExecution := context.WithCancelCause(ctx)
	return &callLifecycle{callID: callID, executionCtx: executionCtx, cancelExecution: cancelExecution}
}

func (l *callLifecycle) Context() context.Context {
	if l == nil || l.executionCtx == nil {
		return context.Background()
	}
	return l.executionCtx
}

func (l *callLifecycle) releaseLease() {
	if l == nil {
		return
	}
	if l.cancelExecution != nil {
		l.cancelExecution(context.Canceled)
	}
}

func (e *Engine) beginSelectedCall(
	ctx context.Context,
	route *routing.RouteResult,
	request canonical.Request,
	operation transport.Operation,
	options ExecuteOptions,
) (*callLifecycle, error) {
	if !unifiedRoute(route) {
		return nil, fmt.Errorf("%w: selected route is not part of the active unified catalog", repository.ErrInvalidInput)
	}
	lifecycle := newUnifiedCallLifecycle(ctx, options.CallID)
	if lifecycle == nil {
		return nil, repository.ErrInvalidInput
	}
	if err := lifecycle.startUnified(ctx, route, request, options); err != nil {
		lifecycle.releaseLease()
		return nil, err
	}
	if options.ProjectConversation {
		if options.ConversationInput == nil {
			_ = lifecycle.finishUnified(ctx, execution.AttemptNotCreated, execution.CallFailed, "conversation_input_missing")
			lifecycle.releaseLease()
			return nil, fmt.Errorf("%w: projected API call input is required", service.ErrAPICallInvalidInput)
		}
		input := cloneConversationProjectionInputRequest(options.ConversationInput)
		input.CallID = lifecycle.callID
		if err := service.StageAPIConversationProjectionInput(input); err != nil {
			_ = lifecycle.finishUnified(ctx, execution.AttemptNotCreated, execution.CallFailed, "conversation_input_failed")
			lifecycle.releaseLease()
			return nil, err
		}
	}
	return lifecycle, nil
}

func (l *callLifecycle) recordResult(data []byte) error {
	if l == nil || len(data) == 0 {
		return nil
	}
	l.mu.Lock()
	u := l.unified
	l.mu.Unlock()
	if u == nil {
		return repository.ErrConflict
	}
	return u.recordPayload(context.Background(), "result", data)
}

func (l *callLifecycle) completeAttempt(attemptID uint, usage *canonical.Usage, providerResponseID string) {
	if l == nil || attemptID == 0 {
		return
	}
	l.mu.Lock()
	if providerResponseID != "" {
		l.providerResponseID = providerResponseID
	}
	l.mu.Unlock()
	_ = usage
	_ = l.finishUnified(context.Background(), execution.AttemptCompleted, execution.CallInProgress, "upstream_completed")
}

func (l *callLifecycle) failAttempt(attemptID uint, requestErr error, usage *canonical.Usage, providerResponseID string) {
	if l == nil || attemptID == 0 {
		return
	}
	_, _, _ = requestErr, usage, providerResponseID
	_ = l.finishUnified(context.Background(), execution.AttemptFailed, execution.CallInProgress, "upstream_failed")
}

func (l *callLifecycle) cancelAttempt(attemptID uint, requestErr error) {
	if l == nil || attemptID == 0 {
		return
	}
	_ = requestErr
	_ = l.finishUnified(context.Background(), execution.AttemptCancelled, execution.CallCancelled, "attempt_cancelled")
}

func (l *callLifecycle) completeCall(
	attemptID uint,
	usage *canonical.Usage,
	projection *service.ConversationProjectionOutputRequest,
) error {
	if l == nil {
		return nil
	}
	_, ok := l.beginFinish(attemptID)
	if !ok {
		return nil
	}
	_, _ = usage, projection
	err := l.finishUnified(context.Background(), "", execution.CallCompleted, "call_completed")
	l.releaseLease()
	return err
}

func (l *callLifecycle) failCall(
	requestErr error,
	usage *canonical.Usage,
	clientDisconnected bool,
	projection *service.ConversationProjectionOutputRequest,
) error {
	if l == nil {
		return nil
	}
	_, ok := l.beginFinish(0)
	if !ok {
		return nil
	}
	_, _, _, _ = requestErr, usage, clientDisconnected, projection
	err := l.finishUnified(context.Background(), "", execution.CallFailed, "call_failed")
	l.releaseLease()
	return err
}

func (l *callLifecycle) cancelCall(
	requestErr error,
	clientDisconnected bool,
	projection *service.ConversationProjectionOutputRequest,
) error {
	if l == nil {
		return nil
	}
	_, ok := l.beginFinish(0)
	if !ok {
		return nil
	}
	_, _, _ = requestErr, clientDisconnected, projection
	err := l.finishUnified(context.Background(), "", execution.CallCancelled, "call_cancelled")
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
	if l == nil || l.unified == nil {
		return 0, false
	}
	l.mu.Lock()
	// HTTP 写入、流关闭和租约失败可能同时触发终态，只有第一个调用者可以修改台账。
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

func cloneConversationProjectionInputRequest(
	request *service.ConversationProjectionInputRequest,
) service.ConversationProjectionInputRequest {
	if request == nil {
		return service.ConversationProjectionInputRequest{}
	}
	clone := *request
	clone.InputItems = canonical.CloneItems(request.InputItems)
	return clone
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
	if strings.TrimSpace(options.CallID) == "" {
		options.CallID = service.GenerateUnifiedCallID()
	}
	requestCtx := ctx
	var ledger *callLifecycle
	streamHandedOff := false
	defer func() {
		// 统一处理所有提前返回，防止已开始的调用永久停留在 running。
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
		if errors.Is(failure, context.Canceled) || errors.Is(requestCtx.Err(), context.Canceled) {
			ledger.cancelCall(failure, true, projection)
			return
		}
		ledger.failCall(failure, nil, false, projection)
	}()

	// 先让每个 transport 评估是否能原生或转换执行，再把可行方案交给数据库路由器选具体 key。
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
		route, selectErr := e.selector.SelectTransport(ctx, request.Model, routingRequirements(selectionRequirements), routing.RouteOptions{
			SelectionKey:    options.CallID,
			OperationMethod: "POST", OperationPath: canonicalOperationPath(request.Endpoint),
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

		// PrepareRoute 可根据已选渠道补充请求；补充后必须重新校验能力，不能沿用选路前的判断。
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
		if ledger == nil {
			ledger, err = e.beginSelectedCall(ctx, route, attemptRequest, operation, options)
			if err != nil {
				e.selector.Release(route.KeyID)
				return nil, err
			}
			if ledger != nil {
				ctx = ledger.Context()
				options.CallID = ledger.callID
			}
		}
		selectedResult, upstreamErr, err := e.executeSelected(
			ctx,
			attemptRequest,
			operation,
			attemptRequirements,
			route,
			options,
			ledger,
		)
		if err == nil && selectedResult != nil && selectedResult.Stream != nil && maxAttempts > 1 {
			// 流交给下游后便不能透明重试。先读取首个事件，可在尚未发送任何内容时替换失败路由。
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
		// 熔断粒度是 key + model + transport，同一 key 上的其他协议仍可继续使用。
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
	// 资源获取顺序固定为并发名额 -> 调用尝试 -> 计费预授权 -> 请求日志 -> 上游请求；
	// 后续每个错误分支都按相反方向释放已经取得的资源。
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
	if ledger == nil {
		e.selector.Release(route.KeyID)
		return nil, false, repository.ErrConflict
	}
	if err := ledger.startUnifiedAttempt(ctx, route, request.Background, fmt.Sprintf("credential:%d", route.CredentialID)); err != nil {
		_ = ledger.finishUnified(ctx, execution.AttemptNotCreated, execution.CallFailed, "attempt_not_created")
		e.selector.Release(route.KeyID)
		return nil, false, err
	}
	attemptID := ledger.currentAttemptID()
	if attemptID == 0 {
		e.selector.Release(route.KeyID)
		return nil, false, repository.ErrConflict
	}
	reservation, err := ledger.reserveUnified(ctx, options.UserID, options.TokenID, route, request)
	if err != nil {
		ledger.failAttempt(attemptID, err, nil, "")
		e.selector.Release(route.KeyID)
		return nil, false, err
	}
	if reservation == nil {
		err = fmt.Errorf("%w: unified billing reservation was not created", repository.ErrConflict)
		ledger.failAttempt(attemptID, err, nil, "")
		e.selector.Release(route.KeyID)
		return nil, false, err
	}
	requestLog, err := StartRequestLog(route, prepared, operation, RequestLogLink{
		CallID: options.CallID, AttemptID: attemptID, unified: ledger.unifiedRequestOwner(),
	})
	if err != nil {
		cancelErr := reservation.Cancel()
		ledger.failAttempt(attemptID, errors.Join(err, cancelErr), nil, "")
		e.selector.Release(route.KeyID)
		return nil, false, errors.Join(err, cancelErr)
	}
	if request.Stream {
		// 流的计费和日志只能在终止事件、读取失败或下游中断时结算，因此所有权转交给 StreamResult。
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

	// 非流式响应在本函数内拥有完整生命周期，可以立即释放并发名额并完成结算。
	response, executeErr := selected.ExecutePrepared(ctx, invocation, prepared)
	e.selector.Release(route.KeyID)
	if executeErr != nil {
		cancelErr := reservation.Cancel()
		combinedErr := errors.Join(executeErr, cancelErr)
		logErr := requestLog.CompleteResponse(nil, 0, combinedErr)
		if errors.Is(executeErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			ledger.cancelAttempt(attemptID, executeErr)
		} else {
			ledger.failAttempt(attemptID, combinedErr, nil, "")
		}
		return nil, true, errors.Join(executeErr, cancelErr, logErr)
	}
	responseBody, payloadErr := json.Marshal(response)
	if payloadErr == nil {
		payloadErr = ledger.recordResult(responseBody)
	}
	if payloadErr != nil {
		return nil, false, errors.Join(payloadErr, reservation.Retain(), requestLog.CompleteResponse(&response, http.StatusOK, payloadErr))
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
		if completeErr := ledger.completeCall(attemptID, response.Usage, terminalProjection); completeErr != nil {
			return nil, false, completeErr
		}
	}
	return &Result{
		Response: &response, Prepared: prepared, Route: route, RequestLogID: requestLog.Record().ID,
		CallID: options.CallID, AttemptID: attemptID, ledger: ledger, usage: cloneUsage(response.Usage),
		conversationProjection: cloneConversationProjectionOutputRequest(terminalProjection),
	}, false, nil
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
		if errors.Is(err, io.EOF) {
			cause = ErrStreamEndedWithoutTerminal
		}
		// 尚未产出内容时可全额取消预授权；已有部分输出但缺少最终 usage 时按预授权金额结算。
		disposition := streamCancel
		if s.hasProduced() {
			disposition = streamRetain
		}
		cancelled := errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)
		return canonical.Event{}, s.finish(disposition, nil, cause, true, cancelled, cancelled)
	}

	s.observe(event)
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
		// finishOnce 把主动关闭、读取错误、终止事件和下游写入失败归并为一次资源结算。
		closeErr := s.closeUnderlying()
		var billingErr error
		var payloadErr error
		if disposition == streamSettle {
			var body []byte
			body, payloadErr = json.Marshal(s.CanonicalResponse())
			if payloadErr == nil {
				payloadErr = s.ledger.recordResult(body)
			}
		}
		if payloadErr != nil {
			billingErr = errors.Join(payloadErr, s.reservation.Retain())
		} else {
			switch disposition {
			case streamSettle:
				billingErr = s.reservation.Settle(usage)
			case streamCancel:
				billingErr = s.reservation.Cancel()
			case streamRetain:
				billingErr = s.reservation.Retain()
			}
		}
		recordedErr := errors.Join(requestErr, billingErr, closeErr)
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
		// Attempt 可以立即记录；Call 要等 Engine 确认不再重试后才允许进入终态。
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
	// 预读期间先暂存调用结果，避免首事件失败时把整个 Call 提前标为失败，阻断 Engine 重试。
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
	// 原生协议优先于转换协议；ID 排序让同类方案的选择在不同进程中保持稳定。
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
