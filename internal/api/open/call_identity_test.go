package open

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/service"
)

func TestAttachCapabilityCallIdentityUsesRequestIDAndReturnsCallHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestID())
	var captured service.InvokeRequest
	router.POST("/v1/images/generations", func(c *gin.Context) {
		attachCapabilityCallIdentity(c, &captured, "images.generate")
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	request.Header.Set(middleware.RequestIDKey, "request-from-client")
	router.ServeHTTP(recorder, request)

	callID := recorder.Header().Get(prismCallIDHeader)
	if !strings.HasPrefix(callID, "call_") || captured.CallID != callID {
		t.Fatalf("call identity = header %q request %q", callID, captured.CallID)
	}
	if captured.RequestID != "request-from-client" || captured.Endpoint != "/v1/images/generations" ||
		captured.Operation != "images.generate" {
		t.Fatalf("captured invocation identity = %#v", captured)
	}
}
