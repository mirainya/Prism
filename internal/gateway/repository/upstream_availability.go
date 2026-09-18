package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	// AvailabilityWindowMinutes matches the provider's own sliding window so the
	// locally measured figure and the upstream one mean the same thing and may
	// be shown in the same column.
	AvailabilityWindowMinutes = 120
	// availabilityMinSamples is the point below which a locally measured rate is
	// not worth showing: three calls of which one failed is not "67% reliable".
	// Below it Prism falls back to the provider's own figure, which is drawn
	// from every tenant's traffic rather than just this deployment's.
	availabilityMinSamples = 20
)

// AvailabilitySourceLocal and AvailabilitySourceUpstream name where a displayed
// rate came from. Users are told which, because the two differ in kind: one is
// what Prism itself measured, the other is what the provider claims.
const (
	AvailabilitySourceLocal    = "local"
	AvailabilitySourceUpstream = "upstream"
)

// SKUAvailability is the success rate shown for one SKU, on a 0..100 scale with
// two decimals.
type SKUAvailability struct {
	SKUID         uint64
	SuccessRate   string
	Source        string
	Samples       uint32
	WindowMinutes uint32
	ObservedAt    time.Time
}

// AvailabilitySource is a configured catalog source reused as the identity for
// availability polling. Reusing the source keeps exactly one place where an
// upstream account is configured, and keeps the credential under the same
// purpose grant and rotation rules as catalog discovery itself.
type AvailabilitySource struct {
	SourceID         uint64
	CredentialID     uint64
	CredentialBlobID uint64
	CredentialSecret []byte
	BaseURL          string
	RequestTimeoutMS uint32
}

// UpstreamAvailabilityRow is one stored observation. Rates are decimal strings
// on the provider's 0..100 scale; see catalogsource.UpstreamAvailability.
type UpstreamAvailabilityRow struct {
	ModelCode                string
	Category                 string
	SuccessRate              string
	AverageCompletionSeconds string
	WindowMinutes            uint32
	ObservedAt               time.Time
}

