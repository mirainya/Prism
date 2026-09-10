package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type conversationCallFacts struct {
	Call                  model.APICall
	UnifiedCallID         uint64
	UnifiedFinalAttemptID uint64
	GatewayStatus         string
}

func loadConversationCallFactsTx(tx *gorm.DB, callID string, forUpdate bool) (*conversationCallFacts, error) {
	if tx == nil {
		return nil, fmt.Errorf("%w: database is nil", ErrAPICallInvalidInput)
	}
	return loadUnifiedConversationCallFactsTx(tx, callID, forUpdate)
}

func loadUnifiedConversationCallFactsTx(tx *gorm.DB, callID string, forUpdate bool) (*conversationCallFacts, error) {

	type unifiedRow struct {
		ID             uint64    `gorm:"column:id"`
		PublicID       string    `gorm:"column:public_id"`
		UserID         uint      `gorm:"column:user_id"`
		TokenID        uint      `gorm:"column:token_id"`
		Status         string    `gorm:"column:status"`
		FinalAttemptID uint64    `gorm:"column:final_attempt_id"`
		Model          string    `gorm:"column:model_code"`
		CreatedAt      time.Time `gorm:"column:created_at"`
		UpdatedAt      time.Time `gorm:"column:updated_at"`
	}
	var row unifiedRow
	query := tx.Table("gw_api_calls AS gateway_call").
		Select(`gateway_call.id, gateway_call.public_id, gateway_call.user_id, gateway_call.token_id,
			gateway_call.status, COALESCE(gateway_call.final_attempt_id, 0) AS final_attempt_id,
			gateway_model.model_code, gateway_call.created_at, gateway_call.updated_at`).
		Joins("JOIN gw_model_operations AS model_operation ON model_operation.id = gateway_call.model_operation_id AND model_operation.release_id = gateway_call.catalog_release_id").
		Joins("JOIN gw_catalog_models AS catalog_model ON catalog_model.id = model_operation.catalog_model_id AND catalog_model.release_id = model_operation.release_id").
		Joins("JOIN gw_models AS gateway_model ON gateway_model.id = catalog_model.model_id").
		Where("gateway_call.public_id = ?", callID)
	if forUpdate {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrAPICallNotFound, callID)
		}
		return nil, err
	}

	callStatus := model.APICallStatus(row.Status)
	if row.Status == "indeterminate" {
		callStatus = model.APICallStatusFailed
	}
	call := model.APICall{
		ID: row.PublicID, UserID: row.UserID, TokenID: row.TokenID,
		Model: row.Model, Status: callStatus, ProjectConversation: true,
		StartedAt: row.CreatedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if !row.UpdatedAt.Before(row.CreatedAt) {
		call.DurationMs = row.UpdatedAt.Sub(row.CreatedAt).Milliseconds()
	}
	var cost struct {
		Amount decimal.Decimal `gorm:"column:amount"`
	}
	if err := tx.Table("billing_reservations AS reservation").
		Select("settlement.actual_amount AS amount").
		Joins("JOIN billing_settlements AS settlement ON settlement.reservation_id = reservation.id").
		Where("reservation.call_id = ?", row.ID).Take(&cost).Error; err == nil {
		call.FinalCost = cost.Amount
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return &conversationCallFacts{
		Call: call, UnifiedCallID: row.ID,
		UnifiedFinalAttemptID: row.FinalAttemptID, GatewayStatus: row.Status,
	}, nil
}

func (facts *conversationCallFacts) turnStatus() (model.ConversationTurnStatus, error) {
	if facts == nil {
		return "", errors.New("API call is required")
	}
	switch facts.GatewayStatus {
	case "completed":
		return model.ConversationTurnCompleted, nil
	case "failed", "indeterminate":
		return model.ConversationTurnFailed, nil
	case "cancelled":
		return model.ConversationTurnAborted, nil
	default:
		return "", fmt.Errorf("API call %s is not terminal: %s", facts.Call.ID, facts.GatewayStatus)
	}
}
