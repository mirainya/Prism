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

	var weeklyStats []DailyStats
	for i := 6; i >= 0; i-- {
		dayStart := todayStart.AddDate(0, 0, -i)
		dayEnd := dayStart.AddDate(0, 0, 1)

		var dayAgg struct {
			Requests int64   `gorm:"column:requests"`
			Cost     float64 `gorm:"column:cost"`
		}

		q := db.Table("tasks")
		if !isAdmin {
			q = q.Where("user_id = ?", userID)
		}
		q.Where("created_at >= ? AND created_at < ?", dayStart, dayEnd).
			Select("COUNT(*) as requests, COALESCE(SUM(cost), 0) as cost").
			Scan(&dayAgg)

		dayStat := DailyStats{
			Date:     dayStart.Format("01-02"),
			Requests: dayAgg.Requests,
			Cost:     dayAgg.Cost,
		}

		var errCount int64
		eq := db.Table("tasks")
		if !isAdmin {
			eq = eq.Where("user_id = ?", userID)
		}
		eq.Where("created_at >= ? AND created_at < ? AND status = ?", dayStart, dayEnd, model.TaskStatusFailed).
			Count(&errCount)
		dayStat.Errors = errCount

		weeklyStats = append(weeklyStats, dayStat)
	}

	var capabilityStats []CapabilityDist
	baseQuery().
		Where("created_at >= ?", todayStart.AddDate(0, 0, -7)).
		Select("capability_code as capability, COUNT(*) as count").
		Group("capability_code").
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
		db = db.Where("task_no LIKE ?", "%"+req.Keyword+"%")
	}

	var total int64
	db.Count(&total)

	var tasks []model.Task
	db.Order("created_at DESC").
		Offset((req.Page - 1) * req.PageSize).
		Limit(req.PageSize).
		Preload("Channel").
		Preload("Capability").
		Find(&tasks)

	items := make([]TaskItem, 0, len(tasks))
	for _, t := range tasks {
		item := TaskItem{
			ID:         t.TaskNo,
			TaskNo:     t.TaskNo,
			Capability: t.CapabilityCode,
			Status:     string(t.Status),
			Progress:   t.Progress,
			Cost:       t.Cost,
			Refunded:   t.Refunded,
			Error:      t.ErrorMessage,
			CreatedAt:  t.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if t.Capability != nil {
			item.CapabilityName = t.Capability.Name
		}
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
		Preload("Capability").
		First(&task).Error
	return &task, err
}
