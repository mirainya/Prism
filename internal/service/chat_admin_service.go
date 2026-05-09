package service

import (
	"gorm.io/datatypes"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
)

type ChatAdminService struct{}

func NewChatAdminService() *ChatAdminService {
	return &ChatAdminService{}
}

// ========== ChatModel CRUD ==========

func (s *ChatAdminService) ListChatModels() ([]model.ChatModel, error) {
	var models []model.ChatModel
	err := model.DB().Order("code ASC").Find(&models).Error
	return models, err
}

func (s *ChatAdminService) GetChatModel(code string) (*model.ChatModel, error) {
	var chatModel model.ChatModel
	err := model.DB().Where("code = ?", code).First(&chatModel).Error
	return &chatModel, err
}

type CreateChatModelRequest struct {
	Code        string `json:"code" binding:"required,max=50"`
	Name        string `json:"name" binding:"required,max=100"`
	Provider    string `json:"provider" binding:"required,max=30"`
	Description string `json:"description"`
}

func (s *ChatAdminService) CreateChatModel(req *CreateChatModelRequest) (*model.ChatModel, error) {
	chatModel := &model.ChatModel{
		Code:        req.Code,
		Name:        req.Name,
		Provider:    model.Provider(req.Provider),
		Description: req.Description,
		Status:      1,
	}
	err := model.DB().Create(chatModel).Error
	return chatModel, err
}

func (s *ChatAdminService) UpdateChatModel(code string, updates map[string]any) (int64, error) {
	result := model.DB().Model(&model.ChatModel{}).Where("code = ?", code).Updates(updates)
	return result.RowsAffected, result.Error
}

func (s *ChatAdminService) DeleteChatModel(code string) (int64, error) {
	// 硬删除关联的 chat_model_channels（避免外键约束）
	model.DB().Unscoped().Where("model_code = ?", code).Delete(&model.ChatModelChannel{})
	result := model.DB().Where("code = ?", code).Delete(&model.ChatModel{})
	return result.RowsAffected, result.Error
}

// ========== Quick Setup ==========

type ModelPreset struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

var ModelPresets = map[string][]ModelPreset{
	"openai": {
		{Code: "gpt-4o", Name: "GPT-4o"},
		{Code: "gpt-4o-mini", Name: "GPT-4o Mini"},
		{Code: "gpt-4.1", Name: "GPT-4.1"},
		{Code: "gpt-4.1-mini", Name: "GPT-4.1 Mini"},
		{Code: "gpt-4.1-nano", Name: "GPT-4.1 Nano"},
		{Code: "o3-mini", Name: "o3-mini"},
		{Code: "o4-mini", Name: "o4-mini"},
	},
	"anthropic": {
		{Code: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4"},
		{Code: "claude-opus-4-20250514", Name: "Claude Opus 4"},
		{Code: "claude-haiku-4-5-20251001", Name: "Claude Haiku 4.5"},
	},
	"deepseek": {
		{Code: "deepseek-chat", Name: "DeepSeek Chat (V3)"},
		{Code: "deepseek-reasoner", Name: "DeepSeek Reasoner (R1)"},
	},
	"google": {
		{Code: "gemini-2.5-flash", Name: "Gemini 2.5 Flash"},
		{Code: "gemini-2.5-pro", Name: "Gemini 2.5 Pro"},
	},
	"qwen": {
		{Code: "qwen-plus", Name: "Qwen Plus"},
		{Code: "qwen-turbo", Name: "Qwen Turbo"},
		{Code: "qwen-max", Name: "Qwen Max"},
	},
	"moonshot": {
		{Code: "moonshot-v1-8k", Name: "Moonshot v1 8K"},
		{Code: "moonshot-v1-32k", Name: "Moonshot v1 32K"},
		{Code: "moonshot-v1-128k", Name: "Moonshot v1 128K"},
	},
}

// GetPresets 获取指定 provider 的预设模型列表
func (s *ChatAdminService) GetPresets(provider string) []ModelPreset {
	if presets, ok := ModelPresets[provider]; ok {
		return presets
	}
	return []ModelPreset{}
}

type QuickSetupRequest struct {
	ChannelID   uint   `json:"channel_id" binding:"required"`
	Provider    string `json:"provider" binding:"required"`
	Models      []struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		VendorModel string `json:"vendor_model"`
	} `json:"models" binding:"required,min=1"`
	PriceMode   string          `json:"price_mode"`
	InputPrice  decimal.Decimal `json:"input_price"`
	OutputPrice decimal.Decimal `json:"output_price"`
	RequestPath string          `json:"request_path"`
}

type QuickSetupResult struct {
	Created int `json:"created"`
	Skipped int `json:"skipped"`
	Mapped  int `json:"mapped"`
}