// ListAvailabilitySources returns active sources whose credential still holds a
// live discovery grant. A revoked grant or a drained source therefore stops
// availability polling without any separate switch.
func (s *Store) ListAvailabilitySources(ctx context.Context, contractCode string) ([]AvailabilitySource, error) {
	if s == nil || !validCatalogSourceText(contractCode, 64) {
		return nil, ErrInvalidInput
	}
	rows, err := s.DB().QueryContext(ctx, `SELECT src.id,c.id,c.secret,COALESCE(cv.encrypted_blob_id,0),p.base_url,p.request_timeout_ms
FROM gw_catalog_sources src
JOIN gw_catalog_source_profiles p ON p.catalog_source_id=src.id
JOIN gw_credentials c ON c.id=src.credential_id AND c.status='active'
JOIN gw_credential_versions cv ON cv.id=c.current_version_id AND cv.credential_id=c.id AND cv.status='active'
JOIN gw_credential_purpose_grants g ON g.id=src.credential_purpose_grant_id AND g.purpose='catalog_discovery' AND g.status='active'
WHERE src.status='active' AND p.contract_code=? ORDER BY src.id`, contractCode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := make([]AvailabilitySource, 0, 4)
	for rows.Next() {
		var source AvailabilitySource
		if err := rows.Scan(&source.SourceID, &source.CredentialID, &source.CredentialSecret, &source.CredentialBlobID,
			&source.BaseURL, &source.RequestTimeoutMS); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// SaveUpstreamAvailability replaces the stored observations for one source.
// Rows are updated in place rather than appended: this is a cache of the
// provider's current view, not an audit trail, and nothing may reconstruct a
// past charge from it.
//
// Models the provider stops reporting keep their last row. Staleness is decided
// by the reader against observed_at, so a provider outage degrades to "last
// seen at T" instead of silently deleting everything a user was relying on.
func (s *Store) SaveUpstreamAvailability(ctx context.Context, sourceID uint64, items []UpstreamAvailabilityRow) error {
	if s == nil || sourceID == 0 || len(items) > 10000 {
		return ErrInvalidInput
	}
	if len(items) == 0 {
		return nil
	}
	now := nowUTC()
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, item := range items {
			if !validCatalogSourceText(item.ModelCode, 128) || !validCatalogSourceText(item.Category, 32) ||
				item.SuccessRate == "" || item.WindowMinutes == 0 || item.ObservedAt.IsZero() {
				return ErrInvalidInput
			}
			completion := item.AverageCompletionSeconds
			if completion == "" {
				completion = "0"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_availability
(catalog_source_id,model_code,category,success_rate,average_completion_seconds,window_minutes,observed_at,updated_at)
VALUES (?,?,?,?,?,?,?,?)
ON DUPLICATE KEY UPDATE category=VALUES(category),success_rate=VALUES(success_rate),
average_completion_seconds=VALUES(average_completion_seconds),window_minutes=VALUES(window_minutes),
observed_at=VALUES(observed_at),updated_at=VALUES(updated_at)`,
				sourceID, item.ModelCode, item.Category, item.SuccessRate, completion,
				item.WindowMinutes, item.ObservedAt.UTC(), now); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceUpstreamAvailability stores one complete provider observation. An
// empty set is meaningful: the provider currently has no sampled model, so an
// older percentage must not keep appearing as though it were current.
func (s *Store) ReplaceUpstreamAvailability(ctx context.Context, sourceID uint64, items []UpstreamAvailabilityRow) error {
	if s == nil || sourceID == 0 || len(items) > 10000 {
		return ErrInvalidInput
	}
	for _, item := range items {
		if !validCatalogSourceText(item.ModelCode, 128) || !validCatalogSourceText(item.Category, 32) ||
			item.SuccessRate == "" || item.WindowMinutes == 0 || item.ObservedAt.IsZero() {
			return ErrInvalidInput
		}
	}
	now := nowUTC()
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM gw_upstream_availability WHERE catalog_source_id=?`, sourceID); err != nil {
			return err
		}
		for _, item := range items {
			completion := item.AverageCompletionSeconds
			if completion == "" {
				completion = "0"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_availability
(catalog_source_id,model_code,category,success_rate,average_completion_seconds,window_minutes,observed_at,updated_at)
VALUES (?,?,?,?,?,?,?,?)`, sourceID, item.ModelCode, item.Category, item.SuccessRate, completion,
				item.WindowMinutes, item.ObservedAt.UTC(), now); err != nil {
				return err
			}
		}
		return nil
	})
}

// LoadSKUAvailability returns the success rate to show for each SKU of a
// release. Locally measured traffic wins wherever there is enough of it: it
// reflects this deployment's own credentials, routing and retry behaviour,
// none of which the provider can see. The provider's figure is the fallback,
// which is what makes a newly listed model show a rate on its first day.
func (s *Store) LoadSKUAvailability(ctx context.Context, releaseID uint64) (map[uint64]SKUAvailability, error) {
	if s == nil || releaseID == 0 {
		return nil, ErrInvalidInput
	}
	local, err := s.loadLocalAvailability(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	upstream, err := s.loadUpstreamAvailability(ctx, releaseID)
	if err != nil {
		return nil, err
	}
	result := make(map[uint64]SKUAvailability, len(local)+len(upstream))
	for skuID, item := range upstream {
		result[skuID] = item
	}
	for skuID, item := range local {
		result[skuID] = item
	}
	return result, nil
}

func (s *Store) loadLocalAvailability(ctx context.Context, releaseID uint64) (map[uint64]SKUAvailability, error) {
	// The window boundary is computed here rather than with UTC_TIMESTAMP(3) in
	// SQL. gw_api_calls.created_at is written from Go (see CreateCall), so the
	// driver serialises it through cfg.Loc; a boundary taken from the database
	// clock would be offset by the connection's zone and silently widen the
	// window to the whole history. Passing a time.Time makes both sides travel
	// through the same conversion.
	since := nowUTC().Add(-AvailabilityWindowMinutes * time.Minute)
	// Only business terminal states count. A cancelled call was the caller's own
	// decision and an indeterminate one is exactly the case where Prism does not
	// know whether it succeeded, so neither may be scored against the model.
	rows, err := s.DB().QueryContext(ctx, `SELECT sku_id,
	SUM(status='completed'),SUM(status IN ('completed','failed')),MAX(created_at)
FROM gw_api_calls
WHERE catalog_release_id=? AND created_at>=?
GROUP BY sku_id`, releaseID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make(map[uint64]SKUAvailability, 16)
	for rows.Next() {
		var skuID uint64
		var completed, total int64
		var rawObserved any
		if err := rows.Scan(&skuID, &completed, &total, &rawObserved); err != nil {
			return nil, err
		}
		if total < availabilityMinSamples || completed > total {
			continue
		}
		observed, err := availabilityTime(rawObserved)
		if err != nil {
			return nil, err
		}
		items[skuID] = SKUAvailability{
			SKUID: skuID, SuccessRate: percentOf(completed, total),
			Source: AvailabilitySourceLocal, Samples: uint32(total),
			WindowMinutes: AvailabilityWindowMinutes, ObservedAt: observed.UTC(),
		}
	}
	return items, rows.Err()
}

// loadUpstreamAvailability maps the provider's per-model figures onto SKUs
// through the vendor model each route actually calls. Where several routes back
// one SKU the lowest rate and the oldest observation win: a rate shown next to a
// price should not read better than the worst path a request may take.
func (s *Store) loadUpstreamAvailability(ctx context.Context, releaseID uint64) (map[uint64]SKUAvailability, error) {
	rows, err := s.DB().QueryContext(ctx, `SELECT route.sku_id,
	MIN(avail.success_rate),MIN(avail.window_minutes),MIN(avail.observed_at)
FROM gw_routes route
JOIN gw_offerings offering ON offering.release_id=route.release_id AND offering.id=route.offering_id
JOIN gw_product_transports pt ON pt.release_id=offering.release_id AND pt.id=offering.product_transport_id
JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id
JOIN gw_upstream_availability avail ON LOWER(TRIM(avail.model_code))=LOWER(TRIM(p.vendor_model))
JOIN gw_catalog_sources source ON source.id=avail.catalog_source_id AND source.channel_id=p.channel_id AND source.status='active'
WHERE route.release_id=?
GROUP BY route.sku_id`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make(map[uint64]SKUAvailability, 16)
	for rows.Next() {
		var item SKUAvailability
		var rawObserved any
		if err := rows.Scan(&item.SKUID, &item.SuccessRate, &item.WindowMinutes, &rawObserved); err != nil {
			return nil, err
		}
		item.ObservedAt, err = availabilityTime(rawObserved)
		if err != nil {
			return nil, err
		}
		item.Source = AvailabilitySourceUpstream
		items[item.SKUID] = item
	}
	return items, rows.Err()
}

func availabilityTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), nil
	case string:
		return parseAvailabilityTime(typed)
	case []byte:
		return parseAvailabilityTime(string(typed))
	default:
		return time.Time{}, fmt.Errorf("invalid availability observation time %T", value)
	}
}

func parseAvailabilityTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid availability observation time")
}

// percentOf renders a ratio on a 0..100 scale with two decimals using integer
// arithmetic only. Floating point is not permitted on this path, and a rate
// shown beside a price must be reproducible exactly.
func percentOf(part, whole int64) string {
	if whole <= 0 {
		return "0.00"
	}
	scaled := part * 10000 / whole
	return fmt.Sprintf("%d.%02d", scaled/100, scaled%100)
}
