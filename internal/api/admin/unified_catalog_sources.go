package admin

import (
	"database/sql"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/catalogsource"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

var catalogReasonCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

func UnifiedCatalogSourceOptions(c *gin.Context) {
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	ctx := c.Request.Context()
	channelRows, err := db.QueryContext(ctx, `SELECT id,channel_code,display_name FROM gateway_channels WHERE status='active' ORDER BY display_name,id`)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	channels := make([]gin.H, 0)
	for channelRows.Next() {
		var id uint64
		var code, name string
		if err := channelRows.Scan(&id, &code, &name); err != nil {
			channelRows.Close()
			unifiedChannelError(c, err)
			return
		}
		channels = append(channels, gin.H{"id": id, "code": code, "name": name})
	}
	if err := channelRows.Err(); err != nil {
		channelRows.Close()
		unifiedChannelError(c, err)
		return
	}
	if err := channelRows.Close(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	credentialRows, err := db.QueryContext(ctx, `SELECT DISTINCT c.id,c.channel_id,c.credential_code
FROM gw_credentials c
JOIN gw_credential_versions v ON v.id=c.current_version_id AND v.credential_id=c.id AND v.status='active'
JOIN gw_credential_purpose_grants g ON g.credential_id=c.id AND g.purpose='catalog_discovery' AND g.status='active'
WHERE c.status='active' ORDER BY c.channel_id,c.credential_code,c.id`)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer credentialRows.Close()
	credentials := make([]gin.H, 0)
	for credentialRows.Next() {
		var id, channelID uint64
		var code string
		if err := credentialRows.Scan(&id, &channelID, &code); err != nil {
			unifiedChannelError(c, err)
			return
		}
		credentials = append(credentials, gin.H{"id": id, "channel_id": channelID, "code": code})
	}
	if err := credentialRows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	providers, err := catalogsource.DefaultProviderRegistry()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	contracts := make([]gin.H, 0)
	for _, contract := range providers.Contracts() {
		contracts = append(contracts, gin.H{"code": contract.Code, "name": contract.Name})
	}
	resp.Success(c, gin.H{
		"channels": channels, "credentials": credentials,
		"contracts": contracts,
	})
}

func ListUnifiedCatalogSources(c *gin.Context) {
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	status := strings.ToLower(strings.TrimSpace(c.Query("status")))
	if status != "" && status != "active" && status != "draining" && status != "disabled" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	ctx := c.Request.Context()
	where := ` WHERE (?='' OR s.status=?)`
	args := []any{status, status}
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_sources s`+where, args...).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT s.id,s.channel_id,ch.channel_code,ch.display_name,s.source_code,p.contract_code,
	p.base_url,p.external_group,p.request_timeout_ms,s.credential_id,cr.credential_code,s.status,s.state_version,
	s.nonterminal_run_count,s.created_at,s.updated_at,
	(SELECT MAX(r.id) FROM gw_control_plane_runs r JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id WHERE rs.catalog_source_id=s.id) AS last_run_id,
	(SELECT r.state FROM gw_control_plane_runs r JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id WHERE rs.catalog_source_id=s.id ORDER BY r.id DESC LIMIT 1) AS last_run_state,
	(SELECT e.reason_code FROM gw_control_plane_run_events e JOIN gw_control_plane_runs r ON r.id=e.control_plane_run_id JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id WHERE rs.catalog_source_id=s.id ORDER BY r.id DESC,e.event_seq DESC LIMIT 1) AS last_run_reason
FROM gw_catalog_sources s
JOIN gateway_channels ch ON ch.id=s.channel_id
JOIN gw_credentials cr ON cr.id=s.credential_id
JOIN gw_catalog_source_profiles p ON p.catalog_source_id=s.id`+where+`
ORDER BY s.id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, channelID, credentialID, stateVersion, running uint64
		var timeout uint32
		var channelCode, channelName, sourceCode, contractCode, baseURL, group, credentialCode, statusValue string
		var createdAt, updatedAt sql.NullTime
		var lastRunID sql.NullInt64
		var lastRunState, lastRunReason sql.NullString
		if err := rows.Scan(&id, &channelID, &channelCode, &channelName, &sourceCode, &contractCode, &baseURL, &group,
			&timeout, &credentialID, &credentialCode, &statusValue, &stateVersion, &running, &createdAt, &updatedAt,
			&lastRunID, &lastRunState, &lastRunReason); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{
			"id": id, "channel_id": channelID, "channel_code": channelCode, "channel_name": channelName,
			"source_code": sourceCode, "contract_code": contractCode, "base_url": baseURL, "external_group": group,
			"request_timeout_ms": timeout, "credential_id": credentialID, "credential_code": credentialCode,
			"status": statusValue, "state_version": stateVersion, "nonterminal_run_count": running,
			"last_run_id": nullableInt(lastRunID), "last_run_state": nullableString(lastRunState),
			"last_run_reason": nullableString(lastRunReason),
			"created_at":      nullableTime(createdAt), "updated_at": nullableTime(updatedAt),
		})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func CreateUnifiedCatalogSource(c *gin.Context) {
	var in repository.CatalogSourceInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Normalize()
	providers, registryErr := catalogsource.DefaultProviderRegistry()
	if registryErr != nil {
		unifiedChannelError(c, registryErr)
		return
	}
	_, _, providerFound := providers.Provider(in.ContractCode)
	descriptor, found := adapter.DescriptorFor(in.ContractCode, 1)
	if !providerFound || !found || descriptor.Protocol != "catalog_discovery" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	in.Adapter = repository.CatalogAdapterInput{
		Code: descriptor.Code, Version: descriptor.Version, Protocol: descriptor.Protocol,
		ImplementationDigest: descriptor.ImplementationDigest, MinimumSemanticVersion: descriptor.MinimumSemanticVersion,
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateManagedCatalogSource(c.Request.Context(), tx, in, actor)
	})
}

func TransitionUnifiedCatalogSource(c *gin.Context) {
	sourceID, err := resp.ParseUintParam(c, "source_id")
	if err != nil {
		return
	}
	var in struct {
		Status          string `json:"status"`
		ExpectedVersion uint64 `json:"expected_version"`
		ReasonCode      string `json:"reason_code"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Status = strings.ToLower(strings.TrimSpace(in.Status))
	in.ReasonCode = strings.ToLower(strings.TrimSpace(in.ReasonCode))
	if in.ExpectedVersion == 0 || !catalogReasonCodePattern.MatchString(in.ReasonCode) {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		var current string
		if err := tx.QueryRowContext(c.Request.Context(), `SELECT status FROM gw_catalog_sources WHERE id=?`, sourceID).Scan(&current); err != nil {
			return 0, err
		}
		if !(current == "active" && in.Status == "draining" || current == "draining" && in.Status == "disabled") {
			return 0, repository.ErrConflict
		}
		return uint64(sourceID), store.TransitionManagedCatalogSource(c.Request.Context(), tx, uint64(sourceID), in.ExpectedVersion, current, in.Status, in.ReasonCode, actor)
	})
}

func ScheduleUnifiedCatalogDiscovery(c *gin.Context) {
	releaseID, sourceID, ok := catalogResourceIDs(c, "source_id")
	if !ok {
		return
	}
	var in struct{}
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.ScheduleCatalogDiscovery(c.Request.Context(), tx, releaseID, sourceID, actor)
	})
}

