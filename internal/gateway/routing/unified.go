package routing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
)

const unifiedKeyMarker uint = 1 << 31

// UnifiedCredentialKeyID returns the Engine-facing key identity for a
// credential stored in the unified catalog.
func UnifiedCredentialKeyID(credentialID uint) uint {
	if credentialID == 0 {
		return 0
	}
	return unifiedKeyMarker | credentialID
}

type unifiedSelector struct{}

type unifiedCandidate struct {
	ReleaseID, OperationContractID, ModelOperationID, SKUID, RouteID, ProductTransportID          uint64
	AbilityID, CredentialID, ChannelID, PoolID, VersionID, OfferingID, CostPlanID, PurposeGrantID uint64
	VendorModel, Protocol, BaseURL, TransportCode, DeliveryMode                                   string
	Priority                                                                                      int64
	RouteWeight, CredentialWeight                                                                 uint64
	RequestMethod, RequestPath                                                                    string
	Nonce, Ciphertext, WrapNonce, WrappedDEK                                                      []byte
	BlobID                                                                                        uint64
	KEKVersion                                                                                    uint32
	Capabilities                                                                                  []byte
}

type unifiedCandidateScanner interface {
	Scan(...any) error
}

func scanUnifiedCandidate(row unifiedCandidateScanner) (unifiedCandidate, error) {
	var candidate unifiedCandidate
	err := row.Scan(
		&candidate.ReleaseID, &candidate.OperationContractID, &candidate.ModelOperationID, &candidate.SKUID, &candidate.RouteID, &candidate.ProductTransportID, &candidate.AbilityID,
		&candidate.ChannelID, &candidate.PoolID, &candidate.CredentialID, &candidate.VersionID, &candidate.OfferingID, &candidate.CostPlanID, &candidate.PurposeGrantID, &candidate.BlobID,
		&candidate.VendorModel, &candidate.Protocol, &candidate.BaseURL, &candidate.TransportCode, &candidate.DeliveryMode, &candidate.Priority, &candidate.RouteWeight, &candidate.CredentialWeight,
		&candidate.RequestMethod, &candidate.RequestPath, &candidate.Nonce, &candidate.Ciphertext, &candidate.WrapNonce, &candidate.WrappedDEK, &candidate.KEKVersion, &candidate.Capabilities,
	)
	return candidate, err
}

