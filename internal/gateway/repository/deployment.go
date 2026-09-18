package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type DeploymentGenerationInput struct {
	GenerationNo                    uint64
	SemanticVersion, SemanticDigest string
}

// DeploymentIdentity binds readiness to one named process role and one exact
// executable artifact. The digest is the SHA-256 of the running binary.
type DeploymentIdentity struct {
	InstanceID    string
	Role          string
	AdapterDigest string
}

// ActiveDeployment is the immutable execution context selected by the
// runtime singleton.  Workers must obtain it while holding the singleton row
// lock so an activation cannot race a new claim.
type ActiveDeployment struct {
	GenerationID uint64
	ReleaseID    uint64
}

func (in DeploymentIdentity) Validate() error {
	if !validDeploymentIdentityPart(in.InstanceID, 128) || !validDeploymentIdentityPart(in.Role, 32) || !validHexDigest(in.AdapterDigest, 32) {
		return ErrInvalidInput
	}
	return nil
}

func validDeploymentIdentityPart(value string, maxRunes int) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char == 0x7f {
			return false
		}
	}
	return true
}

// LockActiveDeployment locks the runtime pointer and verifies the exact
// deployment proof for this process.  The lock is intentionally held by the
// caller's transaction through the claim write; callers must not use a
// separately read pointer for execution authorization.
func (s *Store) LockActiveDeployment(ctx context.Context, tx *sql.Tx, identity DeploymentIdentity) (ActiveDeployment, error) {
	if s == nil || tx == nil || identity.Validate() != nil {
		return ActiveDeployment{}, ErrInvalidInput
	}
	var releaseID, generationID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT active_release_id,active_deployment_generation_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`).Scan(&releaseID, &generationID); err == sql.ErrNoRows {
		return ActiveDeployment{}, ErrNotFound
	} else if err != nil {
		return ActiveDeployment{}, err
	}
	if !releaseID.Valid || releaseID.Int64 <= 0 || !generationID.Valid || generationID.Int64 <= 0 {
		return ActiveDeployment{}, ErrConflict
	}
	generation := uint64(generationID.Int64)
	release := uint64(releaseID.Int64)
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_deployment_generations WHERE id=? FOR SHARE`, generation).Scan(&status); err == sql.ErrNoRows {
		return ActiveDeployment{}, ErrConflict
	} else if err != nil {
		return ActiveDeployment{}, err
	} else if status != "active" {
		return ActiveDeployment{}, ErrConflict
	}
	if err := CheckCatalogReadiness(ctx, tx, generation, release, identity); err != nil {
		return ActiveDeployment{}, err
	}
	if err := CheckCatalogPricing(ctx, tx, release); err != nil {
		return ActiveDeployment{}, err
	}
	if err := CheckCatalogValidations(ctx, tx, release); err != nil {
		return ActiveDeployment{}, err
	}
	if err := CheckCryptoReadiness(ctx, tx, generation); err != nil {
		return ActiveDeployment{}, err
	}
	return ActiveDeployment{GenerationID: generation, ReleaseID: release}, nil
}

