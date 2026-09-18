package routing

import (
	"context"
	"errors"
)

// Router resolves routes from the active gateway catalog.
type Router struct{ unified *unifiedSelector }

func NewRouter() *Router { return &Router{unified: &unifiedSelector{}} }

var (
	// ErrModelNotFound means no enabled route declares the requested model.
	ErrModelNotFound = errors.New("model not found")
	// ErrCapabilityUnavailable means the model exists but none of its routes
	// support every feature required by the request.
	ErrCapabilityUnavailable = errors.New("requested capabilities are not supported by this model")
	// ErrNoCompatibleTransport means semantically matching routes exist, but
	// none declare a transport supported by the request execution plan.
	ErrNoCompatibleTransport = errors.New("no compatible upstream transport")
	// ErrNoRoute means matching routes exist but none are currently available.
	ErrNoRoute             = errors.New("no route currently available")
	ErrAmbiguousSKU        = errors.New("public operation does not resolve to exactly one billable SKU")
	ErrInvalidSelectionKey = errors.New("route selection requires a stable call identity")
	ErrInvalidRouteWeight  = errors.New("route weight is invalid")
)

// Release remains part of the execution selector contract. Unified slots are
// owned and released by the transactional call lifecycle.
func (*Router) Release(uint) {}

// UnifiedActive reports whether the active catalog currently permits traffic.
func (r *Router) UnifiedActive(ctx context.Context) bool {
	return r.unifiedActive(ctx)
}
