package migrate

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/security"
)

type runtimeAttemptRoute struct {
	ReleaseID, SKUID, RouteID, OfferingID, CostPlanID, ProductTransportID int64
	PoolID, CredentialID, CredentialVersionID, GrantID                    int64
	TaskScope, ScopeKind, ScopeKey                                        string
}

func (m *runtimeImporter) importAttempts() error {
	table := m.snapshot.Tables["api_call_attempts"]
	if !table.Present {
		return nil
	}
	required := []string{"id", "call_id", "attempt_no", "ability_id", "key_id", "status", "created_at", "updated_at"}
	if missing := table.missingColumns(required...); len(missing) > 0 {
		m.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		return nil
	}
	if len(table.Rows) == 0 {
		return nil
	}
	for _, row := range table.Rows {
		if row.PrimaryKey == "" {
			m.issue(row, "invalid_source_identity", "attempt id is empty")
			continue
		}
		if _, exists, err := m.existingMapping(row, "api_call_attempt", "gw_api_call_attempts"); err != nil {
			return err
		} else if exists {
			continue
		}
		sourceCallID := row.text("call_id")
		targetCallID, ok, err := m.lookupTarget("api_calls", sourceCallID, "api_call")
		if err != nil {
			return err
		}
		if !ok {
			m.issue(row, "call_mapping_missing", "attempt call_id has no verified target call")
			continue
		}
		callRow, ok := m.calls[sourceCallID]
		if !ok {
			m.issue(row, "call_source_missing", "attempt references a call outside the imported source snapshot")
			continue
		}
		route, ok, err := m.resolveAttemptRoute(row, targetCallID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		attemptNo, parseErr := runtimeUint(row, "attempt_no")
		if parseErr != nil || attemptNo == 0 {
			m.issue(row, "invalid_attempt_number", "attempt_no must be positive")
			continue
		}
		state, ok := runtimeAttemptState(row, callRow)
		if !ok {
			m.issue(row, "invalid_attempt_status", "attempt status is not part of the verified legacy state map")
			continue
		}
		createdAt, valid := runtimeTime(row, "created_at", "started_at")
		if !valid {
			m.issue(row, "invalid_attempt_time", "attempt creation time is missing or invalid")
			continue
		}
		updatedAt, valid := runtimeTime(row, "updated_at", "completed_at", "created_at")
		if !valid {
			updatedAt = createdAt
		}
		var duplicate bool
		if err := m.tx.QueryRowContext(m.ctx, `SELECT EXISTS(SELECT 1 FROM gw_api_call_attempts WHERE call_id=? AND attempt_no=?)`, targetCallID, attemptNo).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			m.issue(row, "target_attempt_conflict", "call attempt number is already owned by an unmapped target")
			continue
		}
		result, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_api_call_attempts(call_id,attempt_no,catalog_release_id,sku_id,route_id,offering_id,cost_plan_id,product_transport_id,credential_pool_id,credential_id,credential_version_id,purpose_grant_id,state,state_version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?)`, targetCallID, attemptNo, route.ReleaseID, route.SKUID, route.RouteID, route.OfferingID, route.CostPlanID, route.ProductTransportID, route.PoolID, route.CredentialID, route.CredentialVersionID, route.GrantID, state, createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("import legacy attempt %s: %w", row.PrimaryKey, err)
		}
		targetID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_state_transition_events(attempt_id,new_state,state_version,reason_code,created_at) VALUES (?,?,1,'legacy_runtime_import',?)`, targetID, state, createdAt); err != nil {
			return err
		}
		if err := m.createMapping(row, "api_call_attempt", targetID, strconv.FormatUint(attemptNo, 10)); err != nil {
			return err
		}
		if err := m.attachAttemptToCall(callRow, row, targetCallID, targetID, state); err != nil {
			return err
		}
		m.report.Attempts++
	}
	return nil
}

