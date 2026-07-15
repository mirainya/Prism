package service

import (
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type RequestLogService struct{}

func NewRequestLogService() *RequestLogService {
	return &RequestLogService{}
}

// Log asynchronously persists an upstream request log with bounded retries.
func (s *RequestLogService) Log(log *model.ChannelRequestLog) {
	go func() {
		const maxRetries = 3
		for i := 0; i < maxRetries; i++ {
			if err := model.DB().Create(log).Error; err != nil {
				logger.Error("save request log failed", zap.Error(err), zap.Int("attempt", i+1))
				time.Sleep(time.Duration(i+1) * time.Second)
				continue
			}
			return
		}
		logger.Error("save request log failed after retries, log dropped",
			zap.Uint("channel_id", log.ChannelID), zap.String("task_no", log.TaskNo))
	}()
}

func (s *RequestLogService) Create(log *model.ChannelRequestLog) error {
	return model.DB().Create(log).Error
}

func (s *RequestLogService) Update(id uint, updates map[string]any) error {
	if id == 0 || len(updates) == 0 {
		return nil
	}
	return model.DB().Model(&model.ChannelRequestLog{}).Where("id = ?", id).Updates(updates).Error
}

type ListRequestLogsRequest struct {
	Page           int    `form:"page"`
	PageSize       int    `form:"page_size"`
	SnapshotID     uint   `form:"snapshot_id"`
	ChannelID      uint   `form:"channel_id"`
	CapabilityCode string `form:"capability_code"`
	RequestType    string `form:"request_type"`
	TaskNo         string `form:"task_no"`
	ConversationID uint   `form:"conversation_id"`
	StartDate      string `form:"start_date"`
	EndDate        string `form:"end_date"`
}

type ListRequestLogsResponse struct {
	Items      []model.ChannelRequestLog `json:"items"`
	Total      int64                     `json:"total"`
	Page       int                       `json:"page"`
	PageSize   int                       `json:"page_size"`
	SnapshotID uint                      `json:"snapshot_id"`
}

func (s *RequestLogService) ListRequestLogs(req *ListRequestLogsRequest) (*ListRequestLogsResponse, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 20
	}
	if req.PageSize > 100 {
		req.PageSize = 100
	}

	query := model.DB().Model(&model.ChannelRequestLog{})
	if req.ChannelID > 0 {
		query = query.Where("channel_id = ?", req.ChannelID)
	}
	if req.CapabilityCode != "" {
		query = query.Where("capability_code = ?", req.CapabilityCode)
	}
	if req.RequestType != "" {
		query = query.Where("request_type = ?", req.RequestType)
	}
	if req.TaskNo != "" {
		query = query.Where("task_no LIKE ?", req.TaskNo+"%")
	}
	if req.ConversationID > 0 {
		query = query.Where("conversation_id = ?", req.ConversationID)
	}
	if req.StartDate != "" {
		query = query.Where("request_at >= ?", req.StartDate+" 00:00:00")
	}
	if req.EndDate != "" {
		query = query.Where("request_at <= ?", req.EndDate+" 23:59:59")
	}

	snapshotID := req.SnapshotID
	if snapshotID == 0 {
		var snapshot struct {
			MaxID uint `gorm:"column:max_id"`
		}
		if err := query.Session(&gorm.Session{}).
			Select("COALESCE(MAX(id), 0) AS max_id").Scan(&snapshot).Error; err != nil {
			return nil, err
		}
		snapshotID = snapshot.MaxID
	}
	if snapshotID > 0 {
		query = query.Where("id <= ?", snapshotID)
	} else {
		query = query.Where("1 = 0")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []model.ChannelRequestLog
	offset := (req.Page - 1) * req.PageSize
	if err := query.Omit("request_body", "response_body", "request_headers").
		Preload("Channel").Preload("Model").Order("id DESC").
		Offset(offset).Limit(req.PageSize).Find(&items).Error; err != nil {
		return nil, err
	}
	return &ListRequestLogsResponse{
		Items: items, Total: total, Page: req.Page, PageSize: req.PageSize, SnapshotID: snapshotID,
	}, nil
}

func (s *RequestLogService) GetRequestLog(id uint) (*model.ChannelRequestLog, error) {
	var log model.ChannelRequestLog
	if err := model.DB().Preload("Channel").Preload("Model").First(&log, id).Error; err != nil {
		return nil, err
	}
	return &log, nil
}

// ClearExpiredBodies removes retained HTTP content while preserving request metadata.
func (s *RequestLogService) ClearExpiredBodies(now time.Time, retentionHours, limit int) (int64, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	retainBodies := false
	if cfg := config.Get(); cfg != nil {
		retainBodies = cfg.Observability.RetainAPICallPayloads
	}
	cutoff := now
	if retainBodies {
		if retentionHours <= 0 {
			return 0, nil
		}
		cutoff = now.Add(-time.Duration(retentionHours) * time.Hour)
	}
	var ids []uint
	err := model.DB().Model(&model.ChannelRequestLog{}).
		Where("created_at <= ?", cutoff).
		Where("request_headers <> '' OR request_body <> '' OR response_body <> '' OR response_preview <> '' OR url <> ''").
		Order("id ASC").Limit(limit).Pluck("id", &ids).Error
	if err != nil {
		return 0, err
	}
	var cleared int64
	if len(ids) > 0 {
		result := model.DB().Model(&model.ChannelRequestLog{}).Where("id IN ?", ids).Updates(map[string]any{
			"request_headers": "", "request_body": "", "response_body": "",
			"response_preview": "", "url": "",
		})
		if result.Error != nil {
			return 0, result.Error
		}
		cleared = result.RowsAffected
	}

	legacyCleared, err := NewRetentionService().ClearLegacyBodies(now, retentionHours, retainBodies, limit)
	return cleared + legacyCleared, err
}
