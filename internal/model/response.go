package model

import (
	"time"

	"gorm.io/datatypes"
)

type AIResponse struct {
	ID                 string            `gorm:"primaryKey;type:varchar(64)" json:"id"`
	UserID             uint              `gorm:"not null;index:idx_ai_responses_user" json:"user_id"`
	TokenID            uint              `gorm:"not null;index:idx_ai_responses_token;uniqueIndex:idx_ai_responses_idempotency,priority:1" json:"token_id"`
	CallID             string            `gorm:"type:varchar(64);not null;default:'';index:idx_ai_responses_call" json:"call_id"`
	Model              string            `gorm:"type:varchar(80);not null;index:idx_ai_responses_model" json:"model"`
	Status             string            `gorm:"type:varchar(24);not null;index:idx_ai_responses_status" json:"status"`
	Background         bool              `gorm:"default:false" json:"background"`
	Store              bool              `gorm:"default:true" json:"store"`
	PreviousResponseID string            `gorm:"type:varchar(64);not null;default:'';index:idx_ai_responses_previous" json:"previous_response_id"`
	ProviderResponseID string            `gorm:"type:varchar(128);not null;default:''" json:"provider_response_id"`
	ChannelID          uint              `gorm:"default:0" json:"channel_id"`
	KeyID              uint              `gorm:"default:0" json:"key_id"`
	UpstreamTransport  UpstreamTransport `gorm:"type:varchar(64);not null;default:''" json:"upstream_transport"`
	RequestLogID       uint              `gorm:"default:0;index:idx_ai_responses_request_log" json:"request_log_id"`
	RequestJSON        datatypes.JSON    `gorm:"type:json" json:"-"`
	RequestHash        string            `gorm:"type:char(64);not null;default:''" json:"-"`
	LeaseOwner         string            `gorm:"type:varchar(64);not null;default:''" json:"-"`
	LeaseExpiresAt     *time.Time        `gorm:"index:idx_ai_responses_lease" json:"-"`
	ExecutionAttempt   int               `gorm:"not null;default:0" json:"-"`
	ResultReadyAt      *time.Time        `json:"-"`
	InputItems         datatypes.JSON    `gorm:"type:json" json:"-"`
	OutputItems        datatypes.JSON    `gorm:"type:json" json:"-"`
	ResponseJSON       datatypes.JSON    `gorm:"type:json" json:"-"`
	UsageJSON          datatypes.JSON    `gorm:"type:json" json:"-"`
	Metadata           datatypes.JSON    `gorm:"type:json" json:"metadata"`
	ErrorJSON          datatypes.JSON    `gorm:"type:json" json:"-"`
	IdempotencyKey     string            `gorm:"type:varbinary(512);not null;default:'';uniqueIndex:idx_ai_responses_idempotency,priority:2" json:"-"`
	CreatedAt          time.Time         `json:"created_at"`
	CompletedAt        *time.Time        `json:"completed_at"`
}

func (AIResponse) TableName() string { return "ai_responses" }
