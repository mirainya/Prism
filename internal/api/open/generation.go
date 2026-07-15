package open

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	perrors "github.com/mirainya/Prism/pkg/errors"
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

func CreateVideoGeneration(c *gin.Context) {
	createGeneration(c, "text2video")
}

func createGeneration(c *gin.Context, capabilityCode string) {
	var raw map[string]any
	if err := c.ShouldBindJSON(&raw); err != nil {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, err.Error()))
		return
	}

	model, _ := raw["model"].(string)
	prompt, _ := raw["prompt"].(string)
	if model == "" || prompt == "" {
		resp.BadRequest(c, perrors.WithMessage(perrors.ErrInvalidParams, "model and prompt are required"))
		return
	}
	callbackURL, _ := raw["callback_url"].(string)

	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}

	params := make(map[string]any, len(raw))
	for k, v := range raw {
		if k == "callback_url" || k == "params" {
			continue
		}
		params[k] = v
	}
	if nested, ok := raw["params"].(map[string]any); ok {
		for k, v := range nested {
			params[k] = v
		}
	} else if rawParams, ok := raw["params"].(json.RawMessage); ok && len(rawParams) > 0 {
		var nested map[string]any
		if json.Unmarshal(rawParams, &nested) == nil {
			for k, v := range nested {
				params[k] = v
			}
		}
	}

	invokeReq := &service.InvokeRequest{
		UserID:      token.UserID,
		TokenID:     token.ID,
		Capability:  capabilityCode,
		Model:       model,
		CallbackURL: callbackURL,
		Params:      params,
	}
	attachCapabilityCallIdentity(c, invokeReq, "videos.generate")
	invokeResp, err := capabilityService.Invoke(c.Request.Context(), invokeReq)
	if err != nil {
		if errors.Is(err, service.ErrInsufficientTokenBalance) || errors.Is(err, service.ErrInsufficientUserBalance) {
			resp.BadRequest(c, perrors.WithMessage(perrors.ErrInsufficientQuota, err.Error()))
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