func (m *runtimeImporter) resolveAttemptRoute(row runtimeSourceRow, callID int64) (runtimeAttemptRoute, bool, error) {
	abilityID, abilityErr := runtimeUint(row, "ability_id")
	keyID, keyErr := runtimeUint(row, "key_id")
	if abilityErr != nil || keyErr != nil || abilityID == 0 || keyID == 0 {
		m.issue(row, "route_mapping_unproven", "attempt lacks a gateway ability and key identity")
		return runtimeAttemptRoute{}, false, nil
	}
	productID, productOK, err := m.lookupProvenAttemptProduct(strconv.FormatUint(abilityID, 10), callID)
	if err != nil {
		return runtimeAttemptRoute{}, false, err
	}
	credentialID, credentialOK, err := m.lookupProvenAttemptCredential(strconv.FormatUint(keyID, 10))
	if err != nil {
		return runtimeAttemptRoute{}, false, err
	}
	if !productOK || !credentialOK {
		m.issue(row, "route_mapping_unproven", "gateway ability or key has no unique target mapping")
		return runtimeAttemptRoute{}, false, nil
	}
	rows, err := m.tx.QueryContext(m.ctx, `SELECT DISTINCT c.catalog_release_id,c.sku_id,r.id,o.id,cost_plan.id,pt.id,o.credential_pool_id,cred.id,cv.id,g.id,pt.task_scope,pt.upstream_scope_kind,pt.upstream_scope_key
		FROM gw_api_calls c
		JOIN gw_products p ON p.id=? AND p.release_id=c.catalog_release_id
		JOIN gw_product_transports pt ON pt.release_id=p.release_id AND pt.product_id=p.id
		JOIN gw_offerings o ON o.release_id=pt.release_id AND o.product_transport_id=pt.id
		JOIN gw_cost_plans cost_plan ON cost_plan.release_id=o.release_id AND cost_plan.offering_id=o.id AND cost_plan.plan_code=o.cost_plan_code
		JOIN gw_routes r ON r.release_id=o.release_id AND r.offering_id=o.id AND r.sku_id=c.sku_id
		JOIN gw_credentials cred ON cred.id=? AND cred.credential_pool_id=o.credential_pool_id AND cred.channel_id=p.channel_id
		JOIN gw_credential_versions cv ON cv.id=cred.current_version_id AND cv.credential_id=cred.id
		JOIN gw_credential_purpose_grants g ON g.credential_id=cred.id AND g.purpose='execution'
		WHERE c.id=?`, productID, credentialID, callID)
	if err != nil {
		return runtimeAttemptRoute{}, false, err
	}
	defer rows.Close()
	var candidates []runtimeAttemptRoute
	for rows.Next() {
		var candidate runtimeAttemptRoute
		if err := rows.Scan(&candidate.ReleaseID, &candidate.SKUID, &candidate.RouteID, &candidate.OfferingID, &candidate.CostPlanID, &candidate.ProductTransportID, &candidate.PoolID, &candidate.CredentialID, &candidate.CredentialVersionID, &candidate.GrantID, &candidate.TaskScope, &candidate.ScopeKind, &candidate.ScopeKey); err != nil {
			return runtimeAttemptRoute{}, false, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return runtimeAttemptRoute{}, false, err
	}
	if len(candidates) != 1 {
		code := "route_mapping_missing"
		if len(candidates) > 1 {
			code = "route_mapping_ambiguous"
		}
		m.issue(row, code, "attempt does not resolve to exactly one route, offering, transport, credential version, and purpose grant")
		return runtimeAttemptRoute{}, false, nil
	}
	return candidates[0], true, nil
}

func (m *runtimeImporter) lookupProvenAttemptProduct(sourcePK string, callID int64) (int64, bool, error) {
	var count, targetID int64
	err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*),COALESCE(MIN(mapping.target_id),0)
		FROM gw_migration_object_map mapping
		JOIN gw_migration_runs run ON run.id=mapping.run_id AND run.status='succeeded'
		JOIN gw_migration_source_revisions revision ON revision.object_map_id=mapping.id
		JOIN gw_migration_mapping_proofs proof ON proof.run_id=mapping.run_id AND proof.object_map_id=mapping.id AND proof.source_hmac=revision.source_hmac
		JOIN gw_products product ON product.id=mapping.target_id
		JOIN gw_api_calls call_row ON call_row.id=? AND call_row.catalog_release_id=product.release_id
		WHERE mapping.source_table='gw_abilities' AND mapping.source_pk=? AND mapping.target_type='product'
		  AND run.operation IN ('legacy_gateway_import','legacy_catalog_import')`, callID, sourcePK).Scan(&count, &targetID)
	if err != nil {
		return 0, false, err
	}
	return targetID, count == 1 && targetID > 0, nil
}

func (m *runtimeImporter) lookupProvenAttemptCredential(sourcePK string) (int64, bool, error) {
	var count, targetID int64
	err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*),COALESCE(MIN(mapping.target_id),0)
		FROM gw_migration_object_map mapping
		JOIN gw_migration_runs run ON run.id=mapping.run_id AND run.status='succeeded'
		JOIN gw_migration_source_revisions revision ON revision.object_map_id=mapping.id
		JOIN gw_migration_mapping_proofs proof ON proof.run_id=mapping.run_id AND proof.object_map_id=mapping.id AND proof.source_hmac=revision.source_hmac
		JOIN gw_credentials credential ON credential.id=mapping.target_id
		WHERE mapping.source_table='gw_channel_keys' AND mapping.source_pk=? AND mapping.target_type='credential'
		  AND run.operation IN ('legacy_gateway_import','legacy_catalog_import')`, sourcePK).Scan(&count, &targetID)
	if err != nil {
		return 0, false, err
	}
	return targetID, count == 1 && targetID > 0, nil
}

func runtimeAttemptState(row, call runtimeSourceRow) (string, bool) {
	switch strings.ToLower(row.text("status")) {
	case "completed":
		return "completed", true
	case "failed":
		return "failed", true
	case "cancelled":
		return "cancelled", true
	case "started", "in_progress", "processing":
		if call.text("resource_type") != "" {
			return "recovery_pending", true
		}
		return "terminated_unknown", true
	case "not_created":
		return "not_created", true
	case "terminated_unknown":
		return "terminated_unknown", true
	default:
		return "", false
	}
}

func (m *runtimeImporter) attachAttemptToCall(callRow, attemptRow runtimeSourceRow, targetCallID, targetAttemptID int64, state string) error {
	finalSourceID := callRow.text("final_attempt_id")
	if finalSourceID != "" && finalSourceID != "0" && finalSourceID == attemptRow.PrimaryKey {
		_, err := m.tx.ExecContext(m.ctx, `UPDATE gw_api_calls SET final_attempt_id=? WHERE id=? AND (final_attempt_id IS NULL OR final_attempt_id=?)`, targetAttemptID, targetCallID, targetAttemptID)
		return err
	}
	if state != "recovery_pending" {
		return nil
	}
	attempts := m.attempts[attemptRow.text("call_id")]
	latest := attemptRow.PrimaryKey
	latestNo, _ := runtimeUint(attemptRow, "attempt_no")
	activeCount := 0
	for _, candidate := range attempts {
		candidateNo, _ := runtimeUint(candidate, "attempt_no")
		if candidateNo > latestNo {
			latest, latestNo = candidate.PrimaryKey, candidateNo
		}
		candidateState, _ := runtimeAttemptState(candidate, callRow)
		if candidateState == "recovery_pending" {
			activeCount++
		}
	}
	if latest != attemptRow.PrimaryKey || activeCount != 1 {
		m.issue(attemptRow, "active_attempt_ambiguous", "call does not have exactly one latest recoverable attempt")
		return nil
	}
	_, err := m.tx.ExecContext(m.ctx, `UPDATE gw_api_calls SET current_attempt_id=? WHERE id=? AND current_attempt_id IS NULL AND status IN ('in_progress','retry_pending')`, targetAttemptID, targetCallID)
	return err
}

