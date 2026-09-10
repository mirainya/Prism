package engine

import (
	"context"
	"errors"
	"sync"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

// DeferredPlan fixes a catalog route without creating an Attempt or sending an
// upstream request. The caller persists the route identity before a worker is
// allowed to execute it.
type DeferredPlan struct {
	Route     *routing.RouteResult
	Request   canonical.Request
	Operation transport.Operation
}

// PlanDeferred selects one immutable catalog route for durable execution.
func (e *Engine) PlanDeferred(ctx context.Context, request canonical.Request, options ExecuteOptions) (*DeferredPlan, error) {
	if e == nil || e.selector == nil || e.transports == nil {
		return nil, repository.ErrInvalidInput
	}
	operation, err := operationFor(request.Endpoint)
	if err != nil {
		return nil, err
	}
	if options.CallID == "" {
		return nil, routing.ErrInvalidSelectionKey
	}
	requirements := request.RequiredFeatures()
	plans, err := e.plans(ctx, operation, request, requirements, options.PrepareTransport)
	if err != nil {
		return nil, err
	}
	if len(plans) == 0 {
		return nil, ErrNoTransportPlan
	}

	attempts := make([]routing.TransportAttempt, 0, 3)
	transportHints := append([]string(nil), request.TransportHints...)
	var planErrors []error
	for len(attempts) < 3 {
		attemptPlans := filterHintedPlans(plans, transportHints)
		if len(attemptPlans) == 0 {
			return nil, errors.Join(append(planErrors, ErrNoTransportPlan)...)
		}
		selectionRequirements := requirementsForPlans(requirements, attemptPlans)
		route, selectErr := e.selector.SelectTransport(ctx, request.Model, routingRequirements(selectionRequirements), routing.RouteOptions{
			SelectionKey: options.CallID, OperationMethod: "POST", OperationPath: canonicalOperationPath(request.Endpoint),
			AllowedTransports: planIDs(attemptPlans), PreferredTransports: preferredPlanIDs(attemptPlans),
			ExcludeAttempts: attempts, ResponsesRequest: operation == transport.OperationResponses,
		})
		if selectErr != nil {
			return nil, errors.Join(append(planErrors, selectErr)...)
		}
		if route == nil || !unifiedRoute(route) {
			return nil, repository.ErrInvalidInput
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
		if routeSupportsFeatures(route, attemptRequirements) && e.transportSupports(route.Transport, operation, attemptRequest, attemptRequirements) {
			e.selector.Release(route.KeyID)
			return &DeferredPlan{Route: route, Request: attemptRequest, Operation: operation}, nil
		}
		e.selector.Release(route.KeyID)
		attempts = append(attempts, routing.TransportAttempt{KeyID: route.KeyID, Transport: route.Transport})
		planErrors = append(planErrors, routing.ErrCapabilityUnavailable)
		requirements = mergeFeatures(requirements, attemptRequirements)
		plans, err = e.plans(ctx, operation, request, requirements, options.PrepareTransport)
		if err != nil {
			return nil, errors.Join(append(planErrors, err)...)
		}
	}
	return nil, errors.Join(planErrors...)
}

// FixedRequest is a prepared request bound to the Attempt selected at create
// time. Execute is single-use so one worker cannot accidentally send it twice.
type FixedRequest struct {
	selected   transport.Transport
	invocation transport.Invocation
	prepared   transport.PreparedRequest
	mu         sync.Mutex
	executed   bool
}

func (r *FixedRequest) Prepared() transport.PreparedRequest {
	if r == nil {
		return transport.PreparedRequest{}
	}
	return r.prepared.Clone()
}

func (r *FixedRequest) Execute(ctx context.Context) (canonical.Response, error) {
	if r == nil || r.selected == nil {
		return canonical.Response{}, repository.ErrInvalidInput
	}
	r.mu.Lock()
	if r.executed {
		r.mu.Unlock()
		return canonical.Response{}, repository.ErrConflict
	}
	r.executed = true
	r.mu.Unlock()
	return r.selected.ExecutePrepared(ctx, r.invocation, r.prepared.Clone())
}

// PrepareFixed validates and prepares a request against an already persisted
// route. It performs no network I/O and creates no lifecycle records.
func (e *Engine) PrepareFixed(ctx context.Context, route *routing.RouteResult, request canonical.Request) (*FixedRequest, error) {
	if e == nil || e.transports == nil || route == nil || !unifiedRoute(route) {
		return nil, repository.ErrInvalidInput
	}
	operation, err := operationFor(request.Endpoint)
	if err != nil {
		return nil, err
	}
	selected, ok := e.transports.Get(route.Transport)
	if !ok {
		return nil, ErrNoTransportPlan
	}
	requirements := request.RequiredFeatures()
	if !routeSupportsFeatures(route, requirements) || !selected.Plan(operation, request.Clone(), requirements).Supported() {
		return nil, routing.ErrCapabilityUnavailable
	}
	invocation := transport.Invocation{Route: routeToTransport(route), Request: request.Clone(), Operation: operation}
	prepared, err := selected.Prepare(ctx, invocation)
	if err != nil {
		return nil, err
	}
	if prepared.Stream {
		return nil, repository.ErrInvalidInput
	}
	return &FixedRequest{selected: selected, invocation: invocation, prepared: prepared.Clone()}, nil
}