func (s *Store) CreateDeploymentGeneration(ctx context.Context, tx *sql.Tx, in DeploymentGenerationInput) (uint64, error) {
	if tx == nil || in.GenerationNo == 0 || in.SemanticVersion == "" || len(in.SemanticVersion) > 64 || !validHexDigest(in.SemanticDigest, 32) {
		return 0, ErrInvalidInput
	}
	var latest uint64
	err := tx.QueryRowContext(ctx, `SELECT generation_no FROM gw_deployment_generations ORDER BY generation_no DESC LIMIT 1 FOR UPDATE`).Scan(&latest)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if in.GenerationNo != latest+1 {
		return 0, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_deployment_generations(generation_no,status,semantic_version,semantic_digest,created_at) VALUES (?, 'preparing',?,?,?)`, in.GenerationNo, in.SemanticVersion, in.SemanticDigest, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create deployment generation: %w", err)
	}
	return lastID(result)
}
func (s *Store) AddDeploymentMember(ctx context.Context, tx *sql.Tx, generationID uint64, instanceID, role string) (uint64, error) {
	if tx == nil || generationID == 0 || instanceID == "" || len(instanceID) > 128 || role == "" || len(role) > 32 {
		return 0, ErrInvalidInput
	}
	var status string
	var frozen sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT status,member_frozen_at FROM gw_deployment_generations WHERE id=? FOR UPDATE`, generationID).Scan(&status, &frozen); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if status != "preparing" || frozen.Valid {
		return 0, ErrConflict
	}
	var existing uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_deployment_members WHERE deployment_generation_id=? AND instance_id=? AND role=?`, generationID, instanceID, role).Scan(&existing); err == nil {
		return existing, nil
	} else if err != sql.ErrNoRows {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_deployment_members(deployment_generation_id,instance_id,role,created_at) VALUES (?,?,?,?)`, generationID, instanceID, role, nowUTC())
	if err != nil {
		return 0, err
	}
	return lastID(result)
}
func (s *Store) RecordCatalogReadiness(ctx context.Context, tx *sql.Tx, generationID, memberID, releaseID uint64, contentHash, semanticDigest, adapterDigest, status string, expiresAt time.Time) error {
	if tx == nil || generationID == 0 || memberID == 0 || releaseID == 0 || !validHexDigest(contentHash, 32) || !validHexDigest(semanticDigest, 32) || !validHexDigest(adapterDigest, 32) || (status != "ready" && status != "failed" && status != "expired") || expiresAt.IsZero() || status == "ready" && !expiresAt.After(nowUTC()) {
		return ErrInvalidInput
	}
	var generationStatus string
	if err := tx.QueryRowContext(ctx, `SELECT g.status FROM gw_deployment_generations g JOIN gw_deployment_members m ON m.deployment_generation_id=g.id WHERE g.id=? AND m.id=? FOR SHARE`, generationID, memberID).Scan(&generationStatus); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	} else if generationStatus != "preparing" && generationStatus != "active" {
		return ErrConflict
	}
	var releaseStatus, releaseContentHash, releaseSemanticDigest string
	if err := tx.QueryRowContext(ctx, `SELECT status,content_hash,semantic_digest FROM gw_catalog_releases WHERE id=? FOR SHARE`, releaseID).Scan(&releaseStatus, &releaseContentHash, &releaseSemanticDigest); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	} else if releaseStatus != "published" || contentHash != releaseContentHash || semanticDigest != releaseSemanticDigest {
		return ErrConflict
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_readiness(deployment_generation_id,deployment_member_id,release_id,content_hash,semantic_digest,adapter_digest,status,heartbeat_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE content_hash=VALUES(content_hash),semantic_digest=VALUES(semantic_digest),adapter_digest=VALUES(adapter_digest),status=VALUES(status),heartbeat_at=VALUES(heartbeat_at),expires_at=VALUES(expires_at)`, generationID, memberID, releaseID, contentHash, semanticDigest, adapterDigest, status, nowUTC(), expiresAt.UTC())
	return err
}
func (s *Store) ActivateDeploymentGeneration(ctx context.Context, tx *sql.Tx, generationID uint64, identity DeploymentIdentity) error {
	if tx == nil || generationID == 0 || identity.Validate() != nil {
		return ErrInvalidInput
	}
	// Serialize deployment changes with catalog activation before freezing the
	// member set; readiness reporters and member registration lock this parent.
	var releaseID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1 FOR UPDATE`).Scan(&releaseID); err != nil {
		return err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_deployment_generations WHERE id=? FOR UPDATE`, generationID).Scan(&status); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "preparing" {
		return ErrConflict
	}
	if !releaseID.Valid {
		// A first deployment may be prepared before the traffic pointer exists,
		// but all its members must prove the same published catalog.
		err := tx.QueryRowContext(ctx, `SELECT r.release_id FROM gw_catalog_readiness r
JOIN gw_deployment_members m ON m.id=r.deployment_member_id AND m.deployment_generation_id=r.deployment_generation_id
JOIN gw_catalog_releases c ON c.id=r.release_id
JOIN gw_deployment_generations g ON g.id=r.deployment_generation_id
WHERE g.id=? AND c.status='published' AND r.status='ready' AND r.expires_at>CURRENT_TIMESTAMP(3)
AND r.content_hash=c.content_hash AND r.semantic_digest=c.semantic_digest AND g.semantic_digest=c.semantic_digest AND r.adapter_digest=?
GROUP BY r.release_id HAVING COUNT(DISTINCT m.id)=(SELECT COUNT(*) FROM gw_deployment_members WHERE deployment_generation_id=?)
ORDER BY r.release_id DESC LIMIT 1`, generationID, identity.AdapterDigest, generationID).Scan(&releaseID)
		if err == sql.ErrNoRows {
			return ErrConflict
		} else if err != nil {
			return err
		}
	}
	if err := CheckCatalogReadiness(ctx, tx, generationID, uint64(releaseID.Int64), identity); err != nil {
		return err
	}
	if err := CheckCryptoReadiness(ctx, tx, generationID); err != nil {
		return fmt.Errorf("deployment crypto readiness: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gw_deployment_generations SET status='retired' WHERE status='active' AND id<>?`, generationID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_deployment_generations SET status='active',member_frozen_at=COALESCE(member_frozen_at,?) WHERE id=? AND status='preparing'`, nowUTC(), generationID)
	if err != nil {
		return err
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	// The generation status and the runtime pointer must become visible in the
	// same transaction.  The foreign-key trigger accepts this write only after
	// the target generation is active.
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_catalog_runtime_state SET active_deployment_generation_id=?,state_version=state_version+1,updated_at=? WHERE id=1`, generationID, nowUTC())); err != nil {
		return fmt.Errorf("activate deployment pointer: %w", err)
	}
	return nil
}

