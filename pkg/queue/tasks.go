package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

const (
	TypeTaskSubmit          = "task:submit"
	TypeTaskPoll            = "task:poll"
	TypeTaskUpload          = "task:upload"
	TypeTaskNotify          = "task:notify"
	TypeTaskTimeoutCheck    = "task:timeout_check"
	TypeResponseBackground  = "response:background"
	TypeResponseRecovery    = "response:recovery"
	TypeVideoSubmit         = "video:submit"
	TypeVideoPoll           = "video:poll"
	TypeVideoNotify         = "video:notify"
	responseBackgroundQueue = "default"
	taskSubmitQueue         = "critical"
	videoSubmitQueue        = "critical"
	videoPollQueue          = "default"
)

type TaskSubmitPayload struct {
	TaskID uint `json:"task_id"`
}

type TaskPollPayload struct {
	TaskID    uint `json:"task_id"`
	PollCount int  `json:"poll_count"`
}

type TaskUploadPayload struct {
	TaskID        uint     `json:"task_id"`
	OriginURL     string   `json:"origin_url"`
	URLs          []string `json:"urls"`
	RevisedPrompt string   `json:"revised_prompt,omitempty"`
}

type TaskNotifyPayload struct {
	TaskID uint `json:"task_id"`
}

type ResponseBackgroundPayload struct {
	ResponseID string `json:"response_id"`
}

func EnqueueResponseBackground(responseID string) error {
	if Client == nil {
		return errors.New("queue client is not initialized")
	}
	payload, err := json.Marshal(ResponseBackgroundPayload{ResponseID: responseID})
	if err != nil {
		return err
	}
	_, err = Client.Enqueue(asynq.NewTask(TypeResponseBackground, payload), asynq.Queue(responseBackgroundQueue), asynq.TaskID(responseBackgroundTaskID(responseID)), asynq.MaxRetry(DefaultMaxRetry()))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// RecoverResponseBackground ensures that a non-terminal response has a
// runnable task. Retained terminal tasks are replaced during startup recovery.
func RecoverResponseBackground(responseID string) error {
	if Client == nil {
		return errors.New("queue client is not initialized")
	}
	inspector := asynq.NewInspector(redisClientOpt())
	defer inspector.Close()

	taskID := responseBackgroundTaskID(responseID)
	info, err := inspector.GetTaskInfo(responseBackgroundQueue, taskID)
	if err != nil {
		if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
			return EnqueueResponseBackground(responseID)
		}
		return fmt.Errorf("inspect response background task %s: %w", responseID, err)
	}
	if info.Type != TypeResponseBackground {
		return fmt.Errorf("response background task ID %s belongs to type %s", taskID, info.Type)
	}
	switch info.State {
	case asynq.TaskStateActive, asynq.TaskStatePending, asynq.TaskStateScheduled,
		asynq.TaskStateRetry, asynq.TaskStateAggregating:
		return nil
	case asynq.TaskStateArchived, asynq.TaskStateCompleted:
		if err := inspector.DeleteTask(responseBackgroundQueue, taskID); err != nil {
			return recoverAfterDeleteRace(inspector, responseID, taskID, err)
		}
		return EnqueueResponseBackground(responseID)
	default:
		return fmt.Errorf("response background task %s has unsupported state %s", taskID, info.State)
	}
}

func recoverAfterDeleteRace(inspector *asynq.Inspector, responseID, taskID string, deleteErr error) error {
	info, err := inspector.GetTaskInfo(responseBackgroundQueue, taskID)
	if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
		return EnqueueResponseBackground(responseID)
	}
	if err == nil && info.Type == TypeResponseBackground {
		switch info.State {
		case asynq.TaskStateActive, asynq.TaskStatePending, asynq.TaskStateScheduled,
			asynq.TaskStateRetry, asynq.TaskStateAggregating:
			return nil
		}
	}
	return fmt.Errorf("replace stale response background task %s: %w", responseID, deleteErr)
}

func responseBackgroundTaskID(responseID string) string {
	return "response-background-" + responseID
}

