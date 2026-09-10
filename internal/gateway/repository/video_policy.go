package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// RoutePolicy is the immutable execution policy selected with a catalog SKU.
// It is read from the catalog snapshot, never inferred from adapter JSON.
type RoutePolicy struct {
	DeliveryMode, IdempotencyMode string
	TaskScope, CancelMode         string
	SourceURLPolicy               string
	UpstreamScopeKind             string
	UpstreamScopeKey              string
	AdapterCode                   string
	AdapterVersion                uint32
	AdapterConfig                 []byte
	ServiceTiers                  []string
}

type VideoRoutePolicy = RoutePolicy

func (s *Store) ReadRoutePolicy(ctx context.Context, releaseID, skuID, productTransportID uint64) (RoutePolicy, error) {
	if releaseID == 0 || skuID == 0 || productTransportID == 0 {
		return RoutePolicy{}, ErrInvalidInput
	}
	var out RoutePolicy
	var serviceTiers []byte
	err := s.db.QueryRowContext(ctx, `SELECT s.delivery_mode,s.idempotency_mode,pt.task_scope,pt.cancel_mode,pt.source_url_policy,pt.upstream_scope_kind,pt.upstream_scope_key,ai.adapter_code,ai.contract_version,COALESCE(p.capability_constraints,'{}'),s.service_tiers
FROM gw_skus s JOIN gw_product_transports pt ON pt.release_id=s.release_id
JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
JOIN gw_adapter_implementations ai ON ai.id=ct.adapter_implementation_id
	JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id
WHERE s.release_id=? AND s.id=? AND pt.release_id=? AND pt.id=?`, releaseID, skuID, releaseID, productTransportID).
		Scan(&out.DeliveryMode, &out.IdempotencyMode, &out.TaskScope, &out.CancelMode, &out.SourceURLPolicy, &out.UpstreamScopeKind, &out.UpstreamScopeKey, &out.AdapterCode, &out.AdapterVersion, &out.AdapterConfig, &serviceTiers)
	if err == sql.ErrNoRows {
		return RoutePolicy{}, ErrNotFound
	}
	if err != nil {
		return RoutePolicy{}, err
	}
	if err := json.Unmarshal(serviceTiers, &out.ServiceTiers); err != nil {
		return RoutePolicy{}, ErrConflict
	}
	seenTiers := make(map[string]struct{}, len(out.ServiceTiers))
	for index := range out.ServiceTiers {
		out.ServiceTiers[index] = strings.TrimSpace(out.ServiceTiers[index])
		if out.ServiceTiers[index] == "" || len(out.ServiceTiers[index]) > 32 {
			return RoutePolicy{}, ErrConflict
		}
		if _, exists := seenTiers[out.ServiceTiers[index]]; exists {
			return RoutePolicy{}, ErrConflict
		}
		seenTiers[out.ServiceTiers[index]] = struct{}{}
	}
	if len(out.ServiceTiers) == 0 {
		return RoutePolicy{}, ErrConflict
	}
	if len(out.AdapterConfig) == 0 || len(out.AdapterConfig) > 16384 || !json.Valid(out.AdapterConfig) {
		return VideoRoutePolicy{}, ErrConflict
	}
	if out.DeliveryMode != "reference" && out.DeliveryMode != "managed_copy" ||
		(out.IdempotencyMode != "required" && out.IdempotencyMode != "optional" && out.IdempotencyMode != "forbidden") ||
		(out.TaskScope != "none" && out.TaskScope != "request" && out.TaskScope != "task") ||
		(out.CancelMode != "none" && out.CancelMode != "upstream" && out.CancelMode != "local_only") ||
		(out.SourceURLPolicy != "fixed" && out.SourceURLPolicy != "refreshable") ||
		!validScope(out.UpstreamScopeKind) || out.UpstreamScopeKey == "" || len(out.UpstreamScopeKey) > 255 || out.AdapterCode == "" || out.AdapterVersion == 0 {
		return RoutePolicy{}, ErrConflict
	}
	return out, nil
}

func (s *Store) ReadVideoRoutePolicy(ctx context.Context, releaseID, skuID, productTransportID uint64) (VideoRoutePolicy, error) {
	return s.ReadRoutePolicy(ctx, releaseID, skuID, productTransportID)
}
