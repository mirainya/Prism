// Package routing 负责基于 gw_* 表选出路由目标(渠道+key+vendor模型)。
// 只做选路,不碰 HTTP/计费。选路面与元数据面分离:此包不读 gw_model_meta。
package routing

import (
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

type Capability string

const (
	CapabilityChat             Capability = "chat"
	CapabilityResponses        Capability = "responses"
	CapabilityNativeResponses  Capability = "native_responses"
	CapabilityVolcResponses    Capability = "volcengine_responses"
	CapabilityStream           Capability = "stream"
	CapabilityVision           Capability = "vision"
	CapabilityFiles            Capability = "files"
	CapabilityAudio            Capability = "audio"
	CapabilityVideo            Capability = "video"
	CapabilityTools            Capability = "tools"
	CapabilityStructuredOutput Capability = "structured_output"
	CapabilityReasoning        Capability = "reasoning"
	CapabilityBackground       Capability = "background"
	CapabilityWebSearch        Capability = "web_search"
	CapabilityFileSearch       Capability = "file_search"
	CapabilityCodeInterpreter  Capability = "code_interpreter"
	CapabilityComputerUse      Capability = "computer_use"
	CapabilityImageGeneration  Capability = "image_generation"
)

// RouteRequirements describes the features an upstream route must support.
type RouteRequirements map[Capability]bool

func (r RouteRequirements) Require(capability Capability) {
	if r != nil {
		r[capability] = true
	}
}

// RouteResult 一次选路的结果:够 pipeline 构造上游请求。
type RouteResult struct {
	// Unified catalog identity. Zero values mean this is a legacy route.
	ReleaseID           uint
	OperationContractID uint
	ModelOperationID    uint
	SKUID               uint
	RouteID             uint
	OfferingID          uint
	ProductTransportID  uint
	CredentialPoolID    uint
	CredentialID        uint
	CredentialVersionID uint
	PurposeGrantID      uint
	AbilityID           uint
	KeyID               uint
	ChannelID           uint
	Protocol            model.Protocol
	BaseURL             string
	APIKey              string
	VendorModel         string
	ModelName           string // 公开模型名(计费/熔断/日志用)
	ExtraHeaders        map[string]string
	Capabilities        map[Capability]bool
	Transport           model.UpstreamTransport
	TransportConfig     map[string]any
	ExecutionMode       ExecutionMode
	ChannelConfig       map[string]any // gw_channels.config 解析(如 image_to_base64)

	PriceMode       string
	InputPrice      decimal.Decimal
	OutputPrice     decimal.Decimal
	Currency        string
	CurrencyVersion uint
}
