package service

import (
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

	baseQuery := func() *gorm.DB {
		q := db.Model(&model.Task{})
		if !isAdmin {
			q = q.Where("user_id = ?", userID)
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

	baseQuery().
		Where("created_at >= ?", todayStart).
		Select("COUNT(*) as total_requests, COALESCE(SUM(cost), 0) as total_cost").
		Scan(&todayStats)

	baseQuery().
		Where("created_at >= ? AND status = ?", todayStart, model.TaskStatusSuccess).
		Count(&todayStats.SuccessCount)
	baseQuery().
		Where("created_at >= ? AND status = ?", todayStart, model.TaskStatusFailed).
		Count(&todayStats.FailedCount)

	var yesterdayStats struct {
		TotalRequests int64   `json:"total_requests"`
		TotalCost     float64 `json:"total_cost"`
	}
	baseQuery().
		Where("created_at >= ? AND created_at < ?", yesterdayStart, todayStart).
		Select("COUNT(*) as total_requests, COALESCE(SUM(cost), 0) as total_cost").
		Scan(&yesterdayStats)

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

	weekStart := todayStart.AddDate(0, 0, -6)

	type dailyAgg struct {
		Date     string  `gorm:"column:date"`
		Requests int64   `gorm:"column:requests"`
		Cost     float64 `gorm:"column:cost"`
		Errors   int64   `gorm:"column:errors"`
	}
	var rawStats []dailyAgg
	wq := db.Table("tasks").
		Select("DATE_FORMAT(created_at, '%m-%d') as date, COUNT(*) as requests, COALESCE(SUM(cost), 0) as cost, SUM(CASE WHEN status = ? THEN 1 ELSE 0 END) as errors", model.TaskStatusFailed).
		Where("created_at >= ? AND created_at < ?", weekStart, todayStart.AddDate(0, 0, 1)).
		Group("DATE_FORMAT(created_at, '%m-%d')").
		Order("date ASC")
	if !isAdmin {
		wq = wq.Where("user_id = ?", userID)
	}
	wq.Scan(&rawStats)

	statsMap := make(map[string]dailyAgg, len(rawStats))
	for _, r := range rawStats {
		statsMap[r.Date] = r
	}
	weeklyStats := make([]DailyStats, 0, 7)
	for i := 6; i >= 0; i-- {
		d := todayStart.AddDate(0, 0, -i)
		key := d.Format("01-02")
		agg := statsMap[key]
		weeklyStats = append(weeklyStats, DailyStats{
			Date:     key,
			Requests: agg.Requests,
			Cost:     agg.Cost,
			Errors:   agg.Errors,
		})
	}

	var capabilityStats []CapabilityDist
	baseQuery().
		Where("created_at >= ?", todayStart.AddDate(0, 0, -7)).
		Select("model_code as capability, COUNT(*) as count").
		Group("model_code").
		Order("count DESC").
		Limit(5).
		Scan(&capabilityStats)

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
	Items    []TaskItem `json:"items"`
	Total    int64      `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
}

func (s *DashboardService) ListTasks(req *ListTasksRequest, userID uint, isAdmin bool) (*ListTasksResult, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 20
	}

	db := model.DB().Model(&model.Task{})

	if !isAdmin {
		db = db.Where("user_id = ?", userID)
	}

	if req.TokenID > 0 {
		db = db.Where("token_id = ?", req.TokenID)
	}
	if req.Status != "" {
		db = db.Where("status = ?", req.Status)
	}
	if req.Capability != "" {
		db = db.Where("capability_code = ?", req.Capability)
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
	db.Count(&total)

	var tasks []model.Task
	db.Order("created_at DESC").
		Offset((req.Page - 1) * req.PageSize).
		Limit(req.PageSize).
		Preload("Channel").
		Find(&tasks)

	items := make([]TaskItem, 0, len(tasks))
	for _, t := range tasks {
		item := TaskItem{
			ID:         t.TaskNo,
			TaskNo:     t.TaskNo,
			Capability: t.ModelCode,
			Status:     string(t.Status),
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
		Items:    items,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
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
	ChannelID   uint    `json:"channel_id"`
	ChannelType string  `json:"channel_type"`
	Total       int64   `json:"total"`
	Success     int64   `json:"success"`
	Rate        float64 `json:"rate"`
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
	TokenUsage      *TokenUsageSummary   `json:"token_usage"`
	ChannelRates    []ChannelSuccessRate `json:"channel_rates"`
	ModelRankings   []ModelCallRanking   `json:"model_rankings"`
}

// GetChatStats 获取 Chat 增强统计（基于 channel_request_logs）
func (s *DashboardService) GetChatStats(days int) (*ChatStatsResult, error) {
	db := model.DB()
	since := time.Now().AddDate(0, 0, -days)

	var usage TokenUsageSummary
	db.Table("channel_request_logs").
		Where("request_type = 'chat' AND request_at >= ?", since).
		Select("COALESCE(SUM(usage_prompt_tokens),0) as total_prompt_tokens, COALESCE(SUM(usage_completion_tokens),0) as total_completion_tokens, COALESCE(SUM(usage_total_tokens),0) as total_tokens").
		Scan(&usage)

	type channelAgg struct {
		ChannelID   uint   `gorm:"column:channel_id"`
		ChannelType string `gorm:"column:channel_type"`
		Total       int64  `gorm:"column:total"`
		Success     int64  `gorm:"column:success"`
	}
	var channelAggs []channelAgg
	db.Table("channel_request_logs l").
		Joins("JOIN channels c ON c.id = l.channel_id").
		Where("l.request_type = 'chat' AND l.request_at >= ?", since).
		Select("l.channel_id, c.type as channel_type, COUNT(*) as total, SUM(CASE WHEN l.status_code >= 200 AND l.status_code < 400 THEN 1 ELSE 0 END) as success").
		Group("l.channel_id, c.type").
		Order("total DESC").
		Scan(&channelAggs)

	rates := make([]ChannelSuccessRate, 0, len(channelAggs))
	for _, a := range channelAggs {
		rate := float64(0)
		if a.Total > 0 {
			rate = float64(a.Success) / float64(a.Total) * 100
		}
		rates = append(rates, ChannelSuccessRate{
			ChannelID:   a.ChannelID,
			ChannelType: a.ChannelType,
			Total:       a.Total,
			Success:     a.Success,
			Rate:        rate,
		})
	}

	var rankings []ModelCallRanking
	db.Table("channel_request_logs").
		Where("request_type = 'chat' AND request_at >= ?", since).
		Select("model_code, COUNT(*) as calls, COALESCE(SUM(usage_total_tokens),0) as total_tokens").
		Group("model_code").
		Order("calls DESC").
		Limit(10).
		Scan(&rankings)

	return &ChatStatsResult{
		TokenUsage:    &usage,
		ChannelRates:  rates,
		ModelRankings: rankings,
	}, nil
}