func (m *runtimeImporter) importPayloads() error {
	table := m.snapshot.Tables["api_call_payloads"]
	if !table.Present {
		return nil
	}
	required := []string{"id", "call_id", "kind", "data", "created_at"}
	if missing := table.missingColumns(required...); len(missing) > 0 {
		m.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		return nil
	}
	if len(table.Rows) == 0 {
		return nil
	}
	for _, row := range table.Rows {
		if _, exists, err := m.existingMapping(row, "api_call_payload", "gw_api_call_payloads"); err != nil {
			return err
		} else if exists {
			continue
		}
		targetCallID, ok, err := m.lookupTarget("api_calls", row.text("call_id"), "api_call")
		if err != nil {
			return err
		}
		if !ok {
			m.issue(row, "call_mapping_missing", "payload call_id has no verified target call")
			continue
		}
		kind := ""
		switch strings.ToLower(row.text("kind")) {
		case "request":
			kind = "request"
		case "response", "result":
			kind = "result"
		default:
			m.issue(row, "payload_kind_unsupported", "payload kind cannot be represented by the unified call payload")
			continue
		}
		contentLength := row.contentLength("data")
		if original, parseErr := runtimeUint(row, "original_bytes"); parseErr == nil && original > contentLength {
			contentLength = original
		}
		contentHMAC := runtimeContentHMAC(row, "data", m.options.HMACKey)
		var existingID int64
		var existingHMAC string
		var existingLength uint64
		err = m.tx.QueryRowContext(m.ctx, `SELECT id,content_hmac,content_length FROM gw_api_call_payloads WHERE call_id=? AND kind=? FOR UPDATE`, targetCallID, kind).Scan(&existingID, &existingHMAC, &existingLength)
		if err == nil {
			if existingHMAC != contentHMAC || existingLength != contentLength {
				m.issue(row, "payload_fact_conflict", "another source payload has different content evidence for the same call and kind")
				continue
			}
			if err := m.createMapping(row, "api_call_payload", existingID, kind); err != nil {
				return err
			}
			m.report.Payloads++
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		createdAt, valid := runtimeTime(row, "created_at")
		if !valid {
			m.issue(row, "invalid_payload_time", "payload creation time is missing or invalid")
			continue
		}
		result, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_api_call_payloads(call_id,kind,schema_version,encrypted_blob_id,content_hmac,content_length,retention_until,purged_at,created_at) VALUES (?,?,1,NULL,?,?,NULL,?,?)`, targetCallID, kind, contentHMAC, contentLength, m.now, createdAt)
		if err != nil {
			return err
		}
		targetID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		column := "request_payload_id"
		if kind == "result" {
			column = "result_payload_id"
		}
		if _, err := m.tx.ExecContext(m.ctx, `UPDATE gw_api_calls SET `+column+`=? WHERE id=? AND (`+column+` IS NULL OR `+column+`=?)`, targetID, targetCallID, targetID); err != nil {
			return err
		}
		if err := m.createMapping(row, "api_call_payload", targetID, kind); err != nil {
			return err
		}
		m.report.Payloads++
	}
	return nil
}

func runtimeContentHMAC(row runtimeSourceRow, name string, key []byte) string {
	digest := security.DomainDigest(key, "legacy-runtime-content-v1", []byte(row.Table), []byte(row.PrimaryKey), []byte(name), []byte(row.contentDigest(name)), []byte(strconv.FormatUint(row.contentLength(name), 10)), []byte(strconv.FormatBool(row.present(name))))
	return hex.EncodeToString(digest[:])
}

func (m *runtimeImporter) importCapabilityTasks() error {
	table := m.snapshot.Tables["tasks"]
	if !table.Present {
		return nil
	}
	required := []string{"id", "task_no", "call_id", "user_id", "token_id", "model_code", "status", "progress", "created_at", "updated_at"}
	if missing := table.missingColumns(required...); len(missing) > 0 {
		m.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		return nil
	}
	if len(table.Rows) == 0 {
		return nil
	}
	for _, row := range table.Rows {
		progress, valid := runtimeProgress(row)
		if !valid {
			m.issue(row, "invalid_task_progress", "task progress must be between 0 and 100")
			continue
		}
		status, valid := runtimeCapabilityStatus(row.text("status"))
		if !valid {
			m.issue(row, "invalid_task_status", "task status is not part of the verified legacy state map")
			continue
		}
		createdAt, valid := runtimeTime(row, "created_at")
		if !valid {
			m.issue(row, "invalid_task_time", "task creation time is missing or invalid")
			continue
		}
		updatedAt, ok := runtimeTime(row, "updated_at", "completed_at", "created_at")
		if !ok {
			updatedAt = createdAt
		}
		summary := runtimeSummary(map[string]any{
			"schema": "legacy-capability-summary-v1", "model": row.text("model_code"), "route_operation": row.text("route_operation"),
			"request_hmac": runtimeContentHMAC(row, "request_params", m.options.HMACKey), "request_bytes": row.contentLength("request_params"),
			"mapped_hmac": runtimeContentHMAC(row, "mapped_params", m.options.HMACKey), "result_hmac": runtimeContentHMAC(row, "result", m.options.HMACKey),
		})
		publicID := runtimePublicID("capability-task", row.text("task_no"), m.options.HMACKey)
		resourceID, created, err := m.ensureRuntimeResource(row, "capability_task")
		if err != nil {
			return err
		}
		if resourceID == 0 {
			continue
		}
		if created {
			if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_capability_tasks(resource_id,task_no,status,progress,prompt_preview,parameter_summary,created_at,updated_at) VALUES (?,?,?,?, '',?,?,?)`, resourceID, publicID, status, progress, summary, createdAt, updatedAt); err != nil {
				return err
			}
			m.report.CapabilityTasks++
		}
		if err := m.importTaskAsync(row, resourceID, "capability_task"); err != nil {
			return err
		}
	}
	return nil
}

