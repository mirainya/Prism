package service

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

type ModelAdminService struct{}

func NewModelAdminService() *ModelAdminService {
	return &ModelAdminService{}
}

type CreateModelRequest struct {
	Code        string         `json:"code" binding:"required"`
	Name        string         `json:"name" binding:"required"`
	Type        string         `json:"type"`
	Provider    string         `json:"provider"`
	Description string         `json:"description"`
	Features    datatypes.JSON `json:"features"`
	ParamSchema datatypes.JSON `json:"param_schema"`
	MaxTokens   int            `json:"max_tokens"`
	Status      int8           `json:"status"`
}

var (
	ErrModelCodeRequired = errors.New("model code is required")
	ErrModelCodeConflict = errors.New("model code already exists")
)

func (s *ModelAdminService) ListModels(status string) ([]model.Model, error) {
	query := model.DB().Model(&model.Model{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var models []model.Model
	err := query.Order("created_at DESC").Find(&models).Error
	return models, err
}

func (s *ModelAdminService) ListModelsByType(typ string) ([]model.Model, error) {
	var models []model.Model
	err := model.DB().Where("type = ?", typ).Order("created_at DESC").Find(&models).Error
	return models, err
}

func (s *ModelAdminService) GetModel(code string) (*model.Model, error) {
	var m model.Model
	err := model.DB().Where("code = ?", code).First(&m).Error
	return &m, err
}

func (s *ModelAdminService) CreateModel(req *CreateModelRequest) (*model.Model, error) {
	if req.Code == "" {
		return nil, ErrModelCodeRequired
	}
	m := &model.Model{
		Code:        req.Code,
		Name:        req.Name,
		Type:        model.ModelType(req.Type),
		Provider:    req.Provider,
		Description: req.Description,
		Features:    req.Features,
		ParamSchema: req.ParamSchema,
		MaxTokens:   req.MaxTokens,
		Status:      req.Status,
	}
	if m.Type == "" {
		m.Type = model.ModelTypeChat
	}
	if m.Status == 0 {
		m.Status = 1
	}
	if err := model.DB().Create(m).Error; err != nil {
		return nil, err
	}
	return m, nil
}

func (s *ModelAdminService) UpdateModel(code string, updates map[string]any) (*model.Model, error) {
	if code == "" {
		return nil, ErrModelCodeRequired
	}
	var m model.Model
	if err := model.DB().Where("code = ?", code).First(&m).Error; err != nil {
		return nil, err
	}
	if newCode, ok := updates["code"].(string); ok && newCode != code {
		var existing model.Model
		if err := model.DB().Where("code = ?", newCode).First(&existing).Error; err == nil {
			return nil, ErrModelCodeConflict
		}
	}
	if err := model.DB().Model(&m).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *ModelAdminService) DeleteModel(code string) (int64, error) {
	result := model.DB().Where("code = ?", code).Delete(&model.Model{})
	return result.RowsAffected, result.Error
}

func (s *ModelAdminService) GetPresets(provider string) []gin.H {
	return []gin.H{}
}

type QuickSetupModelItem struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	VendorModel string `json:"vendor_model"`
}

type QuickSetupRequest struct {
	Provider    string                `json:"provider" binding:"required"`
	Models      []QuickSetupModelItem `json:"models" binding:"required"`
	ChannelID   uint                  `json:"channel_id"`
	PriceMode   string                `json:"price_mode"`
	InputPrice  decimal.Decimal       `json:"input_price"`
	OutputPrice decimal.Decimal       `json:"output_price"`
	RequestPath string                `json:"request_path"`
}

type QuickSetupResult struct {
	Created int `json:"created"`
	Skipped int `json:"skipped"`
	Mapped  int `json:"mapped"`
}

func (s *ModelAdminService) QuickSetup(req *QuickSetupRequest) (*QuickSetupResult, error) {
	created := 0
	skipped := 0
	mapped := 0

	requestPath := req.RequestPath
	if requestPath == "" {
		requestPath = "/v1/chat/completions"
	}
	priceMode := req.PriceMode
	if priceMode == "" {
		priceMode = "token"
	}

	for _, item := range req.Models {
		m := &model.Model{
			Code:     item.Code,
			Name:     item.Name,
			Type:     model.ModelTypeChat,
			Provider: req.Provider,
			Status:   1,
		}
		if err := model.DB().Create(m).Error; err != nil {
			skipped++
		} else {
			created++
		}

		if req.ChannelID > 0 {
			vendorModel := item.VendorModel
			if vendorModel == "" {
				vendorModel = item.Code
			}
			ep := &model.Endpoint{
				ModelCode:       item.Code,
				ChannelID:       req.ChannelID,
				Protocol:        model.ProtocolOpenAI,
				RequestPath:     requestPath,
				RequestMethod:   "POST",
				ContentType:     "application/json",
				AuthLocation:    "header",
				AuthKey:         "Authorization",
				AuthValuePrefix: "Bearer ",
				VendorModel:     vendorModel,
				InteractionMode: model.ModeStream,
				SupportsStream:  true,
				PriceMode:       model.PriceMode(priceMode),
				InputPrice:      req.InputPrice,
				OutputPrice:     req.OutputPrice,
				Status:          1,
				Timeout:         120,
			}
			if err := model.DB().Create(ep).Error; err == nil {
				mapped++
			}
		}
	}
	return &QuickSetupResult{Created: created, Skipped: skipped, Mapped: mapped}, nil
}
