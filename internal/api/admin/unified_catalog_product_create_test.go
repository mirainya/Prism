package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const activeCatalogProductCreateBody = `{
  "expected_active_release_id":7,
  "expected_config_version":3,
  "channel_id":11,
  "credential_pool_id":12,
  "product_code":"gpt-4o-mini-primary",
  "vendor_model":"gpt-4o-mini",
  "capability_constraints":{},
  "constraints_schema_version":1,
  "adapter_code":"openai_chat",
  "adapter_version":1,
  "transport_code":"openai-chat-v1",
  "base_url":"https://api.example.com",
  "request_method":"POST",
  "request_path":"/v1/chat/completions",
  "auth_scheme":"bearer",
  "transport_timeout_ms":30000,
  "task_timeout_ms":30000,
  "task_scope":"request",
  "cancel_mode":"none",
  "source_url_policy":"fixed",
  "upstream_scope_kind":"product_transport",
  "upstream_scope_key":"openai-chat-v1",
  "allowed_hosts":[],
  "actions":[],
  "cost_plan_code":"primary",
  "routes":[{"sku_id":21,"priority":100,"weight":100}]
}`

func activeCatalogProductCreateRouter(actor uint) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != 0 {
			c.Set("user_id", actor)
		}
	})
	router.POST("/catalog-changes/product-create", CreateUnifiedActiveCatalogProduct)
	return router
}

func TestActiveCatalogProductCreateValidatesBeforeAdminWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := map[string]string{
		"missing active release":   strings.Replace(activeCatalogProductCreateBody, `"expected_active_release_id":7`, `"expected_active_release_id":0`, 1),
		"missing config version":   strings.Replace(activeCatalogProductCreateBody, `"expected_config_version":3`, `"expected_config_version":0`, 1),
		"draft version field":      strings.Replace(activeCatalogProductCreateBody, `"expected_config_version":3,`, `"expected_config_version":3,"expected_version":3,`, 1),
		"null draft version field": strings.Replace(activeCatalogProductCreateBody, `"expected_config_version":3,`, `"expected_config_version":3,"expected_version":null,`, 1),
		"semantic version field":   strings.Replace(activeCatalogProductCreateBody, `"expected_config_version":3,`, `"expected_config_version":3,"semantic_version":"1.0.1",`, 1),
		"unknown adapter":          strings.Replace(activeCatalogProductCreateBody, `"adapter_code":"openai_chat"`, `"adapter_code":"missing"`, 1),
		"forbidden secret":         strings.Replace(activeCatalogProductCreateBody, `"capability_constraints":{}`, `"capability_constraints":{"api_key":"secret"}`, 1),
		"unknown field":            strings.Replace(activeCatalogProductCreateBody, `"channel_id":11,`, `"channel_id":11,"release_id":9,`, 1),
		"trailing JSON":            activeCatalogProductCreateBody + ` {}`,
		"null":                     `null`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			activeCatalogProductCreateRouter(1).ServeHTTP(response, httptest.NewRequest("POST", "/catalog-changes/product-create", strings.NewReader(body)))
			if response.Code != 400 {
				t.Fatalf("code=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestActiveCatalogProductCreateRequiresAdminAfterValidation(t *testing.T) {
	response := httptest.NewRecorder()
	activeCatalogProductCreateRouter(0).ServeHTTP(response, httptest.NewRequest("POST", "/catalog-changes/product-create", strings.NewReader(activeCatalogProductCreateBody)))
	if response.Code != 403 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}
