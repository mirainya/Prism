package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// 网关 v2 路由模型(gw_ 前缀)。与老表(channels/channel_accounts/account_models)并存,
// 切换后删老表。路由面(GwChannel/GwChannelKey/GwAbility)与元数据面(GwModelMeta)分离:
// GwModelMeta 永不参与路由,空了路由照跑。

// GwChannel 一个上游服务实例 = 一种协议(protocol 挂这里,同厂商多协议=多渠道)。
type GwChannel struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	Name         string         `gorm:"type:varchar(100);not null" json:"name"`
	Protocol     Protocol       `gorm:"type:varchar(20);not null;default:'openai'" json:"protocol"`
	BaseURL      string         `gorm:"type:varchar(255);not null" json:"base_url"`
	ExtraHeaders datatypes.JSON `gorm:"type:json" json:"extra_headers"` // 如 {"anthropic-version":"2023-06-01"}
	Config       datatypes.JSON `gorm:"type:json" json:"config"`        // 如 {"image_to_base64":true}
	Status       int8           `gorm:"default:1" json:"status"`
	Sort         int            `gorm:"default:0;index" json:"sort"`
	CreatedAt    time.Time      `gorm:"index" json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (GwChannel) TableName() string { return "gw_channels" }

// GwChannelKey 渠道下的一个 API key。
type GwChannelKey struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	ChannelID   uint           `gorm:"not null;index" json:"channel_id"`
	Name        string         `gorm:"type:varchar(100)" json:"name"`
	APIKey      string         `gorm:"type:text;not null" json:"api_key"`
	Weight      int            `gorm:"default:10" json:"weight"`
	Status      int8           `gorm:"default:1" json:"status"`
	MaxConc     int            `gorm:"default:0" json:"max_conc"`     // 0=不限并发
	CurrentConc int            `gorm:"default:0" json:"current_conc"` // 当前并发(原子增减)
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (GwChannelKey) TableName() string { return "gw_channel_keys" }

// GwAbility 路由索引(去规范化):一行=某 key 能跑某 model + vendor名/优先级/价。
// 增删渠道/key/映射时重建(同步)。路由查询只查这张表。
type GwAbility struct {
	ID          uint            `gorm:"primaryKey" json:"id"`
	ModelName   string          `gorm:"type:varchar(80);not null;index:idx_gw_ability_model,priority:1" json:"model_name"`
	ChannelID   uint            `gorm:"not null" json:"channel_id"`
	KeyID       uint            `gorm:"not null;index" json:"key_id"`
	VendorModel string          `gorm:"type:varchar(120);not null" json:"vendor_model"`
	Priority    int             `gorm:"default:0;index:idx_gw_ability_model,priority:2" json:"priority"`
	PriceMode   string          `gorm:"type:varchar(10);default:'token'" json:"price_mode"`
	InputPrice  decimal.Decimal `gorm:"type:decimal(12,8);default:0" json:"input_price"`
	OutputPrice decimal.Decimal `gorm:"type:decimal(12,8);default:0" json:"output_price"`
	Status      int8            `gorm:"default:1;index:idx_gw_ability_model,priority:3" json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

func (GwAbility) TableName() string { return "gw_abilities" }

// GwModelMeta 元数据面:展示/定价/思考档。永不参与路由,缺失也不影响路由。
type GwModelMeta struct {
	ModelName      string         `gorm:"primaryKey;type:varchar(80)" json:"model_name"`
	DisplayName    string         `gorm:"type:varchar(100)" json:"display_name"`
	ThinkingConfig datatypes.JSON `gorm:"type:json" json:"thinking_config"` // 结构见 service.ThinkingConfig
	MaxTokens      int            `gorm:"default:0" json:"max_tokens"`
	Features       datatypes.JSON `gorm:"type:json" json:"features"` // ["tools","vision"]
	GroupName      string         `gorm:"type:varchar(80);default:''" json:"group_name"` // 手动分组名,空=按源渠道分组
	Status         int8           `gorm:"default:1" json:"status"`
	Sort           int            `gorm:"default:0;index" json:"sort"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func (GwModelMeta) TableName() string { return "gw_model_meta" }
