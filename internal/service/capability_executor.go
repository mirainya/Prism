package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/httputil"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

// executeTask 执行任务（异步）
func (s *CapabilityService) executeTask(
	task *model.Task,
	channel *model.Channel,
	cc *model.ChannelCapability,
	account *model.ChannelAccount,
	params map[string]any,
) {
	ctx := context.Background()
	defer s.releaseAccount(account.ID)

	// 更新状态为处理中
	now := time.Now()
	model.DB().Model(task).Updates(map[string]any{
		"status":     model.TaskStatusProcessing,
		"started_at": now,
	})

	// 构建请求URL
	url := channel.BaseURL + cc.RequestPath

	// 处理认证
	headers := make(map[string]string)
	authKey := cc.AuthKey
	if authKey == "" {
		authKey = "Authorization"
	}
	authValue := cc.AuthValuePrefix + account.APIKey

	switch cc.AuthLocation {
	case "header":
		headers[authKey] = authValue
	case "body":
		params[authKey] = account.APIKey
	case "query":
		if strings.Contains(url, "?") {
			url += "&" + authKey + "=" + account.APIKey
		} else {
			url += "?" + authKey + "=" + account.APIKey
		}
	default:
		headers["Authorization"] = "Bearer " + account.APIKey
	}

	// 发送请求（根据 ContentType 选择请求格式）
	detail := httputil.PostWithDetail(ctx, url, params, headers, cc.ContentType)
	s.logRequest(task, model.RequestTypeSubmit, detail)
	if detail.Error != nil {
		s.failTask(task, detail.Error.Error())
		return
	}
	resp := detail.ResponseBody

	// 保存原始响应
	model.DB().Model(task).Update("vendor_response", resp)

	// 解析响应
	var respMap map[string]any
	json.Unmarshal(resp, &respMap)

	// 根据结果模式处理
	switch cc.ResultMode {
	case model.ResultModeSync:
		s.handleSyncResult(task, cc, respMap)
	case model.ResultModePoll:
		s.handlePollResult(task, channel, cc, account, respMap)
	case model.ResultModeCallback:
		s.handleCallbackResult(task, cc, respMap)
	}
}

// handleSyncResult 处理同步结果
func (s *CapabilityService) handleSyncResult(task *model.Task, cc *model.ChannelCapability, resp map[string]any) {
	// 先检查成功条件（基于原始响应）
	isSuccess, isFailed := s.responseMapper.CheckSuccess(resp, cc.ResponseMapping)

	result, err := s.responseMapper.Map(resp, cc.ResponseMapping)
	if err != nil {
		s.failTask(task, err.Error())
		return
	}

	// 如果配置了成功条件
	if isSuccess {
		s.completeTask(task, cc, result)
		return
	}
	if isFailed {
		errMsg, _ := result["error"].(string)
		if errMsg == "" {
			errMsg = "request failed by success condition"
		}
		s.failTask(task, errMsg)
		return
	}

	// 没有配置成功条件时，检查映射后的 status 字段
	status, _ := result["status"].(string)
	if status == "failed" {
		errMsg, _ := result["error"].(string)
		if errMsg == "" {
			errMsg = "request failed"
		}
		s.failTask(task, errMsg)
		return
	}

	// status 不是 failed 则视为成功
	s.completeTask(task, cc, result)
}

