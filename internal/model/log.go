package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// RequestType 请求类型
type RequestType string

const (
	RequestTypeSubmit   RequestType = "submit"
	RequestTypePoll     RequestType = "poll"
	RequestTypeCallback RequestType = "callback"
	RequestTypeChat     RequestType = "chat"
)

// ChannelRequestLog 渠道请求日志
type ChannelRequestLog struct {
	BaseModel
	TaskID                uint        `gorm:"index;comment:关联任务ID" json:"task_id"`
	TaskNo                string      `gorm:"type:varchar(32);index;comment:任务编号" json:"task_no"`
	ConversationID        uint        `gorm:"index;comment:关联对话ID(Chat)" json:"conversation_id"`
	ChannelID             uint        `gorm:"index;index:idx_channel_request_at;comment:渠道ID" json:"channel_id"`
	AccountID             uint        `gorm:"comment:渠道账号ID" json:"account_id"`
	CapabilityCode        string      `gorm:"type:varchar(50);index;index:idx_capability_request_at;comment:能力编码或模型编码" json:"capability_code"`
	RequestType           RequestType `gorm:"type:varchar(20);index;comment:请求类型" json:"request_type"`
	IsStream              bool        `gorm:"default:false;comment:是否流式请求" json:"is_stream"`
	ModelCode             string      `gorm:"type:varchar(50);comment:请求模型" json:"model_code"`
	VendorModel           string      `gorm:"type:varchar(100);comment:供应商模型" json:"vendor_model"`
	RequestPath           string      `gorm:"type:varchar(255);comment:实际上游请求路径" json:"request_path"`
	FinishReason          string      `gorm:"type:varchar(50);comment:完成原因" json:"finish_reason"`
	ResponsePreview       string      `gorm:"type:text;comment:响应摘要" json:"response_preview"`
	UsagePromptTokens     int         `gorm:"default:0;comment:输入token摘要" json:"usage_prompt_tokens"`
	UsageCompletionTokens int         `gorm:"default:0;comment:输出token摘要" json:"usage_completion_tokens"`
	UsageTotalTokens      int         `gorm:"default:0;comment:总token摘要" json:"usage_total_tokens"`

	Method         string `gorm:"type:varchar(10);comment:请求方法" json:"method"`
	URL            string `gorm:"type:varchar(500);comment:请求URL" json:"url"`
	RequestHeaders string `gorm:"type:text;comment:请求头(JSON)" json:"request_headers"`
	RequestBody    string `gorm:"type:mediumtext;comment:请求体" json:"request_body"`

	StatusCode   int    `gorm:"comment:HTTP状态码" json:"status_code"`
	ResponseBody string `gorm:"type:mediumtext;comment:响应体" json:"response_body"`

	DurationMs int64 `gorm:"comment:耗时(毫秒)" json:"duration_ms"`

	ErrorMessage string `gorm:"type:text;comment:错误信息" json:"error_message"`

	RequestAt time.Time `gorm:"index;index:idx_channel_request_at;index:idx_capability_request_at;comment:请求时间" json:"request_at"`

	Channel *Channel `gorm:"foreignKey:ChannelID;constraint:-" json:"channel,omitempty"`
	Model   *Model   `gorm:"foreignKey:ModelCode;references:Code;constraint:-" json:"model,omitempty"`
}

func (ChannelRequestLog) TableName() string {
	return "channel_request_logs"
}

// BillingLog 扣费流水表
type BillingLog struct {
	BaseModel
	IdempotentKey string          `gorm:"type:varchar(100);uniqueIndex;not null;comment:幂等键(如task_no)" json:"idempotent_key"`
	TokenID       uint            `gorm:"index;comment:令牌ID" json:"token_id"`
	UserID        uint            `gorm:"index;comment:用户ID" json:"user_id"`
	Amount        decimal.Decimal `gorm:"type:decimal(10,4);not null;comment:金额" json:"amount"`
	Type          BillingType     `gorm:"type:varchar(10);not null;comment:类型(deduct/refund)" json:"type"`
	Status        string          `gorm:"type:varchar(10);default:'success';comment:状态" json:"status"`
	Remark        string          `gorm:"type:varchar(200);comment:备注" json:"remark"`
}

func (BillingLog) TableName() string {
	return "billing_logs"
}

// BillingType 扣费类型
type BillingType string

const (
	BillingTypeDeduct BillingType = "deduct"
	BillingTypeRefund BillingType = "refund"
)
