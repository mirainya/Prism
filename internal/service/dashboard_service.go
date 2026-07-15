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
		q := db.Model(&model.Task{})
		if !isAdmin {
			q = q.Where("user_id = ?", userID)
		}
		return q
	}
	callQuery := func() *gorm.DB {
		return scopedAPICalls(db.Model(&model.APICall{}), userID, isAdmin)
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
		Where("created_at >= ?", todayStart).
		Select(
			"COUNT(*) as total_requests, COALESCE(SUM(final_cost), 0) as total_cost, "+
				"COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as success_count, "+
				"COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as failed_count",
			model.APICallStatusCompleted, model.APICallStatusFailed,
		).
		Scan(&todayStats).Error; err != nil {
		return nil, err
	}

	var yesterdayStats struct {
		TotalRequests int64   `json:"total_requests"`
		TotalCost     float64 `json:"total_cost"`
	}
	if err := callQuery().
		Where("created_at >= ? AND created_at < ?", yesterdayStart, todayStart).
		Select("COUNT(*) as total_requests, COALESCE(SUM(final_cost), 0) as total_cost").
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
			Where("created_at >= ? AND created_at < ?", dayStart, dayStart.AddDate(0, 0, 1)).
			Select("COUNT(*) as requests, COALESCE(SUM(final_cost), 0) as cost, COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) as errors", model.APICallStatusFailed).
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
		Where("created_at >= ?", todayStart.AddDate(0, 0, -7)).
		Select("model_code as capability, COUNT(*) as count").
		Group("model_code").
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

func scopedAPICalls(query *gorm.DB, userID uint, isAdmin bool) *gorm.DB {
	if isAdmin {
		return query
	}
	return query.Where("user_id = ?", userID)
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

	db := model.DB().Model(&model.Task{}).Where("created_at < ?", snapshot)

	if !isAdmin {
		db = db.Where("user_id = ?", userID)
	}

	if req.TokenID > 0 {
		db = db.Where("token_id = ?", req.TokenID)
	}
	if req.Status != "" {
		if req.Status == string(model.TaskStatusProcessing) {
			db = db.Where("status IN ?", []model.TaskStatus{model.TaskStatusProcessing, model.TaskStatusFinalizing})
		} else {
			db = db.Where("status = ?", req.Status)
		}
	}
	if req.Capability != "" {
		db = db.Where("model_code = ?", req.Capability)
	}
	if req.StartDate != "" {
		if t, err := time.Parse("2006-01-02", req.StartDate); err == nil {
			db = db.Where("created_at >= ?", t)
		}
	}
	if req.EndDate != "" {
		if t, err := time.Parse("2006-01-02", req.EndDate); err == nil {
			db = db.Where("created_at < ?", t.AddDate(0, 0, 1))
		}
	}
	if req.Keyword != "" {
		db = db.Where("task_no LIKE ?", req.Keyword+"%")
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	var tasks []model.Task
	// 列表页只需展示字段,显式 Select 排除 request_params/mapped_params/vendor_response/result
	// 这几个 JSON 列可能存有 MB 级的 base64 图片/视频数据,SELECT * 会拉取巨量数据导致接口卡死
	if err := db.Select("id", "task_no", "call_id", "model_code", "status", "progress", "cost",
		"refunded", "error_message", "created_at", "completed_at", "channel_id").
		Order("created_at DESC").Order("id DESC").
		Offset((req.Page - 1) * req.PageSize).
		Limit(req.PageSize).
		Preload("Channel").
		Find(&tasks).Error; err != nil {
		return nil, err
	}

	items := make([]TaskItem, 0, len(tasks))
	for _, t := range tasks {
		item := TaskItem{
			ID:         t.TaskNo,
			TaskNo:     t.TaskNo,
			CallID:     t.CallID,
			Capability: t.ModelCode,
			Status:     string(t.Status.Public()),
			Progress:   t.Progress,
			Cost:       t.Cost,
			Refunded:   t.Refunded,
			Error:      t.ErrorMessage,
			CreatedAt:  t.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		item.CapabilityName = t.ModelCode
		if t.Channel != nil {
			item.Channel = t.Channel.Type
		}
		if t.CompletedAt != nil {
			item.CompletedAt = t.CompletedAt.Format("2006-01-02 15:04:05")
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
	query := model.DB().Where("task_no = ?", taskNo)
	if !isAdmin {
		query = query.Where("user_id = ?", userID)
	}

	var task model.Task
	err := query.
		Preload("Channel").
		Preload("Endpoint").
		Preload("Endpoint.Model").
		First(&task).Error
	return &task, err
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

	var usage TokenUsageSummary
	if err := scopedAPICalls(db.Table("api_calls"), userID, isAdmin).
		Where("created_at >= ?", since).
		Select("COALESCE(SUM(input_tokens),0) as total_prompt_tokens, COALESCE(SUM(output_tokens),0) as total_completion_tokens, COALESCE(SUM(total_tokens),0) as total_tokens").
		Scan(&usage).Error; err != nil {
		return nil, err
	}

	var channelAggs []channelRateAggregate
	attemptQuery := func() *gorm.DB {
		query := db.Table("api_call_attempts a").
			Joins("JOIN api_calls c ON c.id = a.call_id").
			Where("a.started_at >= ? AND a.status IN ?", since, []model.APICallAttemptStatus{
				model.APICallAttemptStatusCompleted,
				model.APICallAttemptStatusFailed,
				model.APICallAttemptStatusCancelled,
			})
		if !isAdmin {
			query = query.Where("c.user_id = ?", userID)
		}
		return query
	}
	var gatewayAggs []channelRateAggregate
	if err := attemptQuery().
		Where("a.route_kind <> ? AND a.channel_id > 0", model.APICallRouteCapability).
		Select("a.channel_id, COUNT(*) as total, COALESCE(SUM(CASE WHEN a.status = ? THEN 1 ELSE 0 END), 0) as success", model.APICallAttemptStatusCompleted).
		Group("a.channel_id").
		Scan(&gatewayAggs).Error; err != nil {
		return nil, err
	}
	for index := range gatewayAggs {
		gatewayAggs[index].RouteKind = model.APICallRouteGatewayV2
	}
	channelAggs = append(channelAggs, gatewayAggs...)

	var capabilityAggs []channelRateAggregate
	if err := attemptQuery().
		Joins("JOIN endpoints e ON e.id = a.endpoint_id").
		Where("a.route_kind = ? AND e.channel_id > 0", model.APICallRouteCapability).
		Select("e.channel_id as channel_id, COUNT(*) as total, COALESCE(SUM(CASE WHEN a.status = ? THEN 1 ELSE 0 END), 0) as success", model.APICallAttemptStatusCompleted).
		Group("e.channel_id").
		Scan(&capabilityAggs).Error; err != nil {
		return nil, err
	}
	for index := range capabilityAggs {
		capabilityAggs[index].RouteKind = model.APICallRouteCapability
	}
	channelAggs = append(channelAggs, capabilityAggs...)
	if !isAdmin {
		channelAggs = aggregateChannelRatesByRoute(channelAggs)
	}
	sort.SliceStable(channelAggs, func(left, right int) bool {
		return channelAggs[left].Total > channelAggs[right].Total
	})

	gatewayChannelIDs := make([]uint, 0)
	capabilityChannelIDs := make([]uint, 0)
	if isAdmin {
		for _, aggregate := range channelAggs {
			if aggregate.RouteKind == model.APICallRouteCapability {
				capabilityChannelIDs = append(capabilityChannelIDs, aggregate.ChannelID)
			} else {
				gatewayChannelIDs = append(gatewayChannelIDs, aggregate.ChannelID)
			}
		}
	}
	channelNames := make(map[string]string, len(channelAggs))
	if len(gatewayChannelIDs) > 0 {
		var channels []model.GwChannel
		if err := db.Select("id", "name").Where("id IN ?", gatewayChannelIDs).Find(&channels).Error; err != nil {
			return nil, err
		}
		for _, channel := range channels {
			channelNames[channelRateKey(model.APICallRouteGatewayV2, channel.ID)] = channel.Name
		}
	}
	if len(capabilityChannelIDs) > 0 {
		var channels []model.Channel
		if err := db.Select("id", "type", "name").Where("id IN ?", capabilityChannelIDs).Find(&channels).Error; err != nil {
			return nil, err
		}
		for _, channel := range channels {
			name := channel.Type
			if name == "" {
				name = channel.Name
			}
			channelNames[channelRateKey(model.APICallRouteCapability, channel.ID)] = name
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
	if err := scopedAPICalls(db.Table("api_calls"), userID, isAdmin).
		Where("created_at >= ?", since).
		Select("model as model_code, COUNT(*) as calls, COALESCE(SUM(total_tokens),0) as total_tokens").
		Group("model").
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
