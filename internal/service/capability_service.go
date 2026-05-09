package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/mapping"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type CapabilityService struct {
	paramMapper    *mapping.ParamMapper
	responseMapper *mapping.ResponseMapper
}

func NewCapabilityService() *CapabilityService {
	return &CapabilityService{
		paramMapper:    mapping.NewParamMapper(),
		responseMapper: mapping.NewResponseMapper(),
	}
}

// InvokeRequest 调用请求
type InvokeRequest struct {
	UserID      uint
	TokenID     uint
	Capability  string
	Channel     string
	Model       string
	CallbackURL string
	Params      map[string]any
}

// InvokeResponse 调用响应
type InvokeResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// Invoke 调用能力接口
func (s *CapabilityService) Invoke(ctx context.Context, req *InvokeRequest) (*InvokeResponse, error) {
	// 1. 查找渠道
	var channel model.Channel
	var cc model.ChannelCapability

	if req.Channel != "" {
		// 指定了渠道，使用指定渠道
		if err := model.DB().Where("type = ? AND status = ?", req.Channel, 1).First(&channel).Error; err != nil {
			return nil, fmt.Errorf("channel not found: %s", req.Channel)
		}

		// 查找渠道能力配置
		query := model.DB().Where("channel_id = ? AND capability_code = ? AND status = ?",
			channel.ID, req.Capability, 1)
		if req.Model != "" {
			query = query.Where("model = ?", req.Model)
		}
		if err := query.First(&cc).Error; err != nil {
			return nil, fmt.Errorf("capability not supported: %s/%s", req.Channel, req.Capability)
		}
	} else {
		// 未指定渠道，按令牌配置的优先级查找
		found, err := s.selectChannelByTokenPriority(req.TokenID, req.Capability, req.Model, &channel, &cc)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("no channel configured for capability: %s", req.Capability)
		}
	}

	// 3. 如果配置了单价，检查余额并扣费
	logger.Info("capability price check",
		zap.String("capability", req.Capability),
		zap.String("price", cc.Price.String()))

	billingService := NewBillingService()
	charged := false
	if cc.Price.GreaterThan(decimal.Zero) {
		if err := billingService.Deduct(req.TokenID, req.UserID, cc.Price); err != nil {
			return nil, fmt.Errorf("insufficient balance: %w", err)
		}
		charged = true
	}

	// 4. 选择账号
	var account model.ChannelAccount
	err := model.DB().Where("channel_id = ? AND status = 1", channel.ID).
		Order("current_tasks ASC, weight DESC").
		First(&account).Error
	if err != nil {
		// 扣费失败需要退回
		if charged {
			_ = billingService.Refund(req.TokenID, req.UserID, cc.Price)
		}
		return nil, fmt.Errorf("no available account")
	}

	// 增加账号任务数
	model.DB().Model(&account).UpdateColumn("current_tasks", gorm.Expr("current_tasks + 1"))

	// 5. 参数映射
	mappedParams, err := s.paramMapper.Map(req.Params, cc.ParamMapping)
	if err != nil {
		// 扣费失败需要退回
		if charged {
			_ = billingService.Refund(req.TokenID, req.UserID, cc.Price)
		}
		return nil, fmt.Errorf("param mapping failed: %w", err)
	}

	// 6. 创建任务
	requestParamsJSON, _ := json.Marshal(req.Params)
	mappedParamsJSON, _ := json.Marshal(mappedParams)

	task := &model.Task{
		TaskNo:              GenerateTaskNo(),
		UserID:              req.UserID,
		TokenID:             req.TokenID,
		CapabilityCode:      req.Capability,
		ChannelID:           channel.ID,
		ChannelCapabilityID: cc.ID,
		AccountID:           account.ID,
		Status:              model.TaskStatusPending,
		CallbackURL:         req.CallbackURL,
		RequestParams:       requestParamsJSON,
		MappedParams:        mappedParamsJSON,
		Cost:                cc.Price,
	}
	if err := model.DB().Create(task).Error; err != nil {
		// 创建任务失败需要退回
		if charged {
			_ = billingService.Refund(req.TokenID, req.UserID, cc.Price)
		}
		return nil, fmt.Errorf("create task failed: %w", err)
	}

	logger.Info("capability task created",
		zap.String("task_no", task.TaskNo),
		zap.String("capability", req.Capability),
		zap.String("channel", req.Channel),
		zap.String("cost", cc.Price.String()))

	// 7. 异步执行任务
	go s.executeTask(task, &channel, &cc, &account, mappedParams)

	return &InvokeResponse{
		TaskID: task.TaskNo,
		Status: string(task.Status),
	}, nil
}

