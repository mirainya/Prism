package service

import (
	"errors"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var (
	ErrInsufficientTokenBalance = errors.New("insufficient token balance")
	ErrInsufficientUserBalance  = errors.New("insufficient user balance")
	ErrDuplicateDeduction       = errors.New("duplicate deduction")
	ErrInvalidBillingSettlement = errors.New("invalid billing settlement")
	ErrInvalidBalanceAmount     = errors.New("balance amount must be positive")
	ErrLegacyBillingDisabled    = errors.New("legacy billing path is disabled")
)

// BillingService remains only as an explicit rejection boundary for legacy
// callers. Unified calls reserve and settle through gateway/repository.
type BillingService struct{}

type BillingContext struct {
	CallID          string
	AttemptID       uint
	Phase           string
	PricingSnapshot datatypes.JSON
}

func NewBillingService() *BillingService { return &BillingService{} }

func (s *BillingService) Deduct(tokenID, userID uint, amount decimal.Decimal) error {
	return s.DeductWithKey(tokenID, userID, amount, "")
}

func (s *BillingService) DeductWithKey(tokenID, userID uint, amount decimal.Decimal, idempotentKey string) error {
	return s.DeductWithBillingContext(tokenID, userID, amount, idempotentKey, BillingContext{})
}

func (s *BillingService) DeductWithBillingContext(
	tokenID, userID uint,
	amount decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
) error {
	if !amount.IsPositive() {
		return nil
	}
	return ErrLegacyBillingDisabled
}

func (s *BillingService) DeductWithBillingContextTx(
	tx *gorm.DB,
	tokenID, userID uint,
	amount decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
) error {
	return s.deductWithBillingContextTx(tx, tokenID, userID, amount, idempotentKey, billingContext)
}

func (s *BillingService) deductWithBillingContextTx(
	_ *gorm.DB,
	_, _ uint,
	amount decimal.Decimal,
	_ string,
	_ BillingContext,
) error {
	if !amount.IsPositive() {
		return nil
	}
	return ErrLegacyBillingDisabled
}

func (s *BillingService) Refund(tokenID, userID uint, amount decimal.Decimal) error {
	return s.RefundWithKey(tokenID, userID, amount, "")
}

func (s *BillingService) RefundWithKey(tokenID, userID uint, amount decimal.Decimal, idempotentKey string) error {
	return s.RefundWithBillingContext(tokenID, userID, amount, idempotentKey, BillingContext{})
}

func (s *BillingService) RefundWithBillingContext(
	_, _ uint,
	amount decimal.Decimal,
	_ string,
	_ BillingContext,
) error {
	if !amount.IsPositive() {
		return nil
	}
	return ErrLegacyBillingDisabled
}

func (s *BillingService) RefundWithBillingContextTx(
	tx *gorm.DB,
	tokenID, userID uint,
	amount decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
) error {
	return s.refundWithBillingContextTx(tx, tokenID, userID, amount, idempotentKey, billingContext)
}

func (s *BillingService) refundWithBillingContextTx(
	_ *gorm.DB,
	_, _ uint,
	amount decimal.Decimal,
	_ string,
	_ BillingContext,
) error {
	if !amount.IsPositive() {
		return nil
	}
	return ErrLegacyBillingDisabled
}

func (s *BillingService) SettleReservation(
	tokenID, userID uint,
	reserved, actual decimal.Decimal,
	idempotentKey string,
) error {
	return s.SettleReservationWithBillingContext(tokenID, userID, reserved, actual, idempotentKey, BillingContext{})
}

func (s *BillingService) SettleReservationWithBillingContext(
	_, _ uint,
	reserved, actual decimal.Decimal,
	_ string,
	_ BillingContext,
) error {
	if reserved.IsNegative() || actual.IsNegative() {
		return ErrInvalidBillingSettlement
	}
	if reserved.IsZero() && actual.IsZero() {
		return nil
	}
	return ErrLegacyBillingDisabled
}

func (s *BillingService) SettleReservationWithBillingContextTx(
	tx *gorm.DB,
	tokenID, userID uint,
	reserved, actual decimal.Decimal,
	idempotentKey string,
	billingContext BillingContext,
) error {
	return s.settleReservationWithBillingContextTx(tx, tokenID, userID, reserved, actual, idempotentKey, billingContext)
}

func (s *BillingService) settleReservationWithBillingContextTx(
	_ *gorm.DB,
	_, _ uint,
	reserved, actual decimal.Decimal,
	_ string,
	_ BillingContext,
) error {
	if reserved.IsNegative() || actual.IsNegative() {
		return ErrInvalidBillingSettlement
	}
	if reserved.IsZero() && actual.IsZero() {
		return nil
	}
	return ErrLegacyBillingDisabled
}
