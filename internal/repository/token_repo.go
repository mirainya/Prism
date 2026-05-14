package repository

import (
	"context"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type TokenRepo struct {
	db *gorm.DB
}

func NewTokenRepo(db *gorm.DB) *TokenRepo {
	return &TokenRepo{db: db}
}

func (r *TokenRepo) FindByID(ctx context.Context, id uint) (*domain.Token, error) {
	var m model.Token
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, wrapErr(err)
	}
	return tokenToDomain(&m), nil
}

func (r *TokenRepo) FindByKey(ctx context.Context, key string) (*domain.Token, error) {
	var m model.Token
	if err := r.db.WithContext(ctx).Where("`key` = ?", key).First(&m).Error; err != nil {
		return nil, wrapErr(err)
	}
	return tokenToDomain(&m), nil
}

func (r *TokenRepo) List(ctx context.Context, filter domain.TokenFilter) ([]domain.Token, int64, error) {
	q := r.db.WithContext(ctx).Model(&model.Token{})
	if filter.UserID > 0 {
		q = q.Where("user_id = ?", filter.UserID)
	}
	if filter.Status != nil {
		q = q.Where("status = ?", *filter.Status)
	}

	var total int64
	q.Count(&total)

	var list []model.Token
	if filter.Page > 0 && filter.Size > 0 {
		q = q.Offset((filter.Page - 1) * filter.Size).Limit(filter.Size)
	}
	if err := q.Order("id DESC").Find(&list).Error; err != nil {
		return nil, 0, err
	}

	result := make([]domain.Token, len(list))
	for i := range list {
		result[i] = *tokenToDomain(&list[i])
	}
	return result, total, nil
}

func (r *TokenRepo) Create(ctx context.Context, t *domain.Token) error {
	m := tokenToModel(t)
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return wrapErr(err)
	}
	t.ID = m.ID
	return nil
}

func (r *TokenRepo) Update(ctx context.Context, t *domain.Token) error {
	m := tokenToModel(t)
	return wrapErr(r.db.WithContext(ctx).Save(m).Error)
}

func (r *TokenRepo) Delete(ctx context.Context, id uint) error {
	return wrapErr(r.db.WithContext(ctx).Delete(&model.Token{}, id).Error)
}

func (r *TokenRepo) UpdateBalance(ctx context.Context, id uint, delta decimal.Decimal) error {
	return r.db.WithContext(ctx).Model(&model.Token{}).
		Where("id = ?", id).
		UpdateColumn("balance", gorm.Expr("balance + ?", delta)).Error
}

func tokenToDomain(m *model.Token) *domain.Token {
	return &domain.Token{
		ID:        m.ID,
		UserID:    m.UserID,
		Name:      m.Name,
		Key:       m.Key,
		KeyHint:   m.KeyHint,
		PlainKey:  m.PlainKey,
		Balance:   m.Balance,
		TotalUsed: m.TotalUsed,
		RateLimit: m.RateLimit,
		Status:    m.Status,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func tokenToModel(d *domain.Token) *model.Token {
	m := &model.Token{
		UserID:    d.UserID,
		Name:      d.Name,
		Key:       d.Key,
		KeyHint:   d.KeyHint,
		PlainKey:  d.PlainKey,
		Balance:   d.Balance,
		TotalUsed: d.TotalUsed,
		RateLimit: d.RateLimit,
		Status:    d.Status,
	}
	m.ID = d.ID
	return m
}
