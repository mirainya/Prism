package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDecodeTokenFileStorageBodyIsStrict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	valid := `{"api_key":"xfs_0123456789abcdef0123456789abcdef"}`
	for name, body := range map[string]string{
		"empty":         "",
		"malformed":     `{`,
		"null":          `null`,
		"unknown field": strings.TrimSuffix(valid, "}") + `,"provider":"other"}`,
		"trailing json": valid + ` {}`,
		"too large":     `{"api_key":"` + strings.Repeat("a", 1100) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(http.MethodPut, "/api/tokens/1/file-storage", strings.NewReader(body))
			if _, ok := decodeTokenFileStorageBody(ctx); ok {
				t.Fatal("invalid body accepted")
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/tokens/1/file-storage", strings.NewReader(valid))
	req, ok := decodeTokenFileStorageBody(ctx)
	if !ok || req.APIKey != "xfs_0123456789abcdef0123456789abcdef" {
		t.Fatalf("valid body result = %#v, ok=%t", req, ok)
	}
}
