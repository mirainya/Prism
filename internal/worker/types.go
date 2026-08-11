package worker

import "github.com/mirainya/Prism/pkg/queue"

const (
	TypeTaskSubmit            = queue.TypeTaskSubmit
	TypeTaskPoll              = queue.TypeTaskPoll
	TypeTaskUpload            = queue.TypeTaskUpload
	TypeTaskNotify            = queue.TypeTaskNotify
	TypeTaskTimeoutCheck      = queue.TypeTaskTimeoutCheck
	TypeResponseBackground    = queue.TypeResponseBackground
	TypeResponseRecovery      = queue.TypeResponseRecovery
	TypeVideoSubmit           = queue.TypeVideoSubmit
	TypeVideoPoll             = queue.TypeVideoPoll
	TypeVideoNotify           = queue.TypeVideoNotify
	TypeAPICallPayloadCleanup = "api_call:payload_cleanup"
)

type TaskSubmitPayload = queue.TaskSubmitPayload
type TaskPollPayload = queue.TaskPollPayload
type TaskUploadPayload = queue.TaskUploadPayload
type TaskNotifyPayload = queue.TaskNotifyPayload
type ResponseBackgroundPayload = queue.ResponseBackgroundPayload
type VideoSubmitPayload = queue.VideoSubmitPayload
type VideoPollPayload = queue.VideoPollPayload
type VideoNotifyPayload = queue.VideoNotifyPayload
