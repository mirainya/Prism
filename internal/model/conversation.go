package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

// Conversation 对话
type Conversation struct {
	BaseModel
	UserID                uint              `gorm:"not null;index;comment:用户ID" json:"user_id"`
	TokenID               uint              `gorm:"not null;index;comment:Token ID" json:"token_id"`
	CallID                string            `gorm:"type:varchar(64);not null;default:'';index;comment:最近关联调用ID" json:"call_id"`
	Title                 string            `gorm:"type:varchar(200);comment:对话标题" json:"title"`
	Model                 string            `gorm:"type:varchar(80);comment:最后使用模型" json:"model"`
	SystemPrompt          string            `gorm:"type:longtext;comment:系统提示词" json:"system_prompt"`
	LastRequestLogID      uint              `gorm:"default:0;index;comment:最近一次请求日志ID" json:"last_request_log_id"`
	LastStatus            string            `gorm:"type:varchar(20);comment:最近一次请求状态" json:"last_status"`
	ProviderResponseID    string            `gorm:"type:varchar(128);default:'';index:idx_conversations_provider_response_id;comment:上游有状态对话ID(如火山response_id)" json:"provider_response_id"`
	ProviderKeyID         uint              `gorm:"not null;default:0;comment:上游状态所属网关Key" json:"provider_key_id"`
	UpstreamTransport     UpstreamTransport `gorm:"type:varchar(64);not null;default:'';comment:上游状态所属Transport" json:"upstream_transport"`
	TotalTokens           int               `gorm:"default:0;comment:累计token" json:"total_tokens"`
	MessageCount          int               `gorm:"default:0;comment:消息数量" json:"message_count"`
	CanonicalItemCount    uint64            `gorm:"not null;default:0;comment:可匹配 canonical 项数" json:"-"`
	CanonicalBytes        uint64            `gorm:"not null;default:0;comment:已完成 canonical 正文字节数" json:"-"`
	CanonicalMatchHash    string            `gorm:"type:char(64);not null;default:'';comment:已完成 canonical 滚动匹配哈希" json:"-"`
	CanonicalStateVersion uint8             `gorm:"not null;default:0;comment:canonical 匹配状态版本" json:"-"`
	TurnSequence          uint64            `gorm:"not null;default:0;comment:最后分配的轮次序号" json:"-"`
	Status                int8              `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`
}

func (Conversation) TableName() string {
	return "conversations"
}

const conversationCanonicalMatchIndexName = "idx_conversations_canonical_match"

type conversationCanonicalMatchIndex struct {
	UserID    uint      `gorm:"column:user_id;index:idx_conversations_canonical_match,priority:1"`
	TokenID   uint      `gorm:"column:token_id;index:idx_conversations_canonical_match,priority:2"`
	Status    int8      `gorm:"column:status;index:idx_conversations_canonical_match,priority:3"`
	UpdatedAt time.Time `gorm:"column:updated_at;index:idx_conversations_canonical_match,priority:4"`
	ID        uint      `gorm:"column:id;index:idx_conversations_canonical_match,priority:5"`
}

func (conversationCanonicalMatchIndex) TableName() string { return "conversations" }

// Role 常量
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message 消息
type Message struct {
	ID               uint            `gorm:"primarykey;comment:主键ID" json:"id"`
	ConversationID   uint            `gorm:"not null;index:idx_conversation_created;comment:对话ID" json:"conversation_id"`
	CallID           string          `gorm:"type:varchar(64);not null;default:'';index;comment:关联调用ID" json:"call_id"`
	RequestLogID     uint            `gorm:"default:0;index;comment:关联请求日志ID" json:"request_log_id"`
	Role             string          `gorm:"type:varchar(20);not null;comment:角色" json:"role"`
	Content          string          `gorm:"type:mediumtext;not null;comment:内容" json:"content"`
	Attachments      string          `gorm:"type:mediumtext;comment:多模态附件(JSON)" json:"attachments"`
	ReasoningContent string          `gorm:"type:mediumtext;comment:思考/推理内容" json:"reasoning_content"`
	FinishReason     string          `gorm:"type:varchar(50);comment:完成原因" json:"finish_reason"`
	InputTokens      int             `gorm:"default:0;comment:输入token" json:"input_tokens"`
	OutputTokens     int             `gorm:"default:0;comment:输出token" json:"output_tokens"`
	Model            string          `gorm:"type:varchar(50);comment:使用模型" json:"model"`
	ChannelID        uint            `gorm:"default:0;comment:渠道ID" json:"channel_id"`
	AccountID        uint            `gorm:"default:0;comment:账号ID" json:"account_id"`
	LatencyMs        int             `gorm:"default:0;comment:耗时毫秒" json:"latency_ms"`
	Cost             decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0;comment:费用" json:"cost"`
	CreatedAt        time.Time       `gorm:"index:idx_conversation_created;comment:创建时间" json:"created_at"`
}

