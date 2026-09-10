package service

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type DashboardService struct{}

func NewDashboardService() *DashboardService {
	return &DashboardService{}
}

type DailyStats struct {
	Date     string  `json:"date"`
	Requests int64   `json:"requests" gorm:"column:requests"`
	Cost     float64 `json:"cost" gorm:"column:cost"`
	Errors   int64   `json:"errors"`
}

type CapabilityDist struct {
	Capability string `json:"capability"`
	Count      int64  `json:"count"`
}

type StatsResult struct {
	Today          gin.H            `json:"today"`
	WeeklyTrend    []DailyStats     `json:"weekly_trend"`
	CapabilityDist []CapabilityDist `json:"capability_dist"`
}

func (s *DashboardService) GetStats(userID uint, isAdmin bool) (*StatsResult, error) {
	db := model.DB()

	taskQuery := func() *gorm.DB {
		q := unifiedTasksQuery(db)
		if !isAdmin {
			q = q.Where("gateway_call.user_id = ?", userID)
		}
		return q
	}
	callQuery := func() *gorm.DB {
		q := unifiedCallsQuery(db)
		if !isAdmin {
			q = q.Where("c.user_id = ?", userID)
		}
		return q
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterdayStart := todayStart.AddDate(0, 0, -1)

	var todayStats struct {
		TotalRequests int64   `json:"total_requests"`
		TotalCost     float64 `json:"total_cost"`
		SuccessCount  int64   `json:"success_count"`
		FailedCount   int64   `json:"failed_count"`
	}

	if err := callQuery().
		Where("c.created_at >= ?", todayStart).
		Select(
			"COUNT(*) as total_requests, COALESCE(SUM(COALESCE((SELECT settlement.actual_amount FROM billing_reservations reservation JOIN billing_settlements settlement ON settlement.reservation_id = reservation.id WHERE reservation.call_id = c.id LIMIT 1), 0)), 0) as total_cost, "+
				"COALESCE(SUM(CASE WHEN c.status = ? THEN 1 ELSE 0 END), 0) as success_count, "+
				"COALESCE(SUM(CASE WHEN c.status IN ? THEN 1 ELSE 0 END), 0) as failed_count",
			model.APICallStatusCompleted, []string{"failed", "indeterminate"},
		).
		Scan(&todayStats).Error; err != nil {
		return nil, err
	}

	var yesterdayStats struct {
		TotalRequests int64   `json:"total_requests"`
		TotalCost     float64 `json:"total_cost"`
	}
	if err := callQuery().
		Where("c.created_at >= ? AND c.created_at < ?", yesterdayStart, todayStart).
		Select("COUNT(*) as total_requests, COALESCE(SUM(COALESCE((SELECT settlement.actual_amount FROM billing_reservations reservation JOIN billing_settlements settlement ON settlement.reservation_id = reservation.id WHERE reservation.call_id = c.id LIMIT 1), 0)), 0) as total_cost").
		Scan(&yesterdayStats).Error; err != nil {
		return nil, err
	}

	errorRate := float64(0)
	if todayStats.TotalRequests > 0 {
		errorRate = float64(todayStats.FailedCount) / float64(todayStats.TotalRequests) * 100
	}

	requestTrend := float64(0)
	if yesterdayStats.TotalRequests > 0 {
		requestTrend = float64(todayStats.TotalRequests-yesterdayStats.TotalRequests) / float64(yesterdayStats.TotalRequests) * 100
	}
	costTrend := float64(0)
	if yesterdayStats.TotalCost > 0 {
		costTrend = (todayStats.TotalCost - yesterdayStats.TotalCost) / yesterdayStats.TotalCost * 100
	}

	type dailyAgg struct {
		Requests int64   `gorm:"column:requests"`
		Cost     float64 `gorm:"column:cost"`
		Errors   int64   `gorm:"column:errors"`
	}
	weeklyStats := make([]DailyStats, 0, 7)
	for i := 6; i >= 0; i-- {
		dayStart := todayStart.AddDate(0, 0, -i)
		var aggregate dailyAgg
		if err := callQuery().
			Where("c.created_at >= ? AND c.created_at < ?", dayStart, dayStart.AddDate(0, 0, 1)).
			Select("COUNT(*) as requests, COALESCE(SUM(COALESCE((SELECT settlement.actual_amount FROM billing_reservations reservation JOIN billing_settlements settlement ON settlement.reservation_id = reservation.id WHERE reservation.call_id = c.id LIMIT 1), 0)), 0) as cost, COALESCE(SUM(CASE WHEN c.status IN ? THEN 1 ELSE 0 END), 0) as errors", []string{"failed", "indeterminate"}).
			Scan(&aggregate).Error; err != nil {
			return nil, err
		}
		weeklyStats = append(weeklyStats, DailyStats{
			Date:     dayStart.Format("01-02"),
			Requests: aggregate.Requests,
			Cost:     aggregate.Cost,
			Errors:   aggregate.Errors,
		})
	}

	var capabilityStats []CapabilityDist
	if err := taskQuery().
		Where("gateway_call.created_at >= ?", todayStart.AddDate(0, 0, -7)).
		Select("gateway_model.model_code as capability, COUNT(*) as count").
		Group("gateway_model.model_code").
		Order("count DESC").
		Limit(5).
		Scan(&capabilityStats).Error; err != nil {
		return nil, err
	}

	return &StatsResult{
		Today: gin.H{
			"total_requests": todayStats.TotalRequests,
			"total_cost":     todayStats.TotalCost,
			"success_count":  todayStats.SuccessCount,
			"failed_count":   todayStats.FailedCount,
			"error_rate":     errorRate,
			"request_trend":  requestTrend,
			"cost_trend":     costTrend,
		},
		WeeklyTrend:    weeklyStats,
		CapabilityDist: capabilityStats,
	}, nil
}

type ListTasksRequest struct {
	Page       int    `form:"page"`
	PageSize   int    `form:"page_size"`
	SnapshotAt string `form:"snapshot_at"`
	Status     string `form:"status"`
	Capability string `form:"capability"`
	StartDate  string `form:"start_date"`
	EndDate    string `form:"end_date"`
	Keyword    string `form:"keyword"`
	TokenID    uint   `form:"token_id"`
}

type TaskItem struct {
	ID             string          `json:"id"`
	TaskNo         string          `json:"task_no"`
	CallID         string          `json:"call_id"`
	Capability     string          `json:"capability"`
	CapabilityName string          `json:"capability_name"`
	Channel        string          `json:"channel"`
	Status         string          `json:"status"`
	Progress       int             `json:"progress"`
	Cost           decimal.Decimal `json:"cost"`
	Refunded       bool            `json:"refunded"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      string          `json:"created_at"`
	CompletedAt    string          `json:"completed_at,omitempty"`
}

type ListTasksResult struct {
	Items      []TaskItem `json:"items"`
	Total      int64      `json:"total"`
	Page       int        `json:"page"`
	PageSize   int        `json:"page_size"`
	SnapshotAt string     `json:"snapshot_at"`
}

func (s *DashboardService) ListTasks(req *ListTasksRequest, userID uint, isAdmin bool) (*ListTasksResult, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 20
	}

	snapshot := time.Now().Truncate(time.Millisecond)
	if req.SnapshotAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, req.SnapshotAt)
		if err != nil {
			return nil, fmt.Errorf("invalid snapshot_at: %w", err)
		}
		snapshot = parsed.In(time.Local).Truncate(time.Millisecond)
	}

	db := unifiedTasksQuery(model.DB()).Where("gateway_call.created_at < ?", snapshot)

	if !isAdmin {
		db = db.Where("gateway_call.user_id = ?", userID)
	}

	if req.TokenID > 0 {
		db = db.Where("gateway_call.token_id = ?", req.TokenID)
	}
	if req.Status != "" {
		statuses := compatibleUnifiedTaskStatuses(req.Status)
		if len(statuses) == 0 {
			return &ListTasksResult{Items: []TaskItem{}, Page: req.Page, PageSize: req.PageSize, SnapshotAt: snapshot.Format(time.RFC3339Nano)}, nil
		}
		db = db.Where("COALESCE(capability_task.status, video_task.status, gateway_call.status) IN ?", statuses)
	}
	if req.Capability != "" {
		db = db.Where("gateway_model.model_code = ?", req.Capability)
	}
	if req.StartDate != "" {
		if t, err := time.Parse("2006-01-02", req.StartDate); err == nil {
			db = db.Where("gateway_call.created_at >= ?", t)
		}
	}
	if req.EndDate != "" {
		if t, err := time.Parse("2006-01-02", req.EndDate); err == nil {
			db = db.Where("gateway_call.created_at < ?", t.AddDate(0, 0, 1))
		}
	}
	if req.Keyword != "" {
		db = db.Where("COALESCE(capability_task.task_no, video_task.task_no, resource.public_id) LIKE ?", req.Keyword+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var tasks []unifiedTaskRow
	if err := db.Select(unifiedTaskSelect()).
		Order("gateway_call.created_at DESC").Order("resource.id DESC").
		Offset((req.Page - 1) * req.PageSize).
		Limit(req.PageSize).
		Scan(&tasks).Error; err != nil {
		return nil, err
	}

	items := make([]TaskItem, 0, len(tasks))
	for index := range tasks {
		finishUnifiedTaskProjection(&tasks[index])
		task := tasks[index]
		item := TaskItem{
			ID:             task.TaskNo,
			TaskNo:         task.TaskNo,
			CallID:         task.CallID,
			Capability:     task.ModelCode,
			CapabilityName: task.CapabilityName,
			Channel:        task.Channel,
			Status:         task.Status,
			Progress:       task.Progress,
			Cost:           task.Cost,
			Refunded:       task.Refunded,
			CreatedAt:      task.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if item.CapabilityName == "" {
			item.CapabilityName = task.ModelCode
		}
		if task.CompletedAt != nil {
			item.CompletedAt = task.CompletedAt.Format("2006-01-02 15:04:05")
		}
		items = append(items, item)
	}

	return &ListTasksResult{
		Items:      items,
		Total:      total,
		Page:       req.Page,
		PageSize:   req.PageSize,
		SnapshotAt: snapshot.Format(time.RFC3339Nano),
	}, nil
}

func (s *DashboardService) GetTaskDetail(taskNo string, userID uint, isAdmin bool) (*model.Task, error) {
	query := unifiedTasksQuery(model.DB()).Where("COALESCE(capability_task.task_no, video_task.task_no, resource.public_id) = ?", taskNo)
	if !isAdmin {
		query = query.Where("gateway_call.user_id = ?", userID)
	}

	row, err := scanUnifiedTask(query)
	if err != nil {
		return nil, err
	}
	task := &model.Task{
		BaseModel: model.BaseModel{ID: uint(row.ResourceID), CreatedAt: row.CreatedAt, UpdatedAt: row.CreatedAt},
		TaskNo:    row.TaskNo, CallID: row.CallID, UserID: row.UserID, TokenID: row.TokenID,
		ModelCode: row.ModelCode, Status: model.TaskStatus(row.Status), Progress: row.Progress,
		Cost: row.Cost, Refunded: row.Refunded, StartedAt: row.StartedAt, CompletedAt: row.CompletedAt,
	}
	if row.Channel != "" {
		task.Channel = &model.Channel{Type: row.Channel, Name: row.Channel}
	}
	return task, nil
}

// ChannelSuccessRate 渠道成功率
type ChannelSuccessRate struct {
	ChannelID   uint    `json:"channel_id,omitempty"`
	ChannelType string  `json:"channel_type,omitempty"`
	RouteKind   string  `json:"route_kind"`
	Total       int64   `json:"total"`
	Success     int64   `json:"success"`
	Rate        float64 `json:"rate"`
}

type channelRateAggregate struct {
	ChannelID uint   `gorm:"column:channel_id"`
	RouteKind string `gorm:"column:route_kind"`
	Total     int64  `gorm:"column:total"`
	Success   int64  `gorm:"column:success"`
}

// ModelCallRanking 模型调用排行
type ModelCallRanking struct {
	ModelCode   string `json:"model_code"`
	Calls       int64  `json:"calls"`
	TotalTokens int64  `json:"total_tokens"`
}

// TokenUsageSummary Token 用量汇总
type TokenUsageSummary struct {
	TotalPromptTokens     int64 `json:"total_prompt_tokens"`
	TotalCompletionTokens int64 `json:"total_completion_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

// ChatStatsResult Chat 增强统计
type ChatStatsResult struct {
	TokenUsage    *TokenUsageSummary   `json:"token_usage"`
	ChannelRates  []ChannelSuccessRate `json:"channel_rates"`
	ModelRankings []ModelCallRanking   `json:"model_rankings"`
}

// GetChatStats returns call-level usage and rankings plus attempt-level channel rates.
func (s *DashboardService) GetChatStats(days int, userID uint, isAdmin bool) (*ChatStatsResult, error) {
	db := model.DB()
	if days <= 0 {
		days = 7
	}
	since := time.Now().AddDate(0, 0, -days)

	// The unified ledger persists rated amounts but not an unpriced aggregate
	// usage counter. Keep the response fields stable without consulting legacy data.
	usage := TokenUsageSummary{}

	var channelAggs []channelRateAggregate
	attemptQuery := db.Table("gw_api_call_attempts AS attempt").
		Joins("JOIN gw_api_calls AS gateway_call ON gateway_call.id = attempt.call_id").
		Joins("LEFT JOIN gw_api_resources AS resource ON resource.call_id = attempt.call_id").
		Joins("JOIN gw_product_transports AS product_transport ON product_transport.id = attempt.product_transport_id AND product_transport.release_id = attempt.catalog_release_id").
		Joins("JOIN gw_products AS product ON product.id = product_transport.product_id AND product.release_id = product_transport.release_id").
		Where("attempt.created_at >= ? AND attempt.state IN ?", since, []string{"completed", "failed", "cancelled", "not_created", "terminated_unknown"})
	if !isAdmin {
		attemptQuery = attemptQuery.Where("gateway_call.user_id = ?", userID)
	}
	if err := attemptQuery.
		Select("product.channel_id, " + unifiedCallRouteKindSQL() + " AS route_kind, COUNT(*) as total, COALESCE(SUM(CASE WHEN attempt.state = 'completed' THEN 1 ELSE 0 END), 0) as success").
		Group("product.channel_id").Group("resource.resource_kind").
		Scan(&channelAggs).Error; err != nil {
		return nil, err
	}
	if !isAdmin {
		channelAggs = aggregateChannelRatesByRoute(channelAggs)
	}
	sort.SliceStable(channelAggs, func(left, right int) bool {
		return channelAggs[left].Total > channelAggs[right].Total
	})

	channelIDs := make([]uint, 0)
	if isAdmin {
		for _, aggregate := range channelAggs {
			channelIDs = append(channelIDs, aggregate.ChannelID)
		}
	}
	channelNames := make(map[string]string, len(channelAggs))
	if len(channelIDs) > 0 {
		var channels []struct {
			ID          uint
			DisplayName string
		}
		if err := db.Table("gateway_channels").Select("id", "display_name").Where("id IN ?", channelIDs).Scan(&channels).Error; err != nil {
			return nil, err
		}
		for _, channel := range channels {
			for _, routeKind := range []string{model.APICallRouteGatewayV2, model.APICallRouteCapability, model.APICallRouteVideo} {
				channelNames[channelRateKey(routeKind, channel.ID)] = channel.DisplayName
			}
		}
	}

	rates := make([]ChannelSuccessRate, 0, len(channelAggs))
	for _, a := range channelAggs {
		rate := float64(0)
		if a.Total > 0 {
			rate = float64(a.Success) / float64(a.Total) * 100
		}
		rates = append(rates, ChannelSuccessRate{
			ChannelID:   a.ChannelID,
			ChannelType: channelNames[channelRateKey(a.RouteKind, a.ChannelID)],
			RouteKind:   a.RouteKind,
			Total:       a.Total,
			Success:     a.Success,
			Rate:        rate,
		})
	}

	var rankings []ModelCallRanking
	rankingQuery := unifiedCallsQuery(db).Where("c.created_at >= ?", since)
	if !isAdmin {
		rankingQuery = rankingQuery.Where("c.user_id = ?", userID)
	}
	if err := rankingQuery.
		Select("gateway_model.model_code, COUNT(*) as calls, 0 as total_tokens").
		Group("gateway_model.model_code").
		Order("calls DESC").
		Limit(10).
		Scan(&rankings).Error; err != nil {
		return nil, err
	}

	return &ChatStatsResult{
		TokenUsage:    &usage,
		ChannelRates:  rates,
		ModelRankings: rankings,
	}, nil
}

func channelRateKey(routeKind string, channelID uint) string {
	return routeKind + ":" + strconv.FormatUint(uint64(channelID), 10)
}

func aggregateChannelRatesByRoute(items []channelRateAggregate) []channelRateAggregate {
	result := make([]channelRateAggregate, 0, len(items))
	indices := make(map[string]int, len(items))
	for _, item := range items {
		if index, exists := indices[item.RouteKind]; exists {
			result[index].Total += item.Total
			result[index].Success += item.Success
			continue
		}
		item.ChannelID = 0
		indices[item.RouteKind] = len(result)
		result = append(result, item)
	}
	return result
}