func (m *runtimeImporter) importVideoTasks() error {
	table := m.snapshot.Tables["video_tasks"]
	if !table.Present {
		return nil
	}
	required := []string{"id", "call_id", "token_id", "model", "status", "progress", "task_mode", "created_at"}
	if missing := table.missingColumns(required...); len(missing) > 0 {
		m.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		return nil
	}
	if len(table.Rows) == 0 {
		return nil
	}
	for _, row := range table.Rows {
		progress, valid := runtimeProgress(row)
		if !valid {
			m.issue(row, "invalid_video_progress", "video progress must be between 0 and 100")
			continue
		}
		status, valid := runtimeVideoProjectionStatus(row.text("status"))
		if !valid {
			m.issue(row, "invalid_video_status", "video status is not part of the verified legacy state map")
			continue
		}
		createdAt, valid := runtimeTime(row, "created_at")
		if !valid {
			m.issue(row, "invalid_video_time", "video creation time is missing or invalid")
			continue
		}
		updatedAt, ok := runtimeTime(row, "completed_at", "submitted_at", "created_at")
		if !ok {
			updatedAt = createdAt
		}
		summary := map[string]any{
			"schema": "legacy-video-summary-v1", "model": row.text("model"), "vendor_model": row.text("vendor_model"),
			"task_mode": row.text("task_mode"), "service_tier": row.text("service_tier"), "resolution": row.text("resolution"),
			"ratio": row.text("ratio"), "duration_seconds": row.text("duration"), "generate_audio": runtimeBool(row, "generate_audio"),
			"content_hmac": runtimeContentHMAC(row, "content_json", m.options.HMACKey), "params_hmac": runtimeContentHMAC(row, "params_json", m.options.HMACKey),
			"result_hmac": runtimeContentHMAC(row, "result_json", m.options.HMACKey),
		}
		if preview := safeRuntimePreview(row.preview("prompt")); preview != "" {
			summary["prompt_preview"] = preview
		}
		publicID := runtimePublicID("video-task", row.PrimaryKey, m.options.HMACKey)
		resourceID, created, err := m.ensureRuntimeResource(row, "video_task")
		if err != nil {
			return err
		}
		if resourceID == 0 {
			continue
		}
		if created {
			if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_video_tasks(resource_id,task_no,status,progress,specification_summary,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, resourceID, publicID, status, progress, runtimeSummary(summary), createdAt, updatedAt); err != nil {
				return err
			}
			m.report.VideoTasks++
		}
		if err := m.importTaskAsync(row, resourceID, "video_task"); err != nil {
			return err
		}
	}
	return nil
}

