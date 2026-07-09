// Package routing 负责基于 gw_* 表选出路由目标(渠道+key+vendor模型)。
// 只做选路,不碰 HTTP/计费。选路面与元数据面分离:此包不读 gw_model_meta。
package routing

import (
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

// RouteResult 一次选路的结果:够 pipeline 构造上游请求。
type RouteResult struct {
	AbilityID   uint
	KeyID       uint
	ChannelID   uint
	Protocol    model.Protocol
	BaseURL     string
	APIKey      string
	VendorModel string
	ModelName   string // 公开模型名(计费/熔断/日志用)
	ExtraHeaders map[string]string
	ChannelConfig map[string]any // gw_channels.config 解析(如 image_to_base64)

	PriceMode   string
	InputPrice  decimal.Decimal
	OutputPrice decimal.Decimal
}
