// Package engine is the single Gateway V2 execution path.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/service"
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
	circuit    *routing.Circuit
}

func New(selector Selector, transports *transport.Registry, billing *service.BillingService) (*Engine, error) {
	if selector == nil || transports == nil || billing == nil {
		return nil, errors.New("selector, transport registry, and billing service are required")
	}
	return &Engine{selector: selector, transports: transports, billing: billing, circuit: routing.NewCircuit()}, nil
}

type RoutePreparer func(context.Context, canonical.Request, *routing.RouteResult) (canonical.Request, error)
type TransportPreparer func(context.Context, canonical.Request, transport.ID) (canonical.Request, error)

type ExecuteOptions struct {
	UserID           uint
	TokenID          uint
	BillingKey       string
	MaxAttempts      int
	PrepareRoute     RoutePreparer
	PrepareTransport TransportPreparer
}

type Result struct {
	Response     *canonical.Response
	Prepared     transport.PreparedRequest
	Route        *routing.RouteResult
	RequestLogID uint
	Stream       *StreamResult
}

type StreamResult struct {
	Prepared transport.PreparedRequest
	Route    *routing.RouteResult

	stream      transport.EventStream
	reservation *Reservation
	requestLog  *RequestLog
	release     func()

	nextMu     sync.Mutex
	prefetched *canonical.Event
	stateMu    sync.Mutex
	produced   bool
	terminal   bool
	usage      *canonical.Usage
	done       bool
	finishErr  error

	finishOnce sync.Once
	closeOnce  sync.Once
	closeErr   error
}

func (e *Engine) Execute(ctx context.Context, request canonical.Request, options ExecuteOptions) (*Result, error) {
	operation, err := operationFor(request.Endpoint)
	if err != nil {
		return nil, err
	}
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
		result, upstreamErr, err := e.executeSelected(ctx, attemptRequest, operation, attemptRequirements, route, attemptBillingOptions(options, attempt))
		if err == nil && result != nil && result.Stream != nil && maxAttempts > 1 {
			if _, prefetchErr := result.Stream.prefetch(ctx); prefetchErr != nil {
				upstreamErr, err = true, prefetchErr
			} else {
				return result, nil
			}
		}
		if err == nil {
			return result, nil
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

func (e *Engine) executeSelected(ctx context.Context, request canonical.Request, operation transport.Operation, requirements canonical.FeatureSet, route *routing.RouteResult, options ExecuteOptions) (*Result, bool, error) {
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
	reservation, err := Reserve(e.billing, options.TokenID, options.UserID, route, request, options.BillingKey)
	if err != nil {
		e.selector.Release(route.KeyID)
		return nil, false, err
	}
	requestLog, err := StartRequestLog(route, prepared, operation)
	if err != nil {
		cancelErr := reservation.Cancel()
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
			logErr := requestLog.CompleteStream(0, errors.Join(streamErr, cancelErr))
			e.selector.Release(route.KeyID)
			return nil, true, errors.Join(streamErr, cancelErr, logErr)
		}
		return &Result{Prepared: prepared, Route: route, RequestLogID: requestLog.Record().ID, Stream: &StreamResult{
			stream: stream, Prepared: prepared, Route: route, reservation: reservation, requestLog: requestLog,
			release: func() { e.selector.Release(route.KeyID) },
		}}, false, nil
	}

	response, executeErr := selected.ExecutePrepared(ctx, invocation, prepared)
	e.selector.Release(route.KeyID)
	if executeErr != nil {
		cancelErr := reservation.Cancel()
		logErr := requestLog.CompleteResponse(nil, 0, errors.Join(executeErr, cancelErr))
		return nil, true, errors.Join(executeErr, cancelErr, logErr)
	}
	settleErr := reservation.Settle(response.Usage)
	logErr := requestLog.CompleteResponse(&response, http.StatusOK, settleErr)
	if settleErr != nil || logErr != nil {
		return nil, false, errors.Join(settleErr, logErr)
	}
	return &Result{Response: &response, Prepared: prepared, Route: route, RequestLogID: requestLog.Record().ID}, false, nil
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
		if errors.Is(err, io.EOF) {
			cause = ErrStreamEndedWithoutTerminal
		}
		disposition := streamCancel
		if s.hasProduced() {
			disposition = streamRetain
		}
		return canonical.Event{}, s.finish(disposition, nil, cause, true)
	}

	s.observe(event)
	if isTerminalEvent(event.Type) {
		s.markTerminal()
		return event, s.finish(streamSettle, s.currentUsage(), terminalEventError(event), false)
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
		return s.finish(streamSettle, s.currentUsage(), nil, false)
	}
	disposition := streamCancel
	if s.hasProduced() {
		disposition = streamRetain
	}
	return s.finish(disposition, nil, ErrStreamClosedWithoutTerminal, true)
}

func (s *StreamResult) observe(event canonical.Event) {
	s.requestLog.Observe(event)
	s.stateMu.Lock()
	s.produced = true
	if usage := usageFromEvent(event); usage != nil {
		copy := *usage
		s.usage = &copy
	}
	s.stateMu.Unlock()
}

func (s *StreamResult) finish(disposition streamDisposition, usage *canonical.Usage, requestErr error, exposeRequestErr bool) error {
	s.finishOnce.Do(func() {
		closeErr := s.closeUnderlying()
		var billingErr error
		switch disposition {
		case streamSettle:
			billingErr = s.reservation.Settle(usage)
		case streamCancel:
			billingErr = s.reservation.Cancel()
		case streamRetain:
			// Partial output without a terminal usage record keeps the reservation.
		}
		recordedErr := errors.Join(requestErr, billingErr, closeErr)
		logErr := s.requestLog.CompleteStream(0, recordedErr)
		if s.release != nil {
			s.release()
		}
		resultErr := errors.Join(billingErr, closeErr, logErr)
		if exposeRequestErr {
			resultErr = errors.Join(requestErr, resultErr)
		}
		s.stateMu.Lock()
		s.done = true
		s.finishErr = resultErr
		s.stateMu.Unlock()
	})
	return s.finishedError()
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
