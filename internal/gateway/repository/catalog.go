package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// PublishRelease validates the immutable draft and atomically switches the
// release state. It intentionally does not activate traffic; activation is a
// separate operation so readiness checks cannot be skipped.
func (s *Store) PublishRelease(ctx context.Context, tx *sql.Tx, releaseID, reviewerID uint64) error {
	if tx == nil || releaseID == 0 {
		return ErrInvalidInput
	}
	now := nowUTC()
	var reviewer any
	if reviewerID != 0 {
		reviewer = reviewerID
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_catalog_releases SET status='published',reviewed_by=?,published_at=? WHERE id=? AND status='draft'`, reviewer, now, releaseID)
	if err != nil {
		return fmt.Errorf("publish catalog release: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_release_state_events(release_id,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?)`, releaseID, "draft", "published", "publish", now); err != nil {
		return fmt.Errorf("catalog release event: %w", err)
	}
	return nil
}

// ActivateRelease checks that the selected release is published and updates
// the singleton pointer with an optimistic version. Existing calls retain
// their release id, so activation never mutates historical behavior.
func (s *Store) ActivateRelease(ctx context.Context, tx *sql.Tx, releaseID, expectedVersion uint64) error {
	if tx == nil || releaseID == 0 {
		return ErrInvalidInput
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_catalog_releases WHERE id=? FOR SHARE`, releaseID).Scan(&status); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "published" {
		return ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `UPDATE gw_catalog_runtime_state SET active_release_id=?,state_version=state_version+1,updated_at=? WHERE id=1 AND state_version=?`, releaseID, now, expectedVersion)
	if err != nil {
		return fmt.Errorf("activate catalog release: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

// ActivateReleaseWhenReady adds the deployment-generation proof required by
// production activation. Every registered member must report a non-expired
// ready record for the exact release before the singleton pointer moves.
func (s *Store) ActivateReleaseWhenReady(ctx context.Context, tx *sql.Tx, releaseID, expectedVersion, generationID uint64) error {
	if tx == nil || releaseID == 0 || generationID == 0 {
		return ErrInvalidInput
	}
	var generationStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_deployment_generations WHERE id=? FOR SHARE`, generationID).Scan(&generationStatus); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if generationStatus != "preparing" && generationStatus != "active" {
		return ErrConflict
	}
	var members, ready uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_deployment_members WHERE deployment_generation_id=?`, generationID).Scan(&members); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_readiness r JOIN gw_deployment_members m ON m.id=r.deployment_member_id JOIN gw_catalog_releases c ON c.id=r.release_id WHERE r.deployment_generation_id=? AND r.release_id=? AND r.status='ready' AND r.expires_at>UTC_TIMESTAMP(3) AND r.adapter_digest<>'' AND r.content_hash=c.content_hash AND r.semantic_digest=c.semantic_digest`, generationID, releaseID).Scan(&ready); err != nil {
		return err
	}
	if members == 0 || ready != members {
		return ErrConflict
	}
	return s.ActivateRelease(ctx, tx, releaseID, expectedVersion)
}

func (s *Store) RetireRelease(ctx context.Context, tx *sql.Tx, releaseID uint64) error {
	if tx == nil || releaseID == 0 {
		return ErrInvalidInput
	}
	var oldState string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_catalog_releases WHERE id=? FOR UPDATE`, releaseID).Scan(&oldState); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if oldState != "draft" && oldState != "published" {
		return ErrConflict
	}
	var active sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1 FOR SHARE`).Scan(&active); err != nil && err != sql.ErrNoRows {
		return err
	}
	if active.Valid && uint64(active.Int64) == releaseID {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_catalog_releases SET status='retired' WHERE id=? AND status=?`, releaseID, oldState)
	if err != nil {
		return fmt.Errorf("retire catalog release: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_release_state_events(release_id,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?)`, releaseID, oldState, "retired", "retire", nowUTC()); err != nil {
		return fmt.Errorf("catalog retirement event: %w", err)
	}
	return nil
}

