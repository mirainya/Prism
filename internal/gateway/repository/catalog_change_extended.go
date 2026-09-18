package repository

// This file contains the non-pricing catalog edits exposed by the operations
// console. Each edit locks and updates the active release in one transaction;
// no draft, fork, publication, or activation step is required.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RouteWeightChange changes one route's priority and weight.  A route is
// addressed by stable catalog identities rather than surrogate IDs, because a
// fork remaps every release-scoped primary key.
type RouteWeightChange struct {
	catalogChangeGuard
	SKUCode       string `json:"sku_code"`
	ProductCode   string `json:"product_code"`
	PoolCode      string `json:"pool_code"`
	TransportCode string `json:"transport_code"`
	Priority      uint32 `json:"priority"`
	Weight        uint64 `json:"weight"`
}

func (in *RouteWeightChange) Normalize() {
	in.SKUCode = strings.ToLower(strings.TrimSpace(in.SKUCode))
	in.ProductCode = strings.ToLower(strings.TrimSpace(in.ProductCode))
	in.PoolCode = strings.ToLower(strings.TrimSpace(in.PoolCode))
	in.TransportCode = strings.ToLower(strings.TrimSpace(in.TransportCode))
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
}

func (in RouteWeightChange) Validate() error {
	if err := in.catalogChangeGuard.validate(); err != nil {
		return err
	}
	if !catalogIdentityPattern.MatchString(in.SKUCode) ||
		!catalogIdentityPattern.MatchString(in.ProductCode) ||
		!catalogIdentityPattern.MatchString(in.PoolCode) ||
		!catalogIdentityPattern.MatchString(in.TransportCode) ||
		in.Weight == 0 || in.Weight > 1000000 || in.Priority > 1000000000 {
		return ErrInvalidInput
	}
	return nil
}

// SKUVariantChange selects another commercial variant declared by the serving
// adapter manifest.
type SKUVariantChange struct {
	catalogChangeGuard
	SKUCode     string `json:"sku_code"`
	VariantCode string `json:"variant_code"`
}

func (in *SKUVariantChange) Normalize() {
	in.SKUCode = strings.ToLower(strings.TrimSpace(in.SKUCode))
	in.VariantCode = strings.ToLower(strings.TrimSpace(in.VariantCode))
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
}

func (in SKUVariantChange) Validate() error {
	if err := in.catalogChangeGuard.validate(); err != nil {
		return err
	}
	if !catalogIdentityPattern.MatchString(in.SKUCode) ||
		!catalogIdentityPattern.MatchString(in.VariantCode) || len(in.VariantCode) > 64 {
		return ErrInvalidInput
	}
	return nil
}

// SKUDownstreamPathsChange replaces the complete set of downstream protocol
// paths a SKU answers on.  The set is release content, not runtime state.
type SKUDownstreamPathsChange struct {
	catalogChangeGuard
	SKUCode         string   `json:"sku_code"`
	DownstreamPaths []string `json:"downstream_paths"`
}

func (in *SKUDownstreamPathsChange) Normalize() {
	in.SKUCode = strings.ToLower(strings.TrimSpace(in.SKUCode))
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
	for i := range in.DownstreamPaths {
		in.DownstreamPaths[i] = strings.TrimSpace(in.DownstreamPaths[i])
	}
	sort.Strings(in.DownstreamPaths)
}

func (in SKUDownstreamPathsChange) Validate() error {
	if err := in.catalogChangeGuard.validate(); err != nil {
		return err
	}
	if !catalogIdentityPattern.MatchString(in.SKUCode) || len(in.DownstreamPaths) == 0 || len(in.DownstreamPaths) > 32 {
		return ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(in.DownstreamPaths))
	for _, path := range in.DownstreamPaths {
		if !validDownstreamPath(path) || len(path) > 128 {
			return ErrInvalidInput
		}
		if _, exists := seen[path]; exists {
			return ErrInvalidInput
		}
		seen[path] = struct{}{}
	}
	return nil
}