// QuickSetup 快速添加模型 + 渠道映射
func (s *ChatAdminService) QuickSetup(req *QuickSetupRequest) (*QuickSetupResult, error) {
	result := &QuickSetupResult{}

	priceMode := model.PriceMode(req.PriceMode)
	if priceMode == "" {
		priceMode = model.PriceModeToken
	}
	requestPath := req.RequestPath
	if requestPath == "" {
		requestPath = "/v1/chat/completions"
	}

	for _, m := range req.Models {
		vendorModel := m.VendorModel
		if vendorModel == "" {
			vendorModel = m.Code
		}

		// 创建 ChatModel（不存在则创建）
		var existing model.ChatModel
		if err := model.DB().Where("code = ?", m.Code).First(&existing).Error; err != nil {
			chatModel := &model.ChatModel{
				Code:     m.Code,
				Name:     m.Name,
				Provider: model.Provider(req.Provider),
				Status:   1,
			}
			if err := model.DB().Create(chatModel).Error; err != nil {
				return nil, err
			}
			result.Created++
		} else {
			result.Skipped++
		}

		// 创建 ChatModelChannel（检查是否已存在相同映射）
		var existingMC model.ChatModelChannel
		if err := model.DB().Where("model_code = ? AND channel_id = ?", m.Code, req.ChannelID).First(&existingMC).Error; err != nil {
			mc := &model.ChatModelChannel{
				ModelCode:   m.Code,
				ChannelID:   req.ChannelID,
				VendorModel: vendorModel,
				PriceMode:   priceMode,
				InputPrice:  req.InputPrice,
				OutputPrice: req.OutputPrice,
				RequestPath: requestPath,
				Timeout:     120,
				Status:      1,
			}
			if err := model.DB().Create(mc).Error; err != nil {
				return nil, err
			}
			result.Mapped++
		}
	}

	return result, nil
}

// ========== ChatModelChannel CRUD ==========

func (s *ChatAdminService) ListChatModelChannels(modelCode, channelID string) ([]model.ChatModelChannel, error) {
	query := model.DB().Model(&model.ChatModelChannel{})
	if modelCode != "" {
		query = query.Where("model_code = ?", modelCode)
	}
	if channelID != "" {
		query = query.Where("channel_id = ?", channelID)
	}
	var channels []model.ChatModelChannel
	err := query.Preload("ChatModel").Preload("Channel").Order("model_code, priority DESC").Find(&channels).Error
	return channels, err
}

func (s *ChatAdminService) GetChatModelChannel(id string) (*model.ChatModelChannel, error) {
	var mc model.ChatModelChannel
	err := model.DB().Preload("ChatModel").Preload("Channel").First(&mc, id).Error
	return &mc, err
}

type CreateChatModelChannelRequest struct {
	ModelCode      string          `json:"model_code" binding:"required"`
	ChannelID      uint            `json:"channel_id" binding:"required"`
	VendorModel    string          `json:"vendor_model" binding:"required"`
	Priority       int             `json:"priority"`
	PriceMode      string          `json:"price_mode"`
	InputPrice     decimal.Decimal `json:"input_price"`
	OutputPrice    decimal.Decimal `json:"output_price"`
	RequestPath    string          `json:"request_path"`
	Timeout        int             `json:"timeout"`
	SupportsStream *bool           `json:"supports_stream"`
	DefaultStream  *bool           `json:"default_stream"`
	ExtraHeaders   datatypes.JSON  `json:"extra_headers"`
	ExtraConfig    datatypes.JSON  `json:"extra_config"`
}

func (s *ChatAdminService) CreateChatModelChannel(req *CreateChatModelChannelRequest) (*model.ChatModelChannel, error) {
	mc := &model.ChatModelChannel{
		ModelCode:      req.ModelCode,
		ChannelID:      req.ChannelID,
		VendorModel:    req.VendorModel,
		Priority:       req.Priority,
		PriceMode:      model.PriceMode(req.PriceMode),
		InputPrice:     req.InputPrice,
		OutputPrice:    req.OutputPrice,
		RequestPath:    req.RequestPath,
		Timeout:        req.Timeout,
		SupportsStream: req.SupportsStream,
		DefaultStream:  req.DefaultStream,
		ExtraHeaders:   req.ExtraHeaders,
		ExtraConfig:    req.ExtraConfig,
		Status:         1,
	}
	if mc.PriceMode == "" {
		mc.PriceMode = model.PriceModeToken
	}
	if mc.RequestPath == "" {
		mc.RequestPath = "/v1/chat/completions"
	}
	if mc.Timeout == 0 {
		mc.Timeout = 120
	}
	err := model.DB().Create(mc).Error
	return mc, err
}

func (s *ChatAdminService) UpdateChatModelChannel(id string, updates map[string]any) (int64, error) {
	result := model.DB().Model(&model.ChatModelChannel{}).Where("id = ?", id).Updates(updates)
	return result.RowsAffected, result.Error
}

func (s *ChatAdminService) DeleteChatModelChannel(id string) (int64, error) {
	result := model.DB().Delete(&model.ChatModelChannel{}, id)
	return result.RowsAffected, result.Error
}
