package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/queue"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

// InvokeRequest 能力调用请求
type InvokeRequest struct {
	UserID          uint
	TokenID         uint
	Capability      string
	Channel         string
	Model           string
	InteractionMode string
	CallbackURL     string
	Params          map[string]any
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
		taskID, _ = body["id"].(string)
	}
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

	// 用配置驱动的解析器解析回调（读取 response/callback mapping 的 value_mapping、url、error）
	var channel model.Channel
	if err := model.DB().First(&channel, task.ChannelID).Error; err != nil {
		return err
	}
	var account model.ChannelAccount
	model.DB().First(&account, task.AccountID)

	bodyBytes, _ := json.Marshal(body)
	prov, err := provider.NewProvider(&channel, &account, &endpoint)
	if err != nil {
		return err
	}
	parsed, _, err := prov.ParseCallback(ctx, bodyBytes)
	if err != nil {
		return err
	}

	switch parsed.Status {
	case provider.StatusFail:
		errMsg := parsed.Error
		if errMsg == "" {
			errMsg = "upstream task failed"
		}
		taskSvc.UpdateTaskFail(task.ID, errMsg) // 内部自动退款
		s.decrementAccountTasks(task.AccountID)
	case provider.StatusSuccess:
		// 复用上传流水线（转存+通知），与轮询成功路径保持一致
		originURL := ""
		if len(parsed.URLs) > 0 {
			originURL = parsed.URLs[0]
		}
		taskSvc.UpdateTaskProgress(task.ID, 100)
		if err := queue.EnqueueTaskUpload(task.ID, originURL, parsed.URLs); err != nil {
			logger.Error("enqueue upload from callback failed", zap.Uint("task_id", task.ID), zap.Error(err))
		}
		s.decrementAccountTasks(task.AccountID)
	default:
		// 处理中：仅更新进度，不结算
		taskSvc.UpdateTaskProgress(task.ID, parsed.Progress)
	}

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
	if req.InteractionMode != "" {
		query = query.Where("interaction_mode = ?", req.InteractionMode)
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

	var structured struct {
		FieldMapping map[string]string `json:"field_mapping"`
		FixedParams  map[string]any    `json:"fixed_params"`
	}
	if err := json.Unmarshal(mapping, &structured); err == nil && (structured.FieldMapping != nil || structured.FixedParams != nil) {
		result := make(map[string]any)
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
		for k, v := range structured.FixedParams {
			if _, exists := result[k]; !exists {
				result[k] = v
			}
		}
		return result
	}

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