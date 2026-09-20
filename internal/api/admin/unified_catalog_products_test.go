package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

func TestListUnifiedCatalogProductsReturnsPersistedOfferingRuntimeState(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM gw_products`).
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(`(?s)SELECT p\.id,p\.product_code.*ors\.state,ors\.state_version.*JOIN gw_offering_runtime_state ors ON ors\.release_id=o\.release_id AND ors\.offering_id=o\.id`).
		WithArgs(uint64(7), 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"product_id", "product_code", "vendor_model", "capability_constraints", "constraints_schema_version",
			"channel_id", "channel_name", "product_transport_id", "channel_transport_id", "transport_code",
			"base_url", "protocol", "request_method", "request_path", "task_scope", "cancel_mode", "source_url_policy",
			"adapter_code", "adapter_version", "offering_id", "offering_state", "offering_state_version",
			"pool_id", "pool_code", "pool_name", "cost_plan_id", "plan_code", "route_count", "cost_rate_count",
			"commercial_state", "entitled_credential_count",
		}).AddRow(
			701, "seedance-480p", "seedance2.0-480p", []byte(`{"duration":{"max":12}}`), 1,
			42, "AICost", 601, 801, "seedance-v1",
			"https://aicost.me", "seedance", "POST", "/v1/videos", "task", "none", "fixed",
			"seedance", 1, 301, "disabled", 9,
			501, "fseedance", "FSeedance", 901, "primary", 1, 2,
			"not_required", 3,
		))

	router := gin.New()
	router.GET("/catalog/:id/products", ListUnifiedCatalogProducts)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/catalog/7/products?page=1&page_size=20", nil))

	body := response.Body.String()
	for _, want := range []string{
		`"offering_id":301`,
		`"offering_state":"disabled"`,
		`"offering_state_version":9`,
	} {
		if response.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("status=%d missing=%s body=%s", response.Code, want, body)
		}
	}
}
