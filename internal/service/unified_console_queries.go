package service

import (
	"errors"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const unifiedCallSelect = `
	c.public_id AS id,
	c.public_id AS request_id,
	c.user_id,
	c.token_id,
	COALESCE((SELECT MIN(operation_route.route_template) FROM gw_operation_routes operation_route WHERE operation_route.operation_contract_id = c.operation_contract_id), '') AS endpoint,
	operation_contract.operation_code AS operation,
	gateway_model.model_code AS model,
	CASE c.status WHEN 'retry_pending' THEN 'in_progress' WHEN 'indeterminate' THEN 'failed' ELSE c.status END AS status,
	0 AS is_stream,
	0 AS background,
	0 AS store,
	0 AS retain_payload,
	COALESCE(resource.resource_kind, '') AS resource_type,
	COALESCE(resource.public_id, '') AS resource_id,
	COALESCE(c.final_attempt_id, 0) AS final_attempt_id,
	(SELECT COUNT(*) FROM gw_api_call_attempts attempt_count WHERE attempt_count.call_id = c.id) AS attempt_count,
	c.quoted_amount AS reserved_amount,
	COALESCE((SELECT settlement.actual_amount FROM billing_reservations reservation JOIN billing_settlements settlement ON settlement.reservation_id = reservation.id WHERE reservation.call_id = c.id LIMIT 1), 0) AS final_cost,
	COALESCE((SELECT SUM(event.amount) FROM billing_events event WHERE event.call_id = c.id AND event.event_type = 'refund'), 0) AS refunded_amount,
	COALESCE((SELECT request_log.http_status FROM gw_channel_request_logs request_log JOIN gw_api_call_attempts request_attempt ON request_attempt.id = request_log.attempt_id WHERE request_attempt.call_id = c.id ORDER BY request_log.id DESC LIMIT 1), 0) AS http_status,
	COALESCE((SELECT request_log.error_code FROM gw_channel_request_logs request_log JOIN gw_api_call_attempts request_attempt ON request_attempt.id = request_log.attempt_id WHERE request_attempt.call_id = c.id ORDER BY request_log.id DESC LIMIT 1), '') AS error_code,
	c.created_at AS started_at,
	c.updated_at AS completed_at,
	c.created_at,
	c.updated_at`

func unifiedCallsQuery(db *gorm.DB) *gorm.DB {
	return db.Table("gw_api_calls AS c").
		Joins("JOIN gw_operation_contracts AS operation_contract ON operation_contract.id = c.operation_contract_id").
		Joins("JOIN gw_model_operations AS model_operation ON model_operation.id = c.model_operation_id AND model_operation.release_id = c.catalog_release_id").
		Joins("JOIN gw_catalog_models AS catalog_model ON catalog_model.id = model_operation.catalog_model_id AND catalog_model.release_id = model_operation.release_id").
		Joins("JOIN gw_models AS gateway_model ON gateway_model.id = catalog_model.model_id").
		Joins("LEFT JOIN gw_api_resources AS resource ON resource.call_id = c.id")
}

func finishUnifiedCallProjection(call *model.APICall) {
	if call == nil {
		return
	}
	if call.Status != model.APICallStatusCompleted && call.Status != model.APICallStatusFailed && call.Status != model.APICallStatusCancelled {
		call.CompletedAt = nil
	}
	if call.CompletedAt != nil && !call.CompletedAt.Before(call.StartedAt) {
		call.DurationMs = call.CompletedAt.Sub(call.StartedAt).Milliseconds()
	}
}

func compatibleUnifiedCallStatuses(status model.APICallStatus) []string {
	switch status {
	case model.APICallStatusInProgress:
		return []string{"in_progress", "retry_pending"}
	case model.APICallStatusFailed:
		return []string{"failed", "indeterminate"}
	default:
		return []string{string(status)}
	}
}

func unifiedCallRouteKindSQL() string {
	return `CASE resource.resource_kind WHEN 'capability_task' THEN 'capability' WHEN 'video_task' THEN 'video' ELSE 'gateway_v2' END`
}

func applyUnifiedAttemptFilters(query *gorm.DB, req *ListCallsRequest) *gorm.DB {
	if req.RouteKind == "" && req.ChannelID == 0 && req.Transport == "" {
		return query
	}
	attempts := model.DB().Table("gw_api_call_attempts AS filter_attempt").
		Select("filter_attempt.call_id").
		Joins("JOIN gw_product_transports AS filter_transport ON filter_transport.id = filter_attempt.product_transport_id AND filter_transport.release_id = filter_attempt.catalog_release_id").
		Joins("JOIN gw_products AS filter_product ON filter_product.id = filter_transport.product_id AND filter_product.release_id = filter_transport.release_id").
		Joins("JOIN gw_channel_transports AS filter_channel_transport ON filter_channel_transport.id = filter_transport.channel_transport_id AND filter_channel_transport.release_id = filter_transport.release_id").
		Joins("LEFT JOIN gw_api_resources AS filter_resource ON filter_resource.call_id = filter_attempt.call_id")
	if req.RouteKind != "" {
		attempts = attempts.Where(`CASE filter_resource.resource_kind WHEN 'capability_task' THEN 'capability' WHEN 'video_task' THEN 'video' ELSE 'gateway_v2' END = ?`, req.RouteKind)
	}
	if req.ChannelID > 0 {
		attempts = attempts.Where("filter_product.channel_id = ?", req.ChannelID)
	}
	if req.Transport != "" {
		attempts = attempts.Where("filter_channel_transport.transport_code = ?", req.Transport)
	}
	return query.Where("c.id IN (?)", attempts)
}

func loadUnifiedCallAttempts(db *gorm.DB, callID uint64, publicID string) ([]model.APICallAttempt, error) {
	var attempts []model.APICallAttempt
	err := db.Table("gw_api_call_attempts AS attempt").
		Select(`attempt.id, ? AS call_id, attempt.attempt_no,
			CASE resource.resource_kind WHEN 'capability_task' THEN 'capability' WHEN 'video_task' THEN 'video' ELSE 'gateway_v2' END AS route_kind,
			COALESCE(request_log.action, '') AS stage, product.channel_id AS channel_id,
			attempt.credential_id AS key_id, channel_transport.protocol, product.vendor_model,
			channel_transport.transport_code AS transport, channel_transport.request_path,
			CASE attempt.state WHEN 'recovery_pending' THEN 'started' WHEN 'not_created' THEN 'failed' WHEN 'terminated_unknown' THEN 'failed' ELSE attempt.state END AS status,
			COALESCE(request_log.http_status, 0) AS http_status, COALESCE(request_log.error_code, '') AS error_code,
			COALESCE(request_log.duration_ms, 0) AS duration_ms, attempt.created_at AS started_at,
			attempt.updated_at AS completed_at,
			attempt.created_at, attempt.updated_at`, publicID).
		Joins("JOIN gw_product_transports AS product_transport ON product_transport.id = attempt.product_transport_id AND product_transport.release_id = attempt.catalog_release_id").
		Joins("JOIN gw_products AS product ON product.id = product_transport.product_id AND product.release_id = product_transport.release_id").
		Joins("JOIN gw_channel_transports AS channel_transport ON channel_transport.id = product_transport.channel_transport_id AND channel_transport.release_id = product_transport.release_id").
		Joins("LEFT JOIN gw_api_resources AS resource ON resource.call_id = attempt.call_id").
		Joins("LEFT JOIN gw_channel_request_logs AS request_log ON request_log.id = (SELECT MAX(latest_log.id) FROM gw_channel_request_logs latest_log WHERE latest_log.attempt_id = attempt.id)").
		Where("attempt.call_id = ?", callID).
		Order("attempt.attempt_no ASC").Order("attempt.id ASC").
		Scan(&attempts).Error
	if attempts == nil {
		attempts = make([]model.APICallAttempt, 0)
	}
	for index := range attempts {
		if attempts[index].Status != model.APICallAttemptStatusCompleted && attempts[index].Status != model.APICallAttemptStatusFailed && attempts[index].Status != model.APICallAttemptStatusCancelled {
			attempts[index].CompletedAt = nil
		}
	}
	return attempts, err
}

func loadUnifiedBillingLogs(db *gorm.DB, callID uint64, publicID string) ([]model.BillingLog, error) {
	var logs []model.BillingLog
	err := db.Table("billing_events AS event").
		Select(`event.id, gateway_call.token_id, gateway_call.user_id, ? AS call_id,
			COALESCE(gateway_call.final_attempt_id, 0) AS attempt_id,
			CASE event.event_type WHEN 'refund' THEN 'refund' WHEN 'reservation_created' THEN 'reserve' ELSE 'settle' END AS phase,
			event.amount, CASE event.event_type WHEN 'refund' THEN 'refund' ELSE 'deduct' END AS type,
			'success' AS status, event.created_at, event.created_at AS updated_at`, publicID).
		Joins("JOIN gw_api_calls AS gateway_call ON gateway_call.id = event.call_id").
		Where("event.call_id = ? AND event.event_type IN ?", callID, []string{"reservation_created", "reservation_settled", "refund"}).
		Order("event.id ASC").Scan(&logs).Error
	if logs == nil {
		logs = make([]model.BillingLog, 0)
	}
	return logs, err
}

func loadUnifiedPayloadMetadata(db *gorm.DB, callID uint64, publicID string) ([]APICallPayloadDetail, error) {
	var rows []struct {
		ID             uint
		Kind           string
		ContentLength  int64
		RetentionUntil *time.Time
		CreatedAt      time.Time
	}
	err := db.Table("gw_api_call_payloads").
		Select("id, kind, content_length, retention_until, created_at").
		Where("call_id = ? AND purged_at IS NULL AND (retention_until IS NULL OR retention_until > ?)", callID, time.Now()).
		Order("id ASC").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	payloads := make([]APICallPayloadDetail, 0, len(rows))
	for _, row := range rows {
		kind := row.Kind
		if kind == "result" {
			kind = model.APICallPayloadResponse
		}
		payloads = append(payloads, APICallPayloadDetail{APICallPayload: model.APICallPayload{
			ID: row.ID, CallID: publicID, Kind: kind, ContentType: "application/json",
			Encrypted: true, OriginalBytes: row.ContentLength, ExpiresAt: row.RetentionUntil, CreatedAt: row.CreatedAt,
		}})
	}
	return payloads, nil
}

type unifiedTaskRow struct {
	ResourceID     uint64
	TaskNo         string
	CallID         string
	UserID         uint
	TokenID        uint
	ModelCode      string
	CapabilityName string
	Channel        string
	CallStatus     string
	Status         string
	Progress       int
	Cost           decimal.Decimal
	Refunded       bool
	CreatedAt      time.Time
	StartedAt      *time.Time
	CompletedAt    *time.Time
}

func unifiedTasksQuery(db *gorm.DB) *gorm.DB {
	return db.Table("gw_api_resources AS resource").
		Joins("JOIN gw_api_calls AS gateway_call ON gateway_call.id = resource.call_id AND gateway_call.user_id = resource.user_id AND gateway_call.token_id = resource.token_id").
		Joins("LEFT JOIN gw_capability_tasks AS capability_task ON capability_task.resource_id = resource.id").
		Joins("LEFT JOIN gw_video_tasks AS video_task ON video_task.resource_id = resource.id").
		Joins("JOIN gw_model_operations AS model_operation ON model_operation.id = gateway_call.model_operation_id AND model_operation.release_id = gateway_call.catalog_release_id").
		Joins("JOIN gw_catalog_models AS catalog_model ON catalog_model.id = model_operation.catalog_model_id AND catalog_model.release_id = model_operation.release_id").
		Joins("JOIN gw_models AS gateway_model ON gateway_model.id = catalog_model.model_id").
		Joins("LEFT JOIN gw_api_call_attempts AS attempt ON attempt.id = COALESCE(gateway_call.final_attempt_id, gateway_call.current_attempt_id)").
		Joins("LEFT JOIN gw_product_transports AS product_transport ON product_transport.id = attempt.product_transport_id AND product_transport.release_id = attempt.catalog_release_id").
		Joins("LEFT JOIN gw_products AS product ON product.id = product_transport.product_id AND product.release_id = product_transport.release_id").
		Joins("LEFT JOIN gateway_channels AS channel ON channel.id = product.channel_id").
		Joins("LEFT JOIN billing_reservations AS reservation ON reservation.call_id = gateway_call.id").
		Joins("LEFT JOIN billing_settlements AS settlement ON settlement.reservation_id = reservation.id").
		Where("resource.resource_kind IN ?", []string{"capability_task", "video_task"})
}

func unifiedTaskSelect() string {
	return `resource.id AS resource_id, COALESCE(capability_task.task_no, video_task.task_no, resource.public_id) AS task_no,
		gateway_call.public_id AS call_id, gateway_call.user_id, gateway_call.token_id, gateway_model.model_code,
		catalog_model.display_name AS capability_name, COALESCE(channel.display_name, '') AS channel, gateway_call.status AS call_status,
		CASE COALESCE(capability_task.status, video_task.status, gateway_call.status)
			WHEN 'allocated' THEN 'pending' WHEN 'submitting' THEN 'pending' WHEN 'queued' THEN 'pending'
			WHEN 'accepted' THEN 'processing' WHEN 'running' THEN 'processing' WHEN 'tracking' THEN 'processing'
			WHEN 'submission_unknown' THEN 'processing' WHEN 'manual_review' THEN 'processing'
			WHEN 'cancel_requested' THEN 'processing' WHEN 'cancel_unknown' THEN 'processing' WHEN 'unknown' THEN 'processing'
			WHEN 'succeeded' THEN 'success' WHEN 'completed' THEN 'success'
			WHEN 'not_created' THEN 'failed' WHEN 'terminated_unknown' THEN 'failed'
			ELSE COALESCE(capability_task.status, video_task.status, gateway_call.status) END AS status,
		COALESCE(capability_task.progress, video_task.progress, 0) AS progress,
		COALESCE(settlement.actual_amount, 0) AS cost,
		EXISTS(SELECT 1 FROM billing_events refund_event WHERE refund_event.call_id = gateway_call.id AND refund_event.event_type = 'refund') AS refunded,
		gateway_call.created_at, attempt.created_at AS started_at, gateway_call.updated_at AS completed_at`
}

func compatibleUnifiedTaskStatuses(status string) []string {
	switch strings.TrimSpace(status) {
	case "pending":
		return []string{"allocated", "submitting", "queued"}
	case "processing", "finalizing":
		return []string{"accepted", "running", "tracking", "submission_unknown", "manual_review", "cancel_requested", "cancel_unknown", "unknown"}
	case "success":
		return []string{"succeeded", "completed"}
	case "failed":
		return []string{"failed", "not_created", "terminated_unknown"}
	case "cancelled":
		return []string{"cancelled"}
	default:
		return nil
	}
}

func scanUnifiedTask(query *gorm.DB) (*unifiedTaskRow, error) {
	var row unifiedTaskRow
	if err := query.Select(unifiedTaskSelect()).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, err
	}
	finishUnifiedTaskProjection(&row)
	return &row, nil
}

func finishUnifiedTaskProjection(row *unifiedTaskRow) {
	if row == nil {
		return
	}
	switch row.CallStatus {
	case "completed", "failed", "cancelled", "indeterminate":
	default:
		row.CompletedAt = nil
	}
}
