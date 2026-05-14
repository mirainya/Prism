package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/service"
	"gorm.io/datatypes"
)

var endpointAdminService = service.NewEndpointAdminService()

// ListChannelCapabilities 端点列表
func ListChannelCapabilities(c *gin.Context) {
	eps, err := endpointAdminService.ListEndpoints(
		c.Query("channel_id"),
		c.Query("model_code"),
		c.Query("status"),
	)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, eps)
}

// GetChannelCapability 获取端点详情
func GetChannelCapability(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	ep, err := endpointAdminService.GetEndpoint(uint(id))
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "endpoint not found")
		return
	}
	resp.Success(c, ep)
}

// CreateChannelCapability 创建端点
func CreateChannelCapability(c *gin.Context) {
	var req service.CreateEndpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	ep, err := endpointAdminService.CreateEndpoint(&req)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, ep)
}

// UpdateChannelCapability 更新端点
func UpdateChannelCapability(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var req map[string]any
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	delete(req, "id")
	delete(req, "created_at")
	delete(req, "updated_at")

	jsonFields := []string{"param_mapping", "response_mapping", "callback_mapping", "extra_config", "extra_headers", "poll_param_mapping", "poll_response_mapping"}
	for _, field := range jsonFields {
		if v, ok := req[field]; ok {
			if v == nil {
				req[field] = datatypes.JSON([]byte("{}"))
			} else {
				b, err := json.Marshal(v)
				if err != nil {
					resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid "+field)
					return
				}
				req[field] = datatypes.JSON(b)
			}
		}
	}

	ep, err := endpointAdminService.UpdateEndpoint(uint(id), req)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, ep)
}

// DeleteChannelCapability 删除端点
func DeleteChannelCapability(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	rowsAffected, err := endpointAdminService.DeleteEndpoint(uint(id))
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	if rowsAffected == 0 {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "endpoint not found")
		return
	}
	resp.Success(c, gin.H{"message": "deleted"})
}
