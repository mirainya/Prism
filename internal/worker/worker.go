package worker

import (
	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/service"
)

var (
	taskService     = service.NewTaskService()
	strategyService = service.NewStrategyService()
)

func RegisterHandlers(mux *asynq.ServeMux) {
	mux.HandleFunc(TypeTaskSubmit, HandleTaskSubmit)
	mux.HandleFunc(TypeTaskPoll, HandleTaskPoll)
	mux.HandleFunc(TypeTaskUpload, HandleTaskUpload)
	mux.HandleFunc(TypeTaskNotify, HandleTaskNotify)
	mux.HandleFunc(TypeTaskTimeoutCheck, HandleTaskTimeoutCheck)
}
