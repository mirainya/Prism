package service

import (
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

type QueryService struct{}

func NewQueryService() *QueryService {
	return &QueryService{}
}

func (s *QueryService) ListAvailableChannels() ([]string, error) {
	var channels []model.Channel
	if err := model.DB().Where("status = ?", 1).Find(&channels).Error; err != nil {
		return nil, err
	}

	result := make([]string, 0, len(channels))
	for _, ch := range channels {
		result = append(result, ch.Type)
	}
	return result, nil
}

func (s *QueryService) ListAvailableCapabilities(channelType, capabilityType string) ([]gin.H, error) {
	query := model.DB().Where("status = ?", 1)
	if capabilityType != "" {
		query = query.Where("type = ?", capabilityType)
	}

	var capabilities []model.Capability
	if err := query.Find(&capabilities).Error; err != nil {
		return nil, err
	}

	var channels []model.Channel
	if err := model.DB().Where("status = ?", 1).Find(&channels).Error; err != nil {
		return nil, err
	}
	channelMap := make(map[uint]model.Channel)
	for _, ch := range channels {
		channelMap[ch.ID] = ch
	}

	var channelCaps []model.ChannelCapability
	ccQuery := model.DB().Where("status = ?", 1)
	if channelType != "" {
		var channel model.Channel
		if err := model.DB().Where("type = ? AND status = ?", channelType, 1).First(&channel).Error; err != nil {
			return nil, err
		}
		ccQuery = ccQuery.Where("channel_id = ?", channel.ID)
	}
	if err := ccQuery.Find(&channelCaps).Error; err != nil {
		return nil, err
	}

	capChannels := make(map[string][]gin.H)
	for _, cc := range channelCaps {
		if ch, ok := channelMap[cc.ChannelID]; ok {
			capChannels[cc.CapabilityCode] = append(capChannels[cc.CapabilityCode], gin.H{
				"channel_type": ch.Type,
				"channel_name": ch.Name,
				"model":        cc.Model,
				"price":        cc.Price,
			})
		}
	}

	result := make([]gin.H, 0, len(capabilities))
	for _, cap := range capabilities {
		channels := capChannels[cap.Code]
		if channelType != "" && len(channels) == 0 {
			continue
		}
		if channels == nil {
			channels = []gin.H{}
		}
		result = append(result, gin.H{
			"code":            cap.Code,
			"name":            cap.Name,
			"type":            cap.Type,
			"description":     cap.Description,
			"standard_params": cap.StandardParams,
			"channels":        channels,
		})
	}

	return result, nil
}

func (s *QueryService) ListCapabilityChannels() ([]gin.H, error) {
	var capabilities []model.Capability
	if err := model.DB().Where("status = ?", 1).Find(&capabilities).Error; err != nil {
		return nil, err
	}

	var channels []model.Channel
	if err := model.DB().Where("status = ?", 1).Find(&channels).Error; err != nil {
		return nil, err
	}
	channelMap := make(map[uint]model.Channel)
	for _, ch := range channels {
		channelMap[ch.ID] = ch
	}

	var channelCaps []model.ChannelCapability
	if err := model.DB().Where("status = ?", 1).Find(&channelCaps).Error; err != nil {
		return nil, err
	}

	capChannels := make(map[string][]gin.H)
	for _, cc := range channelCaps {
		ch, ok := channelMap[cc.ChannelID]
		if !ok {
			continue
		}
		capChannels[cc.CapabilityCode] = append(capChannels[cc.CapabilityCode], gin.H{
			"channel_id":   ch.ID,
			"channel_type": ch.Type,
			"channel_name": ch.Name,
			"model":        cc.Model,
			"price":        cc.Price,
		})
	}

	result := make([]gin.H, 0, len(capabilities))
	for _, cap := range capabilities {
		channels := capChannels[cap.Code]
		if len(channels) == 0 {
			channels = []gin.H{}
		}
		result = append(result, gin.H{
			"code":        cap.Code,
			"name":        cap.Name,
			"type":        cap.Type,
			"description": cap.Description,
			"channels":    channels,
		})
	}

	return result, nil
}

func (s *QueryService) ListChatModelChannelsForToken() ([]gin.H, error) {
	var chatModels []model.ChatModel
	if err := model.DB().Where("status = ?", 1).Find(&chatModels).Error; err != nil {
		return nil, err
	}

	var channels []model.Channel
	if err := model.DB().Where("status = ?", 1).Find(&channels).Error; err != nil {
		return nil, err
	}
	channelMap := make(map[uint]model.Channel)
	for _, ch := range channels {
		channelMap[ch.ID] = ch
	}

	var modelChannels []model.ChatModelChannel
	if err := model.DB().Where("status = ?", 1).Find(&modelChannels).Error; err != nil {
		return nil, err
	}

	modelChannelMap := make(map[string][]gin.H)
	for _, mc := range modelChannels {
		ch, ok := channelMap[mc.ChannelID]
		if !ok {
			continue
		}
		modelChannelMap[mc.ModelCode] = append(modelChannelMap[mc.ModelCode], gin.H{
			"channel_id":   ch.ID,
			"channel_type": ch.Type,
			"channel_name": ch.Name,
			"model":        mc.VendorModel,
			"price":        mc.InputPrice,
		})
	}

	result := make([]gin.H, 0, len(chatModels))
	for _, m := range chatModels {
		channels := modelChannelMap[m.Code]
		if len(channels) == 0 {
			channels = []gin.H{}
		}
		result = append(result, gin.H{
			"code":        "chat:" + m.Code,
			"name":        m.Name + " (Chat)",
			"type":        "chat",
			"description": m.Description,
			"channels":    channels,
		})
	}

	return result, nil
}

// ---------- PricingService ----------

type PricingService struct{}

func NewPricingService() *PricingService {
	return &PricingService{}
}

type PricingCapability struct {
	Code        string                `json:"code"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Channels    []PricingChannelModel `json:"channels"`
}

type PricingChannelModel struct {
	ChannelCode string          `json:"channel_code"`
	Model       string          `json:"model"`
	Name        string          `json:"name"`
	Price       decimal.Decimal `json:"price"`
	PriceUnit   string          `json:"price_unit"`
}

func (s *PricingService) GetPricing() ([]PricingCapability, error) {
	var capabilities []model.Capability
	if err := model.DB().Where("status = ?", 1).Order("code ASC").Find(&capabilities).Error; err != nil {
		return nil, err
	}

	var channelCapabilities []model.ChannelCapability
	if err := model.DB().
		Where("status = ?", 1).
		Preload("Channel").
		Find(&channelCapabilities).Error; err != nil {
		return nil, err
	}

	ccMap := make(map[string][]PricingChannelModel)
	for _, cc := range channelCapabilities {
		if cc.Channel == nil || cc.Channel.Status != 1 {
			continue
		}
		ccMap[cc.CapabilityCode] = append(ccMap[cc.CapabilityCode], PricingChannelModel{
			ChannelCode: cc.Channel.Type,
			Model:       cc.Model,
			Name:        cc.Name,
			Price:       cc.Price,
			PriceUnit:   cc.PriceUnit,
		})
	}

	result := make([]PricingCapability, 0, len(capabilities))
	for _, cap := range capabilities {
		channels := ccMap[cap.Code]
		if channels == nil {
			channels = []PricingChannelModel{}
		}
		result = append(result, PricingCapability{
			Code:        cap.Code,
			Name:        cap.Name,
			Description: cap.Description,
			Channels:    channels,
		})
	}

	return result, nil
}
