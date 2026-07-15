package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

const (
	BalanceAccountUser  = "user"
	BalanceAccountToken = "token"

	BalanceDirectionDebit  = "debit"
	BalanceDirectionCredit = "credit"
)

// BalanceEntry is the append-only ledger for every persisted balance mutation.
type BalanceEntry struct {
	ID            uint64          `gorm:"primaryKey" json:"id"`
	EntryKey      string          `gorm:"type:varchar(100);not null;uniqueIndex" json:"entry_key"`
	SourceKey     string          `gorm:"type:varchar(128);not null;default:'';index" json:"source_key"`
	AccountType   string          `gorm:"type:varchar(16);not null;index:idx_balance_account_created,priority:1" json:"account_type"`
	AccountID     uint            `gorm:"not null;index:idx_balance_account_created,priority:2" json:"account_id"`
	UserID        uint            `gorm:"not null;default:0;index:idx_balance_user_created,priority:1" json:"user_id"`
	TokenID       uint            `gorm:"not null;default:0;index:idx_balance_token_created,priority:1" json:"token_id"`
	Direction     string          `gorm:"type:varchar(10);not null;index" json:"direction"`
	Category      string          `gorm:"type:varchar(32);not null;index" json:"category"`
	Amount        decimal.Decimal `gorm:"type:decimal(20,8);not null" json:"amount"`
	BalanceBefore decimal.Decimal `gorm:"type:decimal(20,8);not null" json:"balance_before"`
	BalanceAfter  decimal.Decimal `gorm:"type:decimal(20,8);not null" json:"balance_after"`
	CallID        string          `gorm:"type:varchar(64);not null;default:'';index" json:"call_id"`
	AttemptID     uint            `gorm:"not null;default:0;index" json:"attempt_id"`
	ActorUserID   uint            `gorm:"not null;default:0;index" json:"actor_user_id"`
	Metadata      datatypes.JSON  `gorm:"type:json" json:"metadata,omitempty"`
	CreatedAt     time.Time       `gorm:"not null;index;index:idx_balance_account_created,priority:3;index:idx_balance_user_created,priority:2;index:idx_balance_token_created,priority:2" json:"created_at"`
}

func (BalanceEntry) TableName() string { return "balance_entries" }

// APIAccessLog stores request metadata for every API request without bodies or credentials.
type APIAccessLog struct {
	ID         uint64    `gorm:"primaryKey" json:"id"`
	RequestID  string    `gorm:"type:varchar(128);not null;default:'';index" json:"request_id"`
	CallID     string    `gorm:"type:varchar(64);not null;default:'';index" json:"call_id"`
	UserID     uint      `gorm:"not null;default:0;index:idx_api_access_user_created,priority:1" json:"user_id"`
	TokenID    uint      `gorm:"not null;default:0;index:idx_api_access_token_created,priority:1" json:"token_id"`
	ActorType  string    `gorm:"type:varchar(16);not null;default:'anonymous';index" json:"actor_type"`
	Method     string    `gorm:"type:varchar(10);not null;index" json:"method"`
	Path       string    `gorm:"type:varchar(500);not null" json:"path"`
	Route      string    `gorm:"type:varchar(500);not null;default:'';index" json:"route"`
	Query      string    `gorm:"type:text" json:"query"`
	StatusCode int       `gorm:"not null;default:0;index" json:"status_code"`
	DurationMs int64     `gorm:"not null;default:0" json:"duration_ms"`
	IP         string    `gorm:"type:varchar(64);not null;default:''" json:"ip"`
	UserAgent  string    `gorm:"type:varchar(512);not null;default:''" json:"user_agent"`
	ErrorCode  string    `gorm:"type:varchar(128);not null;default:'';index" json:"error_code"`
	CreatedAt  time.Time `gorm:"not null;index;index:idx_api_access_user_created,priority:2;index:idx_api_access_token_created,priority:2" json:"created_at"`
}

func (APIAccessLog) TableName() string { return "api_access_logs" }

// AuditEvent is an append-only record of state-changing console operations.
type AuditEvent struct {
	ID           uint64         `gorm:"primaryKey" json:"id"`
	RequestID    string         `gorm:"type:varchar(128);not null;default:'';index" json:"request_id"`
	ActorType    string         `gorm:"type:varchar(16);not null;default:'anonymous';index" json:"actor_type"`
	ActorUserID  uint           `gorm:"not null;default:0;index:idx_audit_actor_created,priority:1" json:"actor_user_id"`
	ActorTokenID uint           `gorm:"not null;default:0;index" json:"actor_token_id"`
	Action       string         `gorm:"type:varchar(128);not null;index" json:"action"`
	ResourceType string         `gorm:"type:varchar(64);not null;default:'';index" json:"resource_type"`
	ResourceID   string         `gorm:"type:varchar(128);not null;default:'';index" json:"resource_id"`
	Outcome      string         `gorm:"type:varchar(16);not null;index" json:"outcome"`
	HTTPStatus   int            `gorm:"not null;default:0" json:"http_status"`
	IP           string         `gorm:"type:varchar(64);not null;default:''" json:"ip"`
	Metadata     datatypes.JSON `gorm:"type:json" json:"metadata,omitempty"`
	CreatedAt    time.Time      `gorm:"not null;index;index:idx_audit_actor_created,priority:2" json:"created_at"`
}

func (AuditEvent) TableName() string { return "audit_events" }
