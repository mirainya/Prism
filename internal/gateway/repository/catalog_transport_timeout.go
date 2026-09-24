package repository

import (
	"context"
	"database/sql"
	"strings"
)

// TransportTimeoutChange updates the HTTP exchange deadline for one stable
// upstream transport without requiring a catalog rebuild.
type TransportTimeoutChange struct {
	catalogChangeGuard
	TransportCode string `json:"transport_code"`
	TimeoutMS     uint64 `json:"timeout_ms"`
}

func (in *TransportTimeoutChange) Normalize() {
	in.TransportCode = strings.ToLower(strings.TrimSpace(in.TransportCode))
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
}

func (in TransportTimeoutChange) Validate() error {
	if err := in.catalogChangeGuard.validate(); err != nil {
		return err
	}
	if !catalogIdentityPattern.MatchString(in.TransportCode) || in.TimeoutMS < 100 || in.TimeoutMS > 900000 {
		return ErrInvalidInput
	}
	return nil
}

func (s *Store) ChangeTransportTimeout(ctx context.Context, tx *sql.Tx, in TransportTimeoutChange, actorID uint64) (CatalogChangeResult, error) {
	if tx == nil || actorID == 0 {
		return CatalogChangeResult{}, ErrInvalidInput
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		return CatalogChangeResult{}, err
	}
	active, err := lockActiveCatalogRelease(ctx, tx, in.ExpectedActiveReleaseID, in.ExpectedConfigVersion)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	var transportID, before uint64
	if err := tx.QueryRowContext(ctx, `SELECT id,timeout_ms FROM gw_channel_transports WHERE release_id=? AND transport_code=? FOR UPDATE`, active.ID, in.TransportCode).
		Scan(&transportID, &before); err == sql.ErrNoRows {
		return CatalogChangeResult{}, ErrNotFound
	} else if err != nil {
		return CatalogChangeResult{}, err
	}
	if before == in.TimeoutMS {
		return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: active.Version, Activated: true, SourceReleaseID: active.ID}, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_channel_transports SET timeout_ms=? WHERE release_id=? AND id=?`, in.TimeoutMS, active.ID, transportID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	ok, err := affected(result)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	if !ok {
		return CatalogChangeResult{}, ErrNotFound
	}
	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.transport_timeout", actorID, ginSafeMetadata{
		"source_release_id":    active.ID,
		"channel_transport_id": transportID,
		"transport_code":       in.TransportCode,
		"before_timeout_ms":    before,
		"after_timeout_ms":     in.TimeoutMS,
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}
