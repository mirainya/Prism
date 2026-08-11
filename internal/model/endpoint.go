package model

import (
	"encoding/json"
	"strings"
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
	Code     string    `gorm:"primaryKey;type:varchar(80);comment:模型标识" json:"code"`
	Name     string    `gorm:"type:varchar(100);not null;comment:显示名称" json:"name"`
	Type     ModelType `gorm:"type:varchar(20);not null;default:'chat';comment:模型类型" json:"type"`
	Provider string    `gorm:"type:varchar(30);default:'';comment:来源标识" json:"provider"`
	// Protocol chat 去端点化后的协议类型(openai/anthropic/google/volcengine)。仅 chat 走合成虚拟端点时使用;空=openai
	Protocol    Protocol       `gorm:"type:varchar(20);default:'openai';comment:协议类型(chat虚拟端点用)" json:"protocol"`
	Description string         `gorm:"type:varchar(500);default:'';comment:模型描述" json:"description"`
	Features    datatypes.JSON `gorm:"type:json;comment:能力标签" json:"features"`
	ParamSchema datatypes.JSON `gorm:"type:json;comment:参数schema" json:"param_schema"`
	MaxTokens   int            `gorm:"default:0;comment:最大token数" json:"max_tokens"`

	// 思考模式配置 (JSON,空=不支持思考)。结构见 service.ThinkingConfig
	ThinkingConfig datatypes.JSON `gorm:"type:json;comment:思考模式配置(档位/默认/锁定)" json:"thinking_config"`

	Sort      int            `gorm:"default:0;index;comment:排序(降序)" json:"sort"`
	Status    int8           `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`
	CreatedAt time.Time      `gorm:"index;comment:创建时间" json:"created_at"`
	UpdatedAt time.Time      `gorm:"comment:更新时间" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index;comment:删除时间" json:"-"`
}

func (Model) TableName() string {
	return "models"
}

// Protocol 协议类型
type Protocol string