// AdjustCatalogSourceRunCount reserves/releases a non-terminal discovery run.
func (s *Store) AdjustCatalogSourceRunCount(ctx context.Context, tx *sql.Tx, sourceID uint64, delta int64) error {
	if tx == nil || sourceID == 0 || (delta != 1 && delta != -1) {
		return ErrInvalidInput
	}
	var op string
	if delta > 0 {
		op = `UPDATE gw_catalog_sources SET nonterminal_run_count=nonterminal_run_count+1,updated_at=? WHERE id=? AND status IN ('active','draining')`
	} else {
		op = `UPDATE gw_catalog_sources SET nonterminal_run_count=nonterminal_run_count-1,updated_at=? WHERE id=? AND nonterminal_run_count>0`
	}
	res, err := tx.ExecContext(ctx, op, nowUTC(), sourceID)
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

func (s *Store) TransitionCatalogSource(ctx context.Context, tx *sql.Tx, sourceID uint64, from, to, reason string) error {
	if tx == nil || sourceID == 0 || reason == "" {
		return ErrInvalidInput
	}
	if !validCatalogSourceState(from) || !validCatalogSourceState(to) || from == to {
		return ErrInvalidInput
	}
	if !(from == "active" && to == "draining" || from == "draining" && to == "disabled") {
		return ErrConflict
	}
	if to == "disabled" {
		var nonterminal uint64
		if err := tx.QueryRowContext(ctx, `SELECT nonterminal_run_count FROM gw_catalog_sources WHERE id=? AND status=? FOR UPDATE`, sourceID, from).Scan(&nonterminal); err == sql.ErrNoRows {
			return ErrConflict
		} else if err != nil {
			return err
		} else if nonterminal != 0 {
			return ErrConflict
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_catalog_sources SET status=?,state_version=state_version+1,updated_at=? WHERE id=? AND status=?`, to, nowUTC(), sourceID, from)
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
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT state_version FROM gw_catalog_sources WHERE id=?`, sourceID).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_catalog_source_state_events(catalog_source_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,?,?,?,?)`, sourceID, version, from, to, reason, nowUTC())
	return err
}

func validCatalogSourceState(v string) bool {
	return v == "active" || v == "draining" || v == "disabled"
}

type CatalogImportInput struct {
	ReleaseID, ReleaseSourceID, ControlPlaneRunID uint64
	SnapshotHMAC                                  string
	ResultSchemaVersion                           uint32
}

func (s *Store) CreateCatalogImport(ctx context.Context, tx *sql.Tx, in CatalogImportInput) (uint64, error) {
	if tx == nil || in.ReleaseID == 0 || in.ReleaseSourceID == 0 || in.ControlPlaneRunID == 0 || !validHexDigest(in.SnapshotHMAC, 32) || in.ResultSchemaVersion == 0 {
		return 0, ErrInvalidInput
	}
	var runState string
	if err := tx.QueryRowContext(ctx, `SELECT r.state FROM gw_control_plane_runs r JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id WHERE r.id=? AND r.action='catalog_discovery' AND rs.id=? AND rs.release_id=? FOR SHARE`, in.ControlPlaneRunID, in.ReleaseSourceID, in.ReleaseID).Scan(&runState); err == sql.ErrNoRows {
		return 0, ErrConflict
	} else if err != nil {
		return 0, err
	}
	if runState != "running" && runState != "completed" {
		return 0, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_imports(release_id,release_source_id,control_plane_run_id,snapshot_hmac,result_schema_version,created_at) VALUES (?,?,?,?,?,?)`, in.ReleaseID, in.ReleaseSourceID, in.ControlPlaneRunID, in.SnapshotHMAC, in.ResultSchemaVersion, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create catalog import: %w", err)
	}
	return lastID(result)
}
