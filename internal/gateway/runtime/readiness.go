package runtime

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

var ErrNotReady = errors.New("gateway runtime: deployment is not ready")

// ReadinessCheck is the common gate used by HTTP handlers and workers.
// Implementations must return ErrNotReady, directly or through errors.Join,
// whenever this process may not execute data-plane work.
type ReadinessCheck func(context.Context) error

func (check ReadinessCheck) Require(ctx context.Context) error {
	if check == nil {
		return ErrNotReady
	}
	if err := check(ctx); err != nil {
		if errors.Is(err, ErrNotReady) {
			return err
		}
		return errors.Join(ErrNotReady, err)
	}
	return nil
}

// ReadinessGate is a process-lifetime fail-closed latch. Once disabled it
// cannot be re-enabled; a new process must prove readiness before serving.
type ReadinessGate struct {
	ready atomic.Bool
}

func NewReadinessGate(ready bool) *ReadinessGate {
	gate := &ReadinessGate{}
	gate.ready.Store(ready)
	return gate
}

func (gate *ReadinessGate) Require(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if gate == nil || !gate.ready.Load() {
		return ErrNotReady
	}
	return nil
}

func (gate *ReadinessGate) Disable() {
	if gate != nil {
		gate.ready.Store(false)
	}
}

func (gate *ReadinessGate) Ready() bool {
	return gate != nil && gate.ready.Load()
}

// Watch probes durable readiness at a bounded interval. The first failed
// probe permanently disables the process gate and asks the worker group to
// stop by returning the failure.
func (gate *ReadinessGate) Watch(ctx context.Context, interval time.Duration, check ReadinessCheck) error {
	if gate == nil || interval <= 0 || check == nil {
		if gate != nil {
			gate.Disable()
		}
		return ErrNotReady
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := check.Require(ctx); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				gate.Disable()
				return err
			}
		}
	}
}

// RequireConfiguredReadiness verifies that this process may execute data-plane
// work. Control-plane HTTP handlers may still start when this returns
// ErrNotReady; public execution and workers must remain disabled.
func RequireConfiguredReadiness(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return ErrNotReady
	}
	var releaseID, generationID sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT active_release_id,active_deployment_generation_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&releaseID, &generationID); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if !releaseID.Valid || releaseID.Int64 <= 0 || !generationID.Valid || generationID.Int64 <= 0 {
		return ErrNotReady
	}
	return CheckReadiness(ctx, db, uint64(generationID.Int64), uint64(releaseID.Int64))
}

// CheckReadiness verifies the immutable deployment generation before traffic
// is enabled. It is deliberately a read-only gate; activation remains an
// explicit repository transaction.
func CheckReadiness(ctx context.Context, db *sql.DB, generationID, releaseID uint64) error {
	if db == nil || generationID == 0 || releaseID == 0 {
		return ErrNotReady
	}
	// The caller must validate the exact singleton pointer, not merely an
	// independently active generation/release pair. This prevents a stale
	// process or management endpoint from treating an old deployment as ready
	// after a cutover.
	var activeRelease, activeGeneration sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT active_release_id,active_deployment_generation_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&activeRelease, &activeGeneration); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if !activeRelease.Valid || !activeGeneration.Valid || activeRelease.Int64 != int64(releaseID) || activeGeneration.Int64 != int64(generationID) {
		return ErrNotReady
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM gw_deployment_generations WHERE id=?`, generationID).Scan(&status); err != nil || status != "active" {
		return ErrNotReady
	}
	identity, err := CurrentProcessIdentity()
	if err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if err := repository.CheckCatalogReadiness(ctx, db, generationID, releaseID, identity); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if err := repository.CheckCatalogPricing(ctx, db, releaseID); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if err := repository.CheckCatalogValidations(ctx, db, releaseID); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if err := repository.CheckCryptoReadiness(ctx, db, generationID); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if err := repository.CheckBillingReadiness(ctx, db); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	return nil
}
