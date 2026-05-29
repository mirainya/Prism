package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ModelType 模型类型
type ModelType string

const (
	ModelTypeChat      ModelType = "chat"
	ModelTypeImage     ModelType = "image"
	ModelTypeVideo     ModelType = "video"
	ModelTypeAudio     ModelType = "audio"
	ModelTypeEmbedding ModelType = "embedding"
)

// Model 统一模型定义
type Model struct {
	Code        string         `gorm:"primaryKey;type:varchar(80);comment:模型标识" json:"code"`
	Name        string         `gorm:"type:varchar(100);not null;comment:显示名称" json:"name"`
	Type        ModelType      `gorm:"type:varchar(20);not null;default:'chat';comment:模型类型" json:"type"`
	Provider    string         `gorm:"type:varchar(30);default:'';comment:来源标识" json:"provider"`
	Description string         `gorm:"type:varchar(500);default:'';comment:模型描述" json:"description"`
	Features    datatypes.JSON `gorm:"type:json;comment:能力标签" json:"features"`
	ParamSchema datatypes.JSON `gorm:"type:json;comment:参数schema" json:"param_schema"`
	MaxTokens   int            `gorm:"default:0;comment:最大token数" json:"max_tokens"`
	Sort        int            `gorm:"default:0;index;comment:排序(降序)" json:"sort"`
	Status      int8           `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`
	CreatedAt   time.Time      `gorm:"index;comment:创建时间" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"comment:更新时间" json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

func (Model) TableName() string {
	return "models"
}

// Protocol 协议类型
type Protocol string

const (
	ProtocolOpenAI    Protocol = "openai"
	ProtocolAnthropic Protocol = "anthropic"
	ProtocolGoogle    Protocol = "google"
	ProtocolCustom    Protocol = "custom"
)

// InteractionMode 交互模式
type InteractionMode string

const (
	ModeSync     InteractionMode = "sync"
	ModeStream   InteractionMode = "stream"
	ModePoll     InteractionMode = "poll"
	ModeCallback InteractionMode = "callback"
)

// PriceMode 计价模式
type PriceMode string

const (
	PriceModeToken   PriceMode = "token"
	PriceModeRequest PriceMode = "request"
)

// Endpoint 统一端点配置（替代 ChatModelChannel + ChannelCapability）
type Endpoint struct {
	BaseModel
	ModelCode string `gorm:"type:varchar(80);not null;index;comment:模型标识" json:"model_code"`
	ChannelID uint   `gorm:"not null;index;comment:渠道ID" json:"channel_id"`

	// 协议
	Protocol      Protocol `gorm:"type:varchar(20);not null;default:'openai';comment:协议类型" json:"protocol"`
	RequestPath   string   `gorm:"type:varchar(255);not null;default:'/v1/chat/completions';comment:请求路径" json:"request_path"`
	RequestMethod string   `gorm:"type:varchar(10);not null;default:'POST';comment:请求方法" json:"request_method"`
	ContentType   string   `gorm:"type:varchar(50);default:'application/json';comment:内容类型" json:"content_type"`

	// 鉴权
	AuthLocation    string `gorm:"type:varchar(10);default:'header';comment:认证位置" json:"auth_location"`
	AuthKey         string `gorm:"type:varchar(50);default:'Authorization';comment:认证参数名" json:"auth_key"`
	AuthValuePrefix string `gorm:"type:varchar(30);default:'Bearer ';comment:认证值前缀" json:"auth_value_prefix"`

	// 模型映射
	VendorModel string `gorm:"type:varchar(80);not null;comment:上游模型名" json:"vendor_model"`

	// 交互模式
	InteractionMode InteractionMode `gorm:"type:varchar(10);not null;default:'stream';comment:交互模式" json:"interaction_mode"`
	SupportsStream  bool            `gorm:"default:1;comment:是否支持流式" json:"supports_stream"`
	DefaultStream   bool            `gorm:"default:1;comment:默认使用流式" json:"default_stream"`

	// 定价
	PriceMode   PriceMode       `gorm:"type:varchar(10);default:'token';comment:计价模式" json:"price_mode"`
	InputPrice  decimal.Decimal `gorm:"type:decimal(12,8);default:0;comment:输入价格" json:"input_price"`
	OutputPrice decimal.Decimal `gorm:"type:decimal(12,8);default:0;comment:输出价格" json:"output_price"`

	// 映射配置
	ParamMapping    datatypes.JSON `gorm:"type:json;comment:参数映射" json:"param_mapping"`
	ParamSchema     datatypes.JSON `gorm:"type:json;comment:端点参数schema(覆盖模型级)" json:"param_schema"`
	ResponseMapping datatypes.JSON `gorm:"type:json;comment:响应映射" json:"response_mapping"`

	// 轮询
	PollPath            string         `gorm:"type:varchar(255);default:'';comment:轮询路径" json:"poll_path"`
	PollMethod          string         `gorm:"type:varchar(10);default:'GET';comment:轮询方法" json:"poll_method"`
	PollInterval        int            `gorm:"default:5;comment:轮询间隔秒" json:"poll_interval"`
	PollMaxAttempts     int            `gorm:"default:60;comment:最大轮询次数" json:"poll_max_attempts"`
	PollParamMapping    datatypes.JSON `gorm:"type:json;comment:轮询参数映射" json:"poll_param_mapping"`
	PollResponseMapping datatypes.JSON `gorm:"type:json;comment:轮询响应映射" json:"poll_response_mapping"`

	// 回调
	CallbackMapping datatypes.JSON `gorm:"type:json;comment:回调映射" json:"callback_mapping"`

	// 扩展
	ExtraHeaders datatypes.JSON `gorm:"type:json;comment:额外请求头" json:"extra_headers"`
	ExtraConfig  datatypes.JSON `gorm:"type:json;comment:扩展配置" json:"extra_config"`
	Timeout      int            `gorm:"default:120;comment:超时秒" json:"timeout"`
	Priority     int            `gorm:"default:0;comment:优先级" json:"priority"`
	Status       int8           `gorm:"default:1;comment:状态" json:"status"`

	// 关联
	Model   *Model   `gorm:"foreignKey:ModelCode;references:Code" json:"model,omitempty"`
	Channel *Channel `gorm:"foreignKey:ChannelID" json:"channel,omitempty"`
}

func (Endpoint) TableName() string {
	return "endpoints"
}
