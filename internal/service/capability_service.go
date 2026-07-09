package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

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
	UserID          uint
	TokenID         uint
	Capability      string
	Channel         string
	Model           string
	InteractionMode string
	CallbackURL     string
	Params          map[string]any
	// Async=true 时,sync/stream 模式不阻塞等待出图:立即返回 task_no+processing,
	// 后台 goroutine 跑 executeSyncWithFallback,由前端轮询取终态。
	// 仅 playground 置 true;对外 OpenAI 等接口须同步返图,保持 false。
	Async bool
}

// InvokeResponse 能力调用响应
type InvokeResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// Invoke 调用能力
func (s *UnifiedService) Invoke(ctx context.Context, req *InvokeRequest) (*InvokeResponse, error) {
	// 查找该能力所有可用端点(priority 降序),供跨端点/渠道 fallback
	endpoints, err := s.findEndpointsForCapability(req)
	if err != nil {
		return nil, err
	}
	primary := &endpoints[0]

	// 为首选端点选一个账号(白名单+熔断过滤),失败则依次尝试其他端点
	var channel model.Channel
	var account *model.ChannelAccount
	var chosen *model.Endpoint
	for i := range endpoints {
		ep := &endpoints[i]
		var ch model.Channel
		if err := model.DB().First(&ch, ep.ChannelID).Error; err != nil {
			continue
		}
		acc, err := s.selectAccountForEndpoint(ep, nil)
		if err != nil {
			continue // 该端点无可用账号(绑定 key 不可用 / 渠道池空),试下一个
		}
		channel, account, chosen = ch, acc, ep
		break
	}
	if chosen == nil {
		return nil, fmt.Errorf("no available account")
	}

	// 扣费(以首选端点价做余额守卫,结算/退款统一按此价,避免 fallback 端点价差导致账目不一致)
	cost := primary.InputPrice
	deducted := false
	if cost.GreaterThan(decimal.Zero) {
		if err := s.billingService.Deduct(req.TokenID, req.UserID, cost); err != nil {
			s.decrementAccountTasks(account.ID) // 已选中账号 +1,扣费失败需回滚计数
			return nil, ErrInsufficientTokenBalance
		}
		deducted = true
	}

	// 创建任务(记录实际选中的端点/渠道/账号)
	taskSvc := NewTaskService()
	task, err := taskSvc.CreateTask(&CreateTaskRequest{
		UserID:        req.UserID,
		TokenID:       req.TokenID,
		ModelCode:     req.Capability,
		ChannelID:     channel.ID,
		EndpointID:    chosen.ID,
		AccountID:     account.ID,
		RequestParams: req.Params,
		MappedParams:  mapParams(req.Params, chosen.ParamMapping),
		CallbackURL:   req.CallbackURL,
		Cost:          cost,
	})
	if err != nil {
		// 建任务失败: 回滚已扣费用与账号计数,避免钱和容量凭空泄漏
		if deducted {
			if refErr := s.billingService.Refund(req.TokenID, req.UserID, cost); refErr != nil {
				logger.Error("refund after create-task failure failed",
					zap.Uint("token_id", req.TokenID), zap.String("cost", cost.String()), zap.Error(refErr))
			}
		}
		s.decrementAccountTasks(account.ID)
		return nil, err
	}

	// sync/stream 模式直接执行(含换账号 + 跨端点/渠道 fallback)
	if chosen.InteractionMode == model.ModeSync || chosen.InteractionMode == model.ModeStream {
		// Async: 不阻塞 HTTP 请求,后台跑同步执行,前端轮询取终态(用于 playground 慢上游体验)
		if req.Async {
			taskSvc.UpdateTaskStatus(task.ID, model.TaskStatusProcessing, "")
			// HTTP ctx 会在响应返回后取消,后台须用独立 ctx;600s 上限兜底防 goroutine 永久挂起
			// (stream 模式内部另有 endpoint.Timeout 派生的 deadline,此处仅是外层保险)。
			go func(ep model.Endpoint, ch model.Channel, acc model.ChannelAccount) {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("async capability execution panicked",
							zap.Uint("task_id", task.ID), zap.Any("panic", r))
						taskSvc.UpdateTaskFail(task.ID, fmt.Sprintf("async execution panicked: %v", r))
					}
				}()
				bgCtx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
				defer cancel()
				if _, err := s.executeSyncWithFallback(bgCtx, task, req, endpoints, &ep, &ch, &acc); err != nil {
					logger.Warn("async capability execution failed",
						zap.Uint("task_id", task.ID), zap.Error(err))
				}
			}(*chosen, channel, *account)
			return &InvokeResponse{TaskID: task.TaskNo, Status: string(model.TaskStatusProcessing)}, nil
		}
		return s.executeSyncWithFallback(ctx, task, req, endpoints, chosen, &channel, account)
	}

	// 异步模式入队(跨渠道异步 fallback 属 worker 层,本期不做)
	if err := queue.EnqueueTaskSubmit(task.ID); err != nil {
		// 入队失败: 任务无法被 worker 处理,直接判失败并退款+递减计数,避免卡 pending 永久泄漏
		logger.Error("enqueue submit failed", zap.Uint("task_id", task.ID), zap.Error(err))
		taskSvc.UpdateTaskFail(task.ID, "enqueue submit failed: "+err.Error())
		s.decrementAccountTasks(account.ID)
		return nil, fmt.Errorf("enqueue submit failed: %w", err)
	}

	return &InvokeResponse{
		TaskID: task.TaskNo,
		Status: string(task.Status),
	}, nil
}