// GetTask 查询任务
func (s *CapabilityService) GetTask(ctx context.Context, taskNo string, userID uint, tokenID ...uint) (*model.Task, error) {
	query := model.DB().Where("task_no = ? AND user_id = ?", taskNo, userID)
	if len(tokenID) > 0 && tokenID[0] > 0 {
		query = query.Where("token_id = ?", tokenID[0])
	}

	var task model.Task
	err := query.First(&task).Error
	return &task, err
}

// GetTaskByVendorID 根据供应商任务ID查询
func (s *CapabilityService) GetTaskByVendorID(ctx context.Context, vendorTaskID string) (*model.Task, error) {
	var task model.Task
	err := model.DB().Where("vendor_task_id = ?", vendorTaskID).First(&task).Error
	return &task, err
}

// CancelTask 取消任务
func (s *CapabilityService) CancelTask(ctx context.Context, taskNo string, userID uint) error {
	result := model.DB().Model(&model.Task{}).
		Where("task_no = ? AND user_id = ? AND status IN ?", taskNo, userID,
			[]model.TaskStatus{model.TaskStatusPending, model.TaskStatusProcessing}).
		Update("status", model.TaskStatusCancelled)
	if result.RowsAffected == 0 {
		return fmt.Errorf("task not found or cannot be cancelled")
	}
	return result.Error
}

// PollResult 手动轮询接口
func (s *CapabilityService) PollResult(ctx context.Context, taskNo string, userID uint) (*model.Task, error) {
	task, err := s.GetTask(ctx, taskNo, userID)
	if err != nil {
		return nil, err
	}
	return task, nil
}

// selectChannelByTokenPriority 按令牌配置的优先级查找可用渠道
func (s *CapabilityService) selectChannelByTokenPriority(
	tokenID uint,
	capabilityCode string,
	modelName string,
	channel *model.Channel,
	cc *model.ChannelCapability,
) (bool, error) {
	// 查询令牌的渠道优先级配置
	var priorities []model.TokenChannelPriority
	err := model.DB().
		Where("token_id = ? AND capability_code = ?", tokenID, capabilityCode).
		Order("priority ASC").
		Find(&priorities).Error
	if err != nil {
		return false, fmt.Errorf("failed to get token channel priorities: %w", err)
	}

	// 按优先级遍历配置
	for _, p := range priorities {
		// 检查渠道是否启用
		var ch model.Channel
		if err := model.DB().Where("id = ? AND status = ?", p.ChannelID, 1).First(&ch).Error; err != nil {
			continue
		}

		// 检查渠道能力配置是否存在且启用
		query := model.DB().Where("channel_id = ? AND capability_code = ? AND status = ?",
			p.ChannelID, capabilityCode, 1)
		if modelName != "" {
			query = query.Where("model = ?", modelName)
		}

		var capConfig model.ChannelCapability
		if err := query.First(&capConfig).Error; err != nil {
			continue
		}

		// 找到可用渠道
		*channel = ch
		*cc = capConfig
		return true, nil
	}

	// 没有配置优先级或优先级配置的渠道都不可用，使用默认查找
	query := model.DB().Where("capability_code = ? AND status = ?", capabilityCode, 1)
	if modelName != "" {
		query = query.Where("model = ?", modelName)
	}

	var defaultCC model.ChannelCapability
	if err := query.Order("id ASC").First(&defaultCC).Error; err != nil {
		return false, nil
	}

	var defaultCh model.Channel
	if err := model.DB().Where("id = ? AND status = ?", defaultCC.ChannelID, 1).First(&defaultCh).Error; err != nil {
		return false, nil
	}

	*channel = defaultCh
	*cc = defaultCC
	return true, nil
}
