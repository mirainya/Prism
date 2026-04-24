package open

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/errors"
)

type GenerationRequest struct {
	Model       string         `json:"model" binding:"required"`
	Prompt      string         `json:"prompt" binding:"required"`
	Params      map[string]any `json:"params"`
	CallbackURL string         `json:"callback_url"`
}

type GenerationResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func CreateImageGeneration(c *gin.Context) {
	createGeneration(c, model.CapabilityText2Img)
}

func CreateVideoGeneration(c *gin.Context) {
	createGeneration(c, model.CapabilityText2Video)
}

func createGeneration(c *gin.Context, capabilityCode string) {
	var req GenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}

	// 构建统一参数
	params := map[string]any{
		"prompt": req.Prompt,
		"model":  req.Model,
	}
	for k, v := range req.Params {
		params[k] = v
	}

	invokeResp, err := capabilityService.Invoke(c.Request.Context(), &service.InvokeRequest{
		UserID:      token.UserID,
		TokenID:     token.ID,
		Capability:  capabilityCode,
		Model:       req.Model,
		CallbackURL: req.CallbackURL,
		Params:      params,
	})
	if err != nil {
		if err.Error() == "insufficient balance" {
			resp.BadRequest(c, errors.WithMessage(errors.ErrInsufficientQuota, err.Error()))
			return
		}
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, GenerationResponse{
		ID:     invokeResp.TaskID,
		Status: invokeResp.Status,
	})
}
