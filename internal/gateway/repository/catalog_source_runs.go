package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/security"
)

type CatalogDiscoveryRun struct {
	ID, ReleaseID, ReleaseSourceID, SourceID         uint64
	StateVersion, CredentialID, CredentialBlobID     uint64
	ContractCode, BaseURL, ExternalGroup, LeaseOwner string
	RequestTimeoutMS                                 uint32
	LeaseExpiresAt                                   time.Time
}

func (s *Store) ClaimCatalogDiscoveryRun(ctx context.Context, owner string, lease time.Duration) (CatalogDiscoveryRun, bool, error) {
	if s == nil || !validCatalogSourceText(owner, 128) || lease < 15*time.Second || lease > 10*time.Minute {
		return CatalogDiscoveryRun{}, false, ErrInvalidInput
	}
	var out CatalogDiscoveryRun
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		now := nowUTC()
		var state string
		err := tx.QueryRowContext(ctx, `SELECT r.id,rs.release_id,rs.id,rs.catalog_source_id,r.state,r.state_version,r.credential_id,cv.encrypted_blob_id,p.contract_code,p.base_url,p.external_group,p.request_timeout_ms
FROM gw_control_plane_runs r
JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id
JOIN gw_catalog_sources src ON src.id=rs.catalog_source_id
JOIN gw_catalog_source_profiles p ON p.catalog_source_id=src.id
JOIN gw_credential_versions cv ON cv.id=r.auth_credential_version_id AND cv.credential_id=r.credential_id AND cv.encrypted_blob_id IS NOT NULL
WHERE r.action='catalog_discovery' AND src.status='active' AND
      (r.state='scheduled' OR (r.state='running' AND r.lease_expires_at<?))
ORDER BY CASE WHEN r.state='running' THEN 0 ELSE 1 END,r.id
LIMIT 1 FOR UPDATE`, now).Scan(&out.ID, &out.ReleaseID, &out.ReleaseSourceID, &out.SourceID, &state, &out.StateVersion,
			&out.CredentialID, &out.CredentialBlobID, &out.ContractCode, &out.BaseURL, &out.ExternalGroup, &out.RequestTimeoutMS)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		out.StateVersion++
		out.LeaseOwner = owner
		out.LeaseExpiresAt = now.Add(lease)
		result, err := tx.ExecContext(ctx, `UPDATE gw_control_plane_runs SET state='running',state_version=?,lease_owner=?,lease_expires_at=?,updated_at=? WHERE id=? AND state_version=? AND state=?`, out.StateVersion, owner, out.LeaseExpiresAt, now, out.ID, out.StateVersion-1, state)
		if err != nil {
			return err
		}
		ok, err := affected(result)
		if err != nil {
			return err
		}
		if !ok {
			return ErrConflict
		}
		reason := "worker_claimed"
		if state == "running" {
			reason = "lease_reclaimed"
		}
		return appendControlPlaneRunEvent(ctx, tx, out.ID, out.StateVersion, state, "running", reason, now)
	})
	if err != nil {
		return CatalogDiscoveryRun{}, false, err
	}
	return out, out.ID != 0, nil
}

func (s *Store) FinishCatalogDiscoveryRun(ctx context.Context, run CatalogDiscoveryRun, target, reason string) error {
	if run.ID == 0 || run.SourceID == 0 || run.StateVersion == 0 || run.LeaseOwner == "" ||
		target != "failed" && target != "completed" || !catalogIdentityPattern.MatchString(strings.ToLower(strings.TrimSpace(reason))) {
		return ErrInvalidInput
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		return s.finishCatalogDiscoveryRun(ctx, tx, run, target, strings.ToLower(strings.TrimSpace(reason)))
	})
}

