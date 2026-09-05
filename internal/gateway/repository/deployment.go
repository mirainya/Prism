package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type DeploymentGenerationInput struct {
	GenerationNo                    uint64
	SemanticVersion, SemanticDigest string
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
	var releaseStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_catalog_releases WHERE id=? FOR SHARE`, releaseID).Scan(&releaseStatus); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	} else if releaseStatus != "published" {
		return ErrConflict
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_readiness(deployment_generation_id,deployment_member_id,release_id,content_hash,semantic_digest,adapter_digest,status,heartbeat_at,expires_at) VALUES (?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE content_hash=VALUES(content_hash),semantic_digest=VALUES(semantic_digest),adapter_digest=VALUES(adapter_digest),status=VALUES(status),heartbeat_at=VALUES(heartbeat_at),expires_at=VALUES(expires_at)`, generationID, memberID, releaseID, contentHash, semanticDigest, adapterDigest, status, nowUTC(), expiresAt.UTC())
	return err
}
func (s *Store) ActivateDeploymentGeneration(ctx context.Context, tx *sql.Tx, generationID uint64) error {
	if tx == nil || generationID == 0 {
		return ErrInvalidInput
	}
	var members, ready uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_deployment_members WHERE deployment_generation_id=?`, generationID).Scan(&members); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT deployment_member_id) FROM gw_catalog_readiness WHERE deployment_generation_id=? AND status='ready' AND expires_at>UTC_TIMESTAMP(3)`, generationID).Scan(&ready); err != nil {
		return err
	}
	if members == 0 || members != ready {
		return ErrConflict
	}
	var cryptoMembers uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT deployment_member_id FROM crypto_key_readiness WHERE deployment_generation_id=? GROUP BY deployment_member_id HAVING COUNT(DISTINCT CASE WHEN status='ready' AND expires_at>UTC_TIMESTAMP(3) THEN operation END)=5) ready_members`, generationID).Scan(&cryptoMembers); err != nil {
		return err
	}
	if cryptoMembers != members {
		return ErrConflict
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
