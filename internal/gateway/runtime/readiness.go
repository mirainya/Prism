package runtime

import (
	"context"
	"database/sql"
	"errors"
)

var ErrNotReady = errors.New("gateway runtime: deployment is not ready")

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
	var cryptoMembers, badKeys uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT deployment_member_id),COUNT(CASE WHEN status<>'ready' OR expires_at<=UTC_TIMESTAMP(3) THEN 1 END) FROM crypto_key_readiness WHERE deployment_generation_id=?`, generationID).Scan(&cryptoMembers, &badKeys); err != nil || cryptoMembers != members || badKeys != 0 {
		return ErrNotReady
	}
	return nil
}
