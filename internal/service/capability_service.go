package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// InvokeRequest 能力调用请求
type InvokeRequest struct {
	UserID      uint
	TokenID     uint
	Capability  string
	Channel     string
	Model       string
	CallbackURL string
	Params      map[string]any
}

// InvokeResponse 能力调用响应
type InvokeResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// Invoke 调用能力
func (s *UnifiedService) Invoke(ctx context.Context, req *InvokeRequest) (*InvokeResponse, error) {
	// 查找 endpoint
	endpoint, err := s.findEndpointForCapability(req)
	if err != nil {
		return nil, err
	}

	var channel model.Channel
	if err := model.DB().First(&channel, endpoint.ChannelID).Error; err != nil {
		return nil, fmt.Errorf("channel not found")
	}

	account, err := s.selectAccount(channel.ID)
	if err != nil {
		return nil, fmt.Errorf("no available account")
	}

	// 扣费
	cost := endpoint.InputPrice
	if cost.GreaterThan(decimal.Zero) {
		if err := s.billingService.Deduct(req.TokenID, req.UserID, cost); err != nil {
			return nil, ErrInsufficientTokenBalance
		}
	}

	// 参数映射
	mappedParams := mapParams(req.Params, endpoint.ParamMapping)

	// 创建任务
	taskSvc := NewTaskService()
	task, err := taskSvc.CreateTask(&CreateTaskRequest{
		UserID:        req.UserID,
		TokenID:       req.TokenID,
		ModelCode:     req.Capability,
		ChannelID:     channel.ID,
		EndpointID:    endpoint.ID,
		AccountID:     account.ID,
		RequestParams: req.Params,
		MappedParams:  mappedParams,
		CallbackURL:   req.CallbackURL,
		Cost:          cost,
	})
	if err != nil {
		return nil, err
	}

	// sync/stream 模式直接执行
	if endpoint.InteractionMode == model.ModeSync || endpoint.InteractionMode == model.ModeStream {
		return s.executeSyncTask(ctx, task, endpoint, &channel, account)
	}

	// 异步模式入队
	if err := queue.EnqueueTaskSubmit(task.ID); err != nil {
		logger.Error("enqueue submit failed", zap.Uint("task_id", task.ID), zap.Error(err))
	}

	return &InvokeResponse{
		TaskID: task.TaskNo,
		Status: string(task.Status),
	}, nil
}

func (s *UnifiedService) executeSyncTask(ctx context.Context, task *model.Task, endpoint *model.Endpoint, channel *model.Channel, account *model.ChannelAccount) (*InvokeResponse, error) {
	taskSvc := NewTaskService()

	prov, err := provider.NewProvider(channel, account, endpoint)
	if err != nil {
		taskSvc.UpdateTaskFail(task.ID, "create provider error: "+err.Error())
		s.decrementAccountTasks(account.ID)
		return nil, err
	}

	var mappedParams map[string]any
	json.Unmarshal(task.MappedParams, &mappedParams)

	result, err := prov.Submit(ctx, provider.SubmitRequest{
		TaskNo: task.TaskNo,
		Params: mappedParams,
	})
	if err != nil {
		taskSvc.UpdateTaskFail(task.ID, err.Error())
		s.decrementAccountTasks(account.ID)
		return nil, err
	}

	taskSvc.UpdateTaskSuccess(task.ID, map[string]any{"data": result.ProviderTaskID, "urls": result.URLs}, endpoint.InputPrice)
	s.decrementAccountTasks(account.ID)

	return &InvokeResponse{
		TaskID: task.TaskNo,
		Status: string(model.TaskStatusSuccess),
	}, nil
}

// GetTask 获取任务状态
func (s *UnifiedService) GetTask(ctx context.Context, taskNo string, userID uint) (*model.Task, error) {
	taskSvc := NewTaskService()
	return taskSvc.GetTaskByNoAndUser(taskNo, userID)
}

// CancelTask 取消任务
func (s *UnifiedService) CancelTask(ctx context.Context, taskNo string, userID uint) error {
	taskSvc := NewTaskService()
	return taskSvc.CancelTask(taskNo, userID)
}

