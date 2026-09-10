package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
)

// GatewayReadiness fails closed on every /v1 request when this process no
// longer owns a current deployment proof. Result retrieval, catalog reads and
// mutations all depend on the same catalog + crypto readiness, so a
// half-provisioned process must not serve GETs either: decrypting resource
// blobs or projecting the catalog before readiness returns partial or wrong
// data. Health checks live outside this middleware.
func GatewayReadiness(check gatewayruntime.ReadinessCheck) gin.HandlerFunc {
	return gatewayReadiness(check)
}

func gatewayReadiness(check gatewayruntime.ReadinessCheck) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := check.Require(c.Request.Context()); err != nil {
			errorType := "server_error"
			if isAnthropicMessagesPath(c.Request.URL.Path) {
				errorType = "overloaded_error"
			}
			writeGatewayProtocolError(c, http.StatusServiceUnavailable, "Gateway deployment is not ready", errorType, "deployment_not_ready")
			c.Abort()
			return
		}
		c.Next()
	}
}

func isAnthropicMessagesPath(path string) bool {
	return len(path) >= len("/messages") && path[len(path)-len("/messages"):] == "/messages"
}
