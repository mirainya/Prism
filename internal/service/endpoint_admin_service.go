package service

import (
	"errors"

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
	ErrEndpointChannelNotFound = errors.New("channel not found")
	ErrEndpointModelNotFound   = errors.New("model not found")
	ErrEndpointInvalidField    = errors.New("invalid field")
	ErrEndpointConflict        = errors.New("endpoint conflict")
)

func (s *EndpointAdminService) ListEndpoints(channelID, modelCode, status string) ([]model.Endpoint, error) {
	query := model.DB().Preload("Channel").Preload("Model")
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
	err := model.DB().Preload("Channel").Preload("Model").First(&ep, id).Error
	return &ep, err
}

type CreateEndpointRequest struct {
	ModelCode       string          `json:"model_code" binding:"required"`
	ChannelID       uint            `json:"channel_id" binding:"required"`
	Protocol        string          `json:"protocol"`
	RequestPath     string          `json:"request_path"`
	RequestMethod   string          `json:"request_method"`
	ContentType     string          `json:"content_type"`
	AuthLocation    string          `json:"auth_location"`
	AuthKey         string          `json:"auth_key"`
	AuthValuePrefix string          `json:"auth_value_prefix"`
	VendorModel     string          `json:"vendor_model"`
	InteractionMode string          `json:"interaction_mode"`
	SupportsStream  bool            `json:"supports_stream"`
	DefaultStream   bool            `json:"default_stream"`
	PriceMode       string          `json:"price_mode"`
	InputPrice      decimal.Decimal `json:"input_price"`
	OutputPrice     decimal.Decimal `json:"output_price"`
	ParamMapping    datatypes.JSON  `json:"param_mapping"`
	ResponseMapping datatypes.JSON  `json:"response_mapping"`
	PollPath        string          `json:"poll_path"`
	PollMethod      string          `json:"poll_method"`
	PollInterval    int             `json:"poll_interval"`
	PollMaxAttempts int             `json:"poll_max_attempts"`
	CallbackMapping datatypes.JSON  `json:"callback_mapping"`
	ExtraHeaders    datatypes.JSON  `json:"extra_headers"`
	ExtraConfig     datatypes.JSON  `json:"extra_config"`
	Timeout         int             `json:"timeout"`
	Priority        int             `json:"priority"`
	Status          int8            `json:"status"`
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
	ep := &model.Endpoint{
		ModelCode:       req.ModelCode,
		ChannelID:       req.ChannelID,
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
	if err := model.DB().Create(ep).Error; err != nil {
		return nil, err
	}
	return ep, nil
}

func (s *EndpointAdminService) UpdateEndpoint(id uint, updates map[string]any) (*model.Endpoint, error) {
	var ep model.Endpoint
	if err := model.DB().First(&ep, id).Error; err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	if chID, ok := updates["channel_id"]; ok {
		var ch model.Channel
		if err := model.DB().First(&ch, chID).Error; err != nil {
			return nil, ErrEndpointChannelNotFound
		}
	}
	if mc, ok := updates["model_code"]; ok {
		var m model.Model
		if err := model.DB().Where("code = ?", mc).First(&m).Error; err != nil {
			return nil, ErrEndpointModelNotFound
		}
	}
	if err := model.DB().Model(&ep).Updates(updates).Error; err != nil {
		return nil, err
	}
	model.DB().Preload("Channel").Preload("Model").First(&ep, id)
	return &ep, nil
}

func (s *EndpointAdminService) DeleteEndpoint(id uint) (int64, error) {
	result := model.DB().Delete(&model.Endpoint{}, id)
	return result.RowsAffected, result.Error
}