// handlePollResult 处理轮询结果
func (s *CapabilityService) handlePollResult(
	task *model.Task,
	channel *model.Channel,
	cc *model.ChannelCapability,
	account *model.ChannelAccount,
	submitResp map[string]any,
) {
	ctx := context.Background()

	// 从提交响应中获取供应商任务ID
	submitResult, _ := s.responseMapper.Map(submitResp, cc.ResponseMapping)
	vendorTaskID := extractString(submitResult["task_id"])
	model.DB().Model(task).Update("vendor_task_id", vendorTaskID)

	logger.Info("start polling",
		zap.String("task_no", task.TaskNo),
		zap.String("vendor_task_id", vendorTaskID))

	// 确定轮询响应映射（优先使用专用配置，否则使用通用响应映射）
	pollRespMapping := cc.PollResponseMapping
	if len(pollRespMapping) == 0 {
		pollRespMapping = cc.ResponseMapping
	}

	// 确定轮询方法
	pollMethod := cc.PollMethod
	if pollMethod == "" {
		pollMethod = "GET"
	}

	// 构建轮询URL（支持路径中的变量替换）
	pollPath := cc.PollPath
	pollPath = strings.ReplaceAll(pollPath, "{task_id}", vendorTaskID)
	pollURL := channel.BaseURL + pollPath

	// 构建认证头
	authHeaders := s.buildAuthHeaders(cc, account)

	for i := 0; i < cc.PollMaxAttempts; i++ {
		time.Sleep(time.Duration(cc.PollInterval) * time.Second)

		var resp []byte
		var pollErr error

		if pollMethod == "POST" {
			// 构建轮询参数
			pollParams := map[string]any{"task_id": vendorTaskID}
			if len(cc.PollParamMapping) > 0 {
				pollParams, _ = s.paramMapper.Map(pollParams, cc.PollParamMapping)
			}
			// 轮询认证如果是 body，需要加入参数
			if cc.AuthLocation == "body" {
				authKey := cc.AuthKey
				if authKey == "" {
					authKey = "Authorization"
				}
				pollParams[authKey] = account.APIKey
			}
			detail := httputil.PostWithDetail(ctx, pollURL, pollParams, authHeaders, cc.ContentType)
			s.logRequest(task, model.RequestTypePoll, detail)
			resp = detail.ResponseBody
			pollErr = detail.Error
		} else {
			detail := httputil.GetJSONWithDetail(ctx, pollURL, authHeaders)
			s.logRequest(task, model.RequestTypePoll, detail)
			resp = detail.ResponseBody
			pollErr = detail.Error
		}

		if pollErr != nil {
			logger.Error("poll error", zap.Error(pollErr))
			continue
		}

		var respMap map[string]any
		json.Unmarshal(resp, &respMap)

		// 先检查成功条件（基于原始响应）
		isSuccess, isFailed := s.responseMapper.CheckSuccess(respMap, pollRespMapping)

		result, _ := s.responseMapper.Map(respMap, pollRespMapping)

		// 更新进度
		if progress, ok := result["progress"].(float64); ok {
			model.DB().Model(task).Update("progress", int(progress))
		}

		// 如果配置了成功条件，优先使用
		if isSuccess {
			s.completeTask(task, cc, result)
			return
		}
		if isFailed {
			errMsg, _ := result["error"].(string)
			if errMsg == "" {
				errMsg = "request failed by success condition"
			}
			s.failTask(task, errMsg)
			return
		}

		// 如果没有配置成功条件，使用原有的 status 字段判断逻辑
		status, _ := result["status"].(string)
		if status == "success" {
			s.completeTask(task, cc, result)
			return
		} else if status == "failed" {
			errMsg, _ := result["error"].(string)
			s.failTask(task, errMsg)
			return
		}
	}

	s.failTask(task, "poll timeout")
}

// buildAuthHeaders 构建认证头
func (s *CapabilityService) buildAuthHeaders(cc *model.ChannelCapability, account *model.ChannelAccount) map[string]string {
	if cc.AuthLocation != "header" {
		return nil
	}
	authKey := cc.AuthKey
	if authKey == "" {
		authKey = "Authorization"
	}
	authValue := cc.AuthValuePrefix + account.APIKey
	return map[string]string{authKey: authValue}
}

// handleCallbackResult 处理回调结果（提交后等待回调）
func (s *CapabilityService) handleCallbackResult(task *model.Task, cc *model.ChannelCapability, submitResp map[string]any) {
	result, _ := s.responseMapper.Map(submitResp, cc.ResponseMapping)
	vendorTaskID := extractString(result["task_id"])
	model.DB().Model(task).Update("vendor_task_id", vendorTaskID)
	// 等待回调，状态保持 processing
}