func (m *runtimeImporter) importResponses() error {
	table := m.snapshot.Tables["ai_responses"]
	if !table.Present {
		return nil
	}
	required := []string{"id", "user_id", "token_id", "call_id", "model", "status", "created_at"}
	if missing := table.missingColumns(required...); len(missing) > 0 {
		m.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		return nil
	}
	if len(table.Rows) == 0 {
		return nil
	}
	for _, row := range table.Rows {
		status, valid := runtimeResponseStatus(row.text("status"))
		if !valid {
			m.issue(row, "invalid_response_status", "response status is not part of the verified legacy state map")
			continue
		}
		createdAt, valid := runtimeTime(row, "created_at")
		if !valid {
			m.issue(row, "invalid_response_time", "response creation time is missing or invalid")
			continue
		}
		updatedAt, ok := runtimeTime(row, "completed_at", "created_at")
		if !ok {
			updatedAt = createdAt
		}
		summary := runtimeSummary(map[string]any{
			"schema": "legacy-response-summary-v1", "model": row.text("model"),
			"output_hmac": runtimeContentHMAC(row, "output_items", m.options.HMACKey), "response_hmac": runtimeContentHMAC(row, "response_json", m.options.HMACKey),
			"usage_hmac": runtimeContentHMAC(row, "usage_json", m.options.HMACKey), "error_hmac": runtimeContentHMAC(row, "error_json", m.options.HMACKey),
		})
		publicID := runtimePublicID("response", row.PrimaryKey, m.options.HMACKey)
		resourceID, created, err := m.ensureRuntimeResource(row, "response")
		if err != nil {
			return err
		}
		if resourceID == 0 || !created {
			continue
		}
		if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_ai_responses(resource_id,response_no,status,previous_response_resource_id,result_summary,created_at,updated_at) VALUES (?,?,?,NULL,?,?,?)`, resourceID, publicID, status, summary, createdAt, updatedAt); err != nil {
			return err
		}
		m.report.Responses++
	}
	return nil
}

func (m *runtimeImporter) ensureRuntimeResource(row runtimeSourceRow, kind string) (int64, bool, error) {
	targetType := kind
	projectionTable, supported := map[string]string{
		"capability_task": "gw_capability_tasks",
		"video_task":      "gw_video_tasks",
		"response":        "gw_ai_responses",
	}[kind]
	if !supported {
		return 0, false, fmt.Errorf("unsupported runtime resource kind %q", kind)
	}
	userID, tokenID, ok := m.validateOwner(row, kind != "video_task")
	if !ok {
		return 0, false, nil
	}
	callID := row.text("call_id")
	targetCallID, ok, err := m.lookupTarget("api_calls", callID, "api_call")
	if err != nil {
		return 0, false, err
	}
	if !ok {
		m.issue(row, "call_mapping_missing", "resource call_id has no verified target call")
		return 0, false, nil
	}
	var callUserID, callTokenID uint64
	if err := m.tx.QueryRowContext(m.ctx, `SELECT user_id,token_id FROM gw_api_calls WHERE id=?`, targetCallID).Scan(&callUserID, &callTokenID); err != nil {
		return 0, false, err
	}
	if callUserID != userID || callTokenID != tokenID {
		m.issue(row, "resource_owner_mismatch", "resource and target call ownership differ")
		return 0, false, nil
	}
	sourcePublicID := row.PrimaryKey
	if kind == "capability_task" {
		sourcePublicID = row.text("task_no")
	}
	publicID := runtimePublicID(kind, sourcePublicID, m.options.HMACKey)
	createdAt, valid := runtimeTime(row, "created_at")
	if !valid {
		m.issue(row, "invalid_resource_time", "resource creation time is missing or invalid")
		return 0, false, nil
	}
	if mapping, exists, err := m.existingMapping(row, targetType, "gw_api_resources"); err != nil {
		return 0, false, err
	} else if exists {
		if mapping.TargetID == 0 {
			return 0, false, nil
		}
		var mappedPublicID, mappedKind string
		var mappedCallID, mappedUserID, mappedTokenID uint64
		if err := m.tx.QueryRowContext(m.ctx, `SELECT public_id,resource_kind,call_id,user_id,token_id FROM gw_api_resources WHERE id=? FOR SHARE`, mapping.TargetID).Scan(&mappedPublicID, &mappedKind, &mappedCallID, &mappedUserID, &mappedTokenID); err != nil {
			return 0, false, err
		}
		if mapping.Discriminator != publicID || mappedPublicID != publicID || mappedKind != kind || mappedCallID != uint64(targetCallID) || mappedUserID != userID || mappedTokenID != tokenID {
			m.issue(row, "resource_mapping_conflict", "mapped resource does not match immutable source ownership and identity")
			return 0, false, nil
		}
		var projection bool
		if err := m.tx.QueryRowContext(m.ctx, `SELECT EXISTS(SELECT 1 FROM `+projectionTable+` WHERE resource_id=?)`, mapping.TargetID).Scan(&projection); err != nil {
			return 0, false, err
		}
		return mapping.TargetID, !projection, nil
	}
	rows, err := m.tx.QueryContext(m.ctx, `SELECT id,public_id,resource_kind,call_id,user_id,token_id FROM gw_api_resources WHERE call_id=? OR public_id=? FOR UPDATE`, targetCallID, publicID)
	if err != nil {
		return 0, false, err
	}
	var existing []struct {
		ID, CallID, UserID, TokenID int64
		PublicID, Kind              string
	}
	for rows.Next() {
		var item struct {
			ID, CallID, UserID, TokenID int64
			PublicID, Kind              string
		}
		if err := rows.Scan(&item.ID, &item.PublicID, &item.Kind, &item.CallID, &item.UserID, &item.TokenID); err != nil {
			rows.Close()
			return 0, false, err
		}
		existing = append(existing, item)
	}
	if err := rows.Close(); err != nil {
		return 0, false, err
	}
	if len(existing) != 0 {
		m.issue(row, "resource_identity_conflict", "call or public resource identity is already owned by an unmapped target")
		return 0, false, nil
	}
	result, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_api_resources(public_id,resource_kind,call_id,user_id,token_id,created_at) VALUES (?,?,?,?,?,?)`, publicID, kind, targetCallID, userID, tokenID, createdAt)
	if err != nil {
		return 0, false, err
	}
	resourceID, err := result.LastInsertId()
	if err != nil {
		return 0, false, err
	}
	if err := m.createMapping(row, targetType, resourceID, publicID); err != nil {
		return 0, false, err
	}
	return resourceID, true, nil
}

func runtimeProgress(row runtimeSourceRow) (uint64, bool) {
	progress, err := runtimeUint(row, "progress")
	if err != nil {
		return 0, false
	}
	return progress, progress <= 100
}

func runtimeCapabilityStatus(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "pending", "processing", "success", "failed", "cancelled":
		return strings.ToLower(strings.TrimSpace(value)), true
	case "finalizing":
		return "processing", true
	default:
		return "", false
	}
}

func runtimeVideoProjectionStatus(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "queued", "submitted", "tracking", "completed", "failed", "cancelled", "submission_unknown":
		return strings.ToLower(strings.TrimSpace(value)), true
	default:
		return "", false
	}
}

func runtimeResponseStatus(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "queued", "in_progress", "completed", "incomplete", "failed", "cancelled":
		return strings.ToLower(strings.TrimSpace(value)), true
	default:
		return "", false
	}
}

func safeRuntimePreview(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(strings.ToLower(value), "base64,") || strings.HasPrefix(strings.ToLower(value), "data:") {
		return ""
	}
	longRun := 0
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '+' || char == '/' || char == '=' {
			longRun++
			if longRun >= 256 {
				return ""
			}
		} else {
			longRun = 0
		}
	}
	return value
}