const (
	ProtocolOpenAI     Protocol = "openai"
	ProtocolAnthropic  Protocol = "anthropic"
	ProtocolGoogle     Protocol = "google"
	ProtocolVolcengine Protocol = "volcengine" // 火山方舟 Responses API (/api/v3/responses)
	ProtocolCustom     Protocol = "custom"
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

type EndpointOriginType string

const (
	EndpointOriginManual         EndpointOriginType = "manual"
	EndpointOriginKeyDiscovery   EndpointOriginType = "key_discovery"
	EndpointOriginEndpointImport EndpointOriginType = "endpoint_import"
	EndpointOriginLegacyInferred EndpointOriginType = "legacy_inferred"
	EndpointOriginLegacyUnknown  EndpointOriginType = "legacy_unknown"
)

// Endpoint 统一端点配置（替代 ChatModelChannel + ChannelCapability）
type Endpoint struct {
	BaseModel
	ModelCode      string `gorm:"type:varchar(80);not null;index;comment:模型标识" json:"model_code"`
	RouteOperation string `gorm:"type:varchar(40);not null;default:'';index;comment:路由操作" json:"route_operation"`
	// SupportedOperations lists all public operations handled by this physical endpoint.
	// RouteOperation remains the legacy/default operation for older rows.
	SupportedOperations datatypes.JSON `gorm:"type:json;comment:支持的能力操作列表" json:"supported_operations"`
	ChannelID           uint           `gorm:"not null;index;comment:渠道ID" json:"channel_id"`
	// AccountID 是旧版单 Key 绑定字段。能力路由改用 EndpointAccounts 后仅保留用于版本回退。
	AccountID       uint               `gorm:"index;default:0;comment:所属账号(key)ID,0=渠道级兼容" json:"account_id"`
	OriginType      EndpointOriginType `gorm:"type:varchar(32);not null;default:'legacy_unknown';index;comment:端点创建来源" json:"origin_type"`
	OriginAccountID uint               `gorm:"not null;default:0;index;comment:创建来源Key ID" json:"origin_account_id"`
	OriginSnapshot  datatypes.JSON     `gorm:"type:json;comment:创建来源快照" json:"origin_snapshot"`
	DiscoveredAt    *time.Time         `gorm:"comment:自动发现时间" json:"discovered_at"`

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
	Model           *Model            `gorm:"foreignKey:ModelCode;references:Code" json:"model,omitempty"`
	Channel         *Channel          `gorm:"foreignKey:ChannelID" json:"channel,omitempty"`
	AccountBindings []EndpointAccount `gorm:"foreignKey:EndpointID" json:"account_bindings,omitempty"`
}

func (Endpoint) TableName() string {
	return "endpoints"
}

// EndpointAccount records which channel keys may execute an image/video endpoint.
// Bindings are deleted physically, so the unique endpoint/account pair can be recreated.
type EndpointAccount struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	EndpointID uint      `gorm:"not null;uniqueIndex:idx_endpoint_account;index:idx_endpoint_accounts_route,priority:1;comment:端点ID" json:"endpoint_id"`
	AccountID  uint      `gorm:"not null;uniqueIndex:idx_endpoint_account;index;comment:渠道账号(key)ID" json:"account_id"`
	Status     int8      `gorm:"not null;index:idx_endpoint_accounts_route,priority:2;comment:状态(1启用/0禁用)" json:"status"`
	Priority   int       `gorm:"not null;default:0;index:idx_endpoint_accounts_route,priority:3;comment:优先级(降序)" json:"priority"`
	Weight     int       `gorm:"not null;default:10;comment:同优先级流量权重" json:"weight"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	Account *ChannelAccount `gorm:"foreignKey:AccountID" json:"account,omitempty"`
}

func (EndpointAccount) TableName() string {
	return "endpoint_accounts"
}

// ImageEditConfig 声明端点如何接收参考图。Multipart 模式切换编辑路径并上传文件，
// URL 模式沿用请求路径并发送公开图片 URL。
type ImageEditConfig struct {
	Enabled   bool   `json:"enabled"`    // 是否启用图生图输入适配
	InputMode string `json:"input_mode"` // multipart: 文件上传; url: 上传存储后以 URL 透传
	EditPath  string `json:"edit_path"`  // multipart 模式的图生图请求路径,如 /v1/images/edits
	FileField string `json:"file_field"` // 参数映射后的上游图片字段名,如 image 或 image_urls
}

const (
	ImageInputModeMultipart = "multipart"
	ImageInputModeURL       = "url"
)

// ImageEdit 从 ExtraConfig 解析图生图配置,未配置或未启用返回 nil
func (e *Endpoint) ImageEdit() *ImageEditConfig {
	if len(e.ExtraConfig) == 0 {
		return e.inferredImageEdit()
	}
	var cfg struct {
		ImageEdit *ImageEditConfig `json:"image_edit"`
	}
	if err := json.Unmarshal(e.ExtraConfig, &cfg); err != nil {
		return e.inferredImageEdit()
	}
	if cfg.ImageEdit == nil {
		return e.inferredImageEdit()
	}
	if !cfg.ImageEdit.Enabled {
		return nil
	}
	cfg.ImageEdit.InputMode = strings.ToLower(strings.TrimSpace(cfg.ImageEdit.InputMode))
	if cfg.ImageEdit.InputMode == "" {
		cfg.ImageEdit.InputMode = ImageInputModeMultipart
	}
	if cfg.ImageEdit.InputMode != ImageInputModeMultipart && cfg.ImageEdit.InputMode != ImageInputModeURL {
		return nil
	}
	cfg.ImageEdit.EditPath = strings.TrimSpace(cfg.ImageEdit.EditPath)
	cfg.ImageEdit.FileField = strings.TrimSpace(cfg.ImageEdit.FileField)
	if cfg.ImageEdit.InputMode == ImageInputModeMultipart && cfg.ImageEdit.EditPath == "" {
		cfg.ImageEdit.EditPath = "/v1/images/edits"
	}
	if cfg.ImageEdit.FileField == "" {
		if cfg.ImageEdit.InputMode == ImageInputModeURL {
			cfg.ImageEdit.FileField = "image_urls"
		} else {
			cfg.ImageEdit.FileField = "image"
		}
	}
	return cfg.ImageEdit
}

// inferredImageEdit preserves compatibility with legacy multipart edit endpoints
// that declared /images/edits directly without an extra_config.image_edit block.
func (e *Endpoint) inferredImageEdit() *ImageEditConfig {
	path := strings.TrimSpace(e.RequestPath)
	if index := strings.IndexByte(path, '?'); index >= 0 {
		path = path[:index]
	}
	path = strings.TrimRight(strings.ToLower(path), "/")
	if (path != "/v1/images/edits" && !strings.HasSuffix(path, "/images/edits")) ||
		!strings.Contains(strings.ToLower(strings.TrimSpace(e.ContentType)), "multipart/form-data") {
		return nil
	}
	return &ImageEditConfig{
		Enabled:   true,
		InputMode: ImageInputModeMultipart,
		EditPath:  path,
		FileField: "image",
	}
}
