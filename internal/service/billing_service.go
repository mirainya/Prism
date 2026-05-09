package service

import (
	"errors"

	"github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	ErrInsufficientTokenBalance = errors.New("insufficient token balance")
	ErrInsufficientUserBalance  = errors.New("insufficient user balance")
	ErrDuplicateDeduction       = errors.New("duplicate deduction")
)

type BillingService struct{}

func NewBillingService() *BillingService {
	return &BillingService{}
}

// Deduct 扣费（无幂等键，兼容旧调用）
func (s *BillingService) Deduct(tokenID uint, userID uint, amount decimal.Decimal) error {
	return s.DeductWithKey(tokenID, userID, amount, "")
}

// DeductWithKey 带幂等键的扣费
func (s *BillingService) DeductWithKey(tokenID uint, userID uint, amount decimal.Decimal, idempotentKey string) error {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	return model.DB().Transaction(func(tx *gorm.DB) error {
		// 幂等检查：如果提供了幂等键，先检查是否已扣费
		if idempotentKey != "" {
			var count int64
			tx.Model(&model.BillingLog{}).
				Where("idempotent_key = ? AND type = ?", idempotentKey, model.BillingTypeDeduct).
				Count(&count)
			if count > 0 {
				return ErrDuplicateDeduction
			}
		}

		result := tx.Model(&model.Token{}).Where("id = ? AND balance >= ?", tokenID, amount).Updates(map[string]any{
			"balance":    gorm.Expr("balance - ?", amount),
			"total_used": gorm.Expr("total_used + ?", amount),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrInsufficientTokenBalance
		}

		if userID > 0 {
			userResult := tx.Model(&model.User{}).Where("id = ? AND balance >= ?", userID, amount).
				UpdateColumn("balance", gorm.Expr("balance - ?", amount))
			if userResult.Error != nil {
				return userResult.Error
			}
			if userResult.RowsAffected == 0 {
				return ErrInsufficientUserBalance
			}
		}

		// 记录扣费流水
		if idempotentKey != "" {
			log := &model.BillingLog{
				IdempotentKey: idempotentKey,
				TokenID:       tokenID,
				UserID:        userID,
				Amount:        amount,
				Type:          model.BillingTypeDeduct,
				Status:        "success",
			}
			if err := tx.Create(log).Error; err != nil {
				// MySQL 唯一索引冲突 error code 1062
				var mysqlErr *mysql.MySQLError
				if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
					return ErrDuplicateDeduction
				}
				return err
			}
		}

		return nil
	})
}

// Refund 退款（无幂等键，兼容旧调用）
func (s *BillingService) Refund(tokenID uint, userID uint, amount decimal.Decimal) error {
	return s.RefundWithKey(tokenID, userID, amount, "")
}

// RefundWithKey 带幂等键的退款
func (s *BillingService) RefundWithKey(tokenID uint, userID uint, amount decimal.Decimal, idempotentKey string) error {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	return model.DB().Transaction(func(tx *gorm.DB) error {
		// 幂等检查
		if idempotentKey != "" {
			var count int64
			tx.Model(&model.BillingLog{}).
				Where("idempotent_key = ? AND type = ?", idempotentKey, model.BillingTypeRefund).
				Count(&count)
			if count > 0 {
				logger.Info("duplicate refund skipped", zap.String("key", idempotentKey))
				return nil
			}
		}

		result := tx.Model(&model.Token{}).Where("id = ?", tokenID).Updates(map[string]any{
			"balance":    gorm.Expr("balance + ?", amount),
			"total_used": gorm.Expr("total_used - ?", amount),
		})
		if result.Error != nil {
			return result.Error
		}

		if userID > 0 {
			userResult := tx.Model(&model.User{}).Where("id = ?", userID).
				UpdateColumn("balance", gorm.Expr("balance + ?", amount))
			if userResult.Error != nil {
				return userResult.Error
			}
		}

		// 记录退款流水
		if idempotentKey != "" {
			log := &model.BillingLog{
				IdempotentKey: idempotentKey,
				TokenID:       tokenID,
				UserID:        userID,
				Amount:        amount,
				Type:          model.BillingTypeRefund,
				Status:        "success",
			}
			if err := tx.Create(log).Error; err != nil {
				var mysqlErr *mysql.MySQLError
				if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
					return nil // 退款重复不报错
				}
				return err
			}
		}

		return nil
	})
}
