package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

var (
	ErrObservabilityAccessDenied = errors.New("observability access denied")
	ErrObservabilityInvalidInput = errors.New("invalid observability query")
)

type ObservabilityService struct{}

func NewObservabilityService() *ObservabilityService {
	return &ObservabilityService{}
}

type ObservabilityScope struct {
	ActorUserID uint
	IsAdmin     bool
}

type ObservabilityListFilter struct {
	Page       int     `form:"page"`
	PageSize   int     `form:"page_size"`
	SnapshotID *uint64 `form:"snapshot_id"`
	UserID     uint    `form:"user_id"`
	TokenID    uint    `form:"token_id"`
	StartDate  string  `form:"start_date"`
	EndDate    string  `form:"end_date"`
}

type ListAPIAccessLogsRequest struct {
	ObservabilityListFilter
	RequestID  string `form:"request_id"`
	CallID     string `form:"call_id"`
	Method     string `form:"method"`
	Path       string `form:"path"`
	StatusCode int    `form:"status_code"`
	ErrorCode  string `form:"error_code"`
}

type ListAuditEventsRequest struct {
	ObservabilityListFilter
	RequestID    string `form:"request_id"`
	Action       string `form:"action"`
	ResourceType string `form:"resource_type"`
	Outcome      string `form:"outcome"`
}

type ListBalanceEntriesRequest struct {
	ObservabilityListFilter
	AccountType string `form:"account_type"`
	Direction   string `form:"direction"`
	Category    string `form:"category"`
	CallID      string `form:"call_id"`
}

type ObservabilityListResponse[T any] struct {
	Items      []T    `json:"items"`
	Total      int64  `json:"total"`
	Page       int    `json:"page"`
	PageSize   int    `json:"page_size"`
	SnapshotID uint64 `json:"snapshot_id"`
}

func (s *ObservabilityService) ListAPIAccessLogs(
	req *ListAPIAccessLogsRequest,
	scope ObservabilityScope,
) (*ObservabilityListResponse[model.APIAccessLog], error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", ErrObservabilityInvalidInput)
	}

	query, page, pageSize, err := prepareObservabilityQuery(
		model.DB().Model(&model.APIAccessLog{}),
		req.ObservabilityListFilter,
		scope,
		"user_id",
		"token_id",
	)
	if err != nil {
		return nil, err
	}
	if value := strings.TrimSpace(req.RequestID); value != "" {
		query = query.Where("request_id = ?", value)
	}
	if value := strings.TrimSpace(req.CallID); value != "" {
		query = query.Where("call_id = ?", value)
	}
	if value := strings.ToUpper(strings.TrimSpace(req.Method)); value != "" {
		query = query.Where("method = ?", value)
	}
	if value := strings.TrimSpace(req.Path); value != "" {
		query = query.Where("path LIKE ?", "%"+value+"%")
	}
	if req.StatusCode > 0 {
		query = query.Where("status_code = ?", req.StatusCode)
	}
	if value := strings.TrimSpace(req.ErrorCode); value != "" {
		query = query.Where("error_code = ?", value)
	}

	items := make([]model.APIAccessLog, 0)
	total, snapshotID, err := findObservabilityPage(query, page, pageSize, req.SnapshotID, &items)
	if err != nil {
		return nil, err
	}
	return &ObservabilityListResponse[model.APIAccessLog]{
		Items: items, Total: total, Page: page, PageSize: pageSize, SnapshotID: snapshotID,
	}, nil
}

func (s *ObservabilityService) ListAuditEvents(
	req *ListAuditEventsRequest,
	scope ObservabilityScope,
) (*ObservabilityListResponse[model.AuditEvent], error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", ErrObservabilityInvalidInput)
	}

	query, page, pageSize, err := prepareObservabilityQuery(
		model.DB().Model(&model.AuditEvent{}),
		req.ObservabilityListFilter,
		scope,
		"actor_user_id",
		"actor_token_id",
	)
	if err != nil {
		return nil, err
	}
	if value := strings.TrimSpace(req.RequestID); value != "" {
		query = query.Where("request_id = ?", value)
	}
	if value := strings.TrimSpace(req.Action); value != "" {
		query = query.Where("action LIKE ?", "%"+value+"%")
	}
	if value := strings.TrimSpace(req.ResourceType); value != "" {
		query = query.Where("resource_type = ?", value)
	}
	if value := strings.TrimSpace(req.Outcome); value != "" {
		query = query.Where("outcome = ?", value)
	}

	items := make([]model.AuditEvent, 0)
	total, snapshotID, err := findObservabilityPage(query, page, pageSize, req.SnapshotID, &items)
	if err != nil {
		return nil, err
	}
	return &ObservabilityListResponse[model.AuditEvent]{
		Items: items, Total: total, Page: page, PageSize: pageSize, SnapshotID: snapshotID,
	}, nil
}

