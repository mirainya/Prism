package queue

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

const (
	TypeTaskSubmit       = "task:submit"
	TypeTaskPoll         = "task:poll"
	TypeTaskUpload       = "task:upload"
	TypeTaskNotify       = "task:notify"
	TypeTaskTimeoutCheck = "task:timeout_check"
)

type TaskSubmitPayload struct {
	TaskID uint `json:"task_id"`
}

type TaskPollPayload struct {
	TaskID    uint `json:"task_id"`
	PollCount int  `json:"poll_count"`
}

type TaskUploadPayload struct {
	TaskID    uint     `json:"task_id"`
	OriginURL string   `json:"origin_url"`
	URLs      []string `json:"urls"`
}

type TaskNotifyPayload struct {
	TaskID uint `json:"task_id"`
}

func EnqueueTaskSubmit(taskID uint) error {
	payload := TaskSubmitPayload{TaskID: taskID}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	task := asynq.NewTask(TypeTaskSubmit, payloadBytes)
	_, err = Client.Enqueue(task, asynq.Queue("critical"))
	return err
}
