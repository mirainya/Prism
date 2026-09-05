package routing

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

const unifiedKeyMarker uint = 1 << 31

type unifiedSelector struct{}

type unifiedCandidate struct {
	AbilityID, CredentialID, ChannelID, PoolID, VersionID, OfferingID uint64
	VendorModel, Protocol, BaseURL, TransportCode                     string
	Priority, Weight                                                  int64
	RequestPath                                                       string
	Nonce, Ciphertext, WrapNonce, WrappedDEK                          []byte
	BlobID                                                            uint64
	KEKVersion                                                        uint32
	InputPrice, OutputPrice                                           decimal.Decimal
	UnitCode                                                          string
	Capabilities                                                      []byte
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
	// A pointer update alone is not sufficient to enable traffic. Require an
	// active deployment generation whose every member has a fresh, matching
	// catalog and crypto readiness record.
	var generationID uint64
	if err := db.QueryRowContext(ctx, `SELECT id FROM gw_deployment_generations WHERE status='active' ORDER BY id DESC LIMIT 1`).Scan(&generationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var members, ready uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_deployment_members WHERE deployment_generation_id=?`, generationID).Scan(&members); err != nil || members == 0 {
		return false, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_readiness r JOIN gw_deployment_members m ON m.id=r.deployment_member_id JOIN gw_catalog_releases c ON c.id=r.release_id WHERE r.deployment_generation_id=? AND r.release_id=? AND r.status='ready' AND r.expires_at>UTC_TIMESTAMP(3) AND r.adapter_digest<>'' AND r.content_hash=c.content_hash AND r.semantic_digest=c.semantic_digest`, generationID, releaseID.Int64).Scan(&ready); err != nil || ready != members {
		return false, err
	}
	var cryptoMembers, badKeys uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT deployment_member_id),COUNT(CASE WHEN status<>'ready' OR expires_at<=UTC_TIMESTAMP(3) THEN 1 END) FROM crypto_key_readiness WHERE deployment_generation_id=?`, generationID).Scan(&cryptoMembers, &badKeys); err != nil {
		return false, err
	}
	return cryptoMembers == members && badKeys == 0, nil
}

func (s *unifiedSelector) selectTransport(modelName string, requirements RouteRequirements, options RouteOptions) (*RouteResult, error) {
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	var activeRelease int64
	if err := db.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&activeRelease); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoRoute
		}
		return nil, err
	}
	if activeRelease == 0 {
		return nil, ErrNoRoute
	}
	var declared uint64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*)
FROM gw_catalog_releases rel
JOIN gw_catalog_models cm ON cm.release_id=rel.id
JOIN gw_catalog_model_names cmn ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id
JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
WHERE rel.id=? AND rel.status='published' AND mn.api_name=?`, activeRelease, modelName).Scan(&declared); err != nil {
		return nil, err
	}
	if declared == 0 {
		return nil, ErrModelNotFound
	}
	rows, err := db.QueryContext(ctx, `
SELECT mo.id, ch.id, o.credential_pool_id, c.id, cv.id, o.id, eb.id,
       p.vendor_model, ch.protocol, ct.base_url, ct.transport_code,
       r.priority, r.weight, ct.request_path,
       eb.nonce, eb.ciphertext, w.wrap_nonce, w.wrapped_dek, w.kek_version,
	       COALESCE(sr.unit_code,''), COALESCE(sr.unit_price,0), cm.capability_tags
FROM gw_catalog_releases rel
JOIN gw_catalog_models cm ON cm.release_id=rel.id
JOIN gw_catalog_model_names cmn ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id
JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
JOIN gw_model_operations mo ON mo.release_id=cm.release_id AND mo.catalog_model_id=cm.id
JOIN gw_skus sku ON sku.release_id=mo.release_id AND sku.model_operation_id=mo.id
JOIN gw_routes r ON r.release_id=sku.release_id AND r.sku_id=sku.id
JOIN gw_offerings o ON o.release_id=r.release_id AND o.id=r.offering_id
JOIN gw_product_transports pt ON pt.release_id=o.release_id AND pt.id=o.product_transport_id
JOIN gw_products p ON p.release_id=pt.release_id AND p.id=pt.product_id
JOIN gw_channel_transports ct ON ct.release_id=pt.release_id AND ct.id=pt.channel_transport_id
JOIN gateway_channels ch ON ch.id=p.channel_id AND ch.status='active'
JOIN gw_offering_runtime_state ors ON ors.release_id=o.release_id AND ors.offering_id=o.id AND ors.state='active'
JOIN gw_credentials c ON c.channel_id=ch.id AND c.credential_pool_id=o.credential_pool_id AND c.status='active'
JOIN gw_credential_purpose_grants g ON g.credential_id=c.id AND g.purpose='execution' AND g.status='active'
JOIN gw_credential_versions cv ON cv.credential_id=c.id AND cv.id=c.current_version_id AND cv.status='active'
JOIN encrypted_blobs eb ON eb.id=cv.encrypted_blob_id
JOIN encrypted_blob_key_wraps w ON w.encrypted_blob_id=eb.id AND w.keyring_id=eb.keyring_id
LEFT JOIN gw_sell_rates sr ON sr.release_id=sku.release_id AND sr.sku_id=sku.id
WHERE rel.id=? AND rel.status='published' AND mn.api_name=?
ORDER BY r.priority DESC, r.id`, activeRelease, modelName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []unifiedCandidate
	capabilityMatch, transportMatch := false, false
	for rows.Next() {
		var c unifiedCandidate
		var unitPrice string
		if err := rows.Scan(&c.AbilityID, &c.ChannelID, &c.PoolID, &c.CredentialID, &c.VersionID, &c.OfferingID, &c.BlobID, &c.VendorModel, &c.Protocol, &c.BaseURL, &c.TransportCode, &c.Priority, &c.Weight, &c.RequestPath, &c.Nonce, &c.Ciphertext, &c.WrapNonce, &c.WrappedDEK, &c.KEKVersion, &c.UnitCode, &unitPrice, &c.Capabilities); err != nil {
			return nil, err
		}
		price, _ := decimal.NewFromString(unitPrice)
		switch strings.ToLower(c.UnitCode) {
		case "request", "second", "image", "video", "megapixel":
			c.InputPrice, c.OutputPrice = price, decimal.Zero
		case "input_token", "input_tokens":
			c.InputPrice = price
		case "output_token", "output_tokens":
			c.OutputPrice = price
		}
		transport := unifiedTransport(c.Protocol)
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
	if len(candidates) == 0 {
		if !capabilityMatch {
			return nil, ErrCapabilityUnavailable
		}
		if !transportMatch {
			return nil, ErrNoCompatibleTransport
		}
		return nil, ErrNoRoute
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
	chosen := weightedUnifiedCandidate(top)
	apiKey, err := decryptUnifiedCredential(chosen)
	if err != nil {
		return nil, err
	}
	return &RouteResult{
		AbilityID: uint(chosen.AbilityID), KeyID: unifiedKeyMarker | uint(chosen.CredentialID), ChannelID: uint(chosen.ChannelID),
		Protocol: model.Protocol(chosen.Protocol), BaseURL: chosen.BaseURL, APIKey: apiKey,
		VendorModel: chosen.VendorModel, ModelName: modelName, Capabilities: semanticCapabilities(chosen.Capabilities),
		Transport: unifiedTransport(chosen.Protocol), TransportConfig: map[string]any{"request_path": chosen.RequestPath},
		InputPrice: chosen.InputPrice, OutputPrice: chosen.OutputPrice, PriceMode: unifiedPriceMode(chosen.UnitCode),
	}, nil
}

func unifiedPriceMode(unitCode string) string {
	switch strings.ToLower(strings.TrimSpace(unitCode)) {
	case "request", "second", "image", "video", "megapixel":
		return "request"
	default:
		return "token"
	}
}

func excludedUnifiedAttempt(attempts []TransportAttempt, credentialID uint64, transport model.UpstreamTransport) bool {
	for _, attempt := range attempts {
		if uint64(attempt.KeyID) == credentialID && attempt.Transport == transport {
			return true
		}
	}
	return false
}

func weightedUnifiedCandidate(candidates []unifiedCandidate) unifiedCandidate {
	if len(candidates) == 1 {
		return candidates[0]
	}
	var total int64
	for _, candidate := range candidates {
		if candidate.Weight > 0 {
			total += candidate.Weight
		}
	}
	if total <= 0 {
		return candidates[rand.Intn(len(candidates))]
	}
	pick := rand.Int63n(total)
	for _, candidate := range candidates {
		if candidate.Weight <= 0 {
			continue
		}
		if pick < candidate.Weight {
			return candidate
		}
		pick -= candidate.Weight
	}
	return candidates[len(candidates)-1]
}

func containsUint(values []uint, target uint) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func decryptUnifiedCredential(c unifiedCandidate) (string, error) {
	value := os.Getenv("PRISM_GATEWAY_KEK_B64")
	if value == "" {
		return "", errors.New("unified gateway keyring is not configured")
	}
	kek, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(kek) != security.KeySize {
		return "", errors.New("unified gateway keyring has invalid key material")
	}
	aad, err := security.CanonicalAAD(c.BlobID, "credential", 1, []byte(fmt.Sprintf("credential:%d", c.CredentialID)))
	if err != nil {
		return "", err
	}
	plaintext, err := (security.Envelope{Version: security.EnvelopeVersion, AADVersion: 1, KEKVersion: c.KEKVersion, Nonce: c.Nonce, Ciphertext: c.Ciphertext, WrapNonce: c.WrapNonce, WrappedDEK: c.WrappedDEK}).Open(aad, kek)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func unifiedTransport(protocol string) model.UpstreamTransport {
	switch strings.ToLower(protocol) {
	case "anthropic":
		return model.UpstreamTransportAnthropic
	case "google":
		return model.UpstreamTransportGoogle
	case "volcengine":
		return model.UpstreamTransportVolcengineV3
	default:
		return model.UpstreamTransportOpenAIChat
	}
}

func (r *Router) selectUnified(modelName string, requirements RouteRequirements, options RouteOptions) (*RouteResult, error) {
	return r.unified.selectTransport(modelName, requirements, options)
}

func (r *Router) unifiedActive(ctx context.Context) bool {
	if r == nil || r.unified == nil {
		return false
	}
	active, err := r.unified.active(ctx)
	return err == nil && active
}

func (r *Router) releaseUnified(keyID uint) bool { return keyID&unifiedKeyMarker != 0 }
