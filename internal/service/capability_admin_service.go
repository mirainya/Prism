package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var (
	ErrCapabilityCodeRequired               = errors.New("capability code is required")
	ErrCapabilityCodeConflict               = errors.New("capability code conflict")
	ErrChannelCapabilityConflict            = errors.New("channel capability conflict")
	ErrChannelCapabilityChannelNotFound     = errors.New("target channel not found")
	ErrChannelCapabilityInvalidField        = errors.New("invalid channel capability field")
	ErrChannelCapabilityCapabilityNotFound  = errors.New("target capability not found")
)

type CapabilityAdminService struct{}

func NewCapabilityAdminService() *CapabilityAdminService {
	return &CapabilityAdminService{}
}

// ========== Capability CRUD ==========

func (s *CapabilityAdminService) ListCapabilities(status string) ([]model.Capability, error) {
	var capabilities []model.Capability
	query := model.DB().Model(&model.Capability{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	err := query.Order("code ASC").Find(&capabilities).Error
	return capabilities, err
}

func (s *CapabilityAdminService) GetCapability(code string) (*model.Capability, error) {
	var capability model.Capability
	err := model.DB().Where("code = ?", code).First(&capability).Error
	return &capability, err
}

type CreateCapabilityRequest struct {
	Code             string         `json:"code" binding:"required"`
	Name             string         `json:"name" binding:"required"`
	Type             string         `json:"type"`
	Description      string         `json:"description"`
	StandardParams   datatypes.JSON `json:"standard_params"`
	StandardResponse datatypes.JSON `json:"standard_response"`
	Status           int8           `json:"status"`
}

func (s *CapabilityAdminService) CreateCapability(req *CreateCapabilityRequest) (*model.Capability, error) {
	capability := &model.Capability{
		Code:             req.Code,
		Name:             req.Name,
		Type:             model.CapabilityType(req.Type),
		Description:      req.Description,
		StandardParams:   req.StandardParams,
		StandardResponse: req.StandardResponse,
		Status:           req.Status,
	}
	if capability.Status == 0 {
		capability.Status = 1
	}
	if capability.Type == "" {
		capability.Type = model.CapabilityTypeImage
	}
	err := model.DB().Create(capability).Error
	return capability, err
}

func (s *CapabilityAdminService) UpdateCapability(code string, updates map[string]any) (*model.Capability, error) {
	var capability model.Capability
	if err := model.DB().Where("code = ?", code).First(&capability).Error; err != nil {
		return nil, err
	}

	finalCode := code
	if rawCode, ok := updates["code"]; ok {
		targetCode, ok := rawCode.(string)
		if !ok {
			return nil, ErrCapabilityCodeRequired
		}
		targetCode = strings.TrimSpace(targetCode)
		if targetCode == "" {
			return nil, ErrCapabilityCodeRequired
		}
		updates["code"] = targetCode
		finalCode = targetCode
	}

	if finalCode == code {
		if err := model.DB().Model(&capability).Updates(updates).Error; err != nil {
			return nil, err
		}
		if err := model.DB().Where("code = ?", code).First(&capability).Error; err != nil {
			return nil, err
		}
		return &capability, nil
	}

	if err := model.DB().Transaction(func(tx *gorm.DB) error {
		var existing model.Capability
		if err := tx.Select("code").Where("code = ?", finalCode).First(&existing).Error; err == nil {
			return ErrCapabilityCodeConflict
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		newCapability := capability
		newCapability.Code = finalCode
		if name, ok := updates["name"].(string); ok {
			newCapability.Name = name
		}
		if capabilityType, ok := updates["type"].(string); ok {
			newCapability.Type = model.CapabilityType(capabilityType)
		}
		if description, ok := updates["description"].(string); ok {
			newCapability.Description = description
		}
		if standardParams, ok := updates["standard_params"].(datatypes.JSON); ok {
			newCapability.StandardParams = standardParams
		}
		if standardResponse, ok := updates["standard_response"].(datatypes.JSON); ok {
			newCapability.StandardResponse = standardResponse
		}
		if status, ok := updates["status"].(int8); ok {
			newCapability.Status = status
		}
		if err := tx.Create(&newCapability).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ChannelCapability{}).Where("capability_code = ?", code).Update("capability_code", finalCode).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Task{}).Where("capability_code = ?", code).Update("capability_code", finalCode).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.TokenChannelPriority{}).Where("capability_code = ?", code).Update("capability_code", finalCode).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.ChannelRequestLog{}).Where("capability_code = ?", code).Update("capability_code", finalCode).Error; err != nil {
			return err
		}
		if err := tx.Where("code = ?", code).Delete(&model.Capability{}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var updatedCapability model.Capability
	if err := model.DB().Where("code = ?", finalCode).First(&updatedCapability).Error; err != nil {
		return nil, err
	}
	return &updatedCapability, nil
}

func (s *CapabilityAdminService) DeleteCapability(code string) (int64, error) {
	result := model.DB().Where("code = ?", code).Delete(&model.Capability{})
	return result.RowsAffected, result.Error
}

// ========== ChannelCapability CRUD ==========

func (s *CapabilityAdminService) ListChannelCapabilities(channelID, capabilityCode, status string) ([]model.ChannelCapability, error) {
	var ccs []model.ChannelCapability
	query := model.DB().Model(&model.ChannelCapability{}).Preload("Channel").Preload("Capability")
	if channelID != "" {
		query = query.Where("channel_id = ?", channelID)
	}
	if capabilityCode != "" {
		query = query.Where("capability_code = ?", capabilityCode)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	err := query.Order("id DESC").Find(&ccs).Error
	return ccs, err
}

func (s *CapabilityAdminService) GetChannelCapability(id uint64) (*model.ChannelCapability, error) {
	var cc model.ChannelCapability
	err := model.DB().Preload("Channel").Preload("Capability").First(&cc, id).Error
	return &cc, err
}

func (s *CapabilityAdminService) CreateChannelCapability(cc *model.ChannelCapability) error {
	// Set defaults
	if cc.Status == 0 {
		cc.Status = 1
	}
	if cc.ResultMode == "" {
		cc.ResultMode = model.ResultModePoll
	}
	if cc.RequestMethod == "" {
		cc.RequestMethod = "POST"
	}
	if cc.ContentType == "" {
		cc.ContentType = "application/json"
	}
	if cc.AuthLocation == "" {
		cc.AuthLocation = "header"
	}
	if cc.AuthKey == "" {
		cc.AuthKey = "Authorization"
	}
	if cc.AuthValuePrefix == "" {
		cc.AuthValuePrefix = "Bearer "
	}
	if cc.PollMethod == "" {
		cc.PollMethod = "GET"
	}
	if cc.PollInterval == 0 {
		cc.PollInterval = 5
	}
	if cc.PollMaxAttempts == 0 {
		cc.PollMaxAttempts = 60
	}
	return model.DB().Create(cc).Error
}

func (s *CapabilityAdminService) UpdateChannelCapability(id uint64, updates map[string]any) (*model.ChannelCapability, error) {
	var cc model.ChannelCapability
	if err := model.DB().First(&cc, id).Error; err != nil {
		return nil, err
	}

	targetChannelID := cc.ChannelID
	if rawChannelID, ok := updates["channel_id"]; ok {
		channelID, err := parseChannelCapabilityUint(rawChannelID)
		if err != nil {
			return nil, fmt.Errorf("%w: channel_id", ErrChannelCapabilityInvalidField)
		}
		targetChannelID = channelID
		updates["channel_id"] = channelID
	}

	targetCapabilityCode := cc.CapabilityCode
	if rawCapabilityCode, ok := updates["capability_code"]; ok {
		capabilityCode, err := parseChannelCapabilityString(rawCapabilityCode)
		if err != nil || capabilityCode == "" {
			return nil, fmt.Errorf("%w: capability_code", ErrChannelCapabilityInvalidField)
		}
		targetCapabilityCode = capabilityCode
	}

	targetModel := cc.Model
	if rawModel, ok := updates["model"]; ok {
		modelName, err := parseChannelCapabilityString(rawModel)
		if err != nil {
			return nil, fmt.Errorf("%w: model", ErrChannelCapabilityInvalidField)
		}
		targetModel = modelName
	}

	var channel model.Channel
	if err := model.DB().Select("id").First(&channel, targetChannelID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrChannelCapabilityChannelNotFound
		}
		return nil, err
	}

	if _, ok := updates["capability_code"]; ok {
		var capability model.Capability
		if err := model.DB().Select("code").Where("code = ?", targetCapabilityCode).First(&capability).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrChannelCapabilityCapabilityNotFound
			}
			return nil, err
		}
	}

	var conflict int64
	if err := model.DB().Model(&model.ChannelCapability{}).
		Where("id <> ? AND channel_id = ? AND capability_code = ? AND model = ?", id, targetChannelID, targetCapabilityCode, targetModel).
		Count(&conflict).Error; err != nil {
		return nil, err
	}
	if conflict > 0 {
		return nil, ErrChannelCapabilityConflict
	}

	if err := model.DB().Model(&cc).Updates(updates).Error; err != nil {
		return nil, err
	}
	if err := model.DB().Preload("Channel").Preload("Capability").First(&cc, id).Error; err != nil {
		return nil, err
	}
	return &cc, nil
}

