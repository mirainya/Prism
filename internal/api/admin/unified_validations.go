package admin

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

type unifiedValidationRequest struct {
	State      string          `json:"state"`
	ValidUntil *time.Time      `json:"valid_until,omitempty"`
	Evidence   json.RawMessage `json:"evidence"`
}

// RecordUnifiedOfferingValidation records a bounded operator-observed result.
// It does not call an arbitrary upstream URL: the evidence is authenticated,
// the exact credential version and fingerprints are pinned by ControlPlaneRun.
func RecordUnifiedOfferingValidation(c *gin.Context) {
	offeringID, err := resp.ParseUintParam(c, "offering_id")
	if err != nil {
		return
	}
	credentialID, err := resp.ParseUintParam(c, "credential_id")
	if err != nil {
		return
	}
	kind := strings.TrimSpace(c.Param("kind"))
	if kind != "entitlement" && kind != "commercial" && kind != "all" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	var in unifiedValidationRequest
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.State = strings.ToLower(strings.TrimSpace(in.State))
	evidence, err := canonicalValidationEvidence(in.Evidence)
	if err != nil || !validValidationRequest(in, time.Now().UTC()) {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	hmacKey, err := adminGatewayKey("PRISM_GATEWAY_HMAC_B64")
	if err != nil {
		unifiedChannelError(c, repository.ErrCredentialEncryptionUnavailable)
		return
	}
	defer clear(hmacKey)
	payload, err := json.Marshal(struct {
		Kind, State  string
		OfferingID   uint64
		CredentialID uint64
		ValidUntil   *time.Time
		Evidence     json.RawMessage
	}{kind, in.State, uint64(offeringID), uint64(credentialID), in.ValidUntil, evidence})
	if err != nil {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	digest := security.DomainDigest(hmacKey, "gateway-validation-evidence-v1", payload)
	evidenceHMAC := hex.EncodeToString(digest[:])

	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		ctx := c.Request.Context()
		var poolID uint64
		var releaseStatus string
		var entitlementFingerprint, commercialFingerprint string
		if err := tx.QueryRowContext(ctx, `SELECT o.credential_pool_id,o.entitlement_fingerprint,o.commercial_fingerprint,r.status FROM gw_offerings o JOIN gw_catalog_releases r ON r.id=o.release_id WHERE o.id=? FOR SHARE`, offeringID).Scan(&poolID, &entitlementFingerprint, &commercialFingerprint, &releaseStatus); err != nil {
			return 0, err
		}
		if releaseStatus != "published" {
			return 0, repository.ErrConflict
		}
		var credentialPoolID, versionID, configVersion uint64
		var credentialState, versionState string
		if err := tx.QueryRowContext(ctx, `SELECT c.credential_pool_id,c.current_version_id,c.config_version,c.status,v.status FROM gw_credentials c JOIN gw_credential_versions v ON v.id=c.current_version_id AND v.credential_id=c.id WHERE c.id=? FOR SHARE`, credentialID).Scan(&credentialPoolID, &versionID, &configVersion, &credentialState, &versionState); err != nil {
			return 0, err
		}
		if credentialPoolID != poolID || credentialState != "active" || versionState != "active" {
			return 0, repository.ErrConflict
		}
		var grantID uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_credential_purpose_grants WHERE credential_id=? AND purpose='catalog_discovery' AND status='active' FOR SHARE`, credentialID).Scan(&grantID); err != nil {
			return 0, err
		}
		targetCredentialID := uint64(credentialID)
		complete := func(action, fingerprint string, targetOfferingID *uint64) (uint64, error) {
			runID, err := store.CreateControlPlaneRun(ctx, tx, repository.ControlPlaneRunInput{
				Action: action, CredentialID: &targetCredentialID, OfferingID: targetOfferingID,
				TargetFingerprint: fingerprint, TargetConfigVersion: &configVersion,
				AuthCredentialVersionID: versionID, AuthPurposeGrantID: grantID,
			})
			if err != nil {
				return 0, err
			}
			if err := store.FinishControlPlaneRun(ctx, tx, runID, "scheduled", "running", "operator_evidence_received"); err != nil {
				return 0, err
			}
			result := repository.ValidationResultInput{ControlPlaneRunID: runID, ActorID: actor, State: in.State, EvidenceHMAC: evidenceHMAC, ValidUntil: in.ValidUntil}
			if action == "commercial_check" {
				return store.CompleteCommercialValidation(ctx, tx, result)
			}
			return store.CompleteCredentialValidation(ctx, tx, result)
		}
		if kind == "entitlement" {
			return complete("entitlement_probe", entitlementFingerprint, nil)
		}
		targetOfferingID := uint64(offeringID)
		if kind == "commercial" {
			return complete("commercial_check", commercialFingerprint, &targetOfferingID)
		}
		if _, err := complete("entitlement_probe", entitlementFingerprint, nil); err != nil {
			return 0, err
		}
		return complete("commercial_check", commercialFingerprint, &targetOfferingID)
	})
}

func validValidationRequest(in unifiedValidationRequest, now time.Time) bool {
	switch in.State {
	case "valid":
		return in.ValidUntil != nil && in.ValidUntil.After(now) && !in.ValidUntil.After(now.Add(31*24*time.Hour))
	case "drift", "unknown", "expired":
		return in.ValidUntil == nil
	default:
		return false
	}
}

func canonicalValidationEvidence(raw json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if len(raw) == 0 {
		return nil, repository.ErrInvalidInput
	}
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, repository.ErrInvalidInput
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, repository.ErrInvalidInput
	}
	return json.Marshal(value)
}
