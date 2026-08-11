package open

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/video"
	perrors "github.com/mirainya/Prism/pkg/errors"
	"gorm.io/gorm"
)

var videoEngine *video.Engine

func InitVideoEngine(e *video.Engine) {
	videoEngine = e
}

type GenerationRequest struct {
	Model       string         `json:"model" binding:"required"`
	Prompt      string         `json:"prompt"`
	Params      map[string]any `json:"params"`
	CallbackURL string         `json:"callback_url"`
}

type GenerationResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at,omitempty"`
}

type videoEstimateResponse struct {
	EstimatedCost string `json:"estimated_cost"`
	BaseCost      string `json:"base_cost"`
	MarkupRatio   string `json:"markup_ratio"`
	PricingMode   string `json:"pricing_mode"`
}

func EstimateVideoGeneration(c *gin.Context) {
	var raw map[string]any
	if err := c.ShouldBindJSON(&raw); err != nil {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
		return
	}
	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	if videoEngine == nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, video.ErrEngineUnavailable.Error())
		return
	}
	modelName, _ := raw["model"].(string)
	prompt, _ := raw["prompt"].(string)
	callbackURL, _ := raw["callback_url"].(string)
	req, err := buildVideoCreateRequest(raw, token.UserID, token.ID, modelName, prompt, callbackURL)
	if err != nil {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
		return
	}
	estimate, err := videoEngine.EstimateTask(c.Request.Context(), req)
	if err != nil {
		writeVideoEstimateError(c, err)
		return
	}
	resp.Success(c, videoEstimateResponse{
		EstimatedCost: estimate.EstimatedCost.String(), BaseCost: estimate.BaseCost.String(),
		MarkupRatio: estimate.MarkupRatio.String(), PricingMode: estimate.PricingMode,
	})
}

func writeVideoEstimateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, video.ErrInvalidTaskRequest), errors.Is(err, video.ErrInvalidAsset), errors.Is(err, video.ErrAssetNotReady):
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
	case errors.Is(err, video.ErrAssetNotFound):
		resp.ErrorMsg(c, http.StatusNotFound, 404, err.Error())
	case errors.Is(err, video.ErrNoChannel), errors.Is(err, video.ErrNoKey), errors.Is(err, video.ErrEngineUnavailable), errors.Is(err, video.ErrEstimateNotSupported):
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, err.Error())
	default:
		resp.ErrorMsg(c, http.StatusBadGateway, 502, err.Error())
	}
}

func CreateVideoGeneration(c *gin.Context) {
	var raw map[string]any
	if err := c.ShouldBindJSON(&raw); err != nil {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
		return
	}

	modelName, _ := raw["model"].(string)
	prompt, _ := raw["prompt"].(string)
	if modelName == "" {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "model is required"))
		return
	}
	callbackURL, _ := raw["callback_url"].(string)

	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}

	if videoEngine == nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, video.ErrEngineUnavailable.Error())
		return
	}

	req, buildErr := buildVideoCreateRequest(raw, token.UserID, token.ID, modelName, prompt, callbackURL)
	req.CallID = service.GenerateAPICallID()
	req.RequestID = middleware.GetRequestID(c.Request.Context())
	req.Endpoint = c.FullPath()
	req.Operation = "videos.generate"
	c.Header(prismCallIDHeader, req.CallID)
	if buildErr != nil {
		recordVideoCreateFailure(req, http.StatusBadRequest, "invalid_request_error", "invalid_video_request", buildErr)
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, buildErr.Error()))
		return
	}
	result, err := videoEngine.CreateTask(c.Request.Context(), req)
	if err == nil {
		resp.Success(c, GenerationResponse{ID: result.TaskID, Status: "queued"})
		return
	}

	status, errorType, errorCode := classifyVideoCreateError(err)
	recordVideoCreateFailure(req, status, errorType, errorCode, err)
	switch status {
	case http.StatusBadRequest:
		if errors.Is(err, service.ErrInsufficientTokenBalance) || errors.Is(err, service.ErrInsufficientUserBalance) {
			resp.BadRequest(c, perrors.WithMessage(perrors.ErrInsufficientQuota, err.Error()))
		} else {
			resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
		}
	case http.StatusNotFound:
		resp.ErrorMsg(c, status, 404, err.Error())
	default:
		resp.ErrorMsg(c, status, status, err.Error())
	}
}