func (s *Store) RecordCryptoReadiness(ctx context.Context, tx *sql.Tx, generationID, memberID, keyringID uint64, keyVersion uint32, operation, status string, expiresAt time.Time) error {
	operation = strings.ToLower(strings.TrimSpace(operation))
	status = strings.ToLower(strings.TrimSpace(status))
	if tx == nil || generationID == 0 || memberID == 0 || keyringID == 0 || keyVersion == 0 || operation == "" || status == "" || expiresAt.IsZero() {
		return ErrInvalidInput
	}
	if !validCryptoReadinessOperation(operation) || (status != "ready" && status != "failed" && status != "expired") {
		return ErrInvalidInput
	}
	if status == "ready" && !expiresAt.After(nowUTC()) {
		return ErrInvalidInput
	}
	// The composite foreign key protects the write, but checking the parent
	// state here gives callers a stable domain error and prevents readiness
	// reports from being attached to a retired deployment generation.
	var generationStatus string
	if err := tx.QueryRowContext(ctx, `SELECT g.status FROM gw_deployment_generations g JOIN gw_deployment_members m ON m.deployment_generation_id=g.id WHERE g.id=? AND m.id=? FOR SHARE`, generationID, memberID).Scan(&generationStatus); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	} else if generationStatus != "preparing" && generationStatus != "active" {
		return ErrConflict
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO crypto_key_readiness(deployment_generation_id,deployment_member_id,keyring_id,key_version,operation,status,checked_at,expires_at) VALUES (?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE status=VALUES(status),checked_at=VALUES(checked_at),expires_at=VALUES(expires_at)`, generationID, memberID, keyringID, keyVersion, operation, status, nowUTC(), expiresAt.UTC())
	return err
}

func validCryptoReadinessOperation(value string) bool {
	switch value {
	case "mac", "wrap", "unwrap", "encrypt", "decrypt":
		return true
	default:
		return false
	}
}