func (m *runtimeImporter) linkPreviousResponses() error {
	for _, row := range m.snapshot.Tables["ai_responses"].Rows {
		previous := row.text("previous_response_id")
		if previous == "" {
			continue
		}
		resourceID, ok, err := m.lookupTarget("ai_responses", row.PrimaryKey, "response")
		if err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		previousID, ok, err := m.lookupTarget("ai_responses", previous, "response")
		if err != nil {
			return err
		}
		if !ok {
			m.issue(row, "previous_response_mapping_missing", "previous_response_id has no verified target response")
			continue
		}
		var current sql.NullInt64
		if err := m.tx.QueryRowContext(m.ctx, `SELECT previous_response_resource_id FROM gw_ai_responses WHERE resource_id=? FOR UPDATE`, resourceID).Scan(&current); err != nil {
			return err
		}
		if current.Valid && current.Int64 != previousID {
			m.issue(row, "previous_response_conflict", "target response already refers to a different predecessor")
			continue
		}
		if _, err := m.tx.ExecContext(m.ctx, `UPDATE gw_ai_responses SET previous_response_resource_id=? WHERE resource_id=?`, previousID, resourceID); err != nil {
			return err
		}
	}
	return nil
}

func (m *runtimeImporter) importTaskAsync(row runtimeSourceRow, _ int64, kind string) error {
	if _, exists, err := m.existingMapping(row, "async_execution", "gw_async_executions"); err != nil {
		return err
	} else if exists {
		return nil
	}
	sourceCallID := row.text("call_id")
	call, ok := m.calls[sourceCallID]
	if !ok {
		return nil
	}
	sourceAttemptID := call.text("final_attempt_id")
	if sourceAttemptID == "" || sourceAttemptID == "0" {
		attempts := m.attempts[sourceCallID]
		if len(attempts) == 1 {
			sourceAttemptID = attempts[0].PrimaryKey
		} else {
			m.issue(row, "execution_attempt_unproven", "resource does not identify exactly one legacy execution attempt")
			return nil
		}
	}
	targetAttemptID, ok, err := m.lookupTarget("api_call_attempts", sourceAttemptID, "api_call_attempt")
	if err != nil {
		return err
	}
	if !ok {
		m.issue(row, "execution_attempt_unproven", "resource execution attempt has no verified target mapping")
		return nil
	}
	var scope, scopeKind, scopeKey string
	if err := m.tx.QueryRowContext(m.ctx, `SELECT pt.task_scope,pt.upstream_scope_kind,pt.upstream_scope_key FROM gw_api_call_attempts a JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id WHERE a.id=?`, targetAttemptID).Scan(&scope, &scopeKind, &scopeKey); err != nil {
		return err
	}
	if scope != "task" || scopeKind == "" || scopeKey == "" {
		m.issue(row, "task_transport_unproven", "mapped attempt is not bound to a task-scoped product transport")
		return nil
	}
	state, terminal, valid := runtimeAsyncState(row, kind)
	if !valid {
		m.issue(row, "async_status_unproven", "resource status cannot be mapped to an async execution state")
		return nil
	}
	if !terminal {
		m.issue(row, "active_execution_recovery_unproven", "non-terminal history requires retained canonical request and provider identity before it can resume")
		return nil
	}
	var existingID int64
	err = m.tx.QueryRowContext(m.ctx, `SELECT id FROM gw_async_executions WHERE attempt_id=? FOR UPDATE`, targetAttemptID).Scan(&existingID)
	if err == nil {
		m.issue(row, "async_execution_conflict", "attempt is already owned by an unmapped async execution")
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	createdAt, _ := runtimeTime(row, "created_at")
	updatedAt, ok := runtimeTime(row, "completed_at", "created_at")
	if !ok {
		updatedAt = createdAt
	}
	result, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_async_executions(attempt_id,upstream_scope_kind,upstream_scope_key,state,state_version,next_action_at,action_seq,created_at,updated_at) VALUES (?,?,?,?,1,NULL,0,?,?)`, targetAttemptID, scopeKind, scopeKey, state, createdAt, updatedAt)
	if err != nil {
		return err
	}
	asyncID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_state_transition_events(async_execution_id,new_state,state_version,reason_code,created_at) VALUES (?,?,1,'legacy_runtime_import',?)`, asyncID, state, createdAt); err != nil {
		return err
	}
	if err := m.createMapping(row, "async_execution", asyncID, kind); err != nil {
		return err
	}
	m.report.AsyncExecutions++
	return nil
}

func runtimeAsyncState(row runtimeSourceRow, kind string) (state string, terminal, valid bool) {
	status := strings.ToLower(row.text("status"))
	if kind == "video_task" {
		switch status {
		case "queued":
			return "allocated", false, true
		case "submitted":
			return "accepted", false, true
		case "tracking":
			return "running", false, true
		case "submission_unknown":
			return "submission_unknown", false, true
		case "completed":
			return "succeeded", true, true
		case "failed", "cancelled":
			return status, true, true
		default:
			return "", false, false
		}
	}
	switch status {
	case "pending":
		return "allocated", false, true
	case "processing":
		return "running", false, true
	case "success":
		return "succeeded", true, true
	case "failed", "cancelled":
		return status, true, true
	case "finalizing":
		if row.present("submit_checkpoint") && row.present("result") {
			return "succeeded", true, true
		}
		return "", false, false
	default:
		return "", false, false
	}
}

