package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ListChannelCapabilities 渠道能力列表
func ListChannelCapabilities(c *gin.Context) {
	ccs, err := capabilityAdminService.ListChannelCapabilities(
		c.Query("channel_id"),
		c.Query("capability_code"),
		c.Query("status"),
	)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	resp.Success(c, ccs)
}

// GetChannelCapability 获取渠道能力详情
func GetChannelCapability(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	cc, err := capabilityAdminService.GetChannelCapability(id)
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "channel capability not found")
		return
	}
	resp.Success(c, cc)
}

// CreateChannelCapability 创建渠道能力
func CreateChannelCapability(c *gin.Context) {
	var req struct {
		ChannelID           uint            `json:"channel_id" binding:"required"`
		CapabilityCode      string          `json:"capability_code" binding:"required"`
		Model               string          `json:"model"`
		Name                string          `json:"name"`
		Price               decimal.Decimal `json:"price"`
		PriceUnit           string          `json:"price_unit"`
		ResultMode          string          `json:"result_mode"`
		RequestPath         string          `json:"request_path"`
		RequestMethod       string          `json:"request_method"`
		ContentType         string          `json:"content_type"`
		AuthLocation        string          `json:"auth_location"`
		AuthKey             string          `json:"auth_key"`
		AuthValuePrefix     string          `json:"auth_value_prefix"`
		PollPath            string          `json:"poll_path"`
		PollMethod          string          `json:"poll_method"`
		PollInterval        int             `json:"poll_interval"`
		PollMaxAttempts     int             `json:"poll_max_attempts"`
		PollParamMapping    datatypes.JSON  `json:"poll_param_mapping"`
		PollResponseMapping datatypes.JSON  `json:"poll_response_mapping"`
		ParamMapping        datatypes.JSON  `json:"param_mapping"`
		ResponseMapping     datatypes.JSON  `json:"response_mapping"`
		CallbackMapping     datatypes.JSON  `json:"callback_mapping"`
		ExtraConfig         datatypes.JSON  `json:"extra_config"`
		Status              int8            `json:"status"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	cc := &model.ChannelCapability{
		ChannelID:           req.ChannelID,
		CapabilityCode:      req.CapabilityCode,
		Model:               req.Model,
		Name:                req.Name,
		Price:               req.Price,
		PriceUnit:           req.PriceUnit,
		ResultMode:          model.ResultMode(req.ResultMode),
		RequestPath:         req.RequestPath,
		RequestMethod:       req.RequestMethod,
		ContentType:         req.ContentType,
		AuthLocation:        req.AuthLocation,
		AuthKey:             req.AuthKey,
		AuthValuePrefix:     req.AuthValuePrefix,
		PollPath:            req.PollPath,
		PollMethod:          req.PollMethod,
		PollInterval:        req.PollInterval,
		PollMaxAttempts:     req.PollMaxAttempts,
		PollParamMapping:    req.PollParamMapping,
		PollResponseMapping: req.PollResponseMapping,
		ParamMapping:        req.ParamMapping,
		ResponseMapping:     req.ResponseMapping,
		CallbackMapping:     req.CallbackMapping,
		ExtraConfig:         req.ExtraConfig,
		Status:              req.Status,
	}

	if err := capabilityAdminService.CreateChannelCapability(cc); err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	resp.Success(c, cc)
}

// UpdateChannelCapability 更新渠道能力
func UpdateChannelCapability(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	var req map[string]any
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	// 移除不可更新字段
	delete(req, "id")
	delete(req, "created_at")
	delete(req, "updated_at")

	// JSON 字段需要序列化为 []byte
	jsonFields := []string{"param_mapping", "response_mapping", "callback_mapping", "extra_config", "poll_param_mapping", "poll_response_mapping"}
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

	cc, err := capabilityAdminService.UpdateChannelCapability(id, req)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			resp.ErrorMsg(c, http.StatusNotFound, 404, "channel capability not found")
		case errors.Is(err, service.ErrChannelCapabilityChannelNotFound):
			resp.ErrorMsg(c, http.StatusBadRequest, 400, "target channel not found")
		case errors.Is(err, service.ErrChannelCapabilityCapabilityNotFound):
			resp.ErrorMsg(c, http.StatusBadRequest, 400, "target capability not found")
		case errors.Is(err, service.ErrChannelCapabilityInvalidField):
			resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		case errors.Is(err, service.ErrChannelCapabilityConflict):
			resp.ErrorMsg(c, http.StatusConflict, 409, "该渠道下已存在相同能力和模型的配置")
		default:
			resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		}
		return
	}

	resp.Success(c, cc)
}

// DeleteChannelCapability 删除渠道能力
func DeleteChannelCapability(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)

	rowsAffected, err := capabilityAdminService.DeleteChannelCapability(id)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	if rowsAffected == 0 {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "channel capability not found")
		return
	}
	resp.Success(c, gin.H{"message": "deleted"})
}
