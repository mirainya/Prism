package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIDNormalizesOversizedClientValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, GetRequestID(c.Request.Context()))
	})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDKey, strings.Repeat("x", maxRequestIDLength+1))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	requestID := response.Header().Get(RequestIDKey)
	if !strings.HasPrefix(requestID, "req_") || len([]rune(requestID)) > maxRequestIDLength {
		t.Fatalf("normalized request id = %q", requestID)
	}
	if response.Body.String() != requestID {
		t.Fatalf("context request id = %q, header = %q", response.Body.String(), requestID)
	}
}

func TestCORSExposesCallHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORS())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	exposed := response.Header().Get("Access-Control-Expose-Headers")
	if !strings.Contains(exposed, "X-Prism-Call-ID") || !strings.Contains(exposed, "X-Request-ID") {
		t.Fatalf("exposed headers = %q", exposed)
	}
}
