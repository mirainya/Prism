package open

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	perrors "github.com/mirainya/Prism/pkg/errors"
)

var capabilityService = service.NewUnifiedService()

// InvokeCapability 调用能力接口
func InvokeCapability(c *gin.Context) {
	capability := c.Param("capability")
	if capability == "" {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "capability is required")
		return
	}

	var params map[string]any
	if err := c.ShouldBindJSON(&params); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid request body")
		return
	}

	channel, _ := params["channel"].(string)
	model, _ := params["model"].(string)
	callbackURL, _ := params["callback_url"].(string)

	delete(params, "channel")
	delete(params, "callback_url")

	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}

	req := &service.InvokeRequest{
		UserID:      token.UserID,
		TokenID:     token.ID,
		Capability:  capability,
		Channel:     channel,
		Model:       model,
		CallbackURL: callbackURL,
		Params:      params,
	}

	invokeResp, err := capabilityService.Invoke(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrInsufficientTokenBalance) || errors.Is(err, service.ErrInsufficientUserBalance) {
			resp.BadRequest(c, perrors.WithMessage(perrors.ErrInsufficientQuota, err.Error()))
			return
		}
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, invokeResp)
}

// GetTaskByNo 查询任务
func GetTaskByNo(c *gin.Context) {
	taskNo := c.Param("task_no")
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}

	task, err := capabilityService.GetTask(c.Request.Context(), taskNo, token.UserID)
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "task not found")
		return
	}

	resp.Success(c, gin.H{
		"task_id":  task.TaskNo,
		"status":   task.Status,
		"progress": task.Progress,
		"result":   task.Result,
		"error":    task.ErrorMessage,
		"cost":     task.Cost,
	})
}

// CancelTask 取消任务
func CancelTask(c *gin.Context) {
	taskNo := c.Param("task_no")
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}

	err := capabilityService.CancelTask(c.Request.Context(), taskNo, token.UserID)
	if err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	resp.Success(c, gin.H{"message": "task cancelled"})
}
