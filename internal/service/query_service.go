package service

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/simplifiedchinese"
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

func (s *QueryService) ListAvailableCapabilities(channelType, modelType string) ([]gin.H, error) {
	query := model.DB().Where("status = ?", 1)
	if modelType != "" {
		query = query.Where("type = ?", modelType)
	}

	var models []model.Model
	if err := query.Find(&models).Error; err != nil {
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

	var endpoints []model.Endpoint
	epQuery := model.DB().Where("status = ?", 1)
	if channelType != "" {
		var channel model.Channel
		if err := model.DB().Where("type = ? AND status = ?", channelType, 1).First(&channel).Error; err != nil {
			return nil, err
		}
		epQuery = epQuery.Where("channel_id = ?", channel.ID)
	}
	if err := epQuery.Find(&endpoints).Error; err != nil {
		return nil, err
	}

	modelChannels := make(map[string][]gin.H)
	for _, ep := range endpoints {
		if ch, ok := channelMap[ep.ChannelID]; ok {
			entry := gin.H{
				"channel_type":     ch.Type,
				"channel_name":     ch.Name,
				"model":            ep.VendorModel,
				"price":            ep.InputPrice,
				"interaction_mode": ep.InteractionMode,
			}
			if len(ep.ParamSchema) > 0 {
				entry["param_schema"] = ensureUTF8(ep.ParamSchema)
			}
			modelChannels[ep.ModelCode] = append(modelChannels[ep.ModelCode], entry)
		}
	}

	result := make([]gin.H, 0, len(models))
	for _, m := range models {
		chs := modelChannels[m.Code]
		if channelType != "" && len(chs) == 0 {
			continue
		}
		if chs == nil {
			chs = []gin.H{}
		}
		result = append(result, gin.H{
			"code":         m.Code,
			"name":         m.Name,
			"type":         m.Type,
			"description":  m.Description,
			"param_schema": ensureUTF8(m.ParamSchema),
			"channels":     chs,
		})
	}

	return result, nil
}

func (s *QueryService) ListCapabilityChannels() ([]gin.H, error) {
	var models []model.Model
	if err := model.DB().Where("status = ?", 1).Find(&models).Error; err != nil {
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

	var endpoints []model.Endpoint
	if err := model.DB().Where("status = ?", 1).Find(&endpoints).Error; err != nil {
		return nil, err
	}

	modelChannels := make(map[string][]gin.H)
	for _, ep := range endpoints {
		ch, ok := channelMap[ep.ChannelID]
		if !ok {
			continue
		}
		modelChannels[ep.ModelCode] = append(modelChannels[ep.ModelCode], gin.H{
			"channel_id":   ch.ID,
			"channel_type": ch.Type,
			"channel_name": ch.Name,
			"model":        ep.VendorModel,
			"price":        ep.InputPrice,
		})
	}

	result := make([]gin.H, 0, len(models))
	for _, m := range models {
		chs := modelChannels[m.Code]
		if len(chs) == 0 {
			chs = []gin.H{}
		}
		result = append(result, gin.H{
			"code":        m.Code,
			"name":        m.Name,
			"type":        m.Type,
			"description": m.Description,
			"channels":    chs,
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
	var models []model.Model
	if err := model.DB().Where("status = ?", 1).Order("code ASC").Find(&models).Error; err != nil {
		return nil, err
	}

	var endpoints []model.Endpoint
	if err := model.DB().
		Where("status = ?", 1).
		Preload("Channel").
		Find(&endpoints).Error; err != nil {
		return nil, err
	}

	epMap := make(map[string][]PricingChannelModel)
	for _, ep := range endpoints {
		if ep.Channel == nil || ep.Channel.Status != 1 {
			continue
		}
		epMap[ep.ModelCode] = append(epMap[ep.ModelCode], PricingChannelModel{
			ChannelCode: ep.Channel.Type,
			Model:       ep.VendorModel,
			Name:        ep.VendorModel,
			Price:       ep.InputPrice,
			PriceUnit:   string(ep.PriceMode),
		})
	}

	result := make([]PricingCapability, 0, len(models))
	for _, m := range models {
		channels := epMap[m.Code]
		if channels == nil {
			channels = []PricingChannelModel{}
		}
		result = append(result, PricingCapability{
			Code:        m.Code,
			Name:        m.Name,
			Description: m.Description,
			Channels:    channels,
		})
	}

	return result, nil
}

func ensureUTF8(data []byte) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	if utf8.Valid(data) {
		return json.RawMessage(data)
	}
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(data)
	if err != nil {
		return json.RawMessage(data)
	}
	return json.RawMessage(decoded)
}