func EnqueueTaskSubmit(taskID uint) error {
	if Client == nil {
		return errors.New("queue client is not initialized")
	}
	payload := TaskSubmitPayload{TaskID: taskID}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	task := asynq.NewTask(TypeTaskSubmit, payloadBytes)
	_, err = Client.Enqueue(
		task,
		asynq.Queue(taskSubmitQueue),
		asynq.TaskID(taskSubmitTaskID(taskID)),
		asynq.MaxRetry(DefaultMaxRetry()),
	)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// RecoverTaskSubmit makes the task row the durable submit intent. A missing or
// stale Asynq entry is recreated with the same deterministic task ID.
func RecoverTaskSubmit(taskID uint) error {
	if Client == nil {
		return errors.New("queue client is not initialized")
	}
	inspector := asynq.NewInspector(redisClientOpt())
	defer inspector.Close()

	queueTaskID := taskSubmitTaskID(taskID)
	info, err := inspector.GetTaskInfo(taskSubmitQueue, queueTaskID)
	if err != nil {
		if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
			return EnqueueTaskSubmit(taskID)
		}
		return fmt.Errorf("inspect task submit %d: %w", taskID, err)
	}
	if info.Type != TypeTaskSubmit {
		return fmt.Errorf("task submit ID %s belongs to type %s", queueTaskID, info.Type)
	}
	switch info.State {
	case asynq.TaskStateActive, asynq.TaskStatePending, asynq.TaskStateScheduled,
		asynq.TaskStateRetry, asynq.TaskStateAggregating:
		return nil
	case asynq.TaskStateArchived, asynq.TaskStateCompleted:
		if err := inspector.DeleteTask(taskSubmitQueue, queueTaskID); err != nil {
			return recoverTaskSubmitAfterDeleteRace(inspector, taskID, queueTaskID, err)
		}
		return EnqueueTaskSubmit(taskID)
	default:
		return fmt.Errorf("task submit %d has unsupported state %s", taskID, info.State)
	}
}

func recoverTaskSubmitAfterDeleteRace(inspector *asynq.Inspector, taskID uint, queueTaskID string, deleteErr error) error {
	info, err := inspector.GetTaskInfo(taskSubmitQueue, queueTaskID)
	if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
		return EnqueueTaskSubmit(taskID)
	}
	if err == nil && info.Type == TypeTaskSubmit {
		switch info.State {
		case asynq.TaskStateActive, asynq.TaskStatePending, asynq.TaskStateScheduled,
			asynq.TaskStateRetry, asynq.TaskStateAggregating:
			return nil
		}
	}
	return fmt.Errorf("replace stale task submit %d: %w", taskID, deleteErr)
}

func taskSubmitTaskID(taskID uint) string {
	return fmt.Sprintf("task-submit-%d", taskID)
}

