package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const (
	BalanceCategoryInitialCredit  = "initial_credit"
	BalanceCategoryOpeningBalance = "opening_balance"
	BalanceCategoryRecharge       = "recharge"
	BalanceCategoryDeduction      = "deduction"
	BalanceCategoryReservation    = "reservation"
	BalanceCategorySettlement     = "settlement"
	BalanceCategoryRefund         = "refund"
)

type balanceEntryRequest struct {
	AccountType string
	AccountID   uint
	UserID      uint
	TokenID     uint
	Direction   string
	Category    string
	Amount      decimal.Decimal
	SourceKey   string
	CallID      string
	AttemptID   uint
	ActorUserID uint
	Metadata    datatypes.JSON
}

func recordBalanceEntryTx(tx *gorm.DB, req balanceEntryRequest) error {
	if tx == nil {
		return errorsNewDatabaseRequired()
	}
	if req.AccountID == 0 || !req.Amount.IsPositive() {
		return ErrInvalidBalanceAmount
	}
	if req.AccountType != model.BalanceAccountUser && req.AccountType != model.BalanceAccountToken {
		return fmt.Errorf("invalid balance account type %q", req.AccountType)
	}
	if req.Direction != model.BalanceDirectionDebit && req.Direction != model.BalanceDirectionCredit {
		return fmt.Errorf("invalid balance direction %q", req.Direction)
	}

	after, err := loadAccountBalanceTx(tx, req.AccountType, req.AccountID)
	if err != nil {
		return err
	}
	before := after.Sub(req.Amount)
	if req.Direction == model.BalanceDirectionDebit {
		before = after.Add(req.Amount)
	}

	entry := &model.BalanceEntry{
		EntryKey:      balanceEntryKey(req),
		SourceKey:     truncateBalanceSourceKey(req.SourceKey),
		AccountType:   req.AccountType,
		AccountID:     req.AccountID,
		UserID:        req.UserID,
		TokenID:       req.TokenID,
		Direction:     req.Direction,
		Category:      req.Category,
		Amount:        req.Amount,
		BalanceBefore: before,
		BalanceAfter:  after,
		CallID:        req.CallID,
		AttemptID:     req.AttemptID,
		ActorUserID:   req.ActorUserID,
		Metadata:      append(datatypes.JSON(nil), req.Metadata...),
	}
	return tx.Create(entry).Error
}

func loadAccountBalanceTx(tx *gorm.DB, accountType string, accountID uint) (decimal.Decimal, error) {
	var row struct {
		Balance decimal.Decimal
	}
	query := tx.Select("balance")
	if accountType == model.BalanceAccountUser {
		if err := query.Model(&model.User{}).Where("id = ?", accountID).Take(&row).Error; err != nil {
			return decimal.Zero, err
		}
		return row.Balance, nil
	}
	if err := query.Model(&model.Token{}).Where("id = ?", accountID).Take(&row).Error; err != nil {
		return decimal.Zero, err
	}
	return row.Balance, nil
}

func balanceEntryKey(req balanceEntryRequest) string {
	sourceKey := strings.TrimSpace(req.SourceKey)
	if sourceKey == "" {
		return "bal_" + uuid.NewString()
	}
	payload := fmt.Sprintf("%s:%d:%s:%s:%s", req.AccountType, req.AccountID, req.Direction, req.Category, sourceKey)
	digest := sha256.Sum256([]byte(payload))
	return "bal_" + hex.EncodeToString(digest[:])
}

func truncateBalanceSourceKey(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 128 {
		return value
	}
	return value[:128]
}

func errorsNewDatabaseRequired() error {
	return fmt.Errorf("database is required")
}
