package service

import (
	"errors"

	"github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInsufficientTokenBalance = errors.New("insufficient token balance")
	ErrInsufficientUserBalance  = errors.New("insufficient user balance")
	ErrDuplicateDeduction       = errors.New("duplicate deduction")
	ErrInvalidBillingSettlement = errors.New("invalid billing settlement")
	ErrInvalidBalanceAmount     = errors.New("balance amount must be positive")
)

type BillingService struct{}

// BillingContext links a balance mutation to the downstream call and concrete
// upstream attempt that caused it.
type BillingContext struct {
	CallID          string
	AttemptID       uint
	Phase           string
	PricingSnapshot datatypes.JSON
}

func NewBillingService() *BillingService {
	return &BillingService{}
}

// Deduct 扣费（无幂等键，兼容旧调用）
func (s *BillingService) Deduct(tokenID uint, userID uint, amount decimal.Decimal) error {
	return s.DeductWithKey(tokenID, userID, amount, "")
}

// DeductWithKey 带幂等键的扣费
func (s *BillingService) DeductWithKey(tokenID uint, userID uint, amount decimal.Decimal, idempotentKey string) error {
	return s.DeductWithBillingContext(tokenID, userID, amount, idempotentKey, BillingContext{})
}

func (s *BillingService) DeductWithBillingContext(
	tokenID uint,
	userID uint,
	amount decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
) error {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	return model.DB().Transaction(func(tx *gorm.DB) error {
		return s.deductWithBillingContextTx(tx, tokenID, userID, amount, idempotentKey, billingContext)
	})
}

func (s *BillingService) deductWithKeyTx(
	tx *gorm.DB,
	tokenID uint,
	userID uint,
	amount decimal.Decimal,
	idempotentKey string,
) error {
	return s.deductWithBillingContextTx(
		tx, tokenID, userID, amount, idempotentKey, BillingContext{},
	)
}

func (s *BillingService) deductWithBillingContextTx(
	tx *gorm.DB,
	tokenID uint,
	userID uint,
	amount decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
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
			CallID:        billingContext.CallID,
			AttemptID:     billingContext.AttemptID,
			Phase:         billingContext.Phase,
			PricingSnapshot: cloneBillingJSON(
				billingContext.PricingSnapshot,
			),
			Amount: amount,
			Type:   model.BillingTypeDeduct,
			Status: "success",
		}
		if err := tx.Create(log).Error; err != nil {
			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return ErrDuplicateDeduction
			}
			return err
		}
	}
	category := BalanceCategoryDeduction
	if billingContext.Phase == model.BillingPhaseReserve {
		category = BalanceCategoryReservation
	}
	if err := recordBalanceEntryTx(tx, balanceEntryRequest{
		AccountType: model.BalanceAccountToken, AccountID: tokenID,
		UserID: userID, TokenID: tokenID, Direction: model.BalanceDirectionDebit,
		Category: category, Amount: amount, SourceKey: idempotentKey,
		CallID: billingContext.CallID, AttemptID: billingContext.AttemptID,
	}); err != nil {
		return err
	}
	if userID > 0 {
		if err := recordBalanceEntryTx(tx, balanceEntryRequest{
			AccountType: model.BalanceAccountUser, AccountID: userID,
			UserID: userID, TokenID: tokenID, Direction: model.BalanceDirectionDebit,
			Category: category, Amount: amount, SourceKey: idempotentKey,
			CallID: billingContext.CallID, AttemptID: billingContext.AttemptID,
		}); err != nil {
			return err
		}
	}
	if err := updateCallForDeduction(tx, billingContext, amount); err != nil {
		return err
	}

	return nil
}

// Refund 退款（无幂等键，兼容旧调用）
func (s *BillingService) Refund(tokenID uint, userID uint, amount decimal.Decimal) error {
	return s.RefundWithKey(tokenID, userID, amount, "")
}

// RefundWithKey 带幂等键的退款
func (s *BillingService) RefundWithKey(tokenID uint, userID uint, amount decimal.Decimal, idempotentKey string) error {
	return s.RefundWithBillingContext(tokenID, userID, amount, idempotentKey, BillingContext{})
}