// executeSyncWithFallback 同步执行任务:
//
//	外层遍历 sync/stream 端点(跨渠道 fallback),内层对每个端点渠道换账号重试(熔断驱动)。
//	startEP/startCh/startAcc 是调用方已选定的首选端点/渠道/账号(已建入 task,首轮直接复用)。
func (s *UnifiedService) executeSyncWithFallback(ctx context.Context, task *model.Task, req *InvokeRequest, endpoints []model.Endpoint, startEP *model.Endpoint, startCh *model.Channel, startAcc *model.ChannelAccount) (*InvokeResponse, error) {
	taskSvc := NewTaskService()
	circuitSvc := NewAccountCircuitService()
	accountPerEndpoint := 3
	var lastErr error

	for i := range endpoints {
		ep := &endpoints[i]
		if ep.InteractionMode != model.ModeSync && ep.InteractionMode != model.ModeStream {
			continue
		}

		var channel model.Channel
		var account *model.ChannelAccount
		isStart := ep.ID == startEP.ID
		if isStart {
			channel, account = *startCh, startAcc
		} else {
			if err := model.DB().First(&channel, ep.ChannelID).Error; err != nil {
				continue
			}
		}

		mappedParams := mapParams(req.Params, ep.ParamMapping)
		var excludeAccountIDs []uint

		for attempt := 0; attempt < accountPerEndpoint; attempt++ {
			if account == nil {
				next, err := s.selectAccountForEndpoint(ep, excludeAccountIDs)
				if err != nil {
					break
				}
				account = next
			}

			model.DB().Model(&model.Task{}).Where("id = ?", task.ID).
				Updates(map[string]any{"endpoint_id": ep.ID, "channel_id": channel.ID, "account_id": account.ID})

			result, err := s.doSubmit(ctx, task, ep, &channel, account, mappedParams)
			s.decrementAccountTasks(account.ID)
			if err != nil {
				lastErr = err
				circuitSvc.MarkUnavailable(account.ID, ep.ModelCode, err)
				excludeAccountIDs = append(excludeAccountIDs, account.ID)
				logger.Warn("sync attempt failed, trying next account/endpoint",
					zap.Uint("task_id", task.ID), zap.Uint("endpoint_id", ep.ID),
					zap.Uint("account_id", account.ID), zap.Error(err))
				account = nil
				continue
			}

			if result.ProviderTaskID == "" && len(result.URLs) == 0 && len(result.B64Data) == 0 {
				lastErr = fmt.Errorf("upstream returned empty result")
				circuitSvc.MarkUnavailable(account.ID, ep.ModelCode, lastErr)
				excludeAccountIDs = append(excludeAccountIDs, account.ID)
				logger.Warn("sync attempt returned empty result, trying next account/endpoint",
					zap.Uint("task_id", task.ID), zap.Uint("endpoint_id", ep.ID),
					zap.Uint("account_id", account.ID), zap.Error(lastErr))
				account = nil
				continue
			}

			urls := append([]string{}, result.URLs...)
			if len(result.B64Data) > 0 {
				transferred, err := resolveB64ToURLs(ctx, result.B64Data, ep.ModelCode)
				if err != nil {
					lastErr = err
					taskSvc.UpdateTaskFail(task.ID, err.Error())
					return nil, err
				}
				urls = append(urls, transferred...)
			}

			successResult := map[string]any{"data": result.ProviderTaskID}
			if len(urls) > 0 {
				successResult["url"] = urls[0]
				successResult["urls"] = urls
			}
			if result.RevisedPrompt != "" {
				successResult["revised_prompt"] = result.RevisedPrompt
			}

			taskSvc.UpdateTaskSuccess(task.ID, successResult, task.Cost)
			return &InvokeResponse{
				TaskID: task.TaskNo,
				Status: string(model.TaskStatusSuccess),
			}, nil
		}
		account = nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no available endpoint/account for capability: %s", req.Capability)
	}
	taskSvc.UpdateTaskFail(task.ID, lastErr.Error())
	return nil, lastErr
}

