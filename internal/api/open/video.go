package open

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
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
	getLegacyVideoGeneration(c, token.UserID, token.ID)
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

	cancelLegacyVideoGeneration(c, token.UserID, token.ID)
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
