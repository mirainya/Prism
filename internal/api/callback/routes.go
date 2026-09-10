package callback

import "github.com/gin-gonic/gin"

// RegisterUnifiedRoutes registers the isolated unified gateway callback
// endpoint.
func RegisterUnifiedRoutes(group *gin.RouterGroup) {
	// The callback event scope is an internal protocol constant.  It must not
	// be supplied by the provider/client because it participates in the event
	// identity HMAC and replay key.  Keeping a single fixed route also makes
	// ingress logging and rate limiting unambiguous.
	group.POST("/callback", HandleUnifiedCallback)
}