func ListUnifiedCatalogDiscoveries(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
		return
	}
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_control_plane_runs r JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id WHERE rs.release_id=? AND r.action='catalog_discovery'`, releaseID).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT r.id,rs.id,rs.catalog_source_id,s.source_code,p.contract_code,p.external_group,
	r.state,r.state_version,
	(SELECT COUNT(*) FROM gw_control_plane_run_events e WHERE e.control_plane_run_id=r.id AND e.new_state='running') AS attempt_count,
	(SELECT e.reason_code FROM gw_control_plane_run_events e WHERE e.control_plane_run_id=r.id ORDER BY e.event_seq DESC LIMIT 1) AS last_reason_code,
	r.created_at,r.updated_at,sn.id,sn.item_count,sn.observed_at,sn.response_hmac,
	rv.state,rv.state_version
FROM gw_control_plane_runs r
JOIN gw_catalog_release_sources rs ON rs.id=r.catalog_release_source_id
JOIN gw_catalog_sources s ON s.id=rs.catalog_source_id
JOIN gw_catalog_source_profiles p ON p.catalog_source_id=s.id
LEFT JOIN gw_catalog_discovery_snapshots sn ON sn.control_plane_run_id=r.id
LEFT JOIN gw_catalog_discovery_review_state rv ON rv.snapshot_id=sn.id
WHERE rs.release_id=? AND r.action='catalog_discovery'
ORDER BY r.id DESC LIMIT ? OFFSET ?`, releaseID, size, (page-1)*size)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, releaseSourceID, sourceID, stateVersion, attemptCount uint64
		var sourceCode, contractCode, group, state, lastReasonCode string
		var createdAt, updatedAt sql.NullTime
		var snapshotID, itemCount, reviewVersion sql.NullInt64
		var observedAt sql.NullTime
		var responseHMAC, reviewState sql.NullString
		if err := rows.Scan(&id, &releaseSourceID, &sourceID, &sourceCode, &contractCode, &group, &state, &stateVersion,
			&attemptCount, &lastReasonCode,
			&createdAt, &updatedAt, &snapshotID, &itemCount, &observedAt, &responseHMAC, &reviewState, &reviewVersion); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{
			"id": id, "release_source_id": releaseSourceID, "source_id": sourceID, "source_code": sourceCode,
			"contract_code": contractCode, "external_group": group, "state": state, "state_version": stateVersion,
			"attempt_count": attemptCount, "last_reason_code": lastReasonCode,
			"snapshot_id": nullableInt(snapshotID), "item_count": nullableInt(itemCount), "observed_at": nullableTime(observedAt),
			"response_hmac": nullableString(responseHMAC), "review_state": nullableString(reviewState),
			"review_version": nullableInt(reviewVersion), "created_at": nullableTime(createdAt), "updated_at": nullableTime(updatedAt),
		})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func ListUnifiedCatalogDiscoveryItems(c *gin.Context) {
	snapshotID, err := resp.ParseUintParam(c, "snapshot_id")
	if err != nil {
		return
	}
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_discovery_items WHERE snapshot_id=?`, snapshotID).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT i.id,i.ordinal,i.model_code,i.description,i.tags,i.vendor_id,i.provider_quota_type,
	i.model_price,i.model_ratio,i.completion_ratio,i.owner_by,i.pricing_version,i.selected_group_enabled,
	COALESCE(GROUP_CONCAT(DISTINCT g.group_code ORDER BY g.group_code SEPARATOR 0x1F),''),
	COALESCE(GROUP_CONCAT(DISTINCT e.endpoint_type ORDER BY e.endpoint_type SEPARATOR 0x1F),'')
FROM gw_catalog_discovery_items i
LEFT JOIN gw_catalog_discovery_item_groups g ON g.snapshot_item_id=i.id
LEFT JOIN gw_catalog_discovery_item_endpoints e ON e.snapshot_item_id=i.id
WHERE i.snapshot_id=? GROUP BY i.id ORDER BY i.ordinal LIMIT ? OFFSET ?`, snapshotID, size, (page-1)*size)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, ordinal uint64
		var code, description, tags, owner, pricingVersion, groups, endpoints string
		var vendorID, quotaType sql.NullInt64
		var modelPrice, modelRatio, completionRatio sql.NullString
		var selected bool
		if err := rows.Scan(&id, &ordinal, &code, &description, &tags, &vendorID, &quotaType, &modelPrice, &modelRatio,
			&completionRatio, &owner, &pricingVersion, &selected, &groups, &endpoints); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{
			"id": id, "ordinal": ordinal, "model_code": code, "description": description, "tags": tags,
			"vendor_id": nullableInt(vendorID), "provider_quota_type": nullableInt(quotaType),
			"model_price": nullableString(modelPrice), "model_ratio": nullableString(modelRatio),
			"completion_ratio": nullableString(completionRatio), "owner_by": owner, "pricing_version": pricingVersion,
			"selected_group_enabled": selected, "groups": splitCatalogValues(groups), "endpoint_types": splitCatalogValues(endpoints),
		})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func ReviewUnifiedCatalogDiscovery(c *gin.Context) {
	snapshotID, err := resp.ParseUintParam(c, "snapshot_id")
	if err != nil {
		return
	}
	var in repository.CatalogSnapshotReviewInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return uint64(snapshotID), store.ReviewCatalogDiscoverySnapshot(c.Request.Context(), tx, uint64(snapshotID), in, actor)
	})
}

