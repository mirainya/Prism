package repository

import (
	"context"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

type ChannelRepo struct {
	db *gorm.DB
}

func NewChannelRepo(db *gorm.DB) *ChannelRepo {
	return &ChannelRepo{db: db}
}

func (r *ChannelRepo) FindByID(ctx context.Context, id uint) (*domain.Channel, error) {
	var m model.Channel
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return channelToDomain(&m), nil
}

func (r *ChannelRepo) FindByType(ctx context.Context, channelType string) (*domain.Channel, error) {
	var m model.Channel
	if err := r.db.WithContext(ctx).Where("type = ?", channelType).First(&m).Error; err != nil {
		return nil, wrapErr(err)
	}
	return channelToDomain(&m), nil
}

func (r *ChannelRepo) List(ctx context.Context, filter domain.ChannelFilter) ([]domain.Channel, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Channel{})
	if filter.Status != nil {
		q = q.Where("status = ?", *filter.Status)
	}
	if filter.Type != "" {
		q = q.Where("type = ?", filter.Type)
	}

	var total int64
	q.Count(&total)

	var list []model.Channel
	if filter.Page > 0 && filter.Size > 0 {
		q = q.Offset((filter.Page - 1) * filter.Size).Limit(filter.Size)
	}
	if err := q.Order("id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}

	result := make([]domain.Channel, len(list))
	for i := range list {
		result[i] = *channelToDomain(&list[i])
	}
	return result, total, nil
}

func (r *ChannelRepo) Create(ctx context.Context, ch *domain.Channel) error {
	m := channelToModel(ch)
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return wrapErr(err)
	}
	ch.ID = m.ID
	ch.CreatedAt = m.CreatedAt
	return nil
}

func (r *ChannelRepo) Update(ctx context.Context, ch *domain.Channel) error {
	m := channelToModel(ch)
	return wrapErr(r.db.WithContext(ctx).Save(m).Error)
}

func (r *ChannelRepo) Delete(ctx context.Context, id uint) error {
	return wrapErr(r.db.WithContext(ctx).Delete(&model.Channel{}, id).Error)
}

func channelToDomain(m *model.Channel) *domain.Channel {
	var cfg map[string]any
	if m.Config != nil {
		_ = m.Config.Scan(&cfg)
	}
	return &domain.Channel{
		ID:             m.ID,
		Type:           m.Type,
		Name:           m.Name,
		BaseURL:        m.BaseURL,
		CallbackSecret: m.CallbackSecret,
		Config:         cfg,
		Status:         m.Status,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

func channelToModel(d *domain.Channel) *model.Channel {
	m := &model.Channel{
		Type:           d.Type,
		Name:           d.Name,
		BaseURL:        d.BaseURL,
		CallbackSecret: d.CallbackSecret,
		Status:         d.Status,
	}
	m.ID = d.ID
	if d.Config != nil {
		b, _ := jsonMarshal(d.Config)
		m.Config = b
	}
	return m
}
