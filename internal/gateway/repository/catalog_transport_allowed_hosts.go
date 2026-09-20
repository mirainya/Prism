package repository

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

type CatalogTransportAllowedHosts struct {
	ReleaseID     uint64                    `json:"release_id"`
	ConfigVersion uint64                    `json:"config_version"`
	TransportID   uint64                    `json:"transport_id"`
	TransportCode string                    `json:"transport_code"`
	BaseURL       string                    `json:"base_url"`
	AllowedHosts  []CatalogAllowedHostInput `json:"allowed_hosts"`
}

type TransportAllowedHostsChange struct {
	catalogChangeGuard
	TransportCode string                    `json:"transport_code"`
	AllowedHosts  []CatalogAllowedHostInput `json:"allowed_hosts"`
}

func (in *TransportAllowedHostsChange) Normalize() {
	in.TransportCode = strings.ToLower(strings.TrimSpace(in.TransportCode))
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
	for index := range in.AllowedHosts {
		in.AllowedHosts[index].Protocol = strings.ToLower(strings.TrimSpace(in.AllowedHosts[index].Protocol))
		in.AllowedHosts[index].Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.AllowedHosts[index].Host), "."))
	}
}

func (in TransportAllowedHostsChange) Validate() error {
	if err := in.catalogChangeGuard.validate(); err != nil {
		return err
	}
	if in.ExpectedConfigVersion == 0 || !catalogIdentityPattern.MatchString(in.TransportCode) || len(in.AllowedHosts) > 32 {
		return ErrInvalidInput
	}
	for _, host := range in.AllowedHosts {
		if host.Port == 0 || host.Protocol != "http" && host.Protocol != "https" || !validExactRemoteHost(host.Host) {
			return ErrInvalidInput
		}
	}
	return nil
}

func (s *Store) ListCatalogTransportAllowedHosts(ctx context.Context, releaseID, transportID uint64) (CatalogTransportAllowedHosts, error) {
	if s == nil || s.db == nil || releaseID == 0 || transportID == 0 {
		return CatalogTransportAllowedHosts{}, ErrInvalidInput
	}
	result := CatalogTransportAllowedHosts{ReleaseID: releaseID, TransportID: transportID}
	err := s.db.QueryRowContext(ctx, `SELECT ct.transport_code,ct.base_url,r.config_version FROM gw_channel_transports ct JOIN gw_catalog_releases r ON r.id=ct.release_id WHERE ct.release_id=? AND ct.id=?`, releaseID, transportID).
		Scan(&result.TransportCode, &result.BaseURL, &result.ConfigVersion)
	if err == sql.ErrNoRows {
		return CatalogTransportAllowedHosts{}, ErrNotFound
	}
	if err != nil {
		return CatalogTransportAllowedHosts{}, err
	}
	hosts, err := loadCatalogTransportAllowedHosts(ctx, s.db, releaseID, transportID)
	if err != nil {
		return CatalogTransportAllowedHosts{}, err
	}
	result.AllowedHosts = hosts
	return result, nil
}

func (s *Store) ChangeTransportAllowedHosts(ctx context.Context, tx *sql.Tx, in TransportAllowedHostsChange, actorID uint64) (CatalogChangeResult, error) {
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
	var transportID uint64
	var baseURL string
	if err := tx.QueryRowContext(ctx, `SELECT id,base_url FROM gw_channel_transports WHERE release_id=? AND transport_code=? FOR UPDATE`, active.ID, in.TransportCode).
		Scan(&transportID, &baseURL); err == sql.ErrNoRows {
		return CatalogChangeResult{}, ErrNotFound
	} else if err != nil {
		return CatalogChangeResult{}, err
	}
	before, err := loadCatalogTransportAllowedHosts(ctx, tx, active.ID, transportID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	after, err := normalizeCatalogAllowedHosts(baseURL, in.AllowedHosts)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	sortCatalogAllowedHosts(after)
	if equalCatalogAllowedHosts(before, after) {
		return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: active.Version, Activated: true, SourceReleaseID: active.ID}, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gw_transport_allowed_hosts WHERE release_id=? AND channel_transport_id=?`, active.ID, transportID); err != nil {
		return CatalogChangeResult{}, err
	}
	createdAt := nowUTC()
	for _, host := range after {
		if _, err := tx.ExecContext(ctx, `INSERT INTO gw_transport_allowed_hosts(release_id,channel_transport_id,protocol,host_pattern,port,created_at) VALUES (?,?,?,?,?,?)`, active.ID, transportID, host.Protocol, host.Host, host.Port, createdAt); err != nil {
			return CatalogChangeResult{}, err
		}
	}
	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.transport_allowed_hosts", actorID, ginSafeMetadata{
		"source_release_id":    active.ID,
		"channel_transport_id": transportID,
		"transport_code":       in.TransportCode,
		"base_url":             baseURL,
		"before":               before,
		"after":                after,
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}

func loadCatalogTransportAllowedHosts(ctx context.Context, db DB, releaseID, transportID uint64) ([]CatalogAllowedHostInput, error) {
	rows, err := db.QueryContext(ctx, `SELECT protocol,host_pattern,port FROM gw_transport_allowed_hosts WHERE release_id=? AND channel_transport_id=? ORDER BY protocol,host_pattern,port`, releaseID, transportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hosts := make([]CatalogAllowedHostInput, 0)
	for rows.Next() {
		var host CatalogAllowedHostInput
		if err := rows.Scan(&host.Protocol, &host.Host, &host.Port); err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	return hosts, rows.Err()
}

func sortCatalogAllowedHosts(hosts []CatalogAllowedHostInput) {
	sort.Slice(hosts, func(left, right int) bool {
		if hosts[left].Protocol != hosts[right].Protocol {
			return hosts[left].Protocol < hosts[right].Protocol
		}
		if hosts[left].Host != hosts[right].Host {
			return hosts[left].Host < hosts[right].Host
		}
		return hosts[left].Port < hosts[right].Port
	})
}

func equalCatalogAllowedHosts(left, right []CatalogAllowedHostInput) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]CatalogAllowedHostInput(nil), left...)
	rightCopy := append([]CatalogAllowedHostInput(nil), right...)
	sortCatalogAllowedHosts(leftCopy)
	sortCatalogAllowedHosts(rightCopy)
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}
