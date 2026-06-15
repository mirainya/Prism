package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// Conversation 对话
type Conversation struct {
	BaseModel
	UserID           uint   `gorm:"not null;index;comment:用户ID" json:"user_id"`
	TokenID          uint   `gorm:"not null;index;comment:Token ID" json:"token_id"`
	Title            string `gorm:"type:varchar(200);comment:对话标题" json:"title"`
	Model            string `gorm:"type:varchar(50);comment:最后使用模型" json:"model"`
	SystemPrompt     string `gorm:"type:text;comment:系统提示词" json:"system_prompt"`
	LastRequestLogID uint   `gorm:"default:0;index;comment:最近一次请求日志ID" json:"last_request_log_id"`
	LastStatus       string `gorm:"type:varchar(20);comment:最近一次请求状态" json:"last_status"`
	ProviderResponseID string `gorm:"type:varchar(128);default:'';comment:上游有状态对话ID(如火山response_id)" json:"provider_response_id"`
	TotalTokens      int    `gorm:"default:0;comment:累计token" json:"total_tokens"`
	MessageCount     int    `gorm:"default:0;comment:消息数量" json:"message_count"`
	Status           int8   `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`
}

func (Conversation) TableName() string {
	return "conversations"
}

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
	Cost             decimal.Decimal `gorm:"type:decimal(10,6);default:0;comment:费用" json:"cost"`
	CreatedAt        time.Time       `gorm:"index:idx_conversation_created;comment:创建时间" json:"created_at"`
}

func (Message) TableName() string {
	return "messages"
}
