package admin

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestListUnifiedModelCredentialsLogsQueryErrorWithoutLeakingIt(t *testing.T) {
	mock := unifiedChannelMock(t)
	logs := installUnifiedRelationObservedLogger(t)

	mock.ExpectQuery(`SELECT active_release_id FROM gw_catalog_runtime_state`).
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(2))
	driverErr := errors.New("driver diagnostic must stay out of the response")
	mock.ExpectQuery(`SELECT mn\.api_name`).
		WithArgs(int64(routing.CircuitKeyMask), int64(2), "grok-imagine-video").
		WillReturnError(driverErr)

	router := gin.New()
	router.GET("/model-credentials", ListUnifiedModelCredentials)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/model-credentials?model_name=grok-imagine-video", nil))

	if response.Code != 500 || strings.Contains(response.Body.String(), driverErr.Error()) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	entries := logs.FilterMessage("unified relation query failed").All()
	if len(entries) != 1 {
		t.Fatalf("logs=%v", logs.All())
	}
	fields := entries[0].ContextMap()
	if fields["stage"] != "query" || fields["error"] != driverErr.Error() {
		t.Fatalf("fields=%v", fields)
	}
}

func TestListUnifiedModelCredentialsWithoutActiveReleaseIsEmpty(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery(`SELECT active_release_id FROM gw_catalog_runtime_state`).
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(nil))

	router := gin.New()
	router.GET("/model-credentials", ListUnifiedModelCredentials)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/model-credentials?model_name=grok-imagine-video", nil))

	if response.Code != 200 || !strings.Contains(response.Body.String(), `"active_release_id":null`) || !strings.Contains(response.Body.String(), `"items":[]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestListUnifiedChannelRelationsReturnsTransportMappingAndActions(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery(`SELECT active_release_id FROM gw_catalog_runtime_state`).
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(7))
	mock.ExpectQuery(`SELECT mn\.api_name`).
		WithArgs(int64(routing.CircuitKeyMask), int64(7), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"api_name", "display_name", "visibility",
			"capability_tags", "operation_code",
			"sku_id", "sku_code", "delivery_mode",
			"route_id", "priority", "route_weight",
			"offering_id", "offering_state",
			"credential_id", "credential_code", "credential_status", "credential_version", "credential_weight", "request_limit", "task_limit",
			"pool_id", "pool_code", "pool_name", "pool_status",
			"channel_id", "channel_name", "channel_status", "transport_code", "product_code", "vendor_model",
			"product_id", "product_transport_id", "channel_transport_id", "adapter_code", "adapter_version",
			"base_url", "protocol", "request_method", "request_path", "task_scope", "capability_constraints",
			"commercial_valid", "entitlement_valid", "secret_active", "execution_granted", "version_usable", "breaker_seconds",
		}).AddRow(
			"gpt-4o", "GPT-4o", "public",
			[]byte(`["llm"]`), "chat.completions",
			101, "gpt4o-default", "sync",
			201, 10, 100,
			301, "active",
			401, "key-a", "active", 2, 50, nil, nil,
			501, "pool-a", "Pool A", "active",
			42, "Provider A", "active", "openai-chat", "upstream-gpt-4o", "gpt-4o-2026-08-06",
			701, 601, 801, "openai_chat", 1,
			"https://api.example.com", "openai_chat", "POST", "/v1/chat/completions", "none", []byte(`{"temperature":{"max":2}}`),
			false, false, true, true, true, 0,
		))
	mock.ExpectQuery(`SELECT product_transport_id,action_code`).
		WithArgs(int64(7), uint64(601)).
		WillReturnRows(sqlmock.NewRows([]string{"product_transport_id", "action_code", "allowed_source_state", "idempotency_mode", "request_schema_version", "response_schema_version"}).
			AddRow(601, "submit", "allocated", "user_keyed", 1, 2))

	router := gin.New()
	router.GET("/channels/:id/relations", ListUnifiedChannelRelations)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/channels/42/relations", nil))

	body := response.Body.String()
	for _, want := range []string{
		`"active_release_id":7`, `"channel_id":42`, `"product_id":701`,
		`"product_transport_id":601`, `"channel_transport_id":801`,
		`"adapter_code":"openai_chat"`, `"adapter_version":1`,
		`"model_type":"llm"`,
		`"base_url":"https://api.example.com"`, `"request_path":"/v1/chat/completions"`,
		`"capability_constraints":{"temperature":{"max":2}}`,
		`"actions":[{"action_code":"submit","allowed_source_state":"allocated","idempotency_mode":"user_keyed","request_schema_version":1,"response_schema_version":2}]`,
		`"serving":1`,
	} {
		if response.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("status=%d missing=%s body=%s", response.Code, want, body)
		}
	}
	if strings.Contains(body, "validation_missing") {
		t.Fatalf("legacy validation unexpectedly blocks serving: %s", body)
	}
}

func TestUnifiedRelationSQLDoesNotReadLegacyValidations(t *testing.T) {
	for _, table := range []string{
		"gw_credential_entitlement_state",
		"gw_credential_validation_events",
		"gw_commercial_state",
		"gw_commercial_validation_events",
	} {
		if strings.Contains(unifiedRelationSQL, table) {
			t.Fatalf("relation SQL still reads legacy validation table %q", table)
		}
	}
}

func TestRegisterRoutesIncludesUnifiedChannelRelations(t *testing.T) {
	router := gin.New()
	RegisterRoutes(router.Group("/api/admin"))
	for _, route := range router.Routes() {
		if route.Method == "GET" && route.Path == "/api/admin/unified-gateway/channels/:id/relations" {
			return
		}
	}
	t.Fatal("channel relations route not registered")
}

func TestUnifiedRelationSQLNormalizesRouteStateCollations(t *testing.T) {
	for _, comparison := range []string{
		"rs.model_name COLLATE utf8mb4_unicode_ci=mn.api_name COLLATE utf8mb4_unicode_ci",
		"rs.transport COLLATE utf8mb4_unicode_ci=ct.transport_code COLLATE utf8mb4_unicode_ci",
	} {
		if !strings.Contains(unifiedRelationSQL, comparison) {
			t.Fatalf("relation SQL is missing collation-safe comparison %q", comparison)
		}
	}
}

func installUnifiedRelationObservedLogger(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.ErrorLevel)
	previous := logger.L
	logger.L = zap.New(core)
	t.Cleanup(func() { logger.L = previous })
	return logs
}