func (s *Store) finishCatalogDiscoveryRun(ctx context.Context, tx *sql.Tx, run CatalogDiscoveryRun, target, reason string) error {
	var sourceID uint64
	var state, owner string
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT rs.catalog_source_id,r.state,r.state_version,r.lease_owner FROM gw_control_plane_runs r JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id WHERE r.id=? FOR UPDATE`, run.ID).Scan(&sourceID, &state, &version, &owner); err != nil {
		return err
	}
	if sourceID != run.SourceID || state != "running" || version != run.StateVersion || owner != run.LeaseOwner {
		return ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `UPDATE gw_control_plane_runs SET state=?,state_version=state_version+1,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND state='running' AND state_version=? AND lease_owner=?`, target, now, run.ID, run.StateVersion, run.LeaseOwner)
	if err != nil {
		return err
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	if err := appendControlPlaneRunEvent(ctx, tx, run.ID, run.StateVersion+1, "running", target, reason, now); err != nil {
		return err
	}
	return s.AdjustCatalogSourceRunCount(ctx, tx, run.SourceID, -1)
}

type CatalogDiscoveryItem struct {
	Code, Description, Tags, OwnerBy, PricingVersion string
	VendorID                                         *uint64
	ProviderQuotaType                                *uint8
	ModelPrice, ModelRatio, CompletionRatio          *string
	Groups, EndpointTypes                            []string
	SelectedGroupEnabled                             bool
}

type CatalogDiscoverySnapshotInput struct {
	Run                 CatalogDiscoveryRun
	ResponseHMAC        string
	ResultSchemaVersion uint32
	ObservedAt          time.Time
	Items               []CatalogDiscoveryItem
}

func (s *Store) SaveCatalogDiscoverySnapshot(ctx context.Context, in CatalogDiscoverySnapshotInput) (uint64, error) {
	if in.Run.ID == 0 || in.Run.ReleaseID == 0 || in.Run.ReleaseSourceID == 0 || in.Run.SourceID == 0 ||
		!validCatalogSourceContract(in.Run.ContractCode) || !validHexDigest(in.ResponseHMAC, 32) ||
		in.ResultSchemaVersion == 0 || in.ObservedAt.IsZero() || len(in.Items) == 0 || len(in.Items) > 10000 {
		return 0, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(in.Items))
	for _, item := range in.Items {
		if err := validateCatalogDiscoveryItem(in.Run, item); err != nil {
			return 0, err
		}
		if _, exists := seen[item.Code]; exists {
			return 0, ErrInvalidInput
		}
		seen[item.Code] = struct{}{}
	}
	var snapshotID uint64
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var existing uint64
		err := tx.QueryRowContext(ctx, `SELECT id FROM gw_catalog_discovery_snapshots WHERE control_plane_run_id=?`, in.Run.ID).Scan(&existing)
		if err == nil {
			snapshotID = existing
			return s.finishCatalogDiscoveryRun(ctx, tx, in.Run, "completed", "snapshot_recovered")
		}
		if err != sql.ErrNoRows {
			return err
		}
		var releaseID, releaseSourceID, sourceID uint64
		var state, owner, contract string
		var version uint64
		if err := tx.QueryRowContext(ctx, `SELECT rs.release_id,rs.id,rs.catalog_source_id,r.state,r.state_version,r.lease_owner,p.contract_code
FROM gw_control_plane_runs r JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id
JOIN gw_catalog_source_profiles p ON p.catalog_source_id=rs.catalog_source_id
WHERE r.id=? FOR UPDATE`, in.Run.ID).Scan(&releaseID, &releaseSourceID, &sourceID, &state, &version, &owner, &contract); err != nil {
			return err
		}
		if releaseID != in.Run.ReleaseID || releaseSourceID != in.Run.ReleaseSourceID || sourceID != in.Run.SourceID ||
			state != "running" || version != in.Run.StateVersion || owner != in.Run.LeaseOwner || contract != in.Run.ContractCode {
			return ErrConflict
		}
		now := nowUTC()
		result, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_discovery_snapshots(release_id,release_source_id,control_plane_run_id,contract_code,observed_at,response_hmac,result_schema_version,item_count,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, in.Run.ReleaseID, in.Run.ReleaseSourceID, in.Run.ID, in.Run.ContractCode, in.ObservedAt.UTC(), in.ResponseHMAC, in.ResultSchemaVersion, len(in.Items), now)
		if err != nil {
			return err
		}
		snapshotID, err = lastID(result)
		if err != nil {
			return err
		}
		for ordinal, item := range in.Items {
			result, err = tx.ExecContext(ctx, `INSERT INTO gw_catalog_discovery_items(snapshot_id,ordinal,model_code,description,tags,vendor_id,provider_quota_type,model_price,model_ratio,completion_ratio,owner_by,pricing_version,selected_group_enabled,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, snapshotID, ordinal, item.Code, item.Description, item.Tags, nullableUint64(item.VendorID), nullableUint8(item.ProviderQuotaType), nullableDecimal(item.ModelPrice), nullableDecimal(item.ModelRatio), nullableDecimal(item.CompletionRatio), item.OwnerBy, item.PricingVersion, item.SelectedGroupEnabled, now)
			if err != nil {
				return err
			}
			itemID, err := lastID(result)
			if err != nil {
				return err
			}
			for _, group := range item.Groups {
				if _, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_discovery_item_groups(snapshot_item_id,group_code) VALUES (?,?)`, itemID, group); err != nil {
					return err
				}
			}
			for _, endpoint := range item.EndpointTypes {
				if _, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_discovery_item_endpoints(snapshot_item_id,endpoint_type) VALUES (?,?)`, itemID, endpoint); err != nil {
					return err
				}
			}
			if in.Run.ContractCode == CatalogSourceAICostPricingV1 && item.SelectedGroupEnabled && item.ModelPrice != nil {
				if _, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_price_candidates(snapshot_item_id,created_at) VALUES (?,?)`, itemID, now); err != nil {
					return err
				}
			}
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO gw_catalog_discovery_review_events(snapshot_id,review_seq,decision,reviewer_user_id,reason_code,created_at) VALUES (?,1,'submitted',NULL,'discovered',?)`, snapshotID, now)
		if err != nil {
			return err
		}
		eventID, err := lastID(result)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_discovery_review_state(snapshot_id,state,state_version,latest_review_event_id) VALUES (?,'submitted',1,?)`, snapshotID, eventID); err != nil {
			return err
		}
		return s.finishCatalogDiscoveryRun(ctx, tx, in.Run, "completed", "snapshot_created")
	})
	if err != nil {
		return 0, fmt.Errorf("save catalog discovery snapshot: %w", err)
	}
	return snapshotID, nil
}

type CatalogSnapshotReviewInput struct {
	Decision        string `json:"decision"`
	ReasonCode      string `json:"reason_code"`
	ExpectedVersion uint64 `json:"expected_version"`
}

func (s *Store) ReviewCatalogDiscoverySnapshot(ctx context.Context, tx *sql.Tx, snapshotID uint64, in CatalogSnapshotReviewInput, actorID uint64) error {
	if tx == nil || snapshotID == 0 || actorID == 0 {
		return ErrInvalidInput
	}
	in.Decision = strings.ToLower(strings.TrimSpace(in.Decision))
	in.ReasonCode = strings.ToLower(strings.TrimSpace(in.ReasonCode))
	if in.ExpectedVersion == 0 || in.Decision != "accepted" && in.Decision != "rejected" || !catalogIdentityPattern.MatchString(in.ReasonCode) {
		return ErrInvalidInput
	}
	var state, responseHMAC string
	var version, releaseID, releaseSourceID, runID uint64
	var schemaVersion uint32
	if err := tx.QueryRowContext(ctx, `SELECT st.state,st.state_version,s.release_id,s.release_source_id,s.control_plane_run_id,s.response_hmac,s.result_schema_version
