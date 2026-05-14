package repository

import (
	"context"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ChannelAccountRepo struct {
	db *gorm.DB
}

func NewChannelAccountRepo(db *gorm.DB) *ChannelAccountRepo {
	return &ChannelAccountRepo{db: db}
}

func (r *ChannelAccountRepo) FindByID(ctx context.Context, id uint) (*domain.ChannelAccount, error) {
	var m model.ChannelAccount
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return accountToDomain(&m), nil
}

func (r *ChannelAccountRepo) ListByChannel(ctx context.Context, channelID uint) ([]domain.ChannelAccount, error) {
	var list []model.ChannelAccount
	if err := r.db.WithContext(ctx).Where("channel_id = ?", channelID).Find(&list).Error; err != nil {
		return nil, err
	}
	result := make([]domain.ChannelAccount, len(list))
	for i := range list {
		result[i] = *accountToDomain(&list[i])
	}
	return result, nil
}

func (r *ChannelAccountRepo) Create(ctx context.Context, a *domain.ChannelAccount) error {
	m := accountToModel(a)
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return wrapErr(err)
	}
	a.ID = m.ID
	return nil
}

func (r *ChannelAccountRepo) Update(ctx context.Context, a *domain.ChannelAccount) error {
	m := accountToModel(a)
	return wrapErr(r.db.WithContext(ctx).Save(m).Error)
}

func (r *ChannelAccountRepo) Delete(ctx context.Context, id uint) error {
	return wrapErr(r.db.WithContext(ctx).Delete(&model.ChannelAccount{}, id).Error)
}

func (r *ChannelAccountRepo) SelectAvailable(ctx context.Context, channelID uint) (*domain.ChannelAccount, error) {
	var account model.ChannelAccount
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("channel_id = ? AND status = 1", channelID).
			Where("max_tasks = 0 OR current_tasks < max_tasks").
			Order("current_tasks ASC, weight DESC").
			First(&account).Error; err != nil {
			return err
		}
		return tx.Model(&account).UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1")).Error
	})
	if err != nil {
		return nil, domain.ErrNoAvailableAccount
	}
	return accountToDomain(&account), nil
}

func (r *ChannelAccountRepo) IncrementTasks(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Model(&model.ChannelAccount{}).
		Where("id = ?", id).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1")).Error
}

func (r *ChannelAccountRepo) DecrementTasks(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Model(&model.ChannelAccount{}).
		Where("id = ? AND current_tasks > 0", id).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks - 1")).Error
}

func accountToDomain(m *model.ChannelAccount) *domain.ChannelAccount {
	var cfg map[string]any
	if m.Config != nil {
		_ = m.Config.Scan(&cfg)
	}
	return &domain.ChannelAccount{
		ID:           m.ID,
		ChannelID:    m.ChannelID,
		Name:         m.Name,
		APIKey:       m.APIKey,
		Config:       cfg,
		Weight:       m.Weight,
		MaxTasks:     m.MaxTasks,
		CurrentTasks: m.CurrentTasks,
		Status:       m.Status,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

func accountToModel(d *domain.ChannelAccount) *model.ChannelAccount {
	m := &model.ChannelAccount{
		ChannelID:    d.ChannelID,
		Name:         d.Name,
		APIKey:       d.APIKey,
		Weight:       d.Weight,
		MaxTasks:     d.MaxTasks,
		CurrentTasks: d.CurrentTasks,
		Status:       d.Status,
	}
	m.ID = d.ID
	if d.Config != nil {
		b, _ := jsonMarshal(d.Config)
		m.Config = b
	}
	return m
}