// HandleCallback 处理供应商回调
func (s *UnifiedService) HandleCallback(ctx context.Context, channelType string, body map[string]any) error {
	taskID, _ := body["task_id"].(string)
	if taskID == "" {
		return errors.New("missing task_id in callback")
	}

	taskSvc := NewTaskService()
	task, err := taskSvc.GetTaskByVendorID(taskID)
	if err != nil {
		return fmt.Errorf("task not found for vendor_id: %s", taskID)
	}

	var endpoint model.Endpoint
	if err := model.DB().First(&endpoint, task.EndpointID).Error; err != nil {
		return err
	}

	result := extractCallbackResult(body, endpoint.CallbackMapping)
	taskSvc.UpdateTaskSuccess(task.ID, result, endpoint.InputPrice)
	s.decrementAccountTasks(task.AccountID)

	return nil
}

func (s *UnifiedService) findEndpointForCapability(req *InvokeRequest) (*model.Endpoint, error) {
	query := model.DB().Where("model_code = ? AND status = 1", req.Capability)
	if req.Channel != "" {
		var ch model.Channel
		if err := model.DB().Where("type = ? AND status = 1", req.Channel).First(&ch).Error; err == nil {
			query = query.Where("channel_id = ?", ch.ID)
		}
	}

	var endpoint model.Endpoint
	if err := query.Order("priority DESC").First(&endpoint).Error; err != nil {
		return nil, fmt.Errorf("no available endpoint for capability: %s", req.Capability)
	}
	return &endpoint, nil
}

func mapParams(params map[string]any, mapping datatypes.JSON) map[string]any {
	if len(mapping) == 0 {
		return params
	}

	// 尝试解析为结构化格式 {field_mapping: {...}, fixed_params: {...}}
	var structured struct {
		FieldMapping map[string]string `json:"field_mapping"`
		FixedParams  map[string]any    `json:"fixed_params"`
	}
	if err := json.Unmarshal(mapping, &structured); err == nil && (structured.FieldMapping != nil || structured.FixedParams != nil) {
		result := make(map[string]any)
		// 应用字段映射
		if structured.FieldMapping != nil {
			for k, v := range params {
				if mapped, ok := structured.FieldMapping[k]; ok {
					result[mapped] = v
				} else {
					result[k] = v
				}
			}
		} else {
			for k, v := range params {
				result[k] = v
			}
		}
		// 注入固定参数（不覆盖用户传入的值）
		for k, v := range structured.FixedParams {
			if _, exists := result[k]; !exists {
				result[k] = v
			}
		}
		return result
	}

	// 兼容旧格式：简单 key 映射
	var m map[string]string
	if err := json.Unmarshal(mapping, &m); err != nil {
		return params
	}
	result := make(map[string]any)
	for k, v := range params {
		if mapped, ok := m[k]; ok {
			result[mapped] = v
		} else {
			result[k] = v
		}
	}
	return result
}

func extractCallbackResult(body map[string]any, mapping datatypes.JSON) map[string]any {
	if len(mapping) == 0 {
		return body
	}
	var m map[string]string
	if err := json.Unmarshal(mapping, &m); err != nil {
		return body
	}
	result := make(map[string]any)
	for target, source := range m {
		if v, ok := body[source]; ok {
			result[target] = v
		}
	}
	if len(result) == 0 {
		return body
	}
	return result
}

// --- Admin Service (Model + Endpoint CRUD) ---

type ModelAdminService struct{}

func NewModelAdminService() *ModelAdminService {
	return &ModelAdminService{}
}

// CreateModelRequest 创建模型请求
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

		// 创建 Endpoint（渠道映射）
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

// --- Endpoint Admin ---

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

// CreateEndpointRequest 创建端点请求
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

// QuickSetupRequest 快速设置请求
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

// QuickSetupResult 快速设置结果
type QuickSetupResult struct {
	Created int `json:"created"`
	Skipped int `json:"skipped"`
	Mapped  int `json:"mapped"`
}