// doSubmit 纯执行一次上游提交,不改 task 状态、不动账号计数(由调用方控制)
func (s *UnifiedService) doSubmit(ctx context.Context, task *model.Task, endpoint *model.Endpoint, channel *model.Channel, account *model.ChannelAccount, mappedParams map[string]any) (provider.SubmitResult, error) {
	prov, err := provider.NewProvider(channel, account, endpoint)
	if err != nil {
		return provider.SubmitResult{}, fmt.Errorf("create provider error: %w", err)
	}
	return prov.Submit(ctx, provider.SubmitRequest{
		TaskNo: task.TaskNo,
		Params: mappedParams,
	})
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
		if committed, _ := taskSvc.UpdateTaskFail(task.ID, errMsg); committed { // 内部自动退款
			s.decrementAccountTasks(task.AccountID)
		}
	case provider.StatusSuccess:
		// 复用上传流水线（转存+通知），与轮询成功路径保持一致
		originURLs := append([]string{}, parsed.URLs...)
		originURLs = append(originURLs, parsed.B64Data...)
		originURL := ""
		if len(originURLs) > 0 {
			originURL = originURLs[0]
		}
		taskSvc.UpdateTaskProgress(task.ID, 100)
		if err := queue.EnqueueTaskUpload(task.ID, originURL, originURLs, parsed.RevisedPrompt); err != nil {
			// 入队失败: 任务不会进入 upload_worker(否则由其负责终态+递减),此处判失败并递减,避免卡 Processing
			logger.Error("enqueue upload from callback failed", zap.Uint("task_id", task.ID), zap.Error(err))
			if committed, _ := taskSvc.UpdateTaskFail(task.ID, "enqueue upload from callback failed: "+err.Error()); committed {
				s.decrementAccountTasks(task.AccountID)
			}
		}
		// 入队成功: 终态与账号计数递减由 upload_worker 统一处理,此处不再递减(避免重复)
	default:
		// 处理中：仅更新进度，不结算
		taskSvc.UpdateTaskProgress(task.ID, parsed.Progress)
	}

	return nil
}

// findEndpointsForCapability 返回该能力所有可用端点,按 priority 降序(供跨端点/渠道 fallback)
func (s *UnifiedService) findEndpointsForCapability(req *InvokeRequest) ([]model.Endpoint, error) {
	buildQuery := func() *gorm.DB {
		query := model.DB().Where("status = 1")
		if req.Channel != "" {
			var ch model.Channel
			if err := model.DB().Where("type = ? AND status = 1", req.Channel).First(&ch).Error; err == nil {
				query = query.Where("channel_id = ?", ch.ID)
			}
		}
		if req.InteractionMode != "" {
			query = query.Where("interaction_mode = ?", req.InteractionMode)
		}
		return query
	}

	var endpoints []model.Endpoint
	requestedModel := strings.TrimSpace(req.Model)
	capability := strings.TrimSpace(req.Capability)
	if requestedModel != "" && requestedModel != capability {
		err := buildQuery().
			Where("(model_code = ? OR vendor_model = ?)", requestedModel, requestedModel).
			Order("priority DESC, id ASC").
			Find(&endpoints).Error
		if err == nil && len(endpoints) > 0 {
			return endpoints, nil
		}
		endpoints = nil
	}

	if err := buildQuery().Where("model_code = ?", capability).Order("priority DESC, id ASC").Find(&endpoints).Error; err != nil || len(endpoints) == 0 {
		if requestedModel != "" {
			return nil, fmt.Errorf("no available endpoint for model: %s", requestedModel)
		}
		return nil, fmt.Errorf("no available endpoint for capability: %s", capability)
	}
	return endpoints, nil
}

func mapParams(params map[string]any, mapping datatypes.JSON) map[string]any {
	if len(mapping) == 0 {
		return params
	}

	var structured struct {
		FieldMapping   map[string]string `json:"field_mapping"`
		FixedParams    map[string]any    `json:"fixed_params"`
		ComputedParams map[string]string `json:"computed_params"`
	}
	if err := json.Unmarshal(mapping, &structured); err == nil && (structured.FieldMapping != nil || structured.FixedParams != nil || structured.ComputedParams != nil) {
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
		// 计算参数: 用模板从原始参数拼接目标字段(如 "{width}x{height}" → size)
		// 仅当模板引用的所有字段都存在时才生成,避免产出残缺值
		for targetField, template := range structured.ComputedParams {
			if computed, ok := computeParam(template, params); ok {
				result[targetField] = computed
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

// computeParamPattern 匹配模板中的 {field} 占位符
var computeParamPattern = regexp.MustCompile(`\{(\w+)\}`)

// computeParam 用原始参数填充模板占位符(如 "{width}x{height}")
// 仅当模板引用的所有字段都存在时返回 (结果, true); 缺任一字段返回 ("", false)
func computeParam(template string, params map[string]any) (string, bool) {
	complete := true
	result := computeParamPattern.ReplaceAllStringFunc(template, func(match string) string {
		key := match[1 : len(match)-1]
		if val, ok := params[key]; ok {
			return fmt.Sprintf("%v", val)
		}
		complete = false
		return ""
	})
	if !complete {
		return "", false
	}
	return result, true
}
