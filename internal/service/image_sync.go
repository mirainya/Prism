package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/model"
)

// DefaultSyncWaitMaxSeconds 异步渠道同步等待的默认上限(秒)
// 上限内出图直接返回;超限则降级返回 task_no 交由客户端后续查询
const DefaultSyncWaitMaxSeconds = 300

// SyncWaitPollInterval 同步等待时的轮询间隔(秒)
const SyncWaitPollInterval = 2

// ImageResult 同步等待的统一图片结果
type ImageResult struct {
	// Done 为 true 表示已拿到最终结果(成功或失败);false 表示超时降级
	Done          bool
	Success       bool
	TaskNo        string
	Status        string
	URLs          []string
	RevisedPrompt string
	Error         string
	HTTPStatus    int
	ErrorBody     json.RawMessage
}

// taskResultPayload 对应 task.Result 存储的统一结果结构
type taskResultPayload struct {
	URL           string   `json:"url"`
	URLs          []string `json:"urls"`
	RevisedPrompt string   `json:"revised_prompt"`
}

// InvokeAndWait 调用能力并同步等待结果:
//   - sync/stream 渠道: Invoke 返回时已完成,直接读结果
//   - 异步渠道: 轮询任务状态直到成功/失败/超 maxWaitSeconds
//
// 超时不报错,返回 Done=false 交由 handler 降级处理(返回 task_no)。
func (s *UnifiedService) InvokeAndWait(ctx context.Context, req *InvokeRequest, maxWaitSeconds int) (*ImageResult, error) {
	if maxWaitSeconds <= 0 {
		maxWaitSeconds = DefaultSyncWaitMaxSeconds
	}

	invokeResp, err := s.Invoke(ctx, req)
	if err != nil {
		return nil, err
	}

	// sync 渠道: Invoke 已同步执行完,状态直接是终态
	if invokeResp.Status == string(model.TaskStatusSuccess) {
		task, err := s.taskByNo(invokeResp.TaskID, req.UserID)
		if err != nil {
			return nil, err
		}
		return buildImageResult(task), nil
	}
	if invokeResp.Status == string(model.TaskStatusFailed) {
		task, _ := s.taskByNo(invokeResp.TaskID, req.UserID)
		return buildFailedImageResult(task, invokeResp.TaskID, invokeResp.Status), nil
	}

	// 异步渠道: 轮询等待
	deadline := time.Now().Add(time.Duration(maxWaitSeconds) * time.Second)
	ticker := time.NewTicker(SyncWaitPollInterval * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			task, err := s.taskByNo(invokeResp.TaskID, req.UserID)
			if err != nil {
				return nil, err
			}
			switch task.Status {
			case model.TaskStatusSuccess:
				return buildImageResult(task), nil
			case model.TaskStatusFailed, model.TaskStatusCancelled:
				return buildFailedImageResult(task, task.TaskNo, string(task.Status)), nil
			}
			// 仍在处理中,检查是否超时
			if time.Now().After(deadline) {
				return &ImageResult{
					Done:   false,
					TaskNo: task.TaskNo,
					Status: string(task.Status.Public()),
				}, nil
			}
		}
	}
}

func buildFailedImageResult(task *model.Task, taskNo, status string) *ImageResult {
	result := &ImageResult{
		Done:    true,
		Success: false,
		TaskNo:  taskNo,
		Status:  status,
	}
	if task == nil {
		return result
	}
	result.Error = task.ErrorMessage
	result.HTTPStatus, result.ErrorBody = imageUpstreamFailure(json.RawMessage(task.VendorResponse), result.Error)
	if result.HTTPStatus == 0 && result.Error != "" {
		result.HTTPStatus = normalizedImageFailureStatus(domain.UpstreamStatusCode(errors.New(result.Error)))
	}
	return result
}

func imageUpstreamFailure(raw json.RawMessage, fallbackMessage string) (int, json.RawMessage) {
	if len(raw) == 0 || !json.Valid(raw) {
		return 0, nil
	}

	var payload any
	if json.Unmarshal(raw, &payload) == nil {
		if statusCode, body := findEmbeddedOpenAIError(payload); statusCode != 0 && len(body) > 0 {
			return statusCode, body
		}
	}

	var directEnvelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &directEnvelope) == nil && len(directEnvelope.Error) > 0 {
		statusCode := normalizedImageFailureStatus(domain.UpstreamStatusCode(errors.New(string(raw))))
		if statusCode == 0 {
			statusCode = http.StatusUnprocessableEntity
		}
		return statusCode, append(json.RawMessage(nil), raw...)
	}

	statusCode := normalizedImageFailureStatus(domain.UpstreamStatusCode(errors.New(string(raw))))
	if statusCode == 0 {
		statusCode = http.StatusUnprocessableEntity
	}
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"message":           fallbackMessage,
			"type":              "upstream_task_error",
			"code":              "upstream_task_failed",
			"upstream_response": json.RawMessage(raw),
		},
	})
	if err != nil {
		return statusCode, nil
	}
	return statusCode, body
}

func findEmbeddedOpenAIError(value any) (int, json.RawMessage) {
	switch typed := value.(type) {
	case string:
		statusCode := normalizedImageFailureStatus(domain.UpstreamStatusCode(errors.New(typed)))
		if statusCode == 0 {
			return 0, nil
		}
		for index := strings.Index(typed, "{"); index >= 0; {
			candidate := strings.TrimSpace(typed[index:])
			if json.Valid([]byte(candidate)) {
				var envelope struct {
					Error json.RawMessage `json:"error"`
				}
				if json.Unmarshal([]byte(candidate), &envelope) == nil && len(envelope.Error) > 0 {
					return statusCode, json.RawMessage(candidate)
				}
			}
			next := strings.Index(typed[index+1:], "{")
			if next < 0 {
				break
			}
			index += next + 1
		}
	case []any:
		for _, item := range typed {
			if statusCode, body := findEmbeddedOpenAIError(item); statusCode != 0 {
				return statusCode, body
			}
		}
	case map[string]any:
		for _, item := range typed {
			if statusCode, body := findEmbeddedOpenAIError(item); statusCode != 0 {
				return statusCode, body
			}
		}
	}
	return 0, nil
}

func normalizedImageFailureStatus(statusCode int) int {
	if statusCode >= 400 && statusCode < 600 {
		return statusCode
	}
	return 0
}

// taskByNo 按 task_no + user 读取任务
func (s *UnifiedService) taskByNo(taskNo string, userID uint) (*model.Task, error) {
	return NewTaskService().GetTaskByNoAndUser(taskNo, userID)
}

// buildImageResult 从成功任务的 Result 字段构造图片结果
func buildImageResult(task *model.Task) *ImageResult {
	res := &ImageResult{
		Done:    true,
		Success: true,
		TaskNo:  task.TaskNo,
		Status:  string(task.Status),
	}
	if len(task.Result) > 0 {
		var payload taskResultPayload
		if json.Unmarshal(task.Result, &payload) == nil {
			if len(payload.URLs) > 0 {
				res.URLs = payload.URLs
			} else if payload.URL != "" {
				res.URLs = []string{payload.URL}
			}
			res.RevisedPrompt = payload.RevisedPrompt
		}
	}
	return res
}
