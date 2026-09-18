package open

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
)

// GetChatModelDetail GET /v1/models/:code
// Model metadata comes from the executable routes in the active catalog.
func GetChatModelDetail(c *gin.Context) {
	code := c.Param("code")
	rows, err := service.NewQueryService().ListAvailableCapabilities(c.Request.Context(), "", "")
	if err != nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "model catalog is unavailable")
		return
	}
	for _, m := range rows {
		if m.ID != code {
			continue
		}
		c.JSON(http.StatusOK, publicChatModel(m))
		return
	}
	resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
}

// ListChatModelsPublic GET /v1/models
// Every executable model in the active catalog appears. The response retains
// the OpenAI model-list fields and adds Prism metadata for multimodal clients.
func ListChatModelsPublic(c *gin.Context) {
	rows, err := service.NewQueryService().ListAvailableCapabilities(c.Request.Context(), "", "")
	if err != nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "model catalog is unavailable")
		return
	}

	data := make([]gin.H, 0, len(rows))
	for _, m := range rows {
		data = append(data, publicChatModel(m))
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

func publicChatModel(model service.AvailableModelCapability) gin.H {
	endpoints := make([]string, 0, len(model.Operations))
	operations := make([]string, 0, len(model.Operations))
	for _, operation := range model.Operations {
		if operation.Path != "" && !containsPublicModelValue(endpoints, operation.Path) {
			endpoints = append(endpoints, operation.Path)
		}
		if operation.ID != "" && !containsPublicModelValue(operations, operation.ID) {
			operations = append(operations, operation.ID)
		}
	}
	item := gin.H{
		"id":                   model.ID,
		"object":               "model",
		"owned_by":             "prism",
		"name":                 model.Name,
		"model_code":           model.ID,
		"description":          model.Description,
		"type":                 model.Type,
		"types":                model.Types,
		"visibility":           model.Visibility,
		"features":             model.Features,
		"native_transports":    model.Transports,
		"supported_operations": operations,
		"supported_endpoints":  endpoints,
	}
	if model.Availability != nil {
		item["availability"] = model.Availability
	}
	return item
}

func containsPublicModelValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
