package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestExtendedCatalogChangeHandlersRejectMalformedBodiesBeforeStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name, path, body string
		handler          gin.HandlerFunc
	}{
		{"route weight", "/catalog-changes/route-weight", `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"s","product_code":"p","pool_code":"pool","transport_code":"t","weight":0}`, ChangeUnifiedRouteWeight},
		{"sku variant", "/catalog-changes/sku-variant", `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"s","variant_code":""}`, ChangeUnifiedSKUVariant},
		{"downstream paths", "/catalog-changes/sku-downstream-paths", `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"s","downstream_paths":[]}`, ChangeUnifiedSKUDownstreamPaths},
		{"product", "/catalog-changes/product", `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"p","vendor_model":"m","capability_constraints":{"api_key":"no"}}`, ChangeUnifiedProduct},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST(test.path, test.handler)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("POST", test.path, strings.NewReader(test.body)))
			if response.Code != 400 {
				t.Fatalf("code=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestExtendedCatalogChangeHandlersRequireAdminAfterValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name, path, body string
		handler          gin.HandlerFunc
	}{
		{"route weight", "/catalog-changes/route-weight", `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"s","product_code":"p","pool_code":"pool","transport_code":"t","priority":100,"weight":100}`, ChangeUnifiedRouteWeight},
		{"sku variant", "/catalog-changes/sku-variant", `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"s","variant_code":"official"}`, ChangeUnifiedSKUVariant},
		{"downstream paths", "/catalog-changes/sku-downstream-paths", `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"s","downstream_paths":["/v1/responses"]}`, ChangeUnifiedSKUDownstreamPaths},
		{"product", "/catalog-changes/product", `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"p","vendor_model":"m","capability_constraints":{}}`, ChangeUnifiedProduct},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST(test.path, test.handler)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("POST", test.path, strings.NewReader(test.body)))
			if response.Code != 403 {
				t.Fatalf("code=%d body=%s", response.Code, response.Body)
			}
		})
	}
}

func TestRollbackHandlerRequiresAdminAfterValidation(t *testing.T) {
	router := gin.New()
	router.POST("/releases/:id/rollback", RollbackUnifiedCatalogRelease)
	response := httptest.NewRecorder()
	body := `{"expected_active_release_id":7,"semantic_version":"1.0.1"}`
	router.ServeHTTP(response, httptest.NewRequest("POST", "/releases/2/rollback", strings.NewReader(body)))
	if response.Code != 403 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}

func TestRollbackHandlerRejectsMalformedRequestsBeforeStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const validBody = `{"expected_active_release_id":7,"semantic_version":"1.0.1"}`
	tests := []struct {
		name, path, body string
	}{
		{"missing active guard", "/releases/2/rollback", `{"semantic_version":"1.0.1"}`},
		{"missing semantic version", "/releases/2/rollback", `{"expected_active_release_id":7}`},
		{"invalid semantic version", "/releases/2/rollback", `{"expected_active_release_id":7,"semantic_version":"1.0/rollback"}`},
		{"unknown field", "/releases/2/rollback", validBody[:len(validBody)-1] + `,"release_id":9}`},
		{"forged semantic digest", "/releases/2/rollback", validBody[:len(validBody)-1] + `,"semantic_digest":"deadbeef"}`},
		{"trailing json", "/releases/2/rollback", validBody + ` {}`},
		{"null body", "/releases/2/rollback", `null`},
		{"zero source release", "/releases/0/rollback", validBody},
		{"non-numeric source release", "/releases/not-a-number/rollback", validBody},
		{"negative source release", "/releases/-1/rollback", validBody},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/releases/:id/rollback", RollbackUnifiedCatalogRelease)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest("POST", test.path, strings.NewReader(test.body)))
			if response.Code != 400 {
				t.Fatalf("code=%d body=%s", response.Code, response.Body)
			}
		})
	}
}
