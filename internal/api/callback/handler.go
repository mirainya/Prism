package callback

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

type callbackService interface {
	HandleCallback(ctx context.Context, task *model.Task, body map[string]any) error
}

var unifiedService callbackService = service.NewUnifiedService()

// HandleCapabilityCallback 处理供应商回调
func HandleCapabilityCallback(c *gin.Context) {
	taskValue, ok := c.Get(middleware.CallbackTaskContextKey)
	task, validTask := taskValue.(*model.Task)
	if !ok || !validTask {
		resp.ErrorMsg(c, http.StatusForbidden, 403, "invalid callback authentication")
		return
	}

	var body map[string]any
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid request body")
		return
	}

	err := unifiedService.HandleCallback(c.Request.Context(), task, body)
	if err != nil {
		if errors.Is(err, service.ErrVendorTaskMismatch) ||
			errors.Is(err, service.ErrTaskNotFound) ||
			errors.Is(err, service.ErrInvalidCallbackStatus) {
			resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
			return
		}
		logger.Error("handle capability callback failed",
			zap.Uint("task_id", task.ID),
			zap.Error(err))
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "callback processing failed")
		return
	}

	resp.Success(c, gin.H{"message": "ok"})
}
