package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/httputil"
	"github.com/mirainya/Prism/pkg/logger"
	"github.com/mirainya/Prism/pkg/storage"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// HandleCallback 处理供应商回调
func (s *CapabilityService) HandleCallback(ctx context.Context, channelType string, body map[string]any) error {
	// 查找渠道
	var channel model.Channel
	if err := model.DB().Where("type = ?", channelType).First(&channel).Error; err != nil {
		return fmt.Errorf("channel not found: %s", channelType)
	}

	// 查找该渠道下的能力配置
	var ccs []model.ChannelCapability
	model.DB().Where("channel_id = ?", channel.ID).Find(&ccs)

	// 尝试解析回调
	for _, cc := range ccs {
		mappingData := cc.CallbackMapping
		if len(mappingData) == 0 {
			mappingData = cc.ResponseMapping
		}
		if len(mappingData) == 0 {
			continue
		}

		result, _ := s.responseMapper.Map(body, mappingData)
		vendorTaskID, _ := result["task_id"].(string)
		if vendorTaskID == "" {
			continue
		}

		// 查找任务
		task, err := s.GetTaskByVendorID(ctx, vendorTaskID)
		if err != nil {
			continue
		}

		// 先检查成功条件（基于原始响应）
		isSuccess, isFailed := s.responseMapper.CheckSuccess(body, mappingData)

		// 如果配置了成功条件，优先使用
		if isSuccess {
			s.completeTask(task, &cc, result)
			return nil
		}
		if isFailed {
			errMsg, _ := result["error"].(string)
			if errMsg == "" {
				errMsg = "callback failed by success condition"
			}
			s.failTask(task, errMsg)
			return nil
		}

		// 如果没有配置成功条件，使用原有的 status 字段判断逻辑
		status, _ := result["status"].(string)
		if status == "success" {
			s.completeTask(task, &cc, result)
		} else if status == "failed" {
			errMsg, _ := result["error"].(string)
			s.failTask(task, errMsg)
		}
		return nil
	}

	return fmt.Errorf("no matching task found for callback")
}

// completeTask 完成任务（包含文件转存）
func (s *CapabilityService) completeTask(task *model.Task, cc *model.ChannelCapability, result map[string]any) {
	ctx := context.Background()

	// 尝试转存文件到COS
	if storage.DefaultStorage != nil {
		// 获取结果URL（统一使用 url 字段）
		originURL, _ := result["url"].(string)

		if originURL != "" {
			// 下载原始文件
			downloadResult, err := httputil.Download(ctx, originURL)
			if err != nil {
				logger.Error("download file for transfer failed", zap.String("task_no", task.TaskNo), zap.Error(err))
			} else {
				defer downloadResult.Body.Close()

				// 生成存储路径
				storagePath := s.generateStoragePath(task.CapabilityCode, originURL)

				// 上传到COS
				finalURL, err := storage.Upload(ctx, downloadResult.Body, storagePath, downloadResult.ContentType)
				if err != nil {
					logger.Error("upload to storage failed", zap.String("task_no", task.TaskNo), zap.Error(err))
				} else {
					result["url"] = finalURL
					logger.Info("file transferred to storage", zap.String("task_no", task.TaskNo), zap.String("url", finalURL))
				}
			}
		}
	}

	resultJSON, _ := json.Marshal(result)
	now := time.Now()
	model.DB().Model(task).Updates(map[string]any{
		"status":       model.TaskStatusSuccess,
		"progress":     100,
		"result":       resultJSON,
		"cost":         cc.Price,
		"completed_at": now,
	})

	logger.Info("capability task completed", zap.String("task_no", task.TaskNo))

	// 释放账号并发
	strategyService := NewStrategyService()
	strategyService.DecrementAccountTasks(task.AccountID)

	// 发送回调
	if task.CallbackURL != "" {
		go s.sendCallback(task)
	}
}

// generateStoragePath 生成存储路径
func (s *CapabilityService) generateStoragePath(capabilityCode string, originURL string) string {
	now := time.Now()
	ext := filepath.Ext(originURL)
	if ext == "" || len(ext) > 10 {
		if strings.Contains(capabilityCode, "video") {
			ext = ".mp4"
		} else {
			ext = ".png"
		}
	}
	// 去除ext中可能的查询参数
	if idx := strings.Index(ext, "?"); idx > 0 {
		ext = ext[:idx]
	}
	return fmt.Sprintf("%s/%s/%s%s", capabilityCode, now.Format("2006/01/02"), uuid.New().String(), ext)
}

