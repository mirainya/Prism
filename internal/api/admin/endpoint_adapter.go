package admin

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/gorm"
)

var endpointAdapterService = service.NewEndpointAdapterService()

// DiscoverChannelAccountModels reads the configured channel discovery path
// with the selected key. It is available only when endpoint discovery is
// explicitly enabled in the channel configuration.
func DiscoverChannelAccountModels(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	result, err := endpointAdapterService.DiscoverAccountEndpointModels(c.Request.Context(), id)
	if err != nil {
		writeEndpointAdapterError(c, err)
		return
	}
	resp.Success(c, result)
}

// ImportChannelAccountModels creates or reuses Endpoint-native routes and
// binds the selected key to each imported route.
func ImportChannelAccountModels(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var req service.EndpointModelImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}
	result, err := endpointAdapterService.ImportAccountEndpointModels(id, &req)
	if err != nil {
		writeEndpointAdapterError(c, err)
		return
	}
	resp.Success(c, result)
}

// DiscoverEndpointModels reads the selected Endpoint's upstream /v1/models
// through the Endpoint adapter. It never consults gw_* tables.
func DiscoverEndpointModels(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	result, err := endpointAdapterService.DiscoverEndpointModels(c.Request.Context(), id)
	if err != nil {
		writeEndpointAdapterError(c, err)
		return
	}
	resp.Success(c, result)
}

// ImportEndpointModels creates Endpoint-native models and paired image
// generation/edit endpoints from previously discovered upstream model IDs.
func ImportEndpointModels(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var req service.EndpointModelImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, http.StatusBadRequest, err.Error())
		return
	}
	result, err := endpointAdapterService.ImportEndpointModels(id, &req)
	if err != nil {
		writeEndpointAdapterError(c, err)
		return
	}
	resp.Success(c, result)
}

func writeEndpointAdapterError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, service.ErrEndpointAdapterNotFound) {
		resp.ErrorMsg(c, http.StatusNotFound, http.StatusNotFound, err.Error())
		return
	}
	if errors.Is(err, service.ErrEndpointDiscovery) {
		resp.ErrorMsg(c, http.StatusBadGateway, http.StatusBadGateway, err.Error())
		return
	}
	resp.ErrorMsg(c, http.StatusBadRequest, http.StatusBadRequest, err.Error())
}
