package service

import (
	"context"
	"encoding/json"
	"time"

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
		res := &ImageResult{Done: true, Success: false, TaskNo: invokeResp.TaskID, Status: invokeResp.Status}
		if task != nil {
			res.Error = task.ErrorMessage
		}
		return res, nil
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
				return &ImageResult{
					Done:    true,
					Success: false,
					TaskNo:  task.TaskNo,
					Status:  string(task.Status),
					Error:   task.ErrorMessage,
				}, nil
			}
			// 仍在处理中,检查是否超时
			if time.Now().After(deadline) {
				return &ImageResult{
					Done:   false,
					TaskNo: task.TaskNo,
					Status: string(task.Status),
				}, nil
			}
		}
	}
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
