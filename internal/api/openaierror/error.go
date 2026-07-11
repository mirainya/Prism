package openaierror

import "github.com/gin-gonic/gin"

type Body struct {
	Error Detail `json:"error"`
}

type Detail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    any     `json:"code"`
}

func Write(c *gin.Context, status int, message, errorType string, param *string, code any) {
	c.JSON(status, Body{Error: Detail{
		Message: message,
		Type:    errorType,
		Param:   param,
		Code:    code,
	}})
}

func InvalidRequest(c *gin.Context, message string, param *string, code any) {
	Write(c, 400, message, "invalid_request_error", param, code)
}
