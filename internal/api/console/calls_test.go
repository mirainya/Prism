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
	"github.com/shopspring/decimal"
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
		"/api/calls?user_id=20&request_id=req-20")
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
	createAPICallConsoleTestCall(t, db, call)
	attempt := model.APICallAttempt{
		CallID: call.ID, AttemptNo: 1, ChannelID: 21, KeyID: 31,
		VendorModel: "private-model", RequestPath: "/private/upstream", Transport: model.UpstreamTransportAnthropic,
		Status: model.APICallAttemptStatusCompleted, HTTPStatus: http.StatusOK, StartedAt: now,
	}
	if err := db.Create(&attempt).Error; err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	billing := model.BillingLog{
		IdempotentKey: "call-detail:settle", TokenID: call.TokenID, UserID: call.UserID,
		CallID: call.ID, AttemptID: attempt.ID, Phase: model.BillingPhaseSettle,
		Amount: decimal.RequireFromString("0.125"), Type: model.BillingTypeDeduct, Status: "success",
	}
	if err := db.Create(&billing).Error; err != nil {
		t.Fatalf("create billing log: %v", err)
	}
	payload := model.APICallPayload{
		CallID: call.ID, AttemptID: attempt.ID, Kind: model.APICallPayloadRequest,
		ContentType: "application/json", Data: []byte(`{"input":"hello"}`),
	}
	if err := db.Create(&payload).Error; err != nil {
		t.Fatalf("create payload: %v", err)
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
			Data string `json:"data"`
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
	if len(detail.Payloads) != 1 || detail.Payloads[0].Data != `{"input":"hello"}` ||
		detail.Attempts[0].KeyID != 31 {
		t.Fatalf("admin detail is incomplete: %+v", detail)
	}
}

func setupAPICallConsoleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:api-call-console-%d?mode=memory&cache=shared", atomic.AddInt64(&apiCallConsoleDBCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.APICall{},
		&model.APICallAttempt{},
		&model.APICallPayload{},
		&model.BillingLog{},
	); err != nil {
		t.Fatalf("migrate api call console tables: %v", err)
	}
	model.SetDB(db)
	return db
}

func createAPICallConsoleTestCall(t *testing.T, db *gorm.DB, call model.APICall) {
	t.Helper()
	if err := db.Create(&call).Error; err != nil {
		t.Fatalf("create api call %s: %v", call.ID, err)
	}
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
