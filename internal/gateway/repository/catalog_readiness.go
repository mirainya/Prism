package repository

import "context"

// CheckCatalogReadiness verifies the exact published release against every
// registered member, including the deployment's semantic contract.
func CheckCatalogReadiness(ctx context.Context, db ReadinessQuery, generationID, releaseID uint64, identity DeploymentIdentity) error {
	if db == nil || generationID == 0 || releaseID == 0 || identity.Validate() != nil {
		return ErrConflict
	}
	var members, ready uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_deployment_members WHERE deployment_generation_id=?`, generationID).Scan(&members); err != nil {
		return err
	}
	if members == 0 {
		return ErrConflict
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT m.id) FROM gw_deployment_members m
JOIN gw_deployment_generations g ON g.id=m.deployment_generation_id
JOIN gw_catalog_readiness r ON r.deployment_member_id=m.id AND r.deployment_generation_id=g.id
JOIN gw_catalog_releases c ON c.id=r.release_id
WHERE g.id=? AND c.id=? AND g.status IN ('preparing','active') AND c.status='published'
AND r.status='ready' AND r.expires_at>UTC_TIMESTAMP(3) AND r.adapter_digest=?
AND r.content_hash=c.content_hash AND r.semantic_digest=c.semantic_digest AND g.semantic_digest=c.semantic_digest`, generationID, releaseID, identity.AdapterDigest).Scan(&ready); err != nil {
		return err
	}
	if ready != members {
		return ErrConflict
	}
	var current uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_deployment_members m
JOIN gw_catalog_readiness r ON r.deployment_member_id=m.id AND r.deployment_generation_id=m.deployment_generation_id
JOIN gw_catalog_releases c ON c.id=r.release_id
JOIN gw_deployment_generations g ON g.id=m.deployment_generation_id
WHERE m.deployment_generation_id=? AND m.instance_id=? AND m.role=? AND r.release_id=?
AND g.status IN ('preparing','active') AND c.status='published' AND r.status='ready' AND r.expires_at>UTC_TIMESTAMP(3)
AND r.adapter_digest=? AND r.content_hash=c.content_hash AND r.semantic_digest=c.semantic_digest AND g.semantic_digest=c.semantic_digest`, generationID, identity.InstanceID, identity.Role, releaseID, identity.AdapterDigest).Scan(&current); err != nil {
		return err
	}
	if current != 1 {
		return ErrConflict
	}
	return nil
}
