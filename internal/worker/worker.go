package worker

import (
	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/gateway/engine"
	responsepipeline "github.com/mirainya/Prism/internal/gateway/responses"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/queue"
)

var (
	taskService              = service.NewTaskService()
	circuitService           = service.NewAccountCircuitService()
	responsePipe             *responsepipeline.Pipeline
	newProvider              = provider.NewProvider
	finishCapabilityAttempt  = service.FinishCapabilityAttempt
	saveTaskSubmitCheckpoint = func(taskID uint, leaseOwner string, checkpoint *service.TaskSubmitCheckpoint) error {
		return taskService.SaveTaskSubmitCheckpoint(taskID, leaseOwner, checkpoint)
	}
	recoverTaskSubmit = queue.RecoverTaskSubmit
)

func RegisterHandlers(mux *asynq.ServeMux, executionEngine *engine.Engine) {
	if executionEngine == nil {
		panic("Gateway V2 engine is required")
	}
	responsePipe = responsepipeline.New(executionEngine)
	mux.HandleFunc(TypeTaskSubmit, HandleTaskSubmit)
	mux.HandleFunc(TypeTaskPoll, HandleTaskPoll)
	mux.HandleFunc(TypeTaskUpload, HandleTaskUpload)
	mux.HandleFunc(TypeTaskNotify, HandleTaskNotify)
	mux.HandleFunc(TypeTaskTimeoutCheck, HandleTaskTimeoutCheck)
	mux.HandleFunc(TypeModelDiscoverySync, HandleModelDiscoverySync)
	mux.HandleFunc(TypeResponseBackground, HandleResponseBackground)
	mux.HandleFunc(TypeResponseRecovery, HandleResponseRecovery)
	mux.HandleFunc(TypeAPICallPayloadCleanup, HandleAPICallPayloadCleanup)
}
