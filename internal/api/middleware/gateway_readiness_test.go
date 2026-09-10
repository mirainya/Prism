package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGatewayReadinessRejectsAllMethodsWhenNotReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			handled := false
			router := gin.New()
			router.Use(gatewayReadiness(func(context.Context) error { return errors.New("not ready") }))
			router.Handle(method, "/v1/files", func(c *gin.Context) {
				handled = true
				c.Status(http.StatusNoContent)
			})

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(method, "/v1/files", nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
			}
			if handled {
				t.Fatal("handler ran while deployment was not ready")
			}
			if !strings.Contains(response.Body.String(), "deployment_not_ready") {
				t.Fatalf("unexpected response body: %s", response.Body.String())
			}
		})
	}
}

func TestGatewayReadinessRejectsMutations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			handled := false
			router := gin.New()
			router.Use(gatewayReadiness(func(context.Context) error { return errors.New("database unavailable") }))
			router.Handle(method, "/v1/files", func(c *gin.Context) {
				handled = true
				c.Status(http.StatusNoContent)
			})

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(method, "/v1/files", nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusServiceUnavailable, response.Body.String())
			}
			if handled {
				t.Fatal("mutation handler ran while deployment was not ready")
			}
			if strings.Contains(response.Body.String(), "database unavailable") || !strings.Contains(response.Body.String(), "deployment_not_ready") {
				t.Fatalf("unexpected response body: %s", response.Body.String())
			}
		})
	}
}

func TestGatewayReadinessAllowsReadyMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gatewayReadiness(func(context.Context) error { return nil }))
	router.POST("/v1/files", func(c *gin.Context) { c.Status(http.StatusCreated) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/files", nil))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
}

func TestGatewayReadinessUsesAnthropicErrorEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gatewayReadiness(func(context.Context) error { return errors.New("not ready") }))
	router.POST("/v1/messages", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/messages", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"type":"overloaded_error"`) {
		t.Fatalf("unexpected response: status=%d body=%s", response.Code, response.Body.String())
	}
}