func parseChannelCapabilityUint(value any) (uint, error) {
	switch v := value.(type) {
	case uint:
		return v, nil
	case uint8:
		return uint(v), nil
	case uint16:
		return uint(v), nil
	case uint32:
		return uint(v), nil
	case uint64:
		return uint(v), nil
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("invalid uint value")
		}
		return uint(v), nil
	case int8:
		if v <= 0 {
			return 0, fmt.Errorf("invalid uint value")
		}
		return uint(v), nil
	case int16:
		if v <= 0 {
			return 0, fmt.Errorf("invalid uint value")
		}
		return uint(v), nil
	case int32:
		if v <= 0 {
			return 0, fmt.Errorf("invalid uint value")
		}
		return uint(v), nil
	case int64:
		if v <= 0 {
			return 0, fmt.Errorf("invalid uint value")
		}
		return uint(v), nil
	case float64:
		if v <= 0 || v != float64(uint(v)) {
			return 0, fmt.Errorf("invalid uint value")
		}
		return uint(v), nil
	default:
		return 0, fmt.Errorf("unsupported uint type %T", value)
	}
}

func parseChannelCapabilityString(value any) (string, error) {
	str, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("unsupported string type %T", value)
	}
	return str, nil
}

func (s *CapabilityAdminService) DeleteChannelCapability(id uint64) (int64, error) {
	result := model.DB().Delete(&model.ChannelCapability{}, id)
	return result.RowsAffected, result.Error
}
