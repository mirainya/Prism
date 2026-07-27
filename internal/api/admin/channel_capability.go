package admin

import (
	"encoding/json"
	"errors"
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
		writeEndpointAdminError(c, err)
		return
	}

	resp.Success(c, ep)
}

// UpdateChannelCapability 更新端点
func UpdateChannelCapability(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var raw map[string]any
	if err := c.ShouldBindJSON(&raw); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	allowedFields := map[string]bool{
		"model_code": true, "channel_id": true,
		"protocol": true, "request_path": true, "request_method": true, "content_type": true,
		"auth_location": true, "auth_key": true, "auth_value_prefix": true,
		"vendor_model":     true,
		"interaction_mode": true, "supports_stream": true, "default_stream": true,
		"price_mode": true, "input_price": true, "output_price": true,
		"param_mapping": true, "param_schema": true, "response_mapping": true,
		"poll_path": true, "poll_method": true, "poll_interval": true, "poll_max_attempts": true,
		"poll_param_mapping": true, "poll_response_mapping": true,
		"callback_mapping": true,
		"extra_headers":    true, "extra_config": true,
		"timeout": true, "priority": true, "status": true,
	}

	req := make(map[string]any)
	for k, v := range raw {
		if allowedFields[k] {
			req[k] = v
		}
	}

	jsonFields := []string{"param_schema", "param_mapping", "response_mapping", "callback_mapping", "extra_config", "extra_headers", "poll_param_mapping", "poll_response_mapping"}
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

	var bindingUpdates *[]service.EndpointAccountBindingInput
	if rawBindings, ok := raw["account_bindings"]; ok {
		payload, err := json.Marshal(rawBindings)
		if err != nil {
			resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid account_bindings")
			return
		}
		var bindings []service.EndpointAccountBindingInput
		if err := json.Unmarshal(payload, &bindings); err != nil {
			resp.ErrorMsg(c, http.StatusBadRequest, 400, "invalid account_bindings")
			return
		}
		bindingUpdates = &bindings
	}

	ep, err := endpointAdminService.UpdateEndpoint(uint(id), req, bindingUpdates)
	if err != nil {
		writeEndpointAdminError(c, err)
		return
	}

	resp.Success(c, ep)
}

func writeEndpointAdminError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	code := 500
	if errors.Is(err, service.ErrEndpointAccountMismatch) ||
		errors.Is(err, service.ErrEndpointDuplicateAccount) ||
		errors.Is(err, service.ErrEndpointInvalidBinding) {
		status = http.StatusBadRequest
		code = 400
	}
	resp.ErrorMsg(c, status, code, err.Error())
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
