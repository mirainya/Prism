package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

var (
	ErrAPICallNotFound     = errors.New("api call not found")
	ErrAPICallAccessDenied = errors.New("api call access denied")
	ErrAPICallInvalidInput = errors.New("invalid api call input")
)

// APICallService exposes the unified gateway ledger to the console.
type APICallService struct{}

func NewAPICallService() *APICallService { return &APICallService{} }

func GenerateUnifiedCallID() string { return uuid.NewString() }

func GenerateRequestID() string { return uuid.NewString() }

type ListCallsRequest struct {
	Page        int                     `form:"page"`
	PageSize    int                     `form:"page_size"`
	SnapshotAt  string                  `form:"snapshot_at"`
	ActorUserID uint                    `form:"-"`
	IsAdmin     bool                    `form:"-"`
	UserID      uint                    `form:"user_id"`
	TokenID     uint                    `form:"token_id"`
	Model       string                  `form:"model"`
	Endpoint    string                  `form:"endpoint"`
	Status      model.APICallStatus     `form:"status"`
	RouteKind   string                  `form:"route_kind"`
	ChannelID   uint                    `form:"channel_id"`
	Transport   model.UpstreamTransport `form:"transport"`
	StartDate   string                  `form:"start_date"`
	EndDate     string                  `form:"end_date"`
	RequestID   string                  `form:"request_id"`
	CallID      string                  `form:"call_id"`
}

type ListCallsResponse struct {
	Items      []model.APICall `json:"items"`
	Total      int64           `json:"total"`
	Page       int             `json:"page"`
	PageSize   int             `json:"page_size"`
	SnapshotAt string          `json:"snapshot_at"`
}

type APICallPayloadDetail struct {
	model.APICallPayload
	Data string `json:"data"`
}

type APICallDetail struct {
	Call          model.APICall          `json:"call"`
	GatewayCallID uint64                 `json:"gateway_call_id,omitempty"`
	Attempts      []model.APICallAttempt `json:"attempts"`
	BillingLogs   []model.BillingLog     `json:"billing_logs"`
	Payloads      []APICallPayloadDetail `json:"payloads"`
}

func (s *APICallService) ListCalls(req *ListCallsRequest) (*ListCallsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", ErrAPICallInvalidInput)
	}
	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	snapshot := time.Now().Truncate(time.Millisecond)
	if value := strings.TrimSpace(req.SnapshotAt); value != "" {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid snapshot_at", ErrAPICallInvalidInput)
		}
		snapshot = parsed.In(time.Local).Truncate(time.Millisecond)
	}

	query := unifiedCallsQuery(model.DB()).Where("c.created_at < ?", snapshot)
	if req.IsAdmin {
		if req.UserID > 0 {
			query = query.Where("c.user_id = ?", req.UserID)
		}
	} else {
		if req.ActorUserID == 0 {
			return nil, ErrAPICallAccessDenied
		}
		query = query.Where("c.user_id = ?", req.ActorUserID)
	}
	if req.TokenID > 0 {
		query = query.Where("c.token_id = ?", req.TokenID)
	}
	if modelCode := strings.TrimSpace(req.Model); modelCode != "" {
		query = query.Where("gateway_model.model_code = ?", modelCode)
	}
	if endpoint := strings.TrimSpace(req.Endpoint); endpoint != "" {
		query = query.Where("EXISTS (SELECT 1 FROM gw_operation_routes operation_route WHERE operation_route.operation_contract_id = c.operation_contract_id AND operation_route.route_template = ?)", endpoint)
	}
	if req.Status != "" {
		query = query.Where("c.status IN ?", compatibleUnifiedCallStatuses(req.Status))
	}
	if requestID := strings.TrimSpace(req.RequestID); requestID != "" {
		query = query.Where("c.public_id = ?", requestID)
	}
	if callID := strings.TrimSpace(req.CallID); callID != "" {
		query = query.Where("c.public_id = ?", callID)
	}
	if req.IsAdmin {
		query = applyUnifiedAttemptFilters(query, req)
	}
	if value := strings.TrimSpace(req.StartDate); value != "" {
		start, err := parseCallDate(value, false)
		if err != nil {
			return nil, err
		}
		query = query.Where("c.created_at >= ?", start)
	}
	if value := strings.TrimSpace(req.EndDate); value != "" {
		end, exclusive, err := parseCallEndDate(value)
		if err != nil {
			return nil, err
		}
		operator := "c.created_at <= ?"
		if exclusive {
			operator = "c.created_at < ?"
		}
		query = query.Where(operator, end)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	items := make([]model.APICall, 0)
	if err := query.Select(unifiedCallSelect).
		Order("c.created_at DESC").Order("c.id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Scan(&items).Error; err != nil {
		return nil, err
	}
	for index := range items {
		finishUnifiedCallProjection(&items[index])
		if !req.IsAdmin {
			sanitizeUserCall(&items[index])
		}
	}
	return &ListCallsResponse{
		Items: items, Total: total, Page: page, PageSize: pageSize,
		SnapshotAt: snapshot.Format(time.RFC3339Nano),
	}, nil
}

