package console

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
	"gorm.io/gorm"
)

var apiCallConsoleDBCounter int64

type apiCallEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func TestListAPICallsScopesUsersAndSupportsFilters(t *testing.T) {
	db := setupAPICallConsoleTestDB(t)
	now := time.Now()
	createAPICallConsoleTestCall(t, db, model.APICall{
		ID: "call-user-10-completed", RequestID: "req-10", UserID: 10, TokenID: 100,
		Model: "model-a", Endpoint: "/v1/responses", Status: model.APICallStatusCompleted,
		StartedAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Minute),
	})
	createAPICallConsoleTestCall(t, db, model.APICall{
		ID: "call-user-10-failed", RequestID: "req-11", UserID: 10, TokenID: 101,
		Model: "model-b", Endpoint: "/v1/messages", Status: model.APICallStatusFailed,
		StartedAt: now, CreatedAt: now,
	})
	createAPICallConsoleTestCall(t, db, model.APICall{
		ID: "call-user-20-completed", RequestID: "req-20", UserID: 20, TokenID: 200,
		Model: "model-a", Endpoint: "/v1/responses", Status: model.APICallStatusCompleted,
		StartedAt: now, CreatedAt: now,
	})

	response := requestAPICallConsole(t, 10, string(model.UserRoleUser), http.MethodGet,
		"/api/calls?page=1&page_size=1&model=model-a&status=completed")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var envelope apiCallEnvelope
	decodeAPICallConsoleResponse(t, response, &envelope)
	var list struct {
		Items      []model.APICall `json:"items"`
		Total      int64           `json:"total"`
		Page       int             `json:"page"`
		PageSize   int             `json:"page_size"`
		SnapshotAt string          `json:"snapshot_at"`
	}
	if err := json.Unmarshal(envelope.Data, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != "call-user-10-completed" {
		t.Fatalf("unexpected scoped list: %+v", list)
	}
	if list.Page != 1 || list.PageSize != 1 {
		t.Fatalf("unexpected pagination: page=%d page_size=%d", list.Page, list.PageSize)
	}
	if _, err := time.Parse(time.RFC3339Nano, list.SnapshotAt); err != nil {
		t.Fatalf("invalid snapshot_at %q: %v", list.SnapshotAt, err)
	}

	response = requestAPICallConsole(t, 10, string(model.UserRoleUser), http.MethodGet,
		"/api/calls?user_id=20")
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-user status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body.String())
	}
	decodeAPICallConsoleResponse(t, response, &envelope)
	if envelope.Code != pkgErrors.ErrNoPermission.Code {
		t.Fatalf("cross-user code = %d, want %d", envelope.Code, pkgErrors.ErrNoPermission.Code)
	}

	response = requestAPICallConsole(t, 1, string(model.UserRoleAdmin), http.MethodGet,
		"/api/calls?user_id=20&request_id=call-user-20-completed")
	if response.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	decodeAPICallConsoleResponse(t, response, &envelope)
	if err := json.Unmarshal(envelope.Data, &list); err != nil {
		t.Fatalf("decode admin list: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].UserID != 20 {
		t.Fatalf("unexpected admin list: %+v", list)
	}
}

