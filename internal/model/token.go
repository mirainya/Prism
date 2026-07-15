package model

import "github.com/shopspring/decimal"

type Token struct {
	BaseModel
	UserID    uint            `gorm:"default:0;index;comment:用户ID" json:"user_id"`
	Key       string          `gorm:"type:varchar(64);uniqueIndex;not null;comment:API密钥(hash)" json:"key"`
	KeyHint   string          `gorm:"type:varchar(20);comment:密钥提示(后四位)" json:"key_hint"`
	Name      string          `gorm:"type:varchar(50);comment:令牌名称" json:"name"`
	Balance   decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0;comment:剩余额度" json:"balance"`
	TotalUsed decimal.Decimal `gorm:"type:decimal(20,8);not null;default:0;comment:已使用额度" json:"total_used"`
	RateLimit int             `gorm:"default:60;comment:速率限制(次/分钟)" json:"rate_limit"`
	Status    int8            `gorm:"default:1;comment:状态(1启用/0禁用)" json:"status"`
}

func (Token) TableName() string {
	return "tokens"
}

// TokenChannelPriority 令牌能力渠道优先级配置
type TokenChannelPriority struct {
	BaseModel
	TokenID        uint   `gorm:"not null;index:idx_token_capability;comment:令牌ID" json:"token_id"`
	CapabilityCode string `gorm:"type:varchar(30);not null;index:idx_token_capability;comment:能力编码" json:"capability_code"`
	ChannelID      uint   `gorm:"not null;comment:渠道ID" json:"channel_id"`
	Priority       int    `gorm:"default:1;comment:优先级(1最高)" json:"priority"`
}

func (TokenChannelPriority) TableName() string {
	return "token_channel_priorities"
}