func (s *unifiedSelector) active(ctx context.Context) (bool, error) {
	db, err := model.DB().DB()
	if err != nil {
		return false, err
	}
	var releaseID sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&releaseID); err != nil {
		return false, err
	}
	if !releaseID.Valid || releaseID.Int64 <= 0 {
		return false, nil
	}
	if err := gatewayruntime.CheckReadiness(ctx, db, uint64(releaseID.Int64)); err != nil {
		if errors.Is(err, gatewayruntime.ErrNotReady) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// unifiedKeyStateMask matches the KeyID marker Engine writes into
// gw_route_states so the routing LEFT JOIN can filter with the same
// namespaced identity the circuit uses. It must stay in sync with
// unifiedKeyMarker.
const unifiedKeyStateMask uint64 = 1 << 31

// CircuitKeyMask exposes the same marker to the admin control plane, which has
// to undo it to turn a gw_route_states row back into a credential id. Sharing
// the constant keeps the two sides from drifting apart silently.
const CircuitKeyMask = unifiedKeyStateMask

const unifiedCandidatesSQL = `
SELECT rel.id, mo.operation_contract_id, mo.id, sku.id, r.id, pt.id, mo.id,
       ch.id, o.credential_pool_id, c.id, cv.id, o.id, cost.id, g.id, COALESCE(eb.id,0),
       p.vendor_model, ct.protocol, ct.base_url, ct.transport_code, sku.delivery_mode,
	       r.priority, r.weight, c.weight, ct.request_method, ct.request_path,
       eb.nonce, eb.ciphertext, w.wrap_nonce, w.wrapped_dek, COALESCE(w.kek_version,0),
	       cm.capability_tags
FROM gw_catalog_releases rel
JOIN gw_catalog_models cm ON cm.release_id=rel.id
JOIN gw_catalog_model_names cmn ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id
JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
JOIN gw_model_operations mo ON mo.release_id=cm.release_id AND mo.catalog_model_id=cm.id
JOIN gw_skus sku ON sku.release_id=mo.release_id AND sku.model_operation_id=mo.id
JOIN gw_routes r ON r.release_id=sku.release_id AND r.sku_id=sku.id
JOIN gw_offerings o ON o.release_id=r.release_id AND o.id=r.offering_id
JOIN gw_cost_plans cost ON cost.release_id=o.release_id AND cost.offering_id=o.id AND cost.plan_code=o.cost_plan_code
JOIN gw_product_transports pt ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id
JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
JOIN gateway_channels ch ON ch.id=p.channel_id AND ch.status='active'
JOIN gw_offering_runtime_state ors ON ors.release_id=o.release_id AND ors.offering_id=o.id AND ors.state='active'
JOIN gw_credential_pools pool ON pool.id=o.credential_pool_id AND pool.channel_id=ch.id AND pool.status='active'
JOIN gw_credentials c ON c.channel_id=ch.id AND c.credential_pool_id=o.credential_pool_id AND c.status='active'
JOIN gw_credential_secret_identities si ON si.id=c.secret_identity_id AND si.channel_id=c.channel_id AND si.status='active'
JOIN gw_credential_purpose_grants g ON g.credential_id=c.id AND g.purpose='execution' AND g.status='active'
JOIN gw_credential_versions cv ON cv.credential_id=c.id AND cv.id=c.current_version_id AND cv.status='active' AND (cv.valid_until IS NULL OR cv.valid_until>CURRENT_TIMESTAMP(3))
LEFT JOIN encrypted_blobs eb ON eb.id=cv.encrypted_blob_id
LEFT JOIN crypto_keyring_state ks ON ks.id=eb.keyring_id
LEFT JOIN crypto_key_versions kv ON kv.keyring_id=ks.id AND kv.key_version=ks.current_version AND kv.status='current'
LEFT JOIN encrypted_blob_key_wraps w ON w.encrypted_blob_id=eb.id AND w.keyring_id=eb.keyring_id AND w.kek_version=ks.current_version
-- Cross-request circuit breaker: skip (credential, transport) combos whose
-- backoff window has not yet elapsed. Rows are written by Engine's Circuit
-- (routing/circuit.go) with key_id = unifiedKeyStateMask | credential.id.
LEFT JOIN gw_route_states rs ON rs.key_id=(c.id | ?)
  AND rs.model_name COLLATE utf8mb4_unicode_ci=mn.api_name COLLATE utf8mb4_unicode_ci
  AND rs.transport COLLATE utf8mb4_unicode_ci=ct.transport_code COLLATE utf8mb4_unicode_ci
  AND rs.disabled_until>CURRENT_TIMESTAMP(3)
WHERE rel.id=? AND rel.status='published' AND mn.api_name=? AND sku.id=? AND rs.id IS NULL
  AND ((c.secret IS NOT NULL AND c.secret<>'') OR (cv.encrypted_blob_id IS NOT NULL AND kv.status='current'))
ORDER BY r.priority DESC, r.id`

const unifiedOperationSKUSQL = `SELECT COUNT(DISTINCT sku.id),MIN(sku.id),COUNT(DISTINCT request_contract.id),MIN(request_contract.id)
FROM gw_catalog_model_names cmn
JOIN gw_catalog_models cm ON cm.release_id=cmn.release_id AND cm.id=cmn.catalog_model_id
JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
JOIN gw_model_operations mo ON mo.release_id=cm.release_id AND mo.catalog_model_id=cm.id
JOIN gw_operation_contracts source_contract ON source_contract.id=mo.operation_contract_id AND source_contract.status='active'
JOIN gw_skus sku ON sku.release_id=mo.release_id AND sku.model_operation_id=mo.id
JOIN gw_sku_downstream_paths downstream ON downstream.release_id=sku.release_id AND downstream.sku_id=sku.id
JOIN gw_operation_routes request_route ON request_route.http_method=? AND request_route.route_template=downstream.path
JOIN gw_operation_contracts request_contract ON request_contract.id=request_route.operation_contract_id AND request_contract.status='active'
WHERE cm.release_id=? AND mn.api_name=? AND downstream.path=?
  AND (
    source_contract.operation_code=request_contract.operation_code
    OR NOT EXISTS (
      SELECT 1
      FROM gw_model_operations native_mo
      JOIN gw_operation_contracts native_contract
        ON native_contract.id=native_mo.operation_contract_id
       AND native_contract.status='active'
       AND native_contract.operation_code=request_contract.operation_code
      JOIN gw_skus native_sku
        ON native_sku.release_id=native_mo.release_id
       AND native_sku.model_operation_id=native_mo.id
      JOIN gw_sku_downstream_paths native_downstream
        ON native_downstream.release_id=native_sku.release_id
       AND native_downstream.sku_id=native_sku.id
       AND native_downstream.path=downstream.path
      WHERE native_mo.release_id=mo.release_id
        AND native_mo.catalog_model_id=mo.catalog_model_id
    )
  )`

func resolveUnifiedOperation(ctx context.Context, db *sql.DB, releaseID int64, modelName string, options RouteOptions) (uint64, uint64, error) {
	if options.OperationMethod == "" || options.OperationPath == "" {
		return 0, 0, ErrNoRoute
	}
	var skuCount, contractCount uint64
	var skuID, contractID sql.NullInt64
	if err := db.QueryRowContext(ctx, unifiedOperationSKUSQL, options.OperationMethod, releaseID, modelName, options.OperationPath).
		Scan(&skuCount, &skuID, &contractCount, &contractID); err != nil {
		return 0, 0, err
	}
	if skuCount == 0 || contractCount == 0 {
		return 0, 0, ErrNoRoute
	}
	if skuCount != 1 || !skuID.Valid || skuID.Int64 <= 0 {
		return 0, 0, ErrAmbiguousSKU
	}
	if contractCount != 1 || !contractID.Valid || contractID.Int64 <= 0 {
		return 0, 0, ErrNoRoute
	}
	return uint64(skuID.Int64), uint64(contractID.Int64), nil
}

func resolveUnifiedSKU(ctx context.Context, db *sql.DB, releaseID int64, modelName string, options RouteOptions) (uint64, error) {
	skuID, _, err := resolveUnifiedOperation(ctx, db, releaseID, modelName, options)
	return skuID, err
}

func (s *unifiedSelector) selectTransport(ctx context.Context, modelName string, requirements RouteRequirements, options RouteOptions) (*RouteResult, error) {
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	var activeRelease sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&activeRelease); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoRoute
		}
		return nil, err
	}
	if !activeRelease.Valid || activeRelease.Int64 <= 0 {
		return nil, ErrNoRoute
	}
	activeReleaseID := activeRelease.Int64
	var declared uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*)
