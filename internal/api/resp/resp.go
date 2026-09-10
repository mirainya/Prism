package resp

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/pkg/errors"
)

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

func Error(c *gin.Context, httpCode int, code int, message string) {
	c.JSON(httpCode, Response{
		Code:    code,
		Message: message,
	})
}

func ErrorWithErr(c *gin.Context, httpCode int, err *errors.Error) {
	c.JSON(httpCode, Response{
		Code:    err.Code,
		Message: err.Message,
	})
}

func BadRequest(c *gin.Context, err *errors.Error) {
	ErrorWithErr(c, http.StatusBadRequest, err)
}

func Unauthorized(c *gin.Context, err *errors.Error) {
	ErrorWithErr(c, http.StatusUnauthorized, err)
}

func Forbidden(c *gin.Context, err *errors.Error) {
	ErrorWithErr(c, http.StatusForbidden, err)
}

func NotFound(c *gin.Context, err *errors.Error) {
	ErrorWithErr(c, http.StatusNotFound, err)
}

func InternalError(c *gin.Context, err *errors.Error) {
	ErrorWithErr(c, http.StatusInternalServerError, err)
}

// ErrorMsg 直接传 code+message 的便捷方法
func ErrorMsg(c *gin.Context, httpCode int, code int, message string) {
	c.JSON(httpCode, Response{Code: code, Message: message})
}

// ParseUintParam 从路由参数解析 uint 值
func ParseUintParam(c *gin.Context, name string) (uint, error) {
	param := strings.TrimSpace(c.Param(name))
	id, err := strconv.ParseUint(param, 10, strconv.IntSize)
	if err != nil || id == 0 {
		BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, "invalid "+name))
		if err == nil {
			err = strconv.ErrSyntax
		}
		return 0, err
	}
	return uint(id), nil
}

// ParseOptionalUintQuery 从查询参数解析可选的 uint 值
func ParseOptionalUintQuery(c *gin.Context, name string) (uint, error) {
	param := strings.TrimSpace(c.Query(name))
	if param == "" {
		return 0, nil
	}
	id, err := strconv.ParseUint(param, 10, strconv.IntSize)
	if err != nil || id == 0 {
		if err == nil {
			err = strconv.ErrSyntax
		}
		return 0, err
	}
	return uint(id), nil
}
