package open

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
)

// GetChatModelDetail GET /v1/models/:code
// 与 chat 同源:从 gw 可路由模型(gw_abilities+gw_model_meta)查,不可路由则 404。
func GetChatModelDetail(c *gin.Context) {
	code := c.Param("code")
	svc := service.NewGatewayAdminService()
	rows, err := svc.ListModels()
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	transports, err := svc.ListModelTransports()
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	for _, m := range rows {
		if m.ModelName != code || m.KeyAvailable <= 0 {
			continue
		}
		item := gin.H{
			"id":       m.ModelName,
			"object":   "model",
			"owned_by": "prism",
		}
		addModelProtocolSupport(item, transports[m.ModelName])
		if m.DisplayName != "" {
			item["name"] = m.DisplayName
		}
		if m.MaxTokens > 0 {
			item["max_tokens"] = m.MaxTokens
		}
		if len(m.Features) > 0 {
			var features []string
			if json.Unmarshal(m.Features, &features) == nil && len(features) > 0 {
				item["features"] = features
			}
		}
		c.JSON(http.StatusOK, item)
		return
	}
	resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
}

// ListChatModelsPublic GET /v1/models
// 与 chat 同源:只列 gw 可路由(key_available>0)模型,避免客户端选到 chat 会 404 的模型。
func ListChatModelsPublic(c *gin.Context) {
	svc := service.NewGatewayAdminService()
	rows, err := svc.ListModels()
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	transports, err := svc.ListModelTransports()
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	data := make([]gin.H, 0, len(rows))
	for _, m := range rows {
		if m.KeyAvailable <= 0 {
			continue
		}
		item := gin.H{
			"id":       m.ModelName,
			"object":   "model",
			"owned_by": "prism",
		}
		addModelProtocolSupport(item, transports[m.ModelName])
		if m.MaxTokens > 0 {
			item["max_tokens"] = m.MaxTokens
		}
		if len(m.Features) > 0 {
			var features []string
			if json.Unmarshal(m.Features, &features) == nil && len(features) > 0 {
				item["features"] = features
			}
		}
		data = append(data, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

func addModelProtocolSupport(item gin.H, transports []model.UpstreamTransport) {
	item["native_transports"] = transports
	if len(transports) == 0 {
		item["supported_endpoints"] = []string{}
		return
	}
	item["supported_endpoints"] = []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"}
}
