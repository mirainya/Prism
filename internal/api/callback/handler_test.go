package callback

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

type callbackServiceFunc func(context.Context, *model.Task, map[string]any) error

func (f callbackServiceFunc) HandleCallback(ctx context.Context, task *model.Task, body map[string]any) error {
	return f(ctx, task, body)
}

func TestHandleCapabilityCallbackErrorClassification(t *testing.T) {
	previousService := unifiedService
	previousLogger := logger.L
	logger.L = zap.NewNop()
	t.Cleanup(func() {
		unifiedService = previousService
		logger.L = previousLogger
	})

	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{name: "internal error is retryable", serviceErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
		{name: "invalid status is rejected", serviceErr: service.ErrInvalidCallbackStatus, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unifiedService = callbackServiceFunc(func(context.Context, *model.Task, map[string]any) error {
				return tt.serviceErr
			})
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.POST("/callback", func(c *gin.Context) {
				c.Set(middleware.CallbackTaskContextKey, &model.Task{BaseModel: model.BaseModel{ID: 1}})
				HandleCapabilityCallback(c)
			})

			request := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(`{"status":"SUCCESS"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
		})
	}
}
