package open

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/codec/prismv1"
	perrors "github.com/mirainya/Prism/pkg/errors"
	"gorm.io/gorm"
)

var videoEngine *video.Engine

func InitVideoEngine(e *video.Engine) {
	videoEngine = e
}

type GenerationResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	ServiceTier string `json:"service_tier,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

type videoEstimateResponse struct {
	EstimatedCost string  `json:"estimated_cost"`
	BaseCost      string  `json:"base_cost"`
	MarkupRatio   string  `json:"markup_ratio"`
	PricingMode   string  `json:"pricing_mode"`
	ServiceTier   string  `json:"service_tier,omitempty"`
	UnitCost      float64 `json:"unit_cost,omitempty"`
	Units         float64 `json:"units,omitempty"`
	BillingMode   string  `json:"billing_mode,omitempty"`
	BillingTier   string  `json:"billing_tier,omitempty"`
	PricingSource string  `json:"pricing_source,omitempty"`
	Currency      string  `json:"currency,omitempty"`
}

func EstimateVideoGeneration(c *gin.Context) {
	spec, err := prismv1.Decode(c.Request.Body)
	if err != nil {
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
	req, err := prismv1.ToTaskRequest(spec, token.UserID, token.ID)
	if err != nil {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
		return
	}
	estimate, err := videoEngine.EstimateTask(c.Request.Context(), req)
	if err != nil {
		writeVideoEstimateError(c, err)
		return
	}
	response := videoEstimateResponse{
		EstimatedCost: estimate.EstimatedCost.String(), BaseCost: estimate.BaseCost.String(),
		MarkupRatio: estimate.MarkupRatio.String(), PricingMode: estimate.PricingMode,
		ServiceTier: req.ServiceTier,
	}
	if detail := estimate.ProviderEstimate; detail != nil {
		response.UnitCost = detail.UnitCost
		response.Units = detail.Units
		response.BillingMode = detail.BillingMode
		response.BillingTier = detail.BillingTier
		response.PricingSource = detail.PricingSource
		response.Currency = detail.Currency
	}
	resp.Success(c, response)
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
	spec, err := prismv1.Decode(c.Request.Body)
	if err != nil {
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

	req, buildErr := prismv1.ToTaskRequest(spec, token.UserID, token.ID)
	if buildErr != nil {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, buildErr.Error()))
		return
	}
	req.CallID = service.GenerateAPICallID()
	req.RequestID = middleware.GetRequestID(c.Request.Context())
	req.Endpoint = c.FullPath()
	req.Operation = "videos.generate"
	c.Header(prismCallIDHeader, req.CallID)
	result, err := videoEngine.CreateTask(c.Request.Context(), req)
	if err == nil {
		resp.Success(c, GenerationResponse{ID: result.TaskID, Status: "queued", ServiceTier: req.ServiceTier})
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
