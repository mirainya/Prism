package service

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
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
	Aliases     datatypes.JSON `json:"aliases"`
	ParamSchema datatypes.JSON `json:"param_schema"`
	MaxTokens   int            `json:"max_tokens"`
	Status      int8           `json:"status"`

	ThinkingConfig datatypes.JSON `json:"thinking_config"`
}

var (
	ErrModelCodeRequired                = errors.New("model code is required")
	ErrModelCodeConflict                = errors.New("model code already exists")
	ErrModelAliasesInvalid              = errors.New("model aliases must be a JSON array of non-empty strings")
	ErrVideoModelManagedByVideoChannels = errors.New("video models are managed by video channels")
)

func (s *ModelAdminService) ListModels(status string) ([]model.Model, error) {
	query := model.DB().Model(&model.Model{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var models []model.Model
	err := query.Order("sort DESC, created_at DESC").Find(&models).Error
	return models, err
}

// ReorderCapabilities 按传入 code 顺序批量写 sort(首个最大,降序在前),对齐 ListModels 的 sort DESC。
func (s *ModelAdminService) ReorderCapabilities(codes []string) error {
	n := len(codes)
	return model.DB().Transaction(func(tx *gorm.DB) error {
		for i, code := range codes {
			if err := tx.Model(&model.Model{}).Where("code = ?", code).
				Update("sort", n-i).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *ModelAdminService) ListModelsByType(typ string) ([]model.Model, error) {
	var models []model.Model
	err := model.DB().Where("type = ?", typ).Order("sort DESC, created_at DESC").Find(&models).Error
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
	if model.ModelType(req.Type) == model.ModelTypeVideo {
		return nil, ErrVideoModelManagedByVideoChannels
	}
	if err := validateModelAliases(req.Aliases); err != nil {
		return nil, err
	}
	m := &model.Model{
		Code:        req.Code,
		Name:        req.Name,
		Type:        model.ModelType(req.Type),
		Provider:    req.Provider,
		Description: req.Description,
		Features:    req.Features,
		Aliases:     req.Aliases,
		ParamSchema: req.ParamSchema,
		MaxTokens:   req.MaxTokens,
		Status:      req.Status,

		ThinkingConfig: req.ThinkingConfig,
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
	if aliases, ok := updates["aliases"].(datatypes.JSON); ok {
		if err := validateModelAliases(aliases); err != nil {
			return nil, err
		}
	}
	if typ, ok := updates["type"].(string); ok && model.ModelType(typ) == model.ModelTypeVideo {
		return nil, ErrVideoModelManagedByVideoChannels
	}
	if err := model.DB().Model(&m).Updates(updates).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

func validateModelAliases(raw datatypes.JSON) error {
	if len(raw) == 0 {
		return nil
	}
	var aliases []string
	if err := json.Unmarshal(raw, &aliases); err != nil {
		return ErrModelAliasesInvalid
	}
	for _, alias := range aliases {
		if strings.TrimSpace(alias) == "" {
			return ErrModelAliasesInvalid
		}
	}
	return nil
}

func (s *ModelAdminService) DeleteModel(code string) (int64, error) {
	result := model.DB().Where("code = ?", code).Delete(&model.Model{})
	return result.RowsAffected, result.Error
}
