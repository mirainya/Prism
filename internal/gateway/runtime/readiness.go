package runtime

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync/atomic"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

var ErrNotReady = errors.New("gateway runtime is not ready")

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
// cannot be re-enabled; a new process must pass readiness before serving.
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

// RequireConfiguredReadiness verifies that the active catalog can execute
// data-plane work. Control-plane HTTP handlers may still start when this
// returns ErrNotReady; public execution and workers must remain disabled.
func RequireConfiguredReadiness(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return ErrNotReady
	}
	releaseID, err := configuredReleaseID(ctx, db)
	if err != nil {
		return errors.Join(ErrNotReady, err)
	}
	return CheckReadiness(ctx, db, releaseID)
}

func configuredReleaseID(ctx context.Context, db *sql.DB) (uint64, error) {
	if db == nil {
		return 0, ErrNotReady
	}
	var releaseID sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&releaseID); err != nil {
		return 0, err
	}
	if !releaseID.Valid || releaseID.Int64 <= 0 {
		return 0, ErrNotReady
	}
	return uint64(releaseID.Int64), nil
}

// CheckReadiness validates the currently active published catalog and the
// durable dependencies used by each request. Deployment generations and
// process proofs are retained only as legacy control-plane data.
func CheckReadiness(ctx context.Context, db *sql.DB, releaseID uint64) error {
	if db == nil || releaseID == 0 {
		return ErrNotReady
	}
	activeRelease, err := configuredReleaseID(ctx, db)
	if err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if activeRelease != releaseID {
		return ErrNotReady
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM gw_catalog_releases WHERE id=?`, releaseID).Scan(&status); err != nil || status != "published" {
		return errors.Join(ErrNotReady, err)
	}
	if err := repository.CheckCatalogStructure(ctx, db, releaseID); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if err := repository.CheckCatalogPricing(ctx, db, releaseID); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if err := repository.CheckRuntimeKeyReadiness(ctx, db); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if err := checkRuntimeKeyMaterial(ctx, db); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	if err := repository.CheckBillingReadiness(ctx, db); err != nil {
		return errors.Join(ErrNotReady, err)
	}
	return nil
}

func checkRuntimeKeyMaterial(ctx context.Context, db repository.ReadinessQuery) error {
	names := []string{"PRISM_GATEWAY_PAYLOAD_KEK_B64", "PRISM_GATEWAY_PAYLOAD_HMAC_B64"}
	requireCredential, err := repository.LegacyCredentialCryptoRequired(ctx, db)
	if err != nil {
		return err
	}
	if requireCredential {
		names = append(names, "PRISM_GATEWAY_KEK_B64", "PRISM_GATEWAY_HMAC_B64")
	}
	for _, name := range names {
		key, err := security.DecodeBase64Key(os.Getenv(name))
		if err != nil {
			return err
		}
		clear(key)
	}
	return nil
}