func ListUnifiedCatalogPriceCandidates(c *gin.Context) {
	releaseID, ok := catalogReleaseID(c)
	if !ok {
		return
	}
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	state := strings.ToLower(strings.TrimSpace(c.Query("state")))
	if state != "" && state != "pending" && state != "confirmed" && state != "dismissed" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	where := ` WHERE sn.release_id=? AND (?='' OR (?='pending' AND rv.id IS NULL) OR rv.decision=?)`
	args := []any{releaseID, state, state, state}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_price_candidates c JOIN gw_catalog_discovery_items i ON i.id=c.snapshot_item_id JOIN gw_catalog_discovery_snapshots sn ON sn.id=i.snapshot_id JOIN gw_catalog_discovery_review_state st ON st.snapshot_id=sn.id AND st.state='accepted' LEFT JOIN gw_catalog_price_candidate_reviews rv ON rv.candidate_id=c.id`+where, args...).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT c.id,sn.id,i.model_code,i.model_price,i.provider_quota_type,sn.observed_at,
	s.source_code,p.external_group,rv.decision,rv.unit_code,rv.currency_code,rv.currency_version,rv.rate_evidence_id,rv.created_at
FROM gw_catalog_price_candidates c
JOIN gw_catalog_discovery_items i ON i.id=c.snapshot_item_id
JOIN gw_catalog_discovery_snapshots sn ON sn.id=i.snapshot_id
JOIN gw_catalog_discovery_review_state st ON st.snapshot_id=sn.id AND st.state='accepted'
JOIN gw_catalog_release_sources rs ON rs.id=sn.release_source_id
JOIN gw_catalog_sources s ON s.id=rs.catalog_source_id
JOIN gw_catalog_source_profiles p ON p.catalog_source_id=s.id
LEFT JOIN gw_catalog_price_candidate_reviews rv ON rv.candidate_id=c.id`+where+`
ORDER BY c.id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, snapshotID uint64
		var modelCode, modelPrice, sourceCode, group string
		var quotaType, currencyVersion, evidenceID sql.NullInt64
		var observedAt, reviewedAt sql.NullTime
		var decision, unitCode, currencyCode sql.NullString
		if err := rows.Scan(&id, &snapshotID, &modelCode, &modelPrice, &quotaType, &observedAt, &sourceCode, &group,
			&decision, &unitCode, &currencyCode, &currencyVersion, &evidenceID, &reviewedAt); err != nil {
			unifiedChannelError(c, err)
			return
		}
		stateValue := "pending"
		if decision.Valid {
			stateValue = decision.String
		}
		items = append(items, gin.H{
			"id": id, "snapshot_id": snapshotID, "model_code": modelCode, "model_price": modelPrice,
			"provider_quota_type": nullableInt(quotaType), "observed_at": nullableTime(observedAt),
			"source_code": sourceCode, "external_group": group, "state": stateValue,
			"unit_code": nullableString(unitCode), "currency_code": nullableString(currencyCode),
			"currency_version": nullableInt(currencyVersion), "rate_evidence_id": nullableInt(evidenceID),
			"reviewed_at": nullableTime(reviewedAt),
		})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func ReviewUnifiedCatalogPriceCandidate(c *gin.Context) {
	candidateID, err := resp.ParseUintParam(c, "candidate_id")
	if err != nil {
		return
	}
	var in repository.CatalogPriceCandidateReviewInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	if strings.EqualFold(strings.TrimSpace(in.Decision), "confirmed") {
		in.HMACKey, err = adminGatewayKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
		if err != nil {
			unifiedChannelError(c, repository.ErrCredentialEncryptionUnavailable)
			return
		}
		defer clear(in.HMACKey)
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.ReviewCatalogPriceCandidate(c.Request.Context(), tx, uint64(candidateID), in, actor)
	})
}

func nullableString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func splitCatalogValues(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, string(rune(31)))
}