func classifyVideoCreateError(err error) (int, string, string) {
	switch {
	case errors.Is(err, video.ErrInvalidTaskRequest), errors.Is(err, video.ErrInvalidAsset), errors.Is(err, video.ErrAssetNotReady):
		return http.StatusBadRequest, "invalid_request_error", "invalid_video_request"
	case errors.Is(err, video.ErrAssetNotFound):
		return http.StatusNotFound, "invalid_request_error", "asset_not_found"
	case errors.Is(err, service.ErrInsufficientTokenBalance), errors.Is(err, service.ErrInsufficientUserBalance):
		return http.StatusBadRequest, "billing_error", "insufficient_quota"
	case errors.Is(err, video.ErrNoChannel), errors.Is(err, video.ErrNoKey), errors.Is(err, video.ErrEngineUnavailable):
		return http.StatusServiceUnavailable, "service_unavailable_error", "video_channel_unavailable"
	default:
		return http.StatusInternalServerError, "video_error", "video_create_failed"
	}
}

func recordVideoCreateFailure(req *video.CreateTaskRequest, status int, errorType, errorCode string, cause error) {
	if req == nil || cause == nil || req.CallID == "" {
		return
	}
	_ = model.DB().Transaction(func(tx *gorm.DB) error {
		if _, err := service.NewAPICallService().StartCallTx(tx, &service.StartCallRequest{
			ID: req.CallID, RequestID: req.RequestID, UserID: req.UserID, TokenID: req.TokenID,
			Endpoint: req.Endpoint, Operation: req.Operation, Model: req.Model, Background: true,
		}); err != nil {
			return err
		}
		return service.NewAPICallService().FailCallTx(tx, req.CallID, &service.FailCallRequest{
			HTTPStatus: status, ErrorType: errorType, ErrorCode: errorCode, ErrorMessage: cause.Error(),
		})
	})
}

func buildVideoCreateRequest(raw map[string]any, userID, tokenID uint, modelName, prompt, callbackURL string) (*video.CreateTaskRequest, error) {
	req := &video.CreateTaskRequest{
		UserID:   userID,
		TokenID:  tokenID,
		Model:    modelName,
		Prompt:   prompt,
		Callback: callbackURL,
		Audio:    true,
	}
	if v, ok := raw["resolution"].(string); ok {
		req.Resolution = v
	}
	if v, ok := raw["ratio"].(string); ok {
		req.Ratio = v
	}
	if v, ok := raw["duration"].(float64); ok {
		req.Duration = int(v)
	}
	if v, ok := raw["generate_audio"].(bool); ok {
		req.Audio = v
	}
	if v, ok := raw["task_mode"].(string); ok {
		req.TaskMode = v
	}

	// content 数组
	if rawContent, ok := raw["content"]; ok {
		b, err := json.Marshal(rawContent)
		if err != nil {
			return req, fmt.Errorf("invalid content: %w", err)
		}
		var items []video.ContentItem
		if err := json.Unmarshal(b, &items); err != nil {
			return req, fmt.Errorf("invalid content: %w", err)
		}
		req.Content = items
	}

	// params：把顶层非保留字段 + nested params 合并
	params := make(map[string]any)
	reserved := map[string]bool{
		"model": true, "prompt": true, "callback_url": true,
		"resolution": true, "ratio": true, "duration": true,
		"generate_audio": true, "task_mode": true, "content": true, "params": true,
	}
	for k, v := range raw {
		if !reserved[k] {
			params[k] = v
		}
	}
	if nested, ok := raw["params"].(map[string]any); ok {
		for k, v := range nested {
			params[k] = v
		}
	} else if value, exists := raw["params"]; exists && value != nil {
		return req, fmt.Errorf("params must be an object")
	}
	if len(params) > 0 {
		req.Params = params
	}
	return req, nil
}