func (s *BillingService) RefundWithBillingContext(
	tokenID uint,
	userID uint,
	amount decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
) error {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	return model.DB().Transaction(func(tx *gorm.DB) error {
		return s.refundWithBillingContextTx(tx, tokenID, userID, amount, idempotentKey, billingContext)
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
	return s.refundWithBillingContextTx(
		tx, tokenID, userID, amount, idempotentKey, BillingContext{},
	)
}

func (s *BillingService) refundWithBillingContextTx(
	tx *gorm.DB,
	tokenID uint,
	userID uint,
	amount decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
) error {
	if amount.LessThanOrEqual(decimal.Zero) {
		return nil
	}

	if idempotentKey != "" {
		entry := &model.BillingLog{
			IdempotentKey: idempotentKey,
			TokenID:       tokenID,
			UserID:        userID,
			CallID:        billingContext.CallID,
			AttemptID:     billingContext.AttemptID,
			Phase:         billingContext.Phase,
			PricingSnapshot: cloneBillingJSON(
				billingContext.PricingSnapshot,
			),
			Amount: amount,
			Type:   model.BillingTypeRefund,
			Status: "success",
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
	if err := recordBalanceEntryTx(tx, balanceEntryRequest{
		AccountType: model.BalanceAccountToken, AccountID: tokenID,
		UserID: userID, TokenID: tokenID, Direction: model.BalanceDirectionCredit,
		Category: BalanceCategoryRefund, Amount: amount, SourceKey: idempotentKey,
		CallID: billingContext.CallID, AttemptID: billingContext.AttemptID,
	}); err != nil {
		return err
	}
	if userID > 0 {
		if err := recordBalanceEntryTx(tx, balanceEntryRequest{
			AccountType: model.BalanceAccountUser, AccountID: userID,
			UserID: userID, TokenID: tokenID, Direction: model.BalanceDirectionCredit,
			Category: BalanceCategoryRefund, Amount: amount, SourceKey: idempotentKey,
			CallID: billingContext.CallID, AttemptID: billingContext.AttemptID,
		}); err != nil {
			return err
		}
	}
	if err := updateCallForRefund(tx, billingContext, amount); err != nil {
		return err
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
	return s.SettleReservationWithBillingContext(
		tokenID,
		userID,
		reserved,
		actual,
		idempotentKey,
		BillingContext{},
	)
}

func (s *BillingService) SettleReservationWithBillingContext(
	tokenID uint,
	userID uint,
	reserved decimal.Decimal,
	actual decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
) error {
	return model.DB().Transaction(func(tx *gorm.DB) error {
		return s.settleReservationWithBillingContextTx(
			tx,
			tokenID,
			userID,
			reserved,
			actual,
			idempotentKey,
			billingContext,
		)
	})
}

func (s *BillingService) settleReservationWithBillingContextTx(
	tx *gorm.DB,
	tokenID uint,
	userID uint,
	reserved decimal.Decimal,
	actual decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
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
			CallID:        billingContext.CallID,
			AttemptID:     billingContext.AttemptID,
			Phase:         billingContext.Phase,
			PricingSnapshot: cloneBillingJSON(
				billingContext.PricingSnapshot,
			),
			Amount: logAmount,
			Type:   logType,
			Status: "success",
			Remark: "reservation settlement",
		}
		if err := tx.Create(entry).Error; err != nil {
			if isDuplicateBillingKey(err) {
				return nil
			}
			return err
		}
	}

	if !delta.IsZero() {
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
		direction := model.BalanceDirectionDebit
		amount := delta
		if delta.IsNegative() {
			direction = model.BalanceDirectionCredit
			amount = delta.Abs()
		}
		if err := recordBalanceEntryTx(tx, balanceEntryRequest{
			AccountType: model.BalanceAccountToken, AccountID: tokenID,
			UserID: userID, TokenID: tokenID, Direction: direction,
			Category: BalanceCategorySettlement, Amount: amount, SourceKey: idempotentKey,
			CallID: billingContext.CallID, AttemptID: billingContext.AttemptID,
		}); err != nil {
			return err
		}
		if userID > 0 {
			if err := recordBalanceEntryTx(tx, balanceEntryRequest{
				AccountType: model.BalanceAccountUser, AccountID: userID,
				UserID: userID, TokenID: tokenID, Direction: direction,
				Category: BalanceCategorySettlement, Amount: amount, SourceKey: idempotentKey,
				CallID: billingContext.CallID, AttemptID: billingContext.AttemptID,
			}); err != nil {
				return err
			}
		}
	}

	return updateCallForSettlement(tx, billingContext, reserved, actual)
}

func updateCallForDeduction(tx *gorm.DB, billingContext BillingContext, amount decimal.Decimal) error {
	if billingContext.CallID == "" {
		return nil
	}
	column := "final_cost"
	if billingContext.Phase == model.BillingPhaseReserve {
		column = "reserved_amount"
	}
	return updateCallBillingColumns(tx, billingContext.CallID, map[string]any{
		column: gorm.Expr(column+" + ?", amount),
	})
}

func updateCallForRefund(tx *gorm.DB, billingContext BillingContext, amount decimal.Decimal) error {
	if billingContext.CallID == "" {
		return nil
	}
	return updateCallBillingColumns(tx, billingContext.CallID, map[string]any{
		"refunded_amount": gorm.Expr("refunded_amount + ?", amount),
	})
}

func updateCallForSettlement(
	tx *gorm.DB,
	billingContext BillingContext,
	reserved decimal.Decimal,
	actual decimal.Decimal,
) error {
	if billingContext.CallID == "" {
		return nil
	}
	updates := map[string]any{
		"final_cost": gorm.Expr("final_cost + ?", actual),
	}
	if refund := reserved.Sub(actual); refund.IsPositive() {
		updates["refunded_amount"] = gorm.Expr("refunded_amount + ?", refund)
	}
	return updateCallBillingColumns(tx, billingContext.CallID, updates)
}

func updateCallBillingColumns(tx *gorm.DB, callID string, updates map[string]any) error {
	result := tx.Model(&model.APICall{}).Where("id = ?", callID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAPICallNotFound
	}
	return nil
}

func cloneBillingJSON(value datatypes.JSON) datatypes.JSON {
	if len(value) == 0 {
		return nil
	}
	return append(datatypes.JSON(nil), value...)
}

func isDuplicateBillingKey(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
