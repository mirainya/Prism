package model

import (
	"time"

	"gorm.io/datatypes"
)

type Channel struct {
	BaseModel
	Type           string         `gorm:"type:varchar(20);uniqueIndex;not null;comment:渠道类型标识" json:"type"`
	Name           string         `gorm:"type:varchar(50);comment:渠道名称" json:"name"`
	BaseURL        string         `gorm:"type:varchar(255);comment:基础URL" json:"base_url"`
	CallbackSecret string         `gorm:"type:varchar(128);comment:回调签名密钥" json:"callback_secret,omitempty"`
	Config         datatypes.JSON `gorm:"type:json;comment:渠道配置(JSON)" json:"config"`
	Status         int8           `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`
	Sort           int            `gorm:"default:0;index;comment:排序(降序)" json:"sort"`
}

func (Channel) TableName() string {
	return "channels"
}

// ChannelAccount 渠道账号
type ChannelAccount struct {
	BaseModel
	ChannelID    uint           `gorm:"not null;index;comment:所属渠道ID" json:"channel_id"`
	Name         string         `gorm:"type:varchar(50);comment:账号名称" json:"name"`
	APIKey       string         `gorm:"type:text;comment:API密钥" json:"api_key"`
	Config       datatypes.JSON `gorm:"type:json;comment:账号配置(JSON)" json:"config"`
	Weight       int            `gorm:"default:10;comment:负载均衡权重" json:"weight"`
	Status       int8           `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`
	MaxTasks     int            `gorm:"default:0;comment:最大并发任务数(0=不限制)" json:"max_tasks"`
	CurrentTasks int            `gorm:"default:0;comment:当前任务数" json:"current_tasks"`
	// SupportedModels 支持的模型 Code 列表(JSON数组)。空(NULL/[])=支持所有模型; 非空=白名单
	SupportedModels datatypes.JSON `gorm:"type:json;comment:支持的模型列表(空=全部)" json:"supported_models"`
}

func (ChannelAccount) TableName() string {
	return "channel_accounts"
}

// AccountModelState 账号级 per-model 熔断状态
// 记录某账号(key)对某模型不可用的退避状态,到期后由清理任务物理删除
type AccountModelState struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	AccountID     uint      `gorm:"not null;uniqueIndex:idx_account_model;comment:账号ID" json:"account_id"`
	ModelCode     string    `gorm:"type:varchar(80);not null;uniqueIndex:idx_account_model;comment:模型标识" json:"model_code"`
	DisabledUntil time.Time `gorm:"not null;index;comment:熔断到期时间" json:"disabled_until"`
	Reason        string    `gorm:"type:varchar(500);default:'';comment:熔断原因" json:"reason"`
	StatusCode    int       `gorm:"default:0;comment:触发的HTTP状态码" json:"status_code"`
	FailCount     int       `gorm:"default:1;comment:累计触发次数" json:"fail_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (AccountModelState) TableName() string {
	return "account_model_states"
}