func validDownstreamPath(path string) bool {
	return path != "" && strings.HasPrefix(path, "/") &&
		!strings.ContainsAny(path, "?#\x00\r\n\t")
}

// ProductChange changes only the two mutable product content fields exposed by
// v5.4.  Product identity, transport and credential bindings remain stable.
type ProductChange struct {
	catalogChangeGuard
	ProductCode           string          `json:"product_code"`
	VendorModel           string          `json:"vendor_model"`
	CapabilityConstraints json.RawMessage `json:"capability_constraints"`
}

// CatalogProductValidator validates adapter-owned product configuration while
// the active catalog transaction is locked. Keeping the callback at this
// boundary avoids a repository -> adapter import cycle.
type CatalogProductValidator func(adapterCode string, adapterVersion uint32, baseURL, method, path, vendorModel string, config []byte) error

func (in *ProductChange) Normalize() error {
	in.ProductCode = strings.ToLower(strings.TrimSpace(in.ProductCode))
	if !validUpstreamVendorModel(in.VendorModel) {
		return ErrInvalidInput
	}
	in.VendorModel = strings.TrimSpace(in.VendorModel)
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
	constraints, err := normalizeCatalogJSON(in.CapabilityConstraints)
	if err != nil {
		return err
	}
	in.CapabilityConstraints = constraints
	return nil
}

func (in ProductChange) Validate() error {
	if err := in.catalogChangeGuard.validate(); err != nil {
		return err
	}
	if !catalogIdentityPattern.MatchString(in.ProductCode) ||
		!validUpstreamVendorModel(in.VendorModel) || len(in.CapabilityConstraints) > 16384 {
		return ErrInvalidInput
	}
	return nil
}

// CatalogRollbackInput is the public request shape for a historical release
// rollback.  The release being rolled back to is supplied separately by the
// URL/path parameter; this body carries the optimistic active-release guard and
// the metadata for the new fork.
type CatalogRollbackInput struct {
	ExpectedActiveReleaseID uint64 `json:"expected_active_release_id"`
	ExpectedConfigVersion   uint64 `json:"expected_config_version,omitempty"`
	SemanticVersion         string `json:"semantic_version"`
	SemanticDigest          string `json:"-"`
}

func (in *CatalogRollbackInput) Normalize() {
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
}

func (in CatalogRollbackInput) guard() catalogChangeGuard {
	return catalogChangeGuard{
		ExpectedActiveReleaseID: in.ExpectedActiveReleaseID,
		ExpectedConfigVersion:   in.ExpectedConfigVersion,
		SemanticVersion:         in.SemanticVersion,
		SemanticDigest:          in.SemanticDigest,
	}
}

func (in CatalogRollbackInput) Validate() error {
	if err := in.guard().validate(); err != nil {
		return err
	}
	// Rollback is retained only for in-process compatibility with the old
	// release API.  That API created a new release, so it still requires the
	// metadata that direct edits no longer need.
	if in.SemanticVersion == "" || !semanticVersionPattern.MatchString(in.SemanticVersion) || !validHexDigest(in.SemanticDigest, 32) {
		return ErrInvalidInput
	}
	return nil
}