func (s *APICallService) GetCallDetail(callID string, actorUserID uint, isAdmin bool) (*APICallDetail, error) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil, fmt.Errorf("%w: call id is required", ErrAPICallInvalidInput)
	}
	query := unifiedCallsQuery(model.DB()).Where("c.public_id = ?", callID)
	if !isAdmin {
		if actorUserID == 0 {
			return nil, ErrAPICallAccessDenied
		}
		query = query.Where("c.user_id = ?", actorUserID)
	}

	var call model.APICall
	if err := query.Select(unifiedCallSelect).Take(&call).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAPICallNotFound
		}
		return nil, err
	}
	finishUnifiedCallProjection(&call)
	var gatewayCallID uint64
	if err := model.DB().Table("gw_api_calls").Select("id").Where("public_id = ?", callID).Take(&gatewayCallID).Error; err != nil {
		return nil, err
	}

	detail := &APICallDetail{Call: call}
	if isAdmin {
		detail.GatewayCallID = gatewayCallID
	}
	var err error
	if detail.Attempts, err = loadUnifiedCallAttempts(model.DB(), gatewayCallID, call.ID); err != nil {
		return nil, err
	}
	if detail.BillingLogs, err = loadUnifiedBillingLogs(model.DB(), gatewayCallID, call.ID); err != nil {
		return nil, err
	}
	if isAdmin {
		if detail.Payloads, err = loadUnifiedPayloadMetadata(model.DB(), gatewayCallID, call.ID); err != nil {
			return nil, err
		}
	} else {
		detail.Payloads = make([]APICallPayloadDetail, 0)
		sanitizeUserCallDetail(detail)
	}
	return detail, nil
}

func sanitizeUserCallDetail(detail *APICallDetail) {
	if detail == nil {
		return
	}
	sanitizeUserCall(&detail.Call)
	for index := range detail.Attempts {
		attempt := &detail.Attempts[index]
		attempt.AbilityID = 0
		attempt.ChannelID = 0
		attempt.KeyID = 0
		attempt.EndpointID = 0
		attempt.AccountID = 0
		attempt.Protocol = ""
		attempt.Transport = ""
		attempt.VendorModel = ""
		attempt.RequestPath = ""
		attempt.ProviderResponseID = ""
		attempt.ErrorMessage = ""
	}
	for index := range detail.BillingLogs {
		entry := &detail.BillingLogs[index]
		entry.IdempotentKey = ""
		entry.TokenID = 0
		entry.UserID = 0
		entry.PricingSnapshot = nil
	}
	detail.Payloads = make([]APICallPayloadDetail, 0)
}

func sanitizeUserCall(call *model.APICall) {
	if call == nil {
		return
	}
	call.UserID = 0
	call.TokenID = 0
	call.ErrorMessage = ""
	call.ErrorParam = nil
}

func parseCallDate(value string, endOfDay bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid date %q", ErrAPICallInvalidInput, value)
	}
	if endOfDay {
		return parsed.AddDate(0, 0, 1), nil
	}
	return parsed, nil
}

func parseCallEndDate(value string) (time.Time, bool, error) {
	trimmed := strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed, false, nil
	}
	parsed, err := parseCallDate(trimmed, true)
	return parsed, true, err
}