FROM gw_catalog_discovery_review_state st JOIN gw_catalog_discovery_snapshots s ON s.id=st.snapshot_id
JOIN gw_catalog_releases r ON r.id=s.release_id AND r.status='draft'
WHERE st.snapshot_id=? FOR UPDATE`, snapshotID).Scan(&state, &version, &releaseID, &releaseSourceID, &runID, &responseHMAC, &schemaVersion); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "submitted" || version != in.ExpectedVersion {
		return ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_discovery_review_events(snapshot_id,review_seq,decision,reviewer_user_id,reason_code,created_at) VALUES (?,?,?,?,?,?)`, snapshotID, version+1, in.Decision, actorID, in.ReasonCode, now)
	if err != nil {
		return err
	}
	eventID, err := lastID(result)
	if err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `UPDATE gw_catalog_discovery_review_state SET state=?,state_version=?,latest_review_event_id=? WHERE snapshot_id=? AND state='submitted' AND state_version=?`, in.Decision, version+1, eventID, snapshotID, version)
	if err != nil {
		return err
	}
	ok, err := affected(result)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	if in.Decision == "accepted" {
		importID, err := s.CreateCatalogImport(ctx, tx, CatalogImportInput{ReleaseID: releaseID, ReleaseSourceID: releaseSourceID, ControlPlaneRunID: runID, SnapshotHMAC: responseHMAC, ResultSchemaVersion: schemaVersion})
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_import_snapshots(catalog_import_id,snapshot_id,created_at) VALUES (?,?,?)`, importID, snapshotID, now); err != nil {
			return err
		}
	}
	return recordCatalogAdminChange(ctx, tx, actorID, "unified.catalog_snapshot.review", "catalog_discovery_snapshot", snapshotID, in)
}

type CatalogPriceCandidateReviewInput struct {
	Decision        string `json:"decision"`
	UnitCode        string `json:"unit_code"`
	CurrencyCode    string `json:"currency_code"`
	CurrencyVersion uint32 `json:"currency_version"`
	ReasonCode      string `json:"reason_code"`
	HMACKey         []byte `json:"-"`
}

func (s *Store) ReviewCatalogPriceCandidate(ctx context.Context, tx *sql.Tx, candidateID uint64, in CatalogPriceCandidateReviewInput, actorID uint64) (uint64, error) {
	if tx == nil || candidateID == 0 || actorID == 0 {
		return 0, ErrInvalidInput
	}
	in.Decision = strings.ToLower(strings.TrimSpace(in.Decision))
	in.UnitCode = strings.ToLower(strings.TrimSpace(in.UnitCode))
	in.CurrencyCode = strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	in.ReasonCode = strings.ToLower(strings.TrimSpace(in.ReasonCode))
	if !catalogIdentityPattern.MatchString(in.ReasonCode) || in.Decision != "confirmed" && in.Decision != "dismissed" {
		return 0, ErrInvalidInput
	}
	if in.Decision == "confirmed" && (!catalogIdentityPattern.MatchString(in.UnitCode) || !catalogIdentityPattern.MatchString(in.CurrencyCode) || in.CurrencyVersion == 0 || len(in.HMACKey) != security.KeySize) ||
		in.Decision == "dismissed" && (in.UnitCode != "" || in.CurrencyCode != "" || in.CurrencyVersion != 0 || len(in.HMACKey) != 0) {
		return 0, ErrInvalidInput
	}
	var modelCode, price string
	var observedAt time.Time
	var sourceID, snapshotID uint64
	err := tx.QueryRowContext(ctx, `SELECT i.model_code,i.model_price,s.observed_at,rs.catalog_source_id,s.id
FROM gw_catalog_price_candidates c JOIN gw_catalog_discovery_items i ON i.id=c.snapshot_item_id
JOIN gw_catalog_discovery_snapshots s ON s.id=i.snapshot_id
JOIN gw_catalog_discovery_review_state st ON st.snapshot_id=s.id AND st.state='accepted'
JOIN gw_catalog_release_sources rs ON rs.id=s.release_source_id
LEFT JOIN gw_catalog_price_candidate_reviews rv ON rv.candidate_id=c.id
WHERE c.id=? AND rv.id IS NULL FOR UPDATE`, candidateID).Scan(&modelCode, &price, &observedAt, &sourceID, &snapshotID)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	var evidenceID uint64
	if in.Decision == "confirmed" {
		reference := fmt.Sprintf("catalog-source:%d/snapshot:%d/model:%s", sourceID, snapshotID, modelCode)
		evidence := RateEvidenceInput{SourceType: "catalog_discovery", AuthorityLevel: "provider_api", SourceReference: reference, ObservedAt: observedAt, UnitCode: in.UnitCode, UnitPrice: price, CurrencyCode: in.CurrencyCode, CurrencyVersion: in.CurrencyVersion}
		evidence.FactHMAC, err = RateEvidenceFactHMAC(evidence, in.HMACKey)
		if err != nil {
			return 0, err
		}
		evidenceID, err = s.CreateRateEvidence(ctx, tx, evidence, actorID)
		if err != nil {
			return 0, err
		}
	}
	now := nowUTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_catalog_price_candidate_reviews(candidate_id,decision,unit_code,currency_code,currency_version,rate_evidence_id,reviewer_user_id,reason_code,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, candidateID, in.Decision, emptyAsNull(in.UnitCode), emptyAsNull(in.CurrencyCode), nullableUint32(in.CurrencyVersion), nullablePositiveID(evidenceID), actorID, in.ReasonCode, now)
	if err != nil {
		return 0, err
	}
	if err := recordCatalogAdminChange(ctx, tx, actorID, "unified.catalog_price_candidate.review", "catalog_price_candidate", candidateID, ginSafeMetadata{"decision": in.Decision, "unit_code": in.UnitCode, "currency_code": in.CurrencyCode, "currency_version": in.CurrencyVersion, "rate_evidence_id": evidenceID}); err != nil {
		return 0, err
	}
	return evidenceID, nil
}

func nullableUint8(value *uint8) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableDecimal(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullablePositiveID(value uint64) any {
	if value == 0 {
		return nil
	}
	return value
}

func validateCatalogDiscoveryItem(run CatalogDiscoveryRun, item CatalogDiscoveryItem) error {
	if !catalogIdentityPattern.MatchString(item.Code) || !validOptionalCatalogSourceText(item.Description, 1000) ||
		!validOptionalCatalogSourceText(item.Tags, 512) || !validOptionalCatalogSourceText(item.OwnerBy, 128) ||
		!validOptionalCatalogSourceText(item.PricingVersion, 128) || len(item.Groups) == 0 || len(item.Groups) > 256 || len(item.EndpointTypes) > 256 {
		return ErrInvalidInput
	}
	if item.ProviderQuotaType != nil && *item.ProviderQuotaType > 1 {
		return ErrInvalidInput
	}
	for _, value := range []*string{item.ModelPrice, item.ModelRatio, item.CompletionRatio} {
		if value == nil {
			continue
		}
		amount, err := billing.ParseAmount(*value, 18, true)
		if err != nil || amount.String() != *value {
			return ErrInvalidInput
		}
	}
	groups := make(map[string]struct{}, len(item.Groups))
	for _, group := range item.Groups {
		if !validCatalogSourceText(group, 128) {
			return ErrInvalidInput
		}
		if _, exists := groups[group]; exists {
			return ErrInvalidInput
		}
		groups[group] = struct{}{}
	}
	endpoints := make(map[string]struct{}, len(item.EndpointTypes))
	for _, endpoint := range item.EndpointTypes {
		if !catalogIdentityPattern.MatchString(endpoint) || utf8.RuneCountInString(endpoint) > 64 {
			return ErrInvalidInput
		}
		if _, exists := endpoints[endpoint]; exists {
			return ErrInvalidInput
		}
		endpoints[endpoint] = struct{}{}
	}
	wantSelected := run.ContractCode == CatalogSourceAICostModelsV1
	if run.ContractCode == CatalogSourceAICostPricingV1 {
		_, wantSelected = groups[run.ExternalGroup]
	}
	if item.SelectedGroupEnabled != wantSelected {
		return ErrInvalidInput
	}
	return nil
}

func validOptionalCatalogSourceText(value string, maxRunes int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes && !strings.ContainsAny(value, "\x00\r\n\t")
}
