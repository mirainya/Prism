package service

import (
	"errors"
	"fmt"
	"math"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type EndpointAdminService struct{}

func NewEndpointAdminService() *EndpointAdminService {
	return &EndpointAdminService{}
}

var (
	ErrEndpointChannelNotFound  = errors.New("channel not found")
	ErrEndpointModelNotFound    = errors.New("model not found")
	ErrEndpointInvalidField     = errors.New("invalid field")
	ErrEndpointConflict         = errors.New("endpoint conflict")
	ErrEndpointAccountMismatch  = errors.New("endpoint account belongs to another channel")
	ErrEndpointDuplicateAccount = errors.New("duplicate endpoint account")
	ErrEndpointInvalidBinding   = errors.New("invalid endpoint account binding")
)

func (s *EndpointAdminService) ListEndpoints(channelID, modelCode, status string) ([]model.Endpoint, error) {
	query := model.DB().Preload("Channel").Preload("Model").Preload("AccountBindings.Account")
	if channelID != "" {
		query = query.Where("channel_id = ?", channelID)
	}
	if modelCode != "" {
		query = query.Where("model_code = ?", modelCode)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var endpoints []model.Endpoint
	err := query.Order("created_at DESC").Find(&endpoints).Error
	return endpoints, err
}

func (s *EndpointAdminService) GetEndpoint(id uint) (*model.Endpoint, error) {
	var ep model.Endpoint
	err := model.DB().Preload("Channel").Preload("Model").Preload("AccountBindings.Account").First(&ep, id).Error
	return &ep, err
}

// EndpointAccountBindingInput is the admin representation of one endpoint/key binding.
type EndpointAccountBindingInput struct {
	AccountID uint  `json:"account_id" binding:"required"`
	Status    *int8 `json:"status"`
	Priority  int   `json:"priority"`
	Weight    int   `json:"weight"`
}

type CreateEndpointRequest struct {
	ModelCode       string                        `json:"model_code" binding:"required"`
	ChannelID       uint                          `json:"channel_id" binding:"required"`
	AccountID       uint                          `json:"account_id"`
	AccountBindings []EndpointAccountBindingInput `json:"account_bindings"`
	Protocol        string                        `json:"protocol"`
	RequestPath     string                        `json:"request_path"`
	RequestMethod   string                        `json:"request_method"`
	ContentType     string                        `json:"content_type"`
	AuthLocation    string                        `json:"auth_location"`
	AuthKey         string                        `json:"auth_key"`
	AuthValuePrefix string                        `json:"auth_value_prefix"`
	VendorModel     string                        `json:"vendor_model"`
	InteractionMode string                        `json:"interaction_mode"`
	SupportsStream  bool                          `json:"supports_stream"`
	DefaultStream   bool                          `json:"default_stream"`
	PriceMode       string                        `json:"price_mode"`
	InputPrice      decimal.Decimal               `json:"input_price"`
	OutputPrice     decimal.Decimal               `json:"output_price"`
	ParamSchema     datatypes.JSON                `json:"param_schema"`
	ParamMapping    datatypes.JSON                `json:"param_mapping"`
	ResponseMapping datatypes.JSON                `json:"response_mapping"`
	PollPath        string                        `json:"poll_path"`
	PollMethod      string                        `json:"poll_method"`
	PollInterval    int                           `json:"poll_interval"`
	PollMaxAttempts int                           `json:"poll_max_attempts"`
	CallbackMapping datatypes.JSON                `json:"callback_mapping"`
	ExtraHeaders    datatypes.JSON                `json:"extra_headers"`
	ExtraConfig     datatypes.JSON                `json:"extra_config"`
	Timeout         int                           `json:"timeout"`
	Priority        int                           `json:"priority"`
	Status          int8                          `json:"status"`
}

func (s *EndpointAdminService) CreateEndpoint(req *CreateEndpointRequest) (*model.Endpoint, error) {
	var ch model.Channel
	if err := model.DB().First(&ch, req.ChannelID).Error; err != nil {
		return nil, ErrEndpointChannelNotFound
	}
	var m model.Model
	if err := model.DB().Where("code = ?", req.ModelCode).First(&m).Error; err != nil {
		return nil, ErrEndpointModelNotFound
	}
	bindings := req.AccountBindings
	// 接受旧客户端的单 Key 字段，但新数据立即写入关联表。
	if len(bindings) == 0 && req.AccountID != 0 {
		bindings = []EndpointAccountBindingInput{{AccountID: req.AccountID}}
	}

	var endpointID uint
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		ep := &model.Endpoint{
			ModelCode:       req.ModelCode,
			ChannelID:       req.ChannelID,
			AccountID:       req.AccountID,
			Protocol:        model.Protocol(req.Protocol),
			RequestPath:     req.RequestPath,
			RequestMethod:   req.RequestMethod,
			ContentType:     req.ContentType,
			AuthLocation:    req.AuthLocation,
			AuthKey:         req.AuthKey,
			AuthValuePrefix: req.AuthValuePrefix,
			VendorModel:     req.VendorModel,
			InteractionMode: model.InteractionMode(req.InteractionMode),
			SupportsStream:  req.SupportsStream,
			DefaultStream:   req.DefaultStream,
			PriceMode:       model.PriceMode(req.PriceMode),
			InputPrice:      req.InputPrice,
			OutputPrice:     req.OutputPrice,
			ParamSchema:     req.ParamSchema,
			ParamMapping:    req.ParamMapping,
			ResponseMapping: req.ResponseMapping,
			PollPath:        req.PollPath,
			PollMethod:      req.PollMethod,
			PollInterval:    req.PollInterval,
			PollMaxAttempts: req.PollMaxAttempts,
			CallbackMapping: req.CallbackMapping,
			ExtraHeaders:    req.ExtraHeaders,
			ExtraConfig:     req.ExtraConfig,
			Timeout:         req.Timeout,
			Priority:        req.Priority,
			Status:          req.Status,
		}
		if ep.Status == 0 {
			ep.Status = 1
		}
		if err := tx.Create(ep).Error; err != nil {
			return err
		}
		if err := saveEndpointAccountBindings(tx, ep.ID, ep.ChannelID, bindings); err != nil {
			return err
		}
		endpointID = ep.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetEndpoint(endpointID)
}

func (s *EndpointAdminService) UpdateEndpoint(id uint, updates map[string]any, bindingUpdates ...*[]EndpointAccountBindingInput) (*model.Endpoint, error) {
	var bindingSpec *[]EndpointAccountBindingInput
	if len(bindingUpdates) > 0 {
		bindingSpec = bindingUpdates[0]
	}

	err := model.DB().Transaction(func(tx *gorm.DB) error {
		var ep model.Endpoint
		if err := tx.First(&ep, id).Error; err != nil {
			return gorm.ErrRecordNotFound
		}
		originalChannelID := ep.ChannelID
		channelID := ep.ChannelID
		if raw, ok := updates["channel_id"]; ok {
			parsed, err := endpointUint(raw)
			if err != nil || parsed == 0 {
				return ErrEndpointChannelNotFound
			}
			channelID = parsed
			var ch model.Channel
			if err := tx.First(&ch, channelID).Error; err != nil {
				return ErrEndpointChannelNotFound
			}
		}
		if mc, ok := updates["model_code"]; ok {
			var m model.Model
			if err := tx.Where("code = ?", mc).First(&m).Error; err != nil {
				return ErrEndpointModelNotFound
			}
		}
		if err := tx.Model(&ep).Updates(updates).Error; err != nil {
			return err
		}
		if bindingSpec != nil {
			return saveEndpointAccountBindings(tx, ep.ID, channelID, *bindingSpec)
		}
		if channelID != originalChannelID {
			var totalBindings int64
			if err := tx.Model(&model.EndpointAccount{}).Where("endpoint_id = ?", ep.ID).Count(&totalBindings).Error; err != nil {
				return err
			}
			var validBindings int64
			if err := tx.Model(&model.EndpointAccount{}).
				Joins("JOIN channel_accounts ON channel_accounts.id = endpoint_accounts.account_id AND channel_accounts.deleted_at IS NULL").
				Where("endpoint_accounts.endpoint_id = ? AND channel_accounts.channel_id = ?", ep.ID, channelID).
				Count(&validBindings).Error; err != nil {
				return err
			}
			if validBindings != totalBindings {
				return ErrEndpointAccountMismatch
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetEndpoint(id)
}

func (s *EndpointAdminService) DeleteEndpoint(id uint) (int64, error) {
	var rowsAffected int64
	err := model.DB().Transaction(func(tx *gorm.DB) error {
		result := tx.Where("endpoint_id = ?", id).Delete(&model.EndpointAccount{})
		if result.Error != nil {
			return result.Error
		}
		result = tx.Delete(&model.Endpoint{}, id)
		rowsAffected = result.RowsAffected
		return result.Error
	})
	return rowsAffected, err
}

func endpointUint(value any) (uint, error) {
	switch v := value.(type) {
	case uint:
		return v, nil
	case uint64:
		if uint64(uint(v)) != v {
			return 0, fmt.Errorf("endpoint id overflow")
		}
		return uint(v), nil
	case float64:
		if v < 0 || v != math.Trunc(v) || uint64(v) != uint64(uint(v)) {
			return 0, fmt.Errorf("invalid endpoint id")
		}
		return uint(v), nil
	case int:
		if v < 0 {
			return 0, fmt.Errorf("invalid endpoint id")
		}
		return uint(v), nil
	default:
		return 0, fmt.Errorf("invalid endpoint id type")
	}
}

func saveEndpointAccountBindings(tx *gorm.DB, endpointID, channelID uint, inputs []EndpointAccountBindingInput) error {
	seen := make(map[uint]struct{}, len(inputs))
	accountIDs := make([]uint, 0, len(inputs))
	rows := make([]model.EndpointAccount, 0, len(inputs))
	for _, input := range inputs {
		if input.AccountID == 0 {
			return ErrEndpointInvalidBinding
		}
		if _, ok := seen[input.AccountID]; ok {
			return ErrEndpointDuplicateAccount
		}
		seen[input.AccountID] = struct{}{}
		status := int8(1)
		if input.Status != nil {
			status = *input.Status
			if status != 0 && status != 1 {
				return ErrEndpointInvalidBinding
			}
		}
		weight := input.Weight
		if weight <= 0 {
			weight = 10
		}
		accountIDs = append(accountIDs, input.AccountID)
		rows = append(rows, model.EndpointAccount{
			EndpointID: endpointID,
			AccountID:  input.AccountID,
			Status:     status,
			Priority:   input.Priority,
			Weight:     weight,
		})
	}

	if len(accountIDs) > 0 {
		var count int64
		if err := tx.Model(&model.ChannelAccount{}).
			Where("id IN ? AND channel_id = ? AND deleted_at IS NULL", accountIDs, channelID).
			Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(accountIDs)) {
			return ErrEndpointAccountMismatch
		}
	}
	if err := tx.Where("endpoint_id = ?", endpointID).Delete(&model.EndpointAccount{}).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}
