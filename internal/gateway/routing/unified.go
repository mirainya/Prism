package routing

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

const unifiedKeyMarker uint = 1 << 31

type unifiedSelector struct{}

type unifiedCandidate struct {
	AbilityID, CredentialID, ChannelID, PoolID, VersionID uint64
	VendorModel, Protocol, BaseURL, TransportCode         string
	Priority, Weight                                      int
	RequestPath                                           string
	Nonce, Ciphertext, WrapNonce, WrappedDEK              []byte
	BlobID                                                uint64
	KEKVersion                                            uint32
	InputPrice, OutputPrice                               decimal.Decimal
	Capabilities                                          []byte
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
	return releaseID.Valid && releaseID.Int64 > 0, nil
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
	rows, err := db.QueryContext(ctx, `
SELECT mo.id, ch.id, o.credential_pool_id, c.id, cv.id, eb.id,
       p.vendor_model, ch.protocol, ct.base_url, ct.transport_code,
       r.priority, r.weight, ct.request_path,
       eb.nonce, eb.ciphertext, w.wrap_nonce, w.wrapped_dek, w.kek_version,
       COALESCE(sr.unit_price,0), COALESCE(sr.unit_price,0), cm.capability_tags
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
	for rows.Next() {
		var c unifiedCandidate
		var inputPrice, outputPrice string
		if err := rows.Scan(&c.AbilityID, &c.ChannelID, &c.PoolID, &c.CredentialID, &c.VersionID, &c.BlobID, &c.VendorModel, &c.Protocol, &c.BaseURL, &c.TransportCode, &c.Priority, &c.Weight, &c.RequestPath, &c.Nonce, &c.Ciphertext, &c.WrapNonce, &c.WrappedDEK, &c.KEKVersion, &inputPrice, &outputPrice, &c.Capabilities); err != nil {
			return nil, err
		}
		c.InputPrice, _ = decimal.NewFromString(inputPrice)
		c.OutputPrice, _ = decimal.NewFromString(outputPrice)
		transport := unifiedTransport(c.Protocol)
		if !transportAllowed(transport, options.AllowedTransports) || !supportsSemanticRequirements(semanticCapabilities(c.Capabilities), requirements) {
			continue
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, ErrModelNotFound
	}
	chosen := candidates[rand.Intn(len(candidates))]
	apiKey, err := decryptUnifiedCredential(chosen)
	if err != nil {
		return nil, err
	}
	return &RouteResult{
		AbilityID: uint(chosen.AbilityID), KeyID: unifiedKeyMarker | uint(chosen.CredentialID), ChannelID: uint(chosen.ChannelID),
		Protocol: model.Protocol(chosen.Protocol), BaseURL: chosen.BaseURL, APIKey: apiKey,
		VendorModel: chosen.VendorModel, ModelName: modelName, Capabilities: semanticCapabilities(chosen.Capabilities),
		Transport: unifiedTransport(chosen.Protocol), TransportConfig: map[string]any{"request_path": chosen.RequestPath},
		InputPrice: chosen.InputPrice, OutputPrice: chosen.OutputPrice,
	}, nil
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
