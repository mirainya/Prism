package open

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
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
	getLegacyVideoGeneration(c, token.UserID, token.ID)
}

func GetVideoGenerationQueue(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if videoEngine == nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, video.ErrEngineUnavailable.Error())
		return
	}
	var task video.VideoTask
	if err := videoEngine.DB().Where("id = ? AND token_id = ?", c.Param("id"), token.ID).First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			resp.ErrorMsg(c, http.StatusNotFound, 404, "video task not found")
		} else {
			resp.InternalError(c, perrors.ErrInternalError)
		}
		return
	}
	metadata := video.DecodeProviderMetadata(task.ProviderMetadata)
	resp.Success(c, gin.H{"id": task.ID, "status": task.Status, "queue": metadata})
}

func ListVideoQueue(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if videoEngine == nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, video.ErrEngineUnavailable.Error())
		return
	}
	var tasks []video.VideoTask
	if err := videoEngine.DB().Where("token_id = ? AND status IN ?", token.ID, []video.VideoTaskStatus{video.VideoTaskStatusQueued, video.VideoTaskStatusSubmitted, video.VideoTaskStatusTracking}).Order("created_at ASC").Limit(100).Find(&tasks).Error; err != nil {
		resp.InternalError(c, perrors.ErrInternalError)
		return
	}
	items := make([]any, 0, len(tasks))
	queued, running := 0, 0
	for _, task := range tasks {
		if task.Status == video.VideoTaskStatusQueued || task.Status == video.VideoTaskStatusSubmitted {
			queued++
		} else {
			running++
		}
		items = append(items, gin.H{"id": task.ID, "status": task.Status, "service_tier": task.ServiceTier, "queue": video.DecodeProviderMetadata(task.ProviderMetadata)})
	}
	resp.Success(c, gin.H{"active_count": len(items), "queued_count": queued, "running_count": running, "items": items})
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

// PriorityQueueVideoGeneration upgrades a queued upstream task through the
// channel's configured generic action mapping.
func PriorityQueueVideoGeneration(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if videoEngine == nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, video.ErrEngineUnavailable.Error())
		return
	}
	var task video.VideoTask
	if err := videoEngine.DB().Where("id = ? AND token_id = ?", c.Param("id"), token.ID).First(&task).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			resp.ErrorMsg(c, http.StatusNotFound, 404, "video task not found")
			return
		}
		resp.InternalError(c, perrors.ErrInternalError)
		return
	}
	metadata, err := videoEngine.UpgradeVideoTaskPriority(c.Request.Context(), &task)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInsufficientTokenBalance), errors.Is(err, service.ErrInsufficientUserBalance):
			resp.BadRequest(c, perrors.WithMessage(perrors.ErrInsufficientQuota, err.Error()))
		case errors.Is(err, video.ErrActionNotSupported):
			resp.ErrorMsg(c, http.StatusNotImplemented, 501, err.Error())
		case errors.Is(err, video.ErrActionNotAllowed):
			resp.ErrorMsg(c, http.StatusConflict, 409, err.Error())
		default:
			resp.ErrorMsg(c, http.StatusBadGateway, 502, err.Error())
		}
		return
	}
	resp.Success(c, gin.H{"id": task.ID, "action": "priority_queue", "status": task.Status, "service_tier": "priority", "queue": metadata})
}

type videoTaskResponse struct {
	ID                       string  `json:"id"`
	Model                    string  `json:"model"`
	Status                   string  `json:"status"`
	Progress                 int     `json:"progress"`
	TaskMode                 string  `json:"task_mode"`
	ServiceTier              string  `json:"service_tier,omitempty"`
	Prompt                   string  `json:"prompt,omitempty"`
	Resolution               string  `json:"resolution,omitempty"`
	Ratio                    string  `json:"ratio,omitempty"`
	Duration                 int     `json:"duration,omitempty"`
	GenerateAudio            bool    `json:"generate_audio,omitempty"`
	EstimatedCost            string  `json:"estimated_cost,omitempty"`
	FinalCost                string  `json:"final_cost,omitempty"`
	BillingStatus            string  `json:"billing_status,omitempty"`
	QueueStatus              string  `json:"queue_status,omitempty"`
	QueuePosition            int     `json:"queue_position,omitempty"`
	QueueLimit               int     `json:"queue_limit,omitempty"`
	PriorityQueue            *bool   `json:"priority_queue,omitempty"`
	PointsVIP                *bool   `json:"h_channel_points_vip,omitempty"`
	PrioritySurchargePercent float64 `json:"priority_surcharge_percent,omitempty"`
	Result                   any     `json:"result,omitempty"`
	ErrorMessage             string  `json:"error_message,omitempty"`
	CreatedAt                string  `json:"created_at"`
	CompletedAt              *string `json:"completed_at,omitempty"`
}

func videoTaskToResponse(t *video.VideoTask) videoTaskResponse {
	r := videoTaskResponse{
		ID: t.ID, Model: t.Model, Status: string(t.Status), Progress: t.Progress,
		TaskMode: t.TaskMode, Prompt: t.Prompt, Resolution: t.Resolution, Ratio: t.Ratio,
		ServiceTier: t.ServiceTier,
		Duration:    t.Duration, GenerateAudio: t.GenerateAudio,
		EstimatedCost: t.EstimatedCost.String(), FinalCost: t.FinalCost.String(), BillingStatus: t.BillingStatus,
		CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
	if t.ErrorMessage != "" {
		r.ErrorMessage = t.ErrorMessage
	}
	if metadata := video.DecodeProviderMetadata(t.ProviderMetadata); metadata != nil {
		r.QueueStatus = metadata.QueueStatus
		r.QueuePosition = metadata.QueuePosition
		r.QueueLimit = metadata.QueueLimit
		r.PriorityQueue = metadata.PriorityQueue
		r.PointsVIP = metadata.PointsVIP
		r.PrioritySurchargePercent = metadata.PrioritySurchargePercent
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
