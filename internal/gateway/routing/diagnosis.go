package routing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// A model that is configured correctly can still return 503 for the whole
// backoff window, because the candidate query drops circuit-broken routes with
// `rs.id IS NULL` — by the time the selector sees an empty result set it no
// longer knows whether the model was never routable or is merely resting. That
// is the single biggest reason "I configured it and it doesn't work" was
// impossible to diagnose from the outside.
//
// The fix deliberately leaves the candidate SQL alone. Changing it would change
// which routes get selected; instead the selector runs one extra query on the
// path where it is already about to fail, so routing behaviour is identical and
// only the error gets richer.

// NoRouteDetail explains an ErrNoRoute in terms an operator can act on. It
// wraps ErrNoRoute, so every existing errors.Is check keeps matching and
// callers that do not care about the detail need no changes.
type NoRouteDetail struct {
	// BrokenRoutes counts circuit breakers still holding routes for this model
	// out of rotation.
	BrokenRoutes int64
	// RetryAfterSeconds is when the earliest of those breakers expires. Zero
	// when nothing is broken, meaning the model has no usable route at all
	// rather than a temporarily suspended one.
	RetryAfterSeconds int64
}

func (d *NoRouteDetail) Error() string {
	if d == nil || d.BrokenRoutes == 0 {
		return ErrNoRoute.Error()
	}
	return fmt.Sprintf("%s: %d route(s) circuit-broken, earliest recovery in %ds", ErrNoRoute.Error(), d.BrokenRoutes, d.RetryAfterSeconds)
}

func (d *NoRouteDetail) Unwrap() error { return ErrNoRoute }

// PublicSuffix is the part safe to hand to the API caller. It names no
// credential, pool or channel — only that the outage is temporary and roughly
// how long it lasts, which is exactly what a client needs to decide between
// retrying and failing over.
func (d *NoRouteDetail) PublicSuffix() string {
	if d == nil || d.BrokenRoutes == 0 {
		return ""
	}
	return fmt.Sprintf(": all %d upstream route(s) are temporarily suspended after repeated failures, retry in %ds", d.BrokenRoutes, d.RetryAfterSeconds)
}

// NoRouteDetailFrom pulls the detail back out of a wrapped error. The second
// result is false for an ErrNoRoute raised somewhere without diagnosis, so
// callers fall back to their plain message.
func NoRouteDetailFrom(err error) (*NoRouteDetail, bool) {
	var detail *NoRouteDetail
	if errors.As(err, &detail) && detail != nil && detail.BrokenRoutes > 0 {
		return detail, true
	}
	return nil, false
}

// diagnoseNoRoute annotates ErrNoRoute with the circuit breaker state for this
// model. Any failure here is swallowed: a diagnosis that cannot be produced
// must not turn a 503 into a 500.
func diagnoseNoRoute(ctx context.Context, db *sql.DB, modelName string) error {
	if db == nil || modelName == "" {
		return ErrNoRoute
	}
	var count int64
	var remaining sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*),MIN(TIMESTAMPDIFF(SECOND,CURRENT_TIMESTAMP(3),disabled_until))
FROM gw_route_states WHERE model_name=? AND disabled_until>CURRENT_TIMESTAMP(3)`, modelName).Scan(&count, &remaining)
	if err != nil || count == 0 {
		return ErrNoRoute
	}
	seconds := remaining.Int64
	if !remaining.Valid || seconds < 1 {
		seconds = 1
	}
	return &NoRouteDetail{BrokenRoutes: count, RetryAfterSeconds: seconds}
}