// failTask 任务失败
func (s *CapabilityService) failTask(task *model.Task, errMsg string) {
	now := time.Now()
	updates := map[string]any{
		"status":        model.TaskStatusFailed,
		"error_message": errMsg,
		"completed_at":  now,
	}

	// 如果有扣费，退回余额
	if task.Cost.GreaterThan(decimal.Zero) {
		result := model.DB().Model(&model.Token{}).Where("id = ?", task.TokenID).Updates(map[string]any{
			"balance":    gorm.Expr("balance + ?", task.Cost),
			"total_used": gorm.Expr("total_used - ?", task.Cost),
		})
		if result.RowsAffected > 0 {
			// 退还用户余额
			model.DB().Model(&model.User{}).Where("id = ?", task.UserID).
				UpdateColumn("balance", gorm.Expr("balance + ?", task.Cost))
			updates["refunded"] = true
			logger.Info("refunded cost for failed task",
				zap.String("task_no", task.TaskNo),
				zap.String("cost", task.Cost.String()))
		}
	}

	model.DB().Model(task).Updates(updates)

	logger.Warn("capability task failed", zap.String("task_no", task.TaskNo), zap.String("error", errMsg))

	if task.CallbackURL != "" {
		go s.sendCallback(task)
	}
}

// sendCallback 发送回调给调用方
func (s *CapabilityService) sendCallback(task *model.Task) {
	// 重新查询任务获取最新数据
	model.DB().First(task, task.ID)

	ctx := context.Background()
	payload := map[string]any{
		"task_id": task.TaskNo,
		"status":  task.Status,
	}

	if task.Status == model.TaskStatusSuccess {
		var result map[string]any
		json.Unmarshal(task.Result, &result)
		payload["result"] = result
	}
	if task.ErrorMessage != "" {
		payload["error"] = task.ErrorMessage
	}

	maxAttempts := 3
	for i := 0; i < maxAttempts; i++ {
		detail := httputil.PostJSONWithDetail(ctx, task.CallbackURL, payload, nil)
		s.logRequest(task, model.RequestTypeCallback, detail)
		if detail.Error == nil {
			model.DB().Model(task).Updates(map[string]any{
				"callback_status":   model.CallbackStatusSuccess,
				"callback_attempts": i + 1,
			})
			return
		}
		time.Sleep(time.Duration(i+1) * 5 * time.Second)
	}

	model.DB().Model(task).Updates(map[string]any{
		"callback_status":   model.CallbackStatusFailed,
		"callback_attempts": maxAttempts,
	})
}

// releaseAccount 释放账号
func (s *CapabilityService) releaseAccount(accountID uint) {
	model.DB().Model(&model.ChannelAccount{}).
		Where("id = ? AND current_tasks > 0", accountID).
		UpdateColumn("current_tasks", gorm.Expr("current_tasks - 1"))
}

// extractString 安全地从 any 类型提取字符串
func extractString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%.0f", val)
	case int:
		return fmt.Sprintf("%d", val)
	case int64:
		return fmt.Sprintf("%d", val)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// logRequest 记录渠道请求日志
func (s *CapabilityService) logRequest(task *model.Task, reqType model.RequestType, detail *httputil.RequestDetail) {
	headersJSON, _ := json.Marshal(detail.RequestHeaders)
	log := &model.ChannelRequestLog{
		TaskID:         task.ID,
		TaskNo:         task.TaskNo,
		ChannelID:      task.ChannelID,
		AccountID:      task.AccountID,
		CapabilityCode: task.CapabilityCode,
		RequestType:    reqType,
		Method:         detail.Method,
		URL:            detail.URL,
		RequestHeaders: string(headersJSON),
		RequestBody:    detail.RequestBody,
		StatusCode:     detail.StatusCode,
		ResponseBody:   string(detail.ResponseBody),
		DurationMs:     detail.DurationMs,
		RequestAt:      time.Now(),
	}
	if detail.Error != nil {
		log.ErrorMessage = detail.Error.Error()
	}
	NewRequestLogService().Log(log)
}