func (m *runtimeImporter) importRequestLogs() error {
	current := m.snapshot.Tables["channel_request_logs"]
	legacy := m.snapshot.Tables["request_logs"]
	if len(current.Rows) > 0 && len(legacy.Rows) > 0 {
		m.issue(runtimeSourceRow{Table: "channel_request_logs", PrimaryKey: "*"}, "ambiguous_request_log_sources", "both supported legacy request-log tables contain rows")
		m.issue(runtimeSourceRow{Table: "request_logs", PrimaryKey: "*"}, "ambiguous_request_log_sources", "both supported legacy request-log tables contain rows")
		return nil
	}
	table := current
	if len(table.Rows) == 0 {
		table = legacy
	}
	if !table.Present || len(table.Rows) == 0 {
		return nil
	}
	required := []string{"id", "attempt_id", "request_type", "created_at"}
	if missing := table.missingColumns(required...); len(missing) > 0 {
		m.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		return nil
	}
	for _, row := range table.Rows {
		if _, exists, err := m.existingMapping(row, "request_log", "gw_channel_request_logs"); err != nil {
			return err
		} else if exists {
			continue
		}
		sourceAttemptID := row.text("attempt_id")
		if sourceAttemptID == "" || sourceAttemptID == "0" {
			m.issue(row, "request_attempt_unproven", "request log does not identify a legacy attempt")
			continue
		}
		targetAttemptID, ok, err := m.lookupTarget("api_call_attempts", sourceAttemptID, "api_call_attempt")
		if err != nil {
			return err
		}
		if !ok {
			m.issue(row, "request_attempt_unproven", "request log attempt has no verified target mapping")
			continue
		}
		action, ok := runtimeRequestAction(row.text("request_type"))
		if !ok {
			m.issue(row, "request_action_unproven", "request_type cannot be mapped to a unified outbound action")
			continue
		}
		requestSeq, parseErr := strconv.ParseUint(row.PrimaryKey, 10, 64)
		if parseErr != nil || requestSeq == 0 {
			m.issue(row, "invalid_request_sequence", "request-log primary key is not a positive sequence")
			continue
		}
		statusCode, _ := runtimeUint(row, "status_code")
		status := "unknown"
		if statusCode > 0 || row.present("response_body") {
			status = "response_recorded"
		}
		createdAt, valid := runtimeTime(row, "request_at", "created_at")
		if !valid {
			m.issue(row, "invalid_request_time", "request time is missing or invalid")
			continue
		}
		duration, _ := runtimeUint(row, "duration_ms")
		requestMapping := security.DomainDigest(m.options.HMACKey, "legacy-runtime-request-map-v1", []byte(row.Table), []byte(row.PrimaryKey), []byte(row.Revision))
		requestHMAC, responseHMAC := runtimeContentHMAC(row, "request_body", m.options.HMACKey), runtimeContentHMAC(row, "response_body", m.options.HMACKey)
		var requestValue, responseValue any
		if row.present("request_body") {
			requestValue = requestHMAC
		}
		if row.present("response_body") {
			responseValue = responseHMAC
		}
		var httpStatus, durationValue any
		if statusCode > 0 {
			httpStatus = statusCode
		}
		if row.value("duration_ms").Valid {
			durationValue = duration
		}
		errorCode := ""
		if row.present("error_message") {
			errorCode = "legacy_request_error"
		}
		result, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_channel_request_logs(attempt_id,control_plane_run_id,request_seq,action,status,request_mapping_hmac,request_bytes_hmac,response_bytes_hmac,request_bytes_complete,response_bytes_complete,diagnostic_blob_id,http_status,duration_ms,error_code,created_at,completed_at) VALUES (?,NULL,?,?,?,?,?,?,?,?,NULL,?,?,?,?,?)`, targetAttemptID, requestSeq, action, status, hex.EncodeToString(requestMapping[:]), requestValue, responseValue, row.present("request_body"), row.present("response_body"), httpStatus, durationValue, errorCode, createdAt, nullableRuntimeCompletion(status, createdAt))
		if err != nil {
			return err
		}
		targetID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if err := m.createMapping(row, "request_log", targetID, strconv.FormatUint(requestSeq, 10)); err != nil {
			return err
		}
		m.report.RequestLogs++
	}
	return nil
}

func runtimeRequestAction(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "submit", "chat", "responses":
		return "submit", true
	case "poll", "query":
		return "query", true
	case "cancel", "recover", "result_fetch", "named_action":
		return strings.ToLower(strings.TrimSpace(value)), true
	default:
		return "", false
	}
}

func nullableRuntimeCompletion(status string, createdAt any) any {
	if status == "response_recorded" {
		return createdAt
	}
	return nil
}

func (m *runtimeImporter) importRuntimeBillingEvidence() error {
	if err := m.auditBillingLogs(); err != nil {
		return err
	}
	return m.auditBalanceEntries()
}

func (m *runtimeImporter) auditBillingLogs() error {
	table := m.snapshot.Tables["billing_logs"]
	if !table.Present {
		return nil
	}
	required := []string{"id", "token_id", "user_id", "amount", "type", "status", "created_at"}
	if missing := table.missingColumns(required...); len(missing) > 0 {
		m.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		return nil
	}
	if len(table.Rows) == 0 {
		return nil
	}
	for _, row := range table.Rows {
		if _, exists, err := m.existingMapping(row, "legacy_billing_evidence", "gw_legacy_billing_evidence"); err != nil {
			return err
		} else if exists {
			continue
		}
		userID, tokenID, ownerOK := m.validateOwner(row, true)
		amount, amountErr := billing.ParseAmount(row.text("amount"), 18, true)
		typeOK := row.text("type") == "deduct" || row.text("type") == "refund"
		statusOK := row.text("status") == "success" || row.text("status") == "failed" || row.text("status") == "pending"
		targetCallID, targetAttemptID, callOK, attemptOK, relationErr := m.resolveBillingReferences(row)
		if relationErr != nil {
			return relationErr
		}
		createdAt, timeOK := runtimeTime(row, "created_at")
		if !ownerOK || amountErr != nil || amount.Sign() <= 0 || !typeOK || !statusOK || !callOK || !attemptOK || !timeOK {
			m.issue(row, "billing_evidence_invalid", "billing row ownership, amount, state, call, or attempt could not be verified")
			continue
		}
		if err := m.createLegacyBillingEvidence(legacyBillingEvidenceInput{
			row: row, recordKind: "billing_log", userID: userID, tokenID: tokenID,
			legacyType: row.text("type"), legacyStatus: row.text("status"), amount: amount,
			targetCallID: targetCallID, targetAttemptID: targetAttemptID, sourceCreatedAt: createdAt,
			evidenceHMACs: map[string]any{
				"pricing_snapshot": runtimeContentHMAC(row, "pricing_snapshot", m.options.HMACKey),
				"remark":           runtimeContentHMAC(row, "remark", m.options.HMACKey),
			},
		}); err != nil {
			return err
		}
		m.report.BillingEvidence++
	}
	return nil
}

func (m *runtimeImporter) auditBalanceEntries() error {
	table := m.snapshot.Tables["balance_entries"]
	if !table.Present {
		return nil
	}
	required := []string{"id", "account_type", "account_id", "user_id", "token_id", "direction", "category", "amount", "balance_before", "balance_after", "created_at"}
	if missing := table.missingColumns(required...); len(missing) > 0 {
		m.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		return nil
	}
	if len(table.Rows) == 0 {
		return nil
	}
	for _, row := range table.Rows {
		if _, exists, err := m.existingMapping(row, "legacy_billing_evidence", "gw_legacy_billing_evidence"); err != nil {
			return err
		} else if exists {
			continue
		}
		accountType := row.text("account_type")
		var userID, tokenID uint64
		var ownerOK bool
		accountID, accountErr := runtimeUint(row, "account_id")
		if accountType == "token" {
			userID, tokenID, ownerOK = m.validateOwner(row, false)
			ownerOK = ownerOK && accountErr == nil && accountID == tokenID
		} else if accountType == "user" {
			var parseErr error
			userID, parseErr = runtimeUint(row, "user_id")
			if rawToken := row.text("token_id"); rawToken != "" && rawToken != "0" {
				tokenID, _ = runtimeUint(row, "token_id")
			}
			_, userExists := m.users[userID]
			ownerOK = parseErr == nil && accountErr == nil && userExists && userID != 0 && userID == accountID
			if ownerOK && tokenID != 0 {
				owner, tokenExists := m.owners[tokenID]
				ownerOK = tokenExists && owner.UserID == userID
			}
		}
		amount, amountErr := billing.ParseAmount(row.text("amount"), 18, true)
		before, beforeErr := billing.ParseAmount(row.text("balance_before"), 18, false)
		after, afterErr := billing.ParseAmount(row.text("balance_after"), 18, false)
		direction := row.text("direction")
		directionOK := direction == "debit" || direction == "credit"
		balanceOK := amountErr == nil && beforeErr == nil && afterErr == nil && amount.Sign() > 0
		if balanceOK {
			expected := before.Add(amount)
			if direction == "debit" {
				expected = before.Sub(amount)
			}
			balanceOK = expected.Cmp(after) == 0
		}
		targetCallID, targetAttemptID, callOK, attemptOK, relationErr := m.resolveBillingReferences(row)
		if relationErr != nil {
			return relationErr
		}
		createdAt, timeOK := runtimeTime(row, "created_at")
		category := row.text("category")
		if !ownerOK || !balanceOK || !directionOK || !callOK || !attemptOK || !timeOK || category == "" || len(category) > 64 {
			m.issue(row, "balance_evidence_invalid", "balance row ownership, amount, or direction could not be verified")
			continue
		}
		if err := m.createLegacyBillingEvidence(legacyBillingEvidenceInput{
			row: row, recordKind: "balance_entry", userID: userID, tokenID: tokenID,
			accountType: accountType, accountID: accountID, direction: direction, category: category,
			amount: amount, balanceBefore: &before, balanceAfter: &after,
			targetCallID: targetCallID, targetAttemptID: targetAttemptID, sourceCreatedAt: createdAt,
			evidenceHMACs: map[string]any{"metadata": runtimeContentHMAC(row, "metadata", m.options.HMACKey)},
		}); err != nil {
			return err
		}
		m.report.BalanceEvidence++
	}
	return nil
}

func (m *runtimeImporter) resolveBillingReferences(row runtimeSourceRow) (targetCallID, targetAttemptID int64, callOK, attemptOK bool, err error) {
	callOK, attemptOK = true, true
	if callID := row.text("call_id"); callID != "" {
		targetCallID, callOK, err = m.lookupTarget("api_calls", callID, "api_call")
		if err != nil {
			return 0, 0, false, false, err
		}
	}
	if attemptID := row.text("attempt_id"); attemptID != "" && attemptID != "0" {
		targetAttemptID, attemptOK, err = m.lookupTarget("api_call_attempts", attemptID, "api_call_attempt")
		if err != nil || !attemptOK || targetCallID == 0 {
			return targetCallID, targetAttemptID, callOK, false, err
		}
		var attemptCallID int64
		if err = m.tx.QueryRowContext(m.ctx, `SELECT call_id FROM gw_api_call_attempts WHERE id=?`, targetAttemptID).Scan(&attemptCallID); err != nil {
			return targetCallID, targetAttemptID, false, false, err
		}
		attemptOK = attemptCallID == targetCallID
	}
	return targetCallID, targetAttemptID, callOK, attemptOK, nil
}
