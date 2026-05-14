package repository

import (
	"context"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

type BillingRepo struct {
	db *gorm.DB
}

func NewBillingRepo(db *gorm.DB) *BillingRepo {
	return &BillingRepo{db: db}
}

func (r *BillingRepo) Deduct(ctx context.Context, log *domain.BillingLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 幂等检查
		var existing model.BillingLog
		if err := tx.Where("idempotent_key = ?", log.IdempotentKey).First(&existing).Error; err == nil {
			return domain.ErrIdempotentConflict
		}

		// 检查余额
		var token model.Token
		if err := tx.First(&token, log.TokenID).Error; err != nil {
			return err
		}
		if token.Balance.LessThan(log.Amount) {
			return domain.ErrInsufficientBalance
		}

		// 扣费
		if err := tx.Model(&token).UpdateColumn("balance", gorm.Expr("balance - ?", log.Amount)).Error; err != nil {
			return err
		}

		// 记录
		m := &model.BillingLog{
			IdempotentKey: log.IdempotentKey,
			TokenID:       log.TokenID,
			UserID:        log.UserID,
			Amount:        log.Amount,
			Type:          model.BillingTypeDeduct,
			Status:        "success",
			Remark:        log.Remark,
		}
		return tx.Create(m).Error
	})
}

func (r *BillingRepo) Refund(ctx context.Context, log *domain.BillingLog) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.BillingLog
		if err := tx.Where("idempotent_key = ?", log.IdempotentKey).First(&existing).Error; err == nil {
			return domain.ErrIdempotentConflict
		}

		if err := tx.Model(&model.Token{}).Where("id = ?", log.TokenID).
			UpdateColumn("balance", gorm.Expr("balance + ?", log.Amount)).Error; err != nil {
			return err
		}

		m := &model.BillingLog{
			IdempotentKey: log.IdempotentKey,
			TokenID:       log.TokenID,
			UserID:        log.UserID,
			Amount:        log.Amount,
			Type:          model.BillingTypeRefund,
			Status:        "success",
			Remark:        log.Remark,
		}
		return tx.Create(m).Error
	})
}

func (r *BillingRepo) ListByToken(ctx context.Context, tokenID uint, limit int) ([]domain.BillingLog, error) {
	var list []model.BillingLog
	q := r.db.WithContext(ctx).Where("token_id = ?", tokenID).Order("id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}

	result := make([]domain.BillingLog, len(list))
	for i, m := range list {
		result[i] = domain.BillingLog{
			ID:            m.ID,
			IdempotentKey: m.IdempotentKey,
			TokenID:       m.TokenID,
			UserID:        m.UserID,
			Amount:        m.Amount,
			Type:          domain.BillingType(m.Type),
			Status:        m.Status,
			Remark:        m.Remark,
			CreatedAt:     m.CreatedAt,
		}
	}
	return result, nil
}

var _ domain.BillingRepository = (*BillingRepo)(nil)
var _ domain.TokenRepository = (*TokenRepo)(nil)
var _ domain.UserRepository = (*UserRepo)(nil)
var _ domain.TaskRepository = (*TaskRepo)(nil)
var _ domain.ChannelRepository = (*ChannelRepo)(nil)
var _ domain.ChannelAccountRepository = (*ChannelAccountRepo)(nil)