FROM gw_catalog_releases rel
JOIN gw_catalog_models cm ON cm.release_id=rel.id
JOIN gw_catalog_model_names cmn ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id
JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
		WHERE rel.id=? AND rel.status='published' AND mn.api_name=?`, activeReleaseID, modelName).Scan(&declared); err != nil {
		return nil, err
	}
	if declared == 0 {
		return nil, ErrModelNotFound
	}
	skuID, operationContractID, err := resolveUnifiedOperation(ctx, db, activeReleaseID, modelName, options)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, unifiedCandidatesSQL, unifiedKeyStateMask, activeReleaseID, modelName, skuID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []unifiedCandidate
	capabilityMatch, transportMatch := false, false
	for rows.Next() {
		c, err := scanUnifiedCandidate(rows)
		if err != nil {
			return nil, err
		}
		transport := unifiedTransport(c.Protocol)
		if transport == "" {
			continue
		}
		if !supportsSemanticRequirements(semanticCapabilities(c.Capabilities), requirements) {
			continue
		}
		capabilityMatch = true
		if !transportAllowed(transport, options.AllowedTransports) {
			continue
		}
		transportMatch = true
		if containsUint(options.ExcludeChannels, uint(c.ChannelID)) || containsUint(options.ExcludeKeys, uint(c.CredentialID)) || excludedUnifiedAttempt(options.ExcludeAttempts, c.CredentialID, transport) {
			continue
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		if !capabilityMatch {
			return nil, ErrCapabilityUnavailable
		}
		if !transportMatch {
			return nil, ErrNoCompatibleTransport
		}
		// Candidates exist on paper but every one was filtered out — most often
		// by the circuit breaker LEFT JOIN above, which leaves no trace in the
		// result set. Ask gw_route_states directly so the 503 can say so.
		return nil, diagnoseNoRoute(ctx, db, modelName)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftRank := transportRank(unifiedTransport(candidates[i].Protocol), options.PreferredTransports)
		rightRank := transportRank(unifiedTransport(candidates[j].Protocol), options.PreferredTransports)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		return candidates[i].AbilityID < candidates[j].AbilityID
	})
	topRank := transportRank(unifiedTransport(candidates[0].Protocol), options.PreferredTransports)
	topPriority := candidates[0].Priority
	top := candidates[:0]
	for _, candidate := range candidates {
		if transportRank(unifiedTransport(candidate.Protocol), options.PreferredTransports) != topRank || candidate.Priority != topPriority {
			break
		}
		top = append(top, candidate)
	}
	chosen, err := deterministicUnifiedCandidate(options.SelectionKey, top)
	if err != nil {
		return nil, err
	}
	schedule, err := repository.LoadSellSchedule(ctx, db, chosen.ReleaseID, chosen.SKUID)
	if err != nil {
		return nil, err
	}
	apiKey, err := resolveUnifiedCredentialSecret(ctx, db, chosen)
	if err != nil {
		return nil, err
	}
	return &RouteResult{
		// Cross-protocol aliases keep the SKU's native model operation for pricing,
		// while the call belongs to the contract selected by this request path.
		ReleaseID: uint(chosen.ReleaseID), OperationContractID: uint(operationContractID), ModelOperationID: uint(chosen.ModelOperationID),
		SKUID: uint(chosen.SKUID), RouteID: uint(chosen.RouteID), OfferingID: uint(chosen.OfferingID), CostPlanID: uint(chosen.CostPlanID), ProductTransportID: uint(chosen.ProductTransportID),
		CredentialPoolID: uint(chosen.PoolID), CredentialID: uint(chosen.CredentialID), CredentialVersionID: uint(chosen.VersionID), PurposeGrantID: uint(chosen.PurposeGrantID),
		AbilityID: uint(chosen.AbilityID), KeyID: UnifiedCredentialKeyID(uint(chosen.CredentialID)), ChannelID: uint(chosen.ChannelID),
		Protocol: model.Protocol(chosen.Protocol), BaseURL: chosen.BaseURL, APIKey: apiKey,
		VendorModel: chosen.VendorModel, ModelName: modelName, Capabilities: semanticCapabilities(chosen.Capabilities),
		Transport: unifiedTransport(chosen.Protocol), TransportConfig: map[string]any{"request_method": chosen.RequestMethod, "request_path": chosen.RequestPath},
		SellSchedule: &schedule, Currency: schedule.Currency.Code, CurrencyVersion: uint(schedule.Currency.Version), DeliveryMode: chosen.DeliveryMode,
	}, nil
}

func excludedUnifiedAttempt(attempts []TransportAttempt, credentialID uint64, transport model.UpstreamTransport) bool {
	for _, attempt := range attempts {
		if uint64(attempt.KeyID&^unifiedKeyMarker) == credentialID && attempt.Transport == transport {
			return true
		}
	}
	return false
}

func deterministicUnifiedCandidate(selectionKey string, candidates []unifiedCandidate) (unifiedCandidate, error) {
	if len(candidates) == 0 {
		return unifiedCandidate{}, ErrNoRoute
	}
	best := candidates[0]
	bestScore, err := weightedRendezvousScore(selectionKey, best.RouteID, best.OfferingID, best.CredentialID, best.RouteWeight, best.CredentialWeight)
	if err != nil {
		return unifiedCandidate{}, err
	}
	for _, candidate := range candidates[1:] {
		score, scoreErr := weightedRendezvousScore(selectionKey, candidate.RouteID, candidate.OfferingID, candidate.CredentialID, candidate.RouteWeight, candidate.CredentialWeight)
		if scoreErr != nil {
			return unifiedCandidate{}, scoreErr
		}
		if score.Cmp(bestScore) < 0 || score.Cmp(bestScore) == 0 && unifiedCandidateLess(candidate, best) {
			best, bestScore = candidate, score
		}
	}
	return best, nil
}

func unifiedCandidateLess(left, right unifiedCandidate) bool {
	if left.RouteID != right.RouteID {
		return left.RouteID < right.RouteID
	}
	if left.OfferingID != right.OfferingID {
		return left.OfferingID < right.OfferingID
	}
	return left.CredentialID < right.CredentialID
}

func containsUint(values []uint, target uint) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func resolveUnifiedCredentialSecret(ctx context.Context, db *sql.DB, c unifiedCandidate) (string, error) {
	// New credentials use the direct secret column. A failed lookup is treated
	// as a legacy schema/row and falls through to the encrypted-blob reader.
	var secret sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT secret FROM gw_credentials WHERE id=?`, c.CredentialID).Scan(&secret); err == nil {
		if secret.Valid && secret.String != "" {
			return secret.String, nil
		}
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", err
	}
	return decryptUnifiedCredential(c)
}

