package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

type APICallStatus string

const (
	APICallStatusReceived   APICallStatus = "received"
	APICallStatusInProgress APICallStatus = "in_progress"
	APICallStatusCompleted  APICallStatus = "completed"
	APICallStatusFailed     APICallStatus = "failed"
	APICallStatusCancelled  APICallStatus = "cancelled"
)

type APICallAttemptStatus string

const (
	APICallAttemptStatusStarted   APICallAttemptStatus = "started"
	APICallAttemptStatusCompleted APICallAttemptStatus = "completed"
	APICallAttemptStatusFailed    APICallAttemptStatus = "failed"
	APICallAttemptStatusCancelled APICallAttemptStatus = "cancelled"
)

const (
	APICallPayloadRequest          = "request"
	APICallPayloadResponse         = "response"
	APICallPayloadUpstreamRequest  = "upstream_request"
	APICallPayloadUpstreamResponse = "upstream_response"

	BillingPhaseReserve = "reserve"
	BillingPhaseSettle  = "settle"
	BillingPhaseRefund  = "refund"

	APICallRouteGatewayV2  = "gateway_v2"
	APICallRouteCapability = "capability"
	APICallRouteVideo      = "video"
	APICallStageSubmit     = "submit"
	APICallStagePoll       = "poll"
)

// APICall is one downstream API invocation, independent of upstream retries.
type APICall struct {
	ID        string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	RequestID string `gorm:"type:varchar(128);not null;default:'';index" json:"request_id"`
	UserID    uint   `gorm:"not null;index:idx_api_calls_user_created,priority:1" json:"user_id"`
	TokenID   uint   `gorm:"not null;index:idx_api_calls_token_created,priority:1" json:"token_id"`

	Endpoint         string        `gorm:"type:varchar(255);not null;default:'';index" json:"endpoint"`
	Operation        string        `gorm:"type:varchar(32);not null;default:'';index" json:"operation"`
	Model            string        `gorm:"type:varchar(80);not null;default:'';index" json:"model"`
	Status           APICallStatus `gorm:"type:varchar(24);not null;default:'received';index:idx_api_calls_status_created,priority:1" json:"status"`
	IsStream         bool          `gorm:"not null;default:false" json:"is_stream"`
	Background       bool          `gorm:"not null;default:false" json:"background"`
	Store            bool          `gorm:"not null;default:false" json:"store"`
	RetainPayload    bool          `gorm:"not null;default:false" json:"retain_payload"`
	PayloadExpiresAt *time.Time    `gorm:"index" json:"payload_expires_at"`

	ResourceType        string `gorm:"type:varchar(32);not null;default:'';index:idx_api_calls_resource,priority:1" json:"resource_type"`
	ResourceID          string `gorm:"type:varchar(64);not null;default:'';index:idx_api_calls_resource,priority:2" json:"resource_id"`
	ConversationID      uint   `gorm:"not null;default:0;index" json:"conversation_id"`
	ProjectConversation bool   `gorm:"not null;default:false" json:"project_conversation"`
	FinalAttemptID      uint   `gorm:"not null;default:0;index" json:"final_attempt_id"`
	AttemptCount        int    `gorm:"not null;default:0" json:"attempt_count"`

	InputTokens           int            `gorm:"not null;default:0" json:"input_tokens"`
	OutputTokens          int            `gorm:"not null;default:0" json:"output_tokens"`
	TotalTokens           int            `gorm:"not null;default:0" json:"total_tokens"`
	CachedInputTokens     int            `gorm:"not null;default:0" json:"cached_input_tokens"`
	ReasoningOutputTokens int            `gorm:"not null;default:0" json:"reasoning_output_tokens"`
	UsageJSON             datatypes.JSON `gorm:"type:json" json:"usage,omitempty"`

	ReservedAmount decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0" json:"reserved_amount"`
	FinalCost      decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0" json:"final_cost"`
	RefundedAmount decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0" json:"refunded_amount"`

	HTTPStatus     int            `gorm:"not null;default:0" json:"http_status"`
	ErrorType      string         `gorm:"type:varchar(64);not null;default:''" json:"error_type"`
	ErrorCode      string         `gorm:"type:varchar(128);not null;default:'';index" json:"error_code"`
	ErrorMessage   string         `gorm:"type:text" json:"error_message"`
	ErrorParam     datatypes.JSON `gorm:"type:json" json:"error_param,omitempty"`
	ErrorRetryable bool           `gorm:"not null;default:false" json:"error_retryable"`

	StartedAt          time.Time  `gorm:"not null;index" json:"started_at"`
	FirstByteAt        *time.Time `json:"first_byte_at"`
	CompletedAt        *time.Time `json:"completed_at"`
	DurationMs         int64      `gorm:"not null;default:0" json:"duration_ms"`
	TTFTMs             int64      `gorm:"not null;default:0" json:"ttft_ms"`
	ClientDisconnected bool       `gorm:"not null;default:false" json:"client_disconnected"`
	LeaseOwner         string     `gorm:"type:varchar(64);not null;default:''" json:"-"`
	LeaseExpiresAt     *time.Time `gorm:"index" json:"-"`
	CreatedAt          time.Time  `gorm:"not null;index;index:idx_api_calls_user_created,priority:2;index:idx_api_calls_token_created,priority:2;index:idx_api_calls_status_created,priority:2" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"not null;index" json:"updated_at"`
}