// RollbackCatalogRelease forks an arbitrary published historical release after
// checking that the operator's expected active pointer is still current.  The
// resulting release is intentionally not activated inside the repository; the
// HTTP layer performs the same readiness proof used by every B-class edit.
func (s *Store) RollbackCatalogRelease(ctx context.Context, tx *sql.Tx, sourceReleaseID uint64, in CatalogRollbackInput, actorID uint64) (CatalogChangeResult, error) {
	if tx == nil || sourceReleaseID == 0 || actorID == 0 {
		return CatalogChangeResult{}, ErrInvalidInput
	}
	in.Normalize()
	guard := in.guard()
	if err := in.Validate(); err != nil {
		return CatalogChangeResult{}, err
	}
	activeLock, err := lockActiveCatalogRelease(ctx, tx, guard.ExpectedActiveReleaseID, guard.ExpectedConfigVersion)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	active := activeLock.ID
	if active == sourceReleaseID {
		return CatalogChangeResult{}, fmt.Errorf("%w: release %d is already active", ErrConflict, sourceReleaseID)
	}
	target, err := s.ForkCatalogRelease(ctx, tx, sourceReleaseID, guard.draft(), actorID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	if err := s.PublishRelease(ctx, tx, target, actorID); err != nil {
		return CatalogChangeResult{}, err
	}
	if err := recordCatalogAdminChange(ctx, tx, actorID, "catalog_change.rollback", "catalog_release", target, ginSafeMetadata{
		"source_release_id":          sourceReleaseID,
		"expected_active_release_id": guard.ExpectedActiveReleaseID,
	}); err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: target, ConfigVersion: 2, SourceReleaseID: active}, nil
}

// ChangeRouteWeight updates one route on the active release.
func (s *Store) ChangeRouteWeight(ctx context.Context, tx *sql.Tx, in RouteWeightChange, actorID uint64) (CatalogChangeResult, error) {
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
	var routeID, skuID uint64
	var oldPriority uint32
	var oldWeight uint64
	query := `SELECT r.id,r.sku_id,r.offering_id
FROM gw_routes r
JOIN gw_skus s ON s.release_id=r.release_id AND s.id=r.sku_id
JOIN gw_offerings o ON o.release_id=r.release_id AND o.id=r.offering_id
JOIN gw_product_transports pt ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id
JOIN gw_credential_pools pool ON pool.id=o.credential_pool_id
JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
WHERE r.release_id=? AND s.sku_code=? AND p.product_code=? AND pool.pool_code=? AND ct.transport_code=? LIMIT 2`
	rows, err := tx.QueryContext(ctx, query, active.ID, in.SKUCode, in.ProductCode, in.PoolCode, in.TransportCode)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	for rows.Next() {
		var candidateRoute, candidateSKU, candidateOffering uint64
		if err := rows.Scan(&candidateRoute, &candidateSKU, &candidateOffering); err != nil {
			rows.Close()
			return CatalogChangeResult{}, err
		}
		if routeID != 0 {
			rows.Close()
			return CatalogChangeResult{}, fmt.Errorf("%w: route address is ambiguous", ErrConflict)
		}
		routeID, skuID = candidateRoute, candidateSKU
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CatalogChangeResult{}, err
	}
	rows.Close()
	if routeID == 0 {
		return CatalogChangeResult{}, ErrNotFound
	}
	if err := tx.QueryRowContext(ctx, `SELECT priority,weight FROM gw_routes WHERE release_id=? AND id=? FOR UPDATE`, active.ID, routeID).Scan(&oldPriority, &oldWeight); err != nil {
		return CatalogChangeResult{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_routes SET priority=?,weight=? WHERE release_id=? AND id=?`, in.Priority, in.Weight, active.ID, routeID)
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
	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.route_weight", actorID, ginSafeMetadata{
		"source_release_id": active.ID, "sku_id": skuID, "sku_code": in.SKUCode, "product_code": in.ProductCode,
		"pool_code": in.PoolCode, "transport_code": in.TransportCode,
		"before": ginSafeMetadata{"priority": oldPriority, "weight": oldWeight},
		"after":  ginSafeMetadata{"priority": in.Priority, "weight": in.Weight},
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}

// ChangeSKUVariant updates one SKU's variant on the active release.
func (s *Store) ChangeSKUVariant(ctx context.Context, tx *sql.Tx, in SKUVariantChange, actorID uint64) (CatalogChangeResult, error) {
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
	var skuID uint64
	var oldVariant string
	if err := tx.QueryRowContext(ctx, `SELECT id,variant_code FROM gw_skus WHERE release_id=? AND sku_code=? FOR UPDATE`, active.ID, in.SKUCode).Scan(&skuID, &oldVariant); err == sql.ErrNoRows {
		return CatalogChangeResult{}, ErrNotFound
	} else if err != nil {
		return CatalogChangeResult{}, err
	}
	if oldVariant == in.VariantCode {
		return CatalogChangeResult{}, fmt.Errorf("%w: SKU already uses variant %q", ErrConflict, in.VariantCode)
	}
	if err := validateRequestedSKUVariant(ctx, tx, active.ID, skuID, in.VariantCode); err != nil {
		return CatalogChangeResult{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_skus SET variant_code=? WHERE release_id=? AND id=?`, in.VariantCode, active.ID, skuID)
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
	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.sku_variant", actorID, ginSafeMetadata{
		"source_release_id": active.ID, "sku_code": in.SKUCode, "before": oldVariant, "after": in.VariantCode,
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}

// ChangeSKUDownstreamPaths replaces a SKU's protocol path set on the active release.
func (s *Store) ChangeSKUDownstreamPaths(ctx context.Context, tx *sql.Tx, in SKUDownstreamPathsChange, actorID uint64) (CatalogChangeResult, error) {
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
	var skuID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_skus WHERE release_id=? AND sku_code=? FOR UPDATE`, active.ID, in.SKUCode).Scan(&skuID); err == sql.ErrNoRows {
		return CatalogChangeResult{}, ErrNotFound
	} else if err != nil {
		return CatalogChangeResult{}, err
	}
	if err := validateRequestedDownstreamPaths(ctx, tx, active.ID, skuID, in.DownstreamPaths); err != nil {
		return CatalogChangeResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM gw_sku_downstream_paths WHERE release_id=? AND sku_id=?`, active.ID, skuID); err != nil {
		return CatalogChangeResult{}, err
	}
	for _, path := range in.DownstreamPaths {
		if _, err := tx.ExecContext(ctx, `INSERT INTO gw_sku_downstream_paths(release_id,sku_id,path,created_at) VALUES (?,?,?,?)`, active.ID, skuID, path, nowUTC()); err != nil {
			return CatalogChangeResult{}, err
		}
	}
	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.sku_downstream_paths", actorID, ginSafeMetadata{
		"source_release_id": active.ID, "sku_code": in.SKUCode, "paths": in.DownstreamPaths,
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}

