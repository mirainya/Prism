package model

import "time"

// ConversationProjectionOutbox keeps canonical conversation data durable
// until the terminal API call has been projected into conversation history.
type ConversationProjectionOutbox struct {
	CallID string `gorm:"primaryKey;type:varchar(64)" json:"call_id"`

	ConversationID     uint   `gorm:"not null;default:0;index:idx_conversation_projection_conversation_id" json:"conversation_id"`
	PreviousResponseID string `gorm:"type:varchar(128);not null;default:'';index:idx_conversation_projection_previous_response_id" json:"previous_response_id"`
	RequestLogID       uint   `gorm:"not null;default:0;index:idx_conversation_projection_request_log_id" json:"request_log_id"`
	ProviderResponseID string `gorm:"type:varchar(128);not null;default:''" json:"provider_response_id"`
	FinishReason       string `gorm:"type:varchar(50);not null;default:''" json:"finish_reason"`

	CanonicalInput  []byte `gorm:"column:input_json;type:longblob" json:"-"`
	CanonicalOutput []byte `gorm:"column:output_json;type:longblob" json:"-"`
	InputReady      bool   `gorm:"not null;default:false" json:"input_ready"`
	InputPrepared   bool   `gorm:"not null;default:false" json:"input_prepared"`
	OutputReady     bool   `gorm:"not null;default:false" json:"output_ready"`

	RetryCount    int        `gorm:"not null;default:0" json:"retry_count"`
	LastError     string     `gorm:"type:text" json:"last_error"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	NextAttemptAt *time.Time `gorm:"index:idx_conversation_projection_retry,priority:1" json:"next_attempt_at"`
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"not null;index:idx_conversation_projection_retry,priority:2" json:"updated_at"`
}

func (ConversationProjectionOutbox) TableName() string {
	return "conversation_projection_outbox"
}
