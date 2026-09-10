package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestUnifiedCapacityHTTPStatusAcrossProtocols(t *testing.T) {
	for name, respond := range map[string]func(*gin.Context, error){
		"chat": respondChatPipelineError, "responses": respondResponsesError, "anthropic": writeAnthropicExecutionError,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest("POST", "/", nil)
			respond(ctx, &domain.AppError{HTTPStatus: 429, Code: "credential_capacity_exhausted", Message: "Capacity exhausted", Err: repository.ErrConcurrencyLimit})
			if recorder.Code != 429 {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