func decryptUnifiedCredential(c unifiedCandidate) (string, error) {
	if c.BlobID == 0 {
		return "", errors.New("unified gateway credential secret is unavailable")
	}
	value := os.Getenv("PRISM_GATEWAY_KEK_B64")
	if value == "" {
		return "", errors.New("unified gateway keyring is not configured")
	}
	kek, err := security.DecodeBase64Key(value)
	if err != nil {
		return "", errors.New("unified gateway keyring has invalid key material")
	}
	defer clear(kek)
	aad, err := security.CanonicalAAD(c.BlobID, "credential", 1, []byte(fmt.Sprintf("credential:%d", c.CredentialID)))
	if err != nil {
		return "", err
	}
	plaintext, err := (security.Envelope{Version: security.EnvelopeVersion, AADVersion: 1, KEKVersion: c.KEKVersion, Nonce: c.Nonce, Ciphertext: c.Ciphertext, WrapNonce: c.WrapNonce, WrappedDEK: c.WrappedDEK}).Open(aad, kek)
	if err != nil {
		return "", err
	}
	result := string(plaintext)
	clear(plaintext)
	return result, nil
}

func unifiedTransport(protocol string) model.UpstreamTransport {
	switch strings.ToLower(protocol) {
	case "openai", "openai_chat":
		return model.UpstreamTransportOpenAIChat
	case "openai_responses":
		return model.UpstreamTransportOpenAIResponses
	case "anthropic", "anthropic_messages":
		return model.UpstreamTransportAnthropic
	case "google", "google_generate_content":
		return model.UpstreamTransportGoogle
	case "volcengine", "volcengine_responses_v3":
		return model.UpstreamTransportVolcengineV3
	case "openai_images":
		return model.UpstreamTransportOpenAIImages
	case "seedance", "video_generation", "seedance_video":
		return model.UpstreamTransportVideoGeneration
	default:
		return ""
	}
}

func (r *Router) selectUnified(ctx context.Context, modelName string, requirements RouteRequirements, options RouteOptions) (*RouteResult, error) {
	return r.unified.selectTransport(ctx, modelName, requirements, options)
}

func (r *Router) unifiedActive(ctx context.Context) bool {
	if r == nil || r.unified == nil {
		return false
	}
	active, err := r.unified.active(ctx)
	return err == nil && active
}