func (APICall) TableName() string { return "api_calls" }

// APICallAttempt records one concrete upstream route and transport execution.
type APICallAttempt struct {
	ID          uint                 `gorm:"primaryKey" json:"id"`
	CallID      string               `gorm:"type:varchar(64);not null;uniqueIndex:idx_api_call_attempt_no,priority:1" json:"call_id"`
	AttemptNo   int                  `gorm:"not null;uniqueIndex:idx_api_call_attempt_no,priority:2" json:"attempt_no"`
	RouteKind   string               `gorm:"type:varchar(24);not null;default:'gateway_v2';index" json:"route_kind"`
	Stage       string               `gorm:"type:varchar(24);not null;default:'';index" json:"stage"`
	AbilityID   uint                 `gorm:"not null;default:0;index" json:"ability_id"`
	ChannelID   uint                 `gorm:"not null;default:0;index" json:"channel_id"`
	KeyID       uint                 `gorm:"not null;default:0;index" json:"key_id"`
	EndpointID  uint                 `gorm:"not null;default:0;index" json:"endpoint_id"`
	AccountID   uint                 `gorm:"not null;default:0;index" json:"account_id"`
	Protocol    Protocol             `gorm:"type:varchar(20);not null;default:''" json:"protocol"`
	VendorModel string               `gorm:"type:varchar(120);not null;default:''" json:"vendor_model"`
	Transport   UpstreamTransport    `gorm:"type:varchar(64);not null;default:'';index" json:"transport"`
	RequestPath string               `gorm:"type:varchar(255);not null;default:''" json:"request_path"`
	Status      APICallAttemptStatus `gorm:"type:varchar(24);not null;default:'started';index" json:"status"`

	HTTPStatus     int    `gorm:"not null;default:0" json:"http_status"`
	ErrorType      string `gorm:"type:varchar(64);not null;default:''" json:"error_type"`
	ErrorCode      string `gorm:"type:varchar(128);not null;default:'';index" json:"error_code"`
	ErrorMessage   string `gorm:"type:text" json:"error_message"`
	ErrorRetryable bool   `gorm:"not null;default:false" json:"error_retryable"`

	InputTokens           int            `gorm:"not null;default:0" json:"input_tokens"`
	OutputTokens          int            `gorm:"not null;default:0" json:"output_tokens"`
	TotalTokens           int            `gorm:"not null;default:0" json:"total_tokens"`
	CachedInputTokens     int            `gorm:"not null;default:0" json:"cached_input_tokens"`
	ReasoningOutputTokens int            `gorm:"not null;default:0" json:"reasoning_output_tokens"`
	UsageJSON             datatypes.JSON `gorm:"type:json" json:"usage,omitempty"`

	DurationMs         int64      `gorm:"not null;default:0" json:"duration_ms"`
	TTFTMs             int64      `gorm:"not null;default:0" json:"ttft_ms"`
	ProviderResponseID string     `gorm:"type:varchar(128);not null;default:'';index" json:"provider_response_id"`
	StartedAt          time.Time  `gorm:"not null;index" json:"started_at"`
	FirstByteAt        *time.Time `json:"first_byte_at"`
	CompletedAt        *time.Time `json:"completed_at"`
	CreatedAt          time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"not null" json:"updated_at"`
}

func (APICallAttempt) TableName() string { return "api_call_attempts" }

// APICallPayload stores retained, redacted request or response content.
type APICallPayload struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	CallID        string     `gorm:"type:varchar(64);not null;index:idx_api_call_payload_kind,priority:1" json:"call_id"`
	AttemptID     uint       `gorm:"not null;default:0;index" json:"attempt_id"`
	Kind          string     `gorm:"type:varchar(32);not null;index:idx_api_call_payload_kind,priority:2" json:"kind"`
	ContentType   string     `gorm:"type:varchar(128);not null;default:'application/json'" json:"content_type"`
	Data          []byte     `gorm:"type:longblob" json:"-"`
	Encrypted     bool       `gorm:"not null;default:false" json:"encrypted"`
	Truncated     bool       `gorm:"not null;default:false" json:"truncated"`
	OriginalBytes int64      `gorm:"not null;default:0" json:"original_bytes"`
	ExpiresAt     *time.Time `gorm:"index" json:"expires_at"`
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"not null" json:"updated_at"`
}

func (APICallPayload) TableName() string { return "api_call_payloads" }
