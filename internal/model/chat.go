package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

// Provider 提供商类型
type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderGoogle    Provider = "google"
	ProviderDeepSeek  Provider = "deepseek"
	ProviderQwen      Provider = "qwen"
	ProviderMoonshot    Provider = "moonshot"
	ProviderVolcengine Provider = "volcengine"
)

// ChatModel 语言模型
type ChatModel struct {
	Code        string    `gorm:"primaryKey;type:varchar(50);comment:模型标识" json:"code"`
	Name        string    `gorm:"type:varchar(100);not null;comment:显示名称" json:"name"`
	Provider    Provider  `gorm:"type:varchar(30);not null;comment:提供商类型" json:"provider"`
	Description string    `gorm:"type:varchar(500);comment:模型描述" json:"description"`
	Status      int8      `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`
	CreatedAt   time.Time `gorm:"comment:创建时间" json:"created_at"`
	UpdatedAt   time.Time `gorm:"comment:更新时间" json:"updated_at"`
}

func (ChatModel) TableName() string {
	return "chat_models"
}

// PriceMode 计价模式
type PriceMode string

const (
	PriceModeToken   PriceMode = "token"
	PriceModeRequest PriceMode = "request"
)

// ChatModelChannel 模型渠道映射
type ChatModelChannel struct {
	BaseModel
	ModelCode      string           `gorm:"type:varchar(50);not null;index;comment:模型标识" json:"model_code"`
	ChannelID      uint             `gorm:"not null;index;comment:渠道ID" json:"channel_id"`
	VendorModel    string           `gorm:"type:varchar(50);not null;comment:供应商模型名" json:"vendor_model"`
	Priority       int              `gorm:"default:0;comment:优先级" json:"priority"`
	PriceMode      PriceMode        `gorm:"type:varchar(10);default:'token';comment:计价模式" json:"price_mode"`
	InputPrice     decimal.Decimal  `gorm:"type:decimal(12,8);default:0;comment:输入价格($/1M tokens)" json:"input_price"`
	OutputPrice    decimal.Decimal  `gorm:"type:decimal(12,8);default:0;comment:输出价格($/1M tokens)" json:"output_price"`
	RequestPath    string           `gorm:"type:varchar(255);default:'/v1/chat/completions';comment:请求路径" json:"request_path"`
	Timeout        int              `gorm:"default:120;comment:超时时间" json:"timeout"`
	SupportsStream *bool            `gorm:"comment:是否支持流式响应" json:"supports_stream"`
	DefaultStream  *bool            `gorm:"comment:默认是否启用流式响应" json:"default_stream"`
	ExtraHeaders   datatypes.JSON   `gorm:"type:json;comment:额外请求头" json:"extra_headers"`
	ExtraConfig    datatypes.JSON   `gorm:"type:json;comment:扩展配置" json:"extra_config"`
	Status         int8             `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`

	// 关联
	ChatModel *ChatModel `gorm:"foreignKey:ModelCode;references:Code" json:"chat_model,omitempty"`
	Channel   *Channel   `gorm:"foreignKey:ChannelID" json:"channel,omitempty"`
}

func (ChatModelChannel) TableName() string {
	return "chat_model_channels"
}
