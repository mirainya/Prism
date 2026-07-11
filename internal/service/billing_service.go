package service

import (
	"errors"

	"github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInsufficientTokenBalance = errors.New("insufficient token balance")
	ErrInsufficientUserBalance  = errors.New("insufficient user balance")
	ErrDuplicateDeduction       = errors.New("duplicate deduction")
	ErrInvalidBillingSettlement = errors.New("invalid billing settlement")
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
		return s.deductWithKeyTx(tx, tokenID, userID, amount, idempotentKey)
	})
}

func (s *BillingService) deductWithKeyTx(
	tx *gorm.DB,
	tokenID uint,
	userID uint,
	amount decimal.Decimal,
	idempotentKey string,
) error {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	if idempotentKey != "" {
		var count int64
		if err := tx.Model(&model.BillingLog{}).
			Where("idempotent_key = ? AND type = ?", idempotentKey, model.BillingTypeDeduct).
			Count(&count).Error; err != nil {
			return err
		}
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
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return ErrDuplicateDeduction
			}
			return err
		}
	}

	return nil
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
		return s.refundWithKeyTx(tx, tokenID, userID, amount, idempotentKey)
	})
}

// refundWithKeyTx applies a refund inside the caller's transaction. The
// idempotency record is reserved before balances are changed, so concurrent
// retries cannot credit the same refund twice.
func (s *BillingService) refundWithKeyTx(
	tx *gorm.DB,
	tokenID uint,
	userID uint,
	amount decimal.Decimal,
	idempotentKey string,
) error {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	if idempotentKey != "" {
		entry := &model.BillingLog{
			IdempotentKey: idempotentKey,
			TokenID:       tokenID,
			UserID:        userID,
			Amount:        amount,
			Type:          model.BillingTypeRefund,
			Status:        "success",
		}
		reserved := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(entry)
		if reserved.Error != nil {
			return reserved.Error
		}
		if reserved.RowsAffected == 0 {
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
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	if userID > 0 {
		userResult := tx.Model(&model.User{}).Where("id = ?", userID).
			UpdateColumn("balance", gorm.Expr("balance + ?", amount))
		if userResult.Error != nil {
			return userResult.Error
		}
		if userResult.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
	}

	return nil
}

// SettleReservation adjusts an earlier deduction to the final charge.
// A positive delta is recorded even when it makes the balance negative: the
// request was already served, so accounting must not silently drop the charge.
func (s *BillingService) SettleReservation(
	tokenID uint,
	userID uint,
	reserved decimal.Decimal,
	actual decimal.Decimal,
	idempotentKey string,
) error {
	if reserved.IsNegative() || actual.IsNegative() {
		return ErrInvalidBillingSettlement
	}

	delta := actual.Sub(reserved)
	logType := model.BillingTypeDeduct
	logAmount := delta
	if delta.IsNegative() {
		logType = model.BillingTypeRefund
		logAmount = delta.Abs()
	}

	return model.DB().Transaction(func(tx *gorm.DB) error {
		if idempotentKey != "" {
			var count int64
			if err := tx.Model(&model.BillingLog{}).
				Where("idempotent_key = ?", idempotentKey).
				Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return nil
			}

			entry := &model.BillingLog{
				IdempotentKey: idempotentKey,
				TokenID:       tokenID,
				UserID:        userID,
				Amount:        logAmount,
				Type:          logType,
				Status:        "success",
				Remark:        "reservation settlement",
			}
			if err := tx.Create(entry).Error; err != nil {
				if isDuplicateBillingKey(err) {
					return nil
				}
				return err
			}
		}

		if delta.IsZero() {
			return nil
		}

		tokenUpdates := map[string]any{
			"balance":    gorm.Expr("balance - ?", delta),
			"total_used": gorm.Expr("total_used + ?", delta),
		}
		result := tx.Model(&model.Token{}).Where("id = ?", tokenID).Updates(tokenUpdates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		if userID > 0 {
			userResult := tx.Model(&model.User{}).Where("id = ?", userID).
				UpdateColumn("balance", gorm.Expr("balance - ?", delta))
			if userResult.Error != nil {
				return userResult.Error
			}
			if userResult.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
		}

		return nil
	})
}

func isDuplicateBillingKey(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
