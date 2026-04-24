package worker

import "github.com/mirainya/Prism/pkg/queue"

const (
	TypeTaskSubmit       = queue.TypeTaskSubmit
	TypeTaskPoll         = queue.TypeTaskPoll
	TypeTaskUpload       = queue.TypeTaskUpload
	TypeTaskNotify       = queue.TypeTaskNotify
	TypeTaskTimeoutCheck = queue.TypeTaskTimeoutCheck
)

type TaskSubmitPayload = queue.TaskSubmitPayload
type TaskPollPayload = queue.TaskPollPayload
type TaskUploadPayload = queue.TaskUploadPayload
type TaskNotifyPayload = queue.TaskNotifyPayload