func (s *ObservabilityService) ListBalanceEntries(
	req *ListBalanceEntriesRequest,
	scope ObservabilityScope,
) (*ObservabilityListResponse[model.BalanceEntry], error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", ErrObservabilityInvalidInput)
	}

	query, page, pageSize, err := prepareObservabilityQuery(
		model.DB().Model(&model.BalanceEntry{}),
		req.ObservabilityListFilter,
		scope,
		"user_id",
		"token_id",
	)
	if err != nil {
		return nil, err
	}
	if value := strings.TrimSpace(req.AccountType); value != "" {
		query = query.Where("account_type = ?", value)
	}
	if value := strings.TrimSpace(req.Direction); value != "" {
		query = query.Where("direction = ?", value)
	}
	if value := strings.TrimSpace(req.Category); value != "" {
		query = query.Where("category = ?", value)
	}
	if value := strings.TrimSpace(req.CallID); value != "" {
		query = query.Where("call_id = ?", value)
	}

	items := make([]model.BalanceEntry, 0)
	total, snapshotID, err := findObservabilityPage(query, page, pageSize, req.SnapshotID, &items)
	if err != nil {
		return nil, err
	}
	return &ObservabilityListResponse[model.BalanceEntry]{
		Items: items, Total: total, Page: page, PageSize: pageSize, SnapshotID: snapshotID,
	}, nil
}

func prepareObservabilityQuery(
	query *gorm.DB,
	filter ObservabilityListFilter,
	scope ObservabilityScope,
	userColumn string,
	tokenColumn string,
) (*gorm.DB, int, int, error) {
	if query == nil {
		return nil, 0, 0, fmt.Errorf("%w: database query is nil", ErrObservabilityInvalidInput)
	}

	page, pageSize := normalizeObservabilityPage(filter.Page, filter.PageSize)
	if scope.IsAdmin {
		if filter.UserID > 0 {
			query = query.Where(userColumn+" = ?", filter.UserID)
		}
	} else {
		if scope.ActorUserID == 0 {
			return nil, 0, 0, ErrObservabilityAccessDenied
		}
		if filter.UserID > 0 && filter.UserID != scope.ActorUserID {
			return nil, 0, 0, ErrObservabilityAccessDenied
		}
		query = query.Where(userColumn+" = ?", scope.ActorUserID)
	}
	if filter.TokenID > 0 {
		query = query.Where(tokenColumn+" = ?", filter.TokenID)
	}

	query, err := applyObservabilityDateRange(query, filter.StartDate, filter.EndDate)
	if err != nil {
		return nil, 0, 0, err
	}
	return query, page, pageSize, nil
}

func normalizeObservabilityPage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func applyObservabilityDateRange(query *gorm.DB, startValue, endValue string) (*gorm.DB, error) {
	var start time.Time
	if strings.TrimSpace(startValue) != "" {
		parsed, err := parseObservabilityDate(startValue)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid start date", ErrObservabilityInvalidInput)
		}
		start = parsed
		query = query.Where("created_at >= ?", parsed)
	}

	if strings.TrimSpace(endValue) == "" {
		return query, nil
	}
	end, exclusive, err := parseObservabilityEndDate(endValue)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid end date", ErrObservabilityInvalidInput)
	}
	if !start.IsZero() && !end.After(start) {
		return nil, fmt.Errorf("%w: end date must be after start date", ErrObservabilityInvalidInput)
	}
	if exclusive {
		return query.Where("created_at < ?", end), nil
	}
	return query.Where("created_at <= ?", end), nil
}

func parseObservabilityEndDate(value string) (time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return parsed.AddDate(0, 0, 1), true, nil
	}
	parsed, err := parseObservabilityDate(value)
	return parsed, false, err
}

func parseObservabilityDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, ErrObservabilityInvalidInput
}

func findObservabilityPage(
	query *gorm.DB,
	page int,
	pageSize int,
	requestedSnapshotID *uint64,
	destination any,
) (int64, uint64, error) {
	snapshotID := uint64(0)
	if requestedSnapshotID != nil {
		snapshotID = *requestedSnapshotID
	} else {
		var snapshot struct {
			MaxID uint64 `gorm:"column:max_id"`
		}
		if err := query.Session(&gorm.Session{}).
			Select("COALESCE(MAX(id), 0) AS max_id").Scan(&snapshot).Error; err != nil {
			return 0, 0, err
		}
		snapshotID = snapshot.MaxID
	}
	query = query.Where("id <= ?", snapshotID)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return 0, 0, err
	}
	if err := query.Order("created_at DESC").Order("id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(destination).Error; err != nil {
		return 0, 0, err
	}
	return total, snapshotID, nil
}
