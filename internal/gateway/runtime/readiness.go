package runtime

import (
	"context"
	"database/sql"
	"errors"
)

var ErrNotReady = errors.New("gateway runtime: deployment is not ready")

// RequireConfiguredReadiness keeps the pre-migration empty database usable,
// but refuses to start once any unified catalog data exists without a fully
// proven active release. This prevents a partial cutover from falling back to
// legacy execution paths.
func RequireConfiguredReadiness(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return ErrNotReady
	}
	var channels, releases uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gateway_channels`).Scan(&channels); err != nil {
		return ErrNotReady
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_releases`).Scan(&releases); err != nil {
		return ErrNotReady
	}
	if channels == 0 && releases == 0 {
		return nil
	}
	var releaseID sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&releaseID); err != nil || !releaseID.Valid || releaseID.Int64 <= 0 {
		return ErrNotReady
	}
	var generationID uint64
	if err := db.QueryRowContext(ctx, `SELECT id FROM gw_deployment_generations WHERE status='active' ORDER BY id DESC LIMIT 1`).Scan(&generationID); err != nil {
		return ErrNotReady
	}
	return CheckReadiness(ctx, db, generationID, uint64(releaseID.Int64))
}

// CheckReadiness verifies the immutable deployment generation before traffic
// is enabled. It is deliberately a read-only gate; activation remains an
// explicit repository transaction.
func CheckReadiness(ctx context.Context, db *sql.DB, generationID, releaseID uint64) error {
	if db == nil || generationID == 0 || releaseID == 0 {
		return ErrNotReady
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM gw_deployment_generations WHERE id=?`, generationID).Scan(&status); err != nil || status != "active" {
		return ErrNotReady
	}
	var members, ready uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_deployment_members WHERE deployment_generation_id=?`, generationID).Scan(&members); err != nil || members == 0 {
		return ErrNotReady
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_readiness r JOIN gw_deployment_members m ON m.id=r.deployment_member_id JOIN gw_catalog_releases c ON c.id=r.release_id WHERE r.deployment_generation_id=? AND r.release_id=? AND r.status='ready' AND r.expires_at>UTC_TIMESTAMP(3) AND r.adapter_digest<>'' AND r.content_hash=c.content_hash AND r.semantic_digest=c.semantic_digest`, generationID, releaseID).Scan(&ready); err != nil || ready != members {
		return ErrNotReady
	}
	var cryptoMembers uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT deployment_member_id FROM crypto_key_readiness WHERE deployment_generation_id=? GROUP BY deployment_member_id HAVING COUNT(DISTINCT CASE WHEN status='ready' AND expires_at>UTC_TIMESTAMP(3) THEN operation END)=5) ready_members`, generationID).Scan(&cryptoMembers); err != nil || cryptoMembers != members {
		return ErrNotReady
	}
	return nil
}