// EnqueueTaskUpload 入队上传任务（回调/轮询成功后复用同一上传流水线）
func EnqueueTaskUpload(taskID uint, originURL string, urls []string, revisedPrompt ...string) error {
	payload := TaskUploadPayload{TaskID: taskID, OriginURL: originURL, URLs: urls}
	if len(revisedPrompt) > 0 {
		payload.RevisedPrompt = revisedPrompt[0]
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	task := asynq.NewTask(TypeTaskUpload, payloadBytes)
	_, err = Client.Enqueue(task,
		asynq.Queue("default"),
		asynq.TaskID(fmt.Sprintf("task-upload-%d", taskID)),
	)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// --- Video Engine Queue ---

type VideoSubmitPayload struct {
	TaskID string `json:"task_id"`
}

type VideoPollPayload struct {
	TaskID    string `json:"task_id"`
	PollCount int    `json:"poll_count"`
}

type VideoNotifyPayload struct {
	TaskID string `json:"task_id"`
}

func EnqueueVideoSubmit(taskID string) error {
	if Client == nil {
		return errors.New("queue client is not initialized")
	}
	payloadBytes, err := json.Marshal(VideoSubmitPayload{TaskID: taskID})
	if err != nil {
		return err
	}
	task := asynq.NewTask(TypeVideoSubmit, payloadBytes)
	_, err = Client.Enqueue(task,
		asynq.Queue(videoSubmitQueue),
		asynq.TaskID(videoSubmitTaskID(taskID)),
		asynq.MaxRetry(DefaultMaxRetry()),
	)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// RecoverVideoSubmit recreates a missing or stale queue entry from video_tasks.
func RecoverVideoSubmit(taskID string) error {
	if Client == nil {
		return errors.New("queue client is not initialized")
	}
	inspector := asynq.NewInspector(redisClientOpt())
	defer inspector.Close()
	queueTaskID := videoSubmitTaskID(taskID)
	info, err := inspector.GetTaskInfo(videoSubmitQueue, queueTaskID)
	if err != nil {
		if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
			return EnqueueVideoSubmit(taskID)
		}
		return fmt.Errorf("inspect video submit %s: %w", taskID, err)
	}
	if info.Type != TypeVideoSubmit {
		return fmt.Errorf("video submit ID %s belongs to type %s", queueTaskID, info.Type)
	}
	switch info.State {
	case asynq.TaskStateActive, asynq.TaskStatePending, asynq.TaskStateScheduled,
		asynq.TaskStateRetry, asynq.TaskStateAggregating:
		return nil
	case asynq.TaskStateArchived, asynq.TaskStateCompleted:
		if err := inspector.DeleteTask(videoSubmitQueue, queueTaskID); err != nil {
			return fmt.Errorf("delete stale video submit %s: %w", taskID, err)
		}
		return EnqueueVideoSubmit(taskID)
	default:
		return fmt.Errorf("video submit %s has unsupported state %s", taskID, info.State)
	}
}

func videoSubmitTaskID(taskID string) string {
	return "video-submit-" + taskID
}

func EnqueueVideoPoll(taskID string, pollCount int, delay ...int) error {
	if Client == nil {
		return errors.New("queue client is not initialized")
	}
	payloadBytes, err := json.Marshal(VideoPollPayload{TaskID: taskID, PollCount: pollCount})
	if err != nil {
		return err
	}
	task := asynq.NewTask(TypeVideoPoll, payloadBytes)
	opts := []asynq.Option{
		asynq.Queue(videoPollQueue),
		asynq.TaskID(videoPollTaskID(taskID, pollCount)),
		asynq.MaxRetry(DefaultMaxRetry()),
	}
	if len(delay) > 0 && delay[0] > 0 {
		opts = append(opts, asynq.ProcessIn(time.Duration(delay[0])*time.Second))
	}
	_, err = Client.Enqueue(task, opts...)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// RecoverVideoPoll recreates a missing or stale poll task using its persisted poll count.
func RecoverVideoPoll(taskID string, pollCount int) error {
	if Client == nil {
		return errors.New("queue client is not initialized")
	}
	inspector := asynq.NewInspector(redisClientOpt())
	defer inspector.Close()
	queueTaskID := videoPollTaskID(taskID, pollCount)
	info, err := inspector.GetTaskInfo(videoPollQueue, queueTaskID)
	if err != nil {
		if errors.Is(err, asynq.ErrTaskNotFound) || errors.Is(err, asynq.ErrQueueNotFound) {
			return EnqueueVideoPoll(taskID, pollCount)
		}
		return fmt.Errorf("inspect video poll %s: %w", taskID, err)
	}
	if info.Type != TypeVideoPoll {
		return fmt.Errorf("video poll ID %s belongs to type %s", queueTaskID, info.Type)
	}
	switch info.State {
	case asynq.TaskStateActive, asynq.TaskStatePending, asynq.TaskStateScheduled,
		asynq.TaskStateRetry, asynq.TaskStateAggregating:
		return nil
	case asynq.TaskStateArchived, asynq.TaskStateCompleted:
		if err := inspector.DeleteTask(videoPollQueue, queueTaskID); err != nil {
			return fmt.Errorf("delete stale video poll %s: %w", taskID, err)
		}
		return EnqueueVideoPoll(taskID, pollCount)
	default:
		return fmt.Errorf("video poll %s has unsupported state %s", taskID, info.State)
	}
}

func videoPollTaskID(taskID string, pollCount int) string {
	return fmt.Sprintf("video-poll-%s-%d", taskID, pollCount)
}

func EnqueueVideoNotify(taskID string) error {
	if Client == nil {
		return errors.New("queue client is not initialized")
	}
	payloadBytes, err := json.Marshal(VideoNotifyPayload{TaskID: taskID})
	if err != nil {
		return err
	}
	task := asynq.NewTask(TypeVideoNotify, payloadBytes)
	_, err = Client.Enqueue(task,
		asynq.Queue("default"),
		asynq.TaskID(fmt.Sprintf("video-notify-%s", taskID)),
		asynq.MaxRetry(DefaultMaxRetry()),
	)
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}
