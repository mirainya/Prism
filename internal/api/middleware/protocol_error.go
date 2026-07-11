package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/openaierror"
)

func writeGatewayProtocolError(c *gin.Context, status int, message, errorType, code string) {
	if strings.HasSuffix(strings.TrimRight(c.Request.URL.Path, "/"), "/messages") {
		c.JSON(status, gin.H{
			"type":       "error",
			"error":      gin.H{"type": errorType, "message": message},
			"request_id": GetRequestID(c.Request.Context()),
		})
		return
	}
	openaierror.Write(c, status, message, errorType, nil, code)
}

func writeAuthenticationError(c *gin.Context, message string) {
	writeGatewayProtocolError(c, http.StatusUnauthorized, message, "authentication_error", "invalid_api_key")
}