// ChangeProduct updates product metadata on the active release.
func (s *Store) ChangeProduct(ctx context.Context, tx *sql.Tx, in ProductChange, validate CatalogProductValidator, actorID uint64) (CatalogChangeResult, error) {
	if tx == nil || validate == nil || actorID == 0 {
		return CatalogChangeResult{}, ErrInvalidInput
	}
	if err := in.Normalize(); err != nil {
		return CatalogChangeResult{}, err
	}
	if err := in.Validate(); err != nil {
		return CatalogChangeResult{}, err
	}
	active, err := lockActiveCatalogRelease(ctx, tx, in.ExpectedActiveReleaseID, in.ExpectedConfigVersion)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	var productID uint64
	var oldVendor string
	var oldConstraints []byte
	if err := tx.QueryRowContext(ctx, `SELECT id,vendor_model,capability_constraints FROM gw_products WHERE release_id=? AND product_code=? FOR UPDATE`, active.ID, in.ProductCode).Scan(&productID, &oldVendor, &oldConstraints); err == sql.ErrNoRows {
		return CatalogChangeResult{}, ErrNotFound
	} else if err != nil {
		return CatalogChangeResult{}, err
	}
	transportRows, err := tx.QueryContext(ctx, `
SELECT a.adapter_code,a.contract_version,ct.base_url,ct.request_method,ct.request_path
FROM gw_product_transports pt
JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
JOIN gw_adapter_implementations a ON a.id=ct.adapter_implementation_id
WHERE pt.release_id=? AND pt.product_id=?`, active.ID, productID)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	for transportRows.Next() {
		var adapterCode, baseURL, method, path string
		var adapterVersion uint32
		if err := transportRows.Scan(&adapterCode, &adapterVersion, &baseURL, &method, &path); err != nil {
			_ = transportRows.Close()
			return CatalogChangeResult{}, err
		}
		if err := validate(adapterCode, adapterVersion, baseURL, method, path, in.VendorModel, in.CapabilityConstraints); err != nil {
			_ = transportRows.Close()
			return CatalogChangeResult{}, fmt.Errorf("%w: invalid %s@%d product configuration: %v", ErrInvalidInput, adapterCode, adapterVersion, err)
		}
	}
	if err := transportRows.Err(); err != nil {
		_ = transportRows.Close()
		return CatalogChangeResult{}, err
	}
	if err := transportRows.Close(); err != nil {
		return CatalogChangeResult{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_products SET vendor_model=?,capability_constraints=? WHERE release_id=? AND id=?`, in.VendorModel, in.CapabilityConstraints, active.ID, productID)
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
	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.product", actorID, ginSafeMetadata{
		"source_release_id": active.ID, "product_code": in.ProductCode,
		"before": ginSafeMetadata{"vendor_model": oldVendor, "capability_constraints": string(oldConstraints)},
		"after":  ginSafeMetadata{"vendor_model": in.VendorModel, "capability_constraints": string(in.CapabilityConstraints)},
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}

// validateRequestedSKUVariant validates the requested value (rather than the
// value already stored on the row) against every adapter that can serve it.
func validateRequestedSKUVariant(ctx context.Context, db CatalogPricingQuery, releaseID, skuID uint64, variant string) error {
	if variant == DefaultVariantCode {
		return nil
	}
	refs, err := loadRateAdapterVariants(ctx, db, releaseID, skuID, false)
	if err != nil {
		return err
	}
	if len(refs[skuID]) == 0 {
		return nil
	}
	source := currentExpressionSpecSource()
	if source == nil {
		return fmt.Errorf("%w: SKU %d claims variant %q", ErrExpressionSpecUnavailable, skuID, variant)
	}
	for _, ref := range refs[skuID] {
		if _, err := source.ExpressionSpec(ref.Code, ref.Version, variant); err != nil {
			return fmt.Errorf("%w: SKU %d claims %s@%d/%s: %v", ErrUnknownVariant, skuID, ref.Code, ref.Version, variant, err)
		}
	}
	return nil
}

// DownstreamPathSource is the optional manifest whitelist used by the path
// change.  It is registered by the adapter package at process startup.
type DownstreamPathSource interface {
	DownstreamPaths(adapterCode string, adapterVersion uint32) ([]string, error)
}

var downstreamPathSource DownstreamPathSource

func SetDownstreamPathSource(source DownstreamPathSource) { downstreamPathSource = source }

func validateRequestedDownstreamPaths(ctx context.Context, db CatalogPricingQuery, releaseID, skuID uint64, paths []string) error {
	refs, err := loadRateAdapterVariants(ctx, db, releaseID, skuID, false)
	if err != nil {
		return err
	}
	if len(refs[skuID]) == 0 || downstreamPathSource == nil {
		return nil
	}
	for _, ref := range refs[skuID] {
		allowed, err := downstreamPathSource.DownstreamPaths(ref.Code, ref.Version)
		if err != nil {
			return fmt.Errorf("%w: no downstream manifest for %s@%d", ErrExpressionSpecUnavailable, ref.Code, ref.Version)
		}
		allowedSet := make(map[string]struct{}, len(allowed))
		for _, path := range allowed {
			allowedSet[path] = struct{}{}
		}
		for _, path := range paths {
			if _, ok := allowedSet[path]; !ok {
				return fmt.Errorf("%w: path %q is not declared by %s@%d", ErrInvalidInput, path, ref.Code, ref.Version)
			}
		}
	}
	return nil
}
