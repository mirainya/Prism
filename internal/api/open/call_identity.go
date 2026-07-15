package open

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/service"
)

const prismCallIDHeader = "X-Prism-Call-ID"

func attachCapabilityCallIdentity(c *gin.Context, req *service.InvokeRequest, operation string) {
	callID := service.GenerateAPICallID()
	requestID := strings.TrimSpace(middleware.GetRequestID(c.Request.Context()))
	if requestID == "" {
		requestID = service.GenerateRequestID()
	}

	req.CallID = callID
	req.RequestID = requestID
	req.Endpoint = c.FullPath()
	req.Operation = operation
	c.Header(prismCallIDHeader, callID)
}
