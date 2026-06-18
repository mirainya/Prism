package model

import "gorm.io/datatypes"

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
}

func (ChannelAccount) TableName() string {
	return "channel_accounts"
}
