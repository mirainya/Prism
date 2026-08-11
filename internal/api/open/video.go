package open

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/video"
	perrors "github.com/mirainya/Prism/pkg/errors"
	"gorm.io/gorm"
)

func GetVideoGeneration(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if videoEngine != nil {
		var task video.VideoTask
		if err := videoEngine.DB().Where("id = ? AND token_id = ?", c.Param("id"), token.ID).First(&task).Error; err == nil {
			resp.Success(c, videoTaskToResponse(&task))
			return
		} else if err != gorm.ErrRecordNotFound {
			resp.InternalError(c, perrors.ErrInternalError)
			return
		}
	}
	legacyTask, err := capabilityService.GetTaskForToken(c.Request.Context(), c.Param("id"), token.UserID, token.ID)
	if err != nil || legacyTask.RouteOperation != service.RouteOperationVideosGenerate {
		resp.NotFound(c, perrors.ErrTaskNotFound)
		return
	}
	resp.Success(c, legacyVideoTaskToResponse(legacyTask))
}

func CancelVideoGeneration(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if videoEngine != nil {
		var task video.VideoTask
		if err := videoEngine.DB().Where("id = ? AND token_id = ?", c.Param("id"), token.ID).First(&task).Error; err == nil {
			if task.Status.IsTerminal() {
				resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "task already in terminal state"))
				return
			}
			cancelled, cancelErr := videoEngine.CancelVideoTask(c.Request.Context(), &task)
			if cancelErr != nil {
				if errors.Is(cancelErr, video.ErrCancelNotSupported) || errors.Is(cancelErr, video.ErrCancelNotAllowed) {
					resp.ErrorMsg(c, http.StatusConflict, 409, cancelErr.Error())
					return
				}
				resp.ErrorMsg(c, http.StatusBadGateway, 502, cancelErr.Error())
				return
			}
			status := video.VideoTaskStatusCancelled
			if !cancelled {
				if loadErr := videoEngine.DB().Select("status").First(&task, "id = ?", task.ID).Error; loadErr != nil {
					resp.InternalError(c, perrors.ErrInternalError)
					return
				}
				status = task.Status
			}
			resp.Success(c, gin.H{"id": task.ID, "status": status})
			return
		} else if err != gorm.ErrRecordNotFound {
			resp.InternalError(c, perrors.ErrInternalError)
			return
		}
	}

	legacyTask, err := capabilityService.GetTaskForToken(c.Request.Context(), c.Param("id"), token.UserID, token.ID)
	if err != nil || legacyTask.RouteOperation != service.RouteOperationVideosGenerate {
		resp.NotFound(c, perrors.ErrTaskNotFound)
		return
	}
	if legacyTask.Status.IsTerminal() {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "task already in terminal state"))
		return
	}
	if err := capabilityService.CancelTaskForToken(c.Request.Context(), legacyTask.TaskNo, token.UserID, token.ID); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}
	resp.Success(c, gin.H{"id": legacyTask.TaskNo, "status": video.VideoTaskStatusCancelled})
}

type videoTaskResponse struct {
	ID            string  `json:"id"`
	Model         string  `json:"model"`
	Status        string  `json:"status"`
	Progress      int     `json:"progress"`
	TaskMode      string  `json:"task_mode"`
	Prompt        string  `json:"prompt,omitempty"`
	Resolution    string  `json:"resolution,omitempty"`
	Ratio         string  `json:"ratio,omitempty"`
	Duration      int     `json:"duration,omitempty"`
	GenerateAudio bool    `json:"generate_audio,omitempty"`
	EstimatedCost string  `json:"estimated_cost,omitempty"`
	FinalCost     string  `json:"final_cost,omitempty"`
	BillingStatus string  `json:"billing_status,omitempty"`
	Result        any     `json:"result,omitempty"`
	ErrorMessage  string  `json:"error_message,omitempty"`
	CreatedAt     string  `json:"created_at"`
	CompletedAt   *string `json:"completed_at,omitempty"`
}

func videoTaskToResponse(t *video.VideoTask) videoTaskResponse {
	r := videoTaskResponse{
		ID: t.ID, Model: t.Model, Status: string(t.Status), Progress: t.Progress,
		TaskMode: t.TaskMode, Prompt: t.Prompt, Resolution: t.Resolution, Ratio: t.Ratio,
		Duration: t.Duration, GenerateAudio: t.GenerateAudio,
		EstimatedCost: t.EstimatedCost.String(), FinalCost: t.FinalCost.String(), BillingStatus: t.BillingStatus,
		CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
	if t.ErrorMessage != "" {
		r.ErrorMessage = t.ErrorMessage
	}
	if len(t.ResultJSON) > 2 {
		var result any
		if json.Unmarshal(t.ResultJSON, &result) == nil {
			r.Result = result
		}
	}
	if t.CompletedAt != nil {
		completedAt := t.CompletedAt.Format(time.RFC3339)
		r.CompletedAt = &completedAt
	}
	return r
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
