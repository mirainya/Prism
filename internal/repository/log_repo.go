package repository

import (
	"context"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

type RequestLogRepo struct {
	db *gorm.DB
}

func NewRequestLogRepo(db *gorm.DB) *RequestLogRepo {
	return &RequestLogRepo{db: db}
}

func (r *RequestLogRepo) Create(ctx context.Context, log *domain.ChannelRequestLog) error {
	m := requestLogToModel(log)
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return err
	}
	log.ID = m.ID
	return nil
}

func (r *RequestLogRepo) Update(ctx context.Context, log *domain.ChannelRequestLog) error {
	m := requestLogToModel(log)
	return r.db.WithContext(ctx).Save(m).Error
}

func (r *RequestLogRepo) FindByID(ctx context.Context, id uint) (*domain.ChannelRequestLog, error) {
	var m model.ChannelRequestLog
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return requestLogToDomain(&m), nil
}

func (r *RequestLogRepo) List(ctx context.Context, filter domain.RequestLogFilter) ([]domain.ChannelRequestLog, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.ChannelRequestLog{})
	if filter.ChannelID > 0 {
		q = q.Where("channel_id = ?", filter.ChannelID)
	}
	if filter.CapabilityCode != "" {
		q = q.Where("capability_code = ?", filter.CapabilityCode)
	}
	if filter.TokenID > 0 {
		q = q.Where("task_id = ?", filter.TokenID)
	}
	if filter.Status != "" {
		q = q.Where("request_type = ?", filter.Status)
	}
	if filter.StartTime != nil {
		q = q.Where("request_at >= ?", *filter.StartTime)
	}
	if filter.EndTime != nil {
		q = q.Where("request_at <= ?", *filter.EndTime)
	}

	var total int64
	q.Count(&total)

	var list []model.ChannelRequestLog
	if filter.Page > 0 && filter.Size > 0 {
		q = q.Offset((filter.Page - 1) * filter.Size).Limit(filter.Size)
	}
	if err := q.Order("id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}

	result := make([]domain.ChannelRequestLog, len(list))
	for i := range list {
		result[i] = *requestLogToDomain(&list[i])
	}
	return result, total, nil
}

func (r *RequestLogRepo) CountToday(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.ChannelRequestLog{}).
		Where("DATE(request_at) = CURDATE()").Count(&count).Error
	return count, err
}

func (r *RequestLogRepo) CountByChannel(ctx context.Context, days int) ([]domain.ChannelStats, error) {
	var results []struct {
		ChannelID   uint   `gorm:"column:channel_id"`
		ChannelName string `gorm:"column:name"`
		Count       int64  `gorm:"column:count"`
	}
	err := r.db.WithContext(ctx).
		Table("channel_request_logs l").
		Select("l.channel_id, c.name, COUNT(*) as count").
		Joins("LEFT JOIN channels c ON c.id = l.channel_id").
		Where("l.request_at >= DATE_SUB(NOW(), INTERVAL ? DAY)", days).
		Group("l.channel_id, c.name").
		Order("count DESC").
		Find(&results).Error
	if err != nil {
		return nil, err
	}

	stats := make([]domain.ChannelStats, len(results))
	for i, r := range results {
		stats[i] = domain.ChannelStats{
			ChannelID:   r.ChannelID,
			ChannelName: r.ChannelName,
			Count:       r.Count,
		}
	}
	return stats, nil
}

func requestLogToDomain(m *model.ChannelRequestLog) *domain.ChannelRequestLog {
	return &domain.ChannelRequestLog{
		ID:                    m.ID,
		TaskID:                m.TaskID,
		TaskNo:                m.TaskNo,
		ConversationID:        m.ConversationID,
		ChannelID:             m.ChannelID,
		AccountID:             m.AccountID,
		CapabilityCode:        m.CapabilityCode,
		RequestType:           string(m.RequestType),
		IsStream:              m.IsStream,
		ModelCode:             m.ModelCode,
		VendorModel:           m.VendorModel,
		RequestPath:           m.RequestPath,
		FinishReason:          m.FinishReason,
		ResponsePreview:       m.ResponsePreview,
		UsagePromptTokens:     m.UsagePromptTokens,
		UsageCompletionTokens: m.UsageCompletionTokens,
		UsageTotalTokens:      m.UsageTotalTokens,
		Method:                m.Method,
		URL:                   m.URL,
		RequestHeaders:        m.RequestHeaders,
		RequestBody:           m.RequestBody,
		StatusCode:            m.StatusCode,
		ResponseBody:          m.ResponseBody,
		DurationMs:            m.DurationMs,
		ErrorMessage:          m.ErrorMessage,
		RequestAt:             m.RequestAt,
		CreatedAt:             m.CreatedAt,
	}
}

func requestLogToModel(d *domain.ChannelRequestLog) *model.ChannelRequestLog {
	m := &model.ChannelRequestLog{
		TaskID:                d.TaskID,
		TaskNo:                d.TaskNo,
		ConversationID:        d.ConversationID,
		ChannelID:             d.ChannelID,
		AccountID:             d.AccountID,
		CapabilityCode:        d.CapabilityCode,
		RequestType:           model.RequestType(d.RequestType),
		IsStream:              d.IsStream,
		ModelCode:             d.ModelCode,
		VendorModel:           d.VendorModel,
		RequestPath:           d.RequestPath,
		FinishReason:          d.FinishReason,
		ResponsePreview:       d.ResponsePreview,
		UsagePromptTokens:     d.UsagePromptTokens,
		UsageCompletionTokens: d.UsageCompletionTokens,
		UsageTotalTokens:      d.UsageTotalTokens,
		Method:                d.Method,
		URL:                   d.URL,
		RequestHeaders:        d.RequestHeaders,
		RequestBody:           d.RequestBody,
		StatusCode:            d.StatusCode,
		ResponseBody:          d.ResponseBody,
		DurationMs:            d.DurationMs,
		ErrorMessage:          d.ErrorMessage,
		RequestAt:             d.RequestAt,
	}
	m.ID = d.ID
	return m
}

var _ domain.RequestLogRepository = (*RequestLogRepo)(nil)
