package open

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/video"
	perrors "github.com/mirainya/Prism/pkg/errors"
)

func getLegacyVideoGeneration(c *gin.Context, userID, tokenID uint) {
	task, err := capabilityService.GetTaskForToken(c.Request.Context(), c.Param("id"), userID, tokenID)
	if err != nil || task.RouteOperation != service.RouteOperationVideosGenerate {
		resp.NotFound(c, perrors.ErrTaskNotFound)
		return
	}
	resp.Success(c, legacyVideoTaskToResponse(task))
}

func cancelLegacyVideoGeneration(c *gin.Context, userID, tokenID uint) {
	task, err := capabilityService.GetTaskForToken(c.Request.Context(), c.Param("id"), userID, tokenID)
	if err != nil || task.RouteOperation != service.RouteOperationVideosGenerate {
		resp.NotFound(c, perrors.ErrTaskNotFound)
		return
	}
	if task.Status.IsTerminal() {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "task already in terminal state"))
		return
	}
	if err := capabilityService.CancelTaskForToken(c.Request.Context(), task.TaskNo, userID, tokenID); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	resp.Success(c, gin.H{"id": task.TaskNo, "status": video.VideoTaskStatusCancelled})
}

func legacyVideoTaskToResponse(t *model.Task) videoTaskResponse {
	status := mapLegacyVideoStatus(t.Status)
	billingStatus := "reserved"
	if status == string(video.VideoTaskStatusCompleted) {
		billingStatus = "charged"
	} else if t.Refunded {
		billingStatus = "refunded"
	}
	r := videoTaskResponse{
		ID: t.TaskNo, Model: t.ModelCode, Status: status, Progress: t.Progress,
		EstimatedCost: t.Cost.String(), FinalCost: t.Cost.String(), BillingStatus: billingStatus,
		CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
	if len(t.RequestParams) > 0 {
		var params map[string]any
		if json.Unmarshal(t.RequestParams, &params) == nil {
			r.Prompt, _ = params["prompt"].(string)
			r.Resolution, _ = params["resolution"].(string)
			r.Ratio, _ = params["ratio"].(string)
			r.TaskMode, _ = params["task_mode"].(string)
			if duration, ok := params["duration"].(float64); ok {
				r.Duration = int(duration)
			}
			r.GenerateAudio, _ = params["generate_audio"].(bool)
		}
	}
	if t.ErrorMessage != "" {
		r.ErrorMessage = t.ErrorMessage
	}
	if len(t.Result) > 2 {
		_ = json.Unmarshal(t.Result, &r.Result)
	}
	if t.CompletedAt != nil {
		completedAt := t.CompletedAt.Format(time.RFC3339)
		r.CompletedAt = &completedAt
	}
	return r
}

func mapLegacyVideoStatus(status model.TaskStatus) string {
	switch status.Public() {
	case model.TaskStatusPending:
		return string(video.VideoTaskStatusQueued)
	case model.TaskStatusProcessing:
		return string(video.VideoTaskStatusTracking)
	case model.TaskStatusSuccess:
		return string(video.VideoTaskStatusCompleted)
	case model.TaskStatusCancelled:
		return string(video.VideoTaskStatusCancelled)
	default:
		return string(video.VideoTaskStatusFailed)
	}
}