func (Message) TableName() string {
	return "messages"
}

type ConversationTurnStatus string

const (
	ConversationTurnCompleted ConversationTurnStatus = "completed"
	ConversationTurnFailed    ConversationTurnStatus = "failed"
	ConversationTurnAborted   ConversationTurnStatus = "aborted"

	ConversationItemInput  = "input"
	ConversationItemOutput = "output"
)

// ConversationTurn records one downstream request within a conversation.
// Execution and billing truth remains in APICall; this row is the durable
// conversation projection used to rebuild ordered session history.
type ConversationTurn struct {
	ID                 uint64                 `gorm:"primaryKey" json:"id"`
	ConversationID     uint                   `gorm:"not null;uniqueIndex:idx_conversation_turn_sequence,priority:1;index:idx_conversation_turn_created,priority:1" json:"conversation_id"`
	Sequence           uint64                 `gorm:"column:turn_sequence;not null;uniqueIndex:idx_conversation_turn_sequence,priority:2" json:"sequence"`
	CallID             string                 `gorm:"type:varchar(64);not null;uniqueIndex" json:"call_id"`
	RequestLogID       uint                   `gorm:"not null;default:0;index" json:"request_log_id"`
	Model              string                 `gorm:"type:varchar(80);not null;default:'';index" json:"model"`
	ProviderResponseID string                 `gorm:"type:varchar(128);not null;default:'';index" json:"provider_response_id"`
	Status             ConversationTurnStatus `gorm:"type:varchar(24);not null;default:'completed';index" json:"status"`
	InputTokens        int                    `gorm:"not null;default:0" json:"input_tokens"`
	OutputTokens       int                    `gorm:"not null;default:0" json:"output_tokens"`
	TotalTokens        int                    `gorm:"not null;default:0" json:"total_tokens"`
	Cost               decimal.Decimal        `gorm:"type:decimal(20,8);not null;default:0" json:"cost"`
	LatencyMs          int64                  `gorm:"not null;default:0" json:"latency_ms"`
	FinishReason       string                 `gorm:"type:varchar(50);not null;default:''" json:"finish_reason"`
	ErrorType          string                 `gorm:"type:varchar(64);not null;default:''" json:"error_type"`
	ErrorCode          string                 `gorm:"type:varchar(128);not null;default:'';index" json:"error_code"`
	ErrorMessage       string                 `gorm:"type:text" json:"error_message"`
	CreatedAt          time.Time              `gorm:"not null;index:idx_conversation_turn_created,priority:2" json:"created_at"`
	UpdatedAt          time.Time              `gorm:"not null" json:"updated_at"`
}

func (ConversationTurn) TableName() string { return "conversation_turns" }

// ConversationItem stores one protocol-neutral canonical item. A turn may
// contain messages, multimodal blocks, reasoning, tool calls, and tool results.
type ConversationItem struct {
	ID             uint64         `gorm:"primaryKey" json:"id"`
	ConversationID uint           `gorm:"not null;index:idx_conversation_items_order,priority:1" json:"conversation_id"`
	TurnID         uint64         `gorm:"not null;uniqueIndex:idx_conversation_item_ordinal,priority:1" json:"turn_id"`
	TurnSequence   uint64         `gorm:"not null;index:idx_conversation_items_order,priority:2" json:"turn_sequence"`
	Direction      string         `gorm:"type:varchar(16);not null;index" json:"direction"`
	Ordinal        int            `gorm:"not null;uniqueIndex:idx_conversation_item_ordinal,priority:2;index:idx_conversation_items_order,priority:3" json:"ordinal"`
	CanonicalJSON  datatypes.JSON `gorm:"column:canonical_json;type:json;not null" json:"canonical"`
	CreatedAt      time.Time      `gorm:"not null" json:"created_at"`
}

func (ConversationItem) TableName() string { return "conversation_items" }