func TestListAPICallsRejectsInvalidQueriesWithoutLeakingErrors(t *testing.T) {
	setupAPICallConsoleTestDB(t)

	response := requestAPICallConsole(t, 10, string(model.UserRoleUser), http.MethodGet,
		"/api/calls?page=invalid")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var envelope apiCallEnvelope
	decodeAPICallConsoleResponse(t, response, &envelope)
	if envelope.Code != pkgErrors.ErrInvalidParams.Code || envelope.Message != "invalid query parameters" {
		t.Fatalf("unexpected public error: %+v", envelope)
	}

	response = requestAPICallConsole(t, 10, string(model.UserRoleUser), http.MethodGet,
		"/api/calls?start_date=not-a-date")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("date status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	decodeAPICallConsoleResponse(t, response, &envelope)
	if envelope.Message != "invalid call query" {
		t.Fatalf("service error leaked: %+v", envelope)
	}

	response = requestAPICallConsole(t, 10, string(model.UserRoleUser), http.MethodGet,
		"/api/calls?snapshot_at=invalid")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("snapshot status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	decodeAPICallConsoleResponse(t, response, &envelope)
	if envelope.Message != "invalid call query" {
		t.Fatalf("snapshot service error leaked: %+v", envelope)
	}
}

func TestGetAPICallReturnsDetailsAndEnforcesOwnership(t *testing.T) {
	db := setupAPICallConsoleTestDB(t)
	now := time.Now()
	call := model.APICall{
		ID: "call-detail", RequestID: "req-detail", UserID: 10, TokenID: 100,
		Model: "model-a", Endpoint: "/v1/responses", Status: model.APICallStatusCompleted,
		Store: true, StartedAt: now, CreatedAt: now,
	}
	internalCallID := createAPICallConsoleTestCall(t, db, call)
	if err := db.Exec(`INSERT INTO gw_api_call_attempts(id,call_id,attempt_no,catalog_release_id,product_transport_id,credential_id,state,created_at,updated_at) VALUES (1,?,1,1,1,31,'completed',?,?)`, internalCallID, now, now.Add(time.Second)).Error; err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	if err := db.Exec(`UPDATE gw_api_calls SET final_attempt_id=1 WHERE id=?`, internalCallID).Error; err != nil {
		t.Fatalf("attach final attempt: %v", err)
	}
	if err := db.Exec(`INSERT INTO gw_channel_request_logs(id,attempt_id,action,http_status,duration_ms,error_code) VALUES (1,1,'submit',200,12,'')`).Error; err != nil {
		t.Fatalf("create request log: %v", err)
	}
	if err := db.Exec(`INSERT INTO billing_events(id,call_id,event_type,amount,created_at) VALUES (1,?,'reservation_settled','0.125',?)`, internalCallID, now).Error; err != nil {
		t.Fatalf("create billing event: %v", err)
	}
	if err := db.Exec(`INSERT INTO gw_api_call_payloads(id,call_id,kind,content_length,retention_until,purged_at,created_at) VALUES (1,?,'request',17,?,NULL,?)`, internalCallID, now.Add(time.Hour), now).Error; err != nil {
		t.Fatalf("create payload metadata: %v", err)
	}

	response := requestAPICallConsole(t, 10, string(model.UserRoleUser), http.MethodGet,
		"/api/calls/call-detail")
	if response.Code != http.StatusOK {
		t.Fatalf("owner status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	var envelope apiCallEnvelope
	decodeAPICallConsoleResponse(t, response, &envelope)
	var detail struct {
		Call        model.APICall          `json:"call"`
		Attempts    []model.APICallAttempt `json:"attempts"`
		BillingLogs []model.BillingLog     `json:"billing_logs"`
		Payloads    []struct {
			Data          string `json:"data"`
			Encrypted     bool   `json:"encrypted"`
			OriginalBytes int64  `json:"original_bytes"`
		} `json:"payloads"`
	}
	if err := json.Unmarshal(envelope.Data, &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Call.ID != call.ID || len(detail.Attempts) != 1 || len(detail.BillingLogs) != 1 || len(detail.Payloads) != 0 {
		t.Fatalf("incomplete detail: %+v", detail)
	}
	if detail.Call.UserID != 0 || detail.Call.TokenID != 0 || detail.Attempts[0].ChannelID != 0 ||
		detail.Attempts[0].KeyID != 0 || detail.Attempts[0].VendorModel != "" ||
		detail.BillingLogs[0].IdempotentKey != "" {
		t.Fatalf("owner detail exposed internal data: %+v", detail)
	}

	emptyCall := model.APICall{
		ID: "call-empty-detail", RequestID: "req-empty-detail", UserID: 10, TokenID: 100,
		Model: "model-a", Endpoint: "/v1/responses", Status: model.APICallStatusCompleted,
		StartedAt: now, CreatedAt: now,
	}
	createAPICallConsoleTestCall(t, db, emptyCall)
	for _, role := range []string{string(model.UserRoleUser), string(model.UserRoleAdmin)} {
		response = requestAPICallConsole(t, 10, role, http.MethodGet, "/api/calls/call-empty-detail")
		if response.Code != http.StatusOK {
			t.Fatalf("empty detail role=%s status=%d; body=%s", role, response.Code, response.Body.String())
		}
		decodeAPICallConsoleResponse(t, response, &envelope)
		var arrays struct {
			Attempts    json.RawMessage `json:"attempts"`
			BillingLogs json.RawMessage `json:"billing_logs"`
			Payloads    json.RawMessage `json:"payloads"`
		}
		if err := json.Unmarshal(envelope.Data, &arrays); err != nil {
			t.Fatalf("decode empty detail arrays: %v", err)
		}
		if string(arrays.Attempts) != "[]" || string(arrays.BillingLogs) != "[]" || string(arrays.Payloads) != "[]" {
			t.Fatalf("empty detail role=%s arrays=%s/%s/%s", role, arrays.Attempts, arrays.BillingLogs, arrays.Payloads)
		}
	}

	response = requestAPICallConsole(t, 20, string(model.UserRoleUser), http.MethodGet,
		"/api/calls/call-detail")
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-user status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
	}
	decodeAPICallConsoleResponse(t, response, &envelope)
	if envelope.Message != "api call not found" {
		t.Fatalf("cross-user response disclosed record state: %+v", envelope)
	}

	response = requestAPICallConsole(t, 1, string(model.UserRoleAdmin), http.MethodGet,
		"/api/calls/call-detail")
	if response.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	decodeAPICallConsoleResponse(t, response, &envelope)
	if err := json.Unmarshal(envelope.Data, &detail); err != nil {
		t.Fatalf("decode admin detail: %v", err)
	}
	if len(detail.Payloads) != 1 || detail.Payloads[0].Data != "" ||
		!detail.Payloads[0].Encrypted || detail.Payloads[0].OriginalBytes != 17 ||
		detail.Attempts[0].KeyID != 31 {
		t.Fatalf("admin detail is incomplete: %+v", detail)
	}
}

func setupAPICallConsoleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:api-call-console-%d?mode=memory&cache=shared", atomic.AddInt64(&apiCallConsoleDBCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE gw_api_calls(id INTEGER PRIMARY KEY AUTOINCREMENT, public_id TEXT UNIQUE, user_id INTEGER, token_id INTEGER, operation_contract_id INTEGER, catalog_release_id INTEGER, model_operation_id INTEGER, sku_id INTEGER, status TEXT, quoted_amount TEXT, current_attempt_id INTEGER, final_attempt_id INTEGER, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE gw_operation_contracts(id INTEGER PRIMARY KEY, operation_code TEXT)`,
		`CREATE TABLE gw_operation_routes(id INTEGER PRIMARY KEY, operation_contract_id INTEGER, route_template TEXT)`,
		`CREATE TABLE gw_models(id INTEGER PRIMARY KEY, model_code TEXT)`,
		`CREATE TABLE gw_catalog_models(id INTEGER PRIMARY KEY, release_id INTEGER, model_id INTEGER, display_name TEXT)`,
		`CREATE TABLE gw_model_operations(id INTEGER PRIMARY KEY, release_id INTEGER, catalog_model_id INTEGER)`,
		`CREATE TABLE gw_api_resources(id INTEGER PRIMARY KEY, public_id TEXT, resource_kind TEXT, call_id INTEGER, user_id INTEGER, token_id INTEGER, created_at DATETIME)`,
		`CREATE TABLE gw_api_call_attempts(id INTEGER PRIMARY KEY, call_id INTEGER, attempt_no INTEGER, catalog_release_id INTEGER, product_transport_id INTEGER, credential_id INTEGER, state TEXT, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE gw_product_transports(id INTEGER PRIMARY KEY, release_id INTEGER, product_id INTEGER, channel_transport_id INTEGER)`,
		`CREATE TABLE gw_products(id INTEGER PRIMARY KEY, release_id INTEGER, channel_id INTEGER, vendor_model TEXT)`,
		`CREATE TABLE gw_channel_transports(id INTEGER PRIMARY KEY, release_id INTEGER, transport_code TEXT, protocol TEXT, request_path TEXT)`,
		`CREATE TABLE gw_channel_request_logs(id INTEGER PRIMARY KEY, attempt_id INTEGER, action TEXT, http_status INTEGER, duration_ms INTEGER, error_code TEXT, request_payload_blob_id INTEGER, response_payload_blob_id INTEGER, request_bytes_complete BOOLEAN DEFAULT 0, response_bytes_complete BOOLEAN DEFAULT 0, created_at DATETIME)`,
		`CREATE TABLE billing_reservations(id INTEGER PRIMARY KEY, call_id INTEGER, state TEXT)`,
		`CREATE TABLE billing_settlements(reservation_id INTEGER PRIMARY KEY, actual_amount TEXT)`,
		`CREATE TABLE billing_events(id INTEGER PRIMARY KEY, call_id INTEGER, event_type TEXT, amount TEXT, created_at DATETIME)`,
		`CREATE TABLE gw_api_call_payloads(id INTEGER PRIMARY KEY, call_id INTEGER, kind TEXT, content_length INTEGER, retention_until DATETIME, purged_at DATETIME, created_at DATETIME)`,
		`CREATE TABLE gateway_channels(id INTEGER PRIMARY KEY, display_name TEXT)`,
		`INSERT INTO gw_operation_contracts(id,operation_code) VALUES (1,'responses.create'),(2,'messages.create')`,
		`INSERT INTO gw_operation_routes(id,operation_contract_id,route_template) VALUES (1,1,'/v1/responses'),(2,2,'/v1/messages')`,
		`INSERT INTO gw_models(id,model_code) VALUES (1,'model-a'),(2,'model-b')`,
		`INSERT INTO gw_catalog_models(id,release_id,model_id,display_name) VALUES (1,1,1,'Model A'),(2,1,2,'Model B')`,
		`INSERT INTO gw_model_operations(id,release_id,catalog_model_id) VALUES (1,1,1),(2,1,2)`,
		`INSERT INTO gateway_channels(id,display_name) VALUES (21,'Private channel')`,
		`INSERT INTO gw_products(id,release_id,channel_id,vendor_model) VALUES (1,1,21,'private-model')`,
		`INSERT INTO gw_channel_transports(id,release_id,transport_code,protocol,request_path) VALUES (1,1,'anthropic_messages','anthropic','/private/upstream')`,
		`INSERT INTO gw_product_transports(id,release_id,product_id,channel_transport_id) VALUES (1,1,1,1)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("prepare unified call schema: %v", err)
		}
	}
	var previous *gorm.DB
	if model.HasDB() {
		previous = model.DB()
	}
	model.SetDB(db)
	sqlDB, _ := db.DB()
	t.Cleanup(func() {
		model.SetDB(previous)
		_ = sqlDB.Close()
	})
	return db
}

func createAPICallConsoleTestCall(t *testing.T, db *gorm.DB, call model.APICall) uint64 {
	t.Helper()
	modelOperationID, operationContractID := 1, 1
	if call.Model == "model-b" {
		modelOperationID, operationContractID = 2, 2
	}
	createdAt := call.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	if err := db.Exec(`INSERT INTO gw_api_calls(public_id,user_id,token_id,operation_contract_id,catalog_release_id,model_operation_id,sku_id,status,quoted_amount,current_attempt_id,final_attempt_id,created_at,updated_at) VALUES (?,?,?,?,1,?,1,?,'0',NULL,NULL,?,?)`, call.ID, call.UserID, call.TokenID, operationContractID, modelOperationID, call.Status, createdAt, createdAt).Error; err != nil {
		t.Fatalf("create api call %s: %v", call.ID, err)
	}
	var id uint64
	if err := db.Raw(`SELECT id FROM gw_api_calls WHERE public_id=?`, call.ID).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("resolve api call %s: id=%d err=%v", call.ID, id, err)
	}
	return id
}

func requestAPICallConsole(
	t *testing.T,
	userID uint,
	role string,
	method string,
	target string,
) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	group.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyUserRole, role)
		c.Next()
	})
	RegisterRoutes(group)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeAPICallConsoleResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}
