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
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var observabilityConsoleDBCounter int64

type observabilityEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type observabilityListEnvelope struct {
	Items []struct {
		ID uint64 `json:"id"`
	} `json:"items"`
	Total      int64  `json:"total"`
	Page       int    `json:"page"`
	PageSize   int    `json:"page_size"`
	SnapshotID uint64 `json:"snapshot_id"`
}

func TestObservabilityListsEnforceOwnershipAndAdminFilters(t *testing.T) {
	db := setupObservabilityConsoleTestDB(t)
	now := time.Now().Truncate(time.Second)

	accessOwn := model.APIAccessLog{
		RequestID: "req-access-own", UserID: 10, TokenID: 100, ActorType: "token",
		Method: http.MethodGet, Path: "/v1/models", Route: "/v1/models",
		StatusCode: http.StatusOK, DurationMs: 12, IP: "127.0.0.1", CreatedAt: now,
	}
	accessOther := model.APIAccessLog{
		RequestID: "req-access-other", UserID: 20, TokenID: 200, ActorType: "token",
		Method: http.MethodPost, Path: "/v1/responses", Route: "/v1/responses",
		StatusCode: http.StatusCreated, DurationMs: 24, IP: "127.0.0.2", CreatedAt: now.Add(time.Second),
	}
	createObservabilityRecord(t, db, &accessOwn)
	createObservabilityRecord(t, db, &accessOther)

	auditOwn := model.AuditEvent{
		RequestID: "req-audit-own", ActorType: "user", ActorUserID: 10, ActorTokenID: 100,
		Action: "POST /api/tokens", ResourceType: "tokens", Outcome: "success",
		HTTPStatus: http.StatusOK, IP: "127.0.0.1", Metadata: datatypes.JSON(`{"route":"/api/tokens"}`), CreatedAt: now,
	}
	auditOther := model.AuditEvent{
		RequestID: "req-audit-other", ActorType: "user", ActorUserID: 20, ActorTokenID: 200,
		Action: "DELETE /api/tokens/:id", ResourceType: "tokens", ResourceID: "200",
		Outcome: "failed", HTTPStatus: http.StatusForbidden, IP: "127.0.0.2",
		Metadata: datatypes.JSON(`{"route":"/api/tokens/:id"}`), CreatedAt: now.Add(time.Second),
	}
	createObservabilityRecord(t, db, &auditOwn)
	createObservabilityRecord(t, db, &auditOther)

	balanceOwn := model.BalanceEntry{
		EntryKey: "balance-own", SourceKey: "source-own", AccountType: model.BalanceAccountUser,
		AccountID: 10, UserID: 10, TokenID: 100, Direction: model.BalanceDirectionCredit,
		Category: "recharge", Amount: decimal.NewFromInt(10), BalanceBefore: decimal.Zero,
		BalanceAfter: decimal.NewFromInt(10), ActorUserID: 10, CreatedAt: now,
	}
	balanceOther := model.BalanceEntry{
		EntryKey: "balance-other", SourceKey: "source-other", AccountType: model.BalanceAccountToken,
		AccountID: 200, UserID: 20, TokenID: 200, Direction: model.BalanceDirectionDebit,
		Category: "deduction", Amount: decimal.NewFromInt(2), BalanceBefore: decimal.NewFromInt(10),
		BalanceAfter: decimal.NewFromInt(8), CallID: "call-other", CreatedAt: now.Add(time.Second),
	}
	createObservabilityRecord(t, db, &balanceOwn)
	createObservabilityRecord(t, db, &balanceOther)

	tests := []struct {
		name       string
		path       string
		ownID      uint64
		otherID    uint64
		adminQuery string
	}{
		{
			name: "access logs", path: "/api/observability/access-logs",
			ownID: accessOwn.ID, otherID: accessOther.ID,
			adminQuery: "user_id=20&token_id=200&method=POST&status_code=201",
		},
		{
			name: "audit events", path: "/api/observability/audit-events",
			ownID: auditOwn.ID, otherID: auditOther.ID,
			adminQuery: "user_id=20&token_id=200&outcome=failed&resource_type=tokens",
		},
		{
			name: "balance entries", path: "/api/observability/balance-entries",
			ownID: balanceOwn.ID, otherID: balanceOther.ID,
			adminQuery: "user_id=20&token_id=200&direction=debit&category=deduction",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := requestObservabilityConsole(t, 10, string(model.UserRoleUser), test.path+"?page=1&page_size=10")
			list := decodeObservabilityList(t, response, http.StatusOK)
			if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != test.ownID {
				t.Fatalf("owner list = %+v, want id %d", list, test.ownID)
			}
			if list.Page != 1 || list.PageSize != 10 {
				t.Fatalf("pagination = page %d size %d", list.Page, list.PageSize)
			}

			response = requestObservabilityConsole(t, 10, string(model.UserRoleUser), test.path+"?user_id=20")
			assertObservabilityError(t, response, http.StatusForbidden, pkgErrors.ErrNoPermission.Code)

			response = requestObservabilityConsole(t, 10, string(model.UserRoleUser), test.path+"?token_id=200")
			list = decodeObservabilityList(t, response, http.StatusOK)
			if list.Total != 0 || len(list.Items) != 0 {
				t.Fatalf("cross-token query leaked records: %+v", list)
			}

			response = requestObservabilityConsole(t, 1, string(model.UserRoleAdmin), test.path+"?"+test.adminQuery)
			list = decodeObservabilityList(t, response, http.StatusOK)
			if list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != test.otherID {
				t.Fatalf("admin filtered list = %+v, want id %d", list, test.otherID)
			}
		})
	}
}

func TestObservabilityListsRejectInvalidQueries(t *testing.T) {
	setupObservabilityConsoleTestDB(t)

	response := requestObservabilityConsole(
		t, 10, string(model.UserRoleUser), "/api/observability/access-logs?page=invalid",
	)
	assertObservabilityError(t, response, http.StatusBadRequest, pkgErrors.ErrInvalidParams.Code)

	response = requestObservabilityConsole(
		t, 10, string(model.UserRoleUser), "/api/observability/audit-events?start_date=invalid",
	)
	assertObservabilityError(t, response, http.StatusBadRequest, pkgErrors.ErrInvalidParams.Code)

	response = requestObservabilityConsole(
		t, 0, "", "/api/observability/balance-entries",
	)
	assertObservabilityError(t, response, http.StatusUnauthorized, pkgErrors.ErrUnauthorized.Code)
}

func TestObservabilityEndpointsKeepSnapshotAcrossInsertedRows(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		create func(*testing.T, *gorm.DB, int, time.Time) uint64
	}{
		{
			name: "access logs", path: "/api/observability/access-logs",
			create: func(t *testing.T, db *gorm.DB, sequence int, createdAt time.Time) uint64 {
				record := model.APIAccessLog{
					RequestID: fmt.Sprintf("access-page-%d", sequence), UserID: 10, ActorType: "user",
					Method: http.MethodGet, Path: "/api/test", Route: "/api/test",
					StatusCode: http.StatusOK, CreatedAt: createdAt,
				}
				createObservabilityRecord(t, db, &record)
				return record.ID
			},
		},
		{
			name: "audit events", path: "/api/observability/audit-events",
			create: func(t *testing.T, db *gorm.DB, sequence int, createdAt time.Time) uint64 {
				record := model.AuditEvent{
					RequestID: fmt.Sprintf("audit-page-%d", sequence), ActorType: "user", ActorUserID: 10,
					Action: "POST /api/test", ResourceType: "test", Outcome: "success",
					HTTPStatus: http.StatusOK, CreatedAt: createdAt,
				}
				createObservabilityRecord(t, db, &record)
				return record.ID
			},
		},
		{
			name: "balance entries", path: "/api/observability/balance-entries",
			create: func(t *testing.T, db *gorm.DB, sequence int, createdAt time.Time) uint64 {
				record := model.BalanceEntry{
					EntryKey: fmt.Sprintf("entry-page-%d", sequence), SourceKey: fmt.Sprintf("source-page-%d", sequence),
					AccountType: model.BalanceAccountUser, AccountID: 10, UserID: 10,
					Direction: model.BalanceDirectionCredit, Category: "recharge",
					Amount: decimal.NewFromInt(1), BalanceBefore: decimal.NewFromInt(int64(sequence - 1)),
					BalanceAfter: decimal.NewFromInt(int64(sequence)), CreatedAt: createdAt,
				}
				createObservabilityRecord(t, db, &record)
				return record.ID
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupObservabilityConsoleTestDB(t)
			base := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)
			ids := make([]uint64, 3)
			for i := range ids {
				ids[i] = test.create(t, db, i+1, base.Add(time.Duration(i)*time.Second))
			}

			first := decodeObservabilityList(t, requestObservabilityConsole(
				t, 10, string(model.UserRoleUser), test.path+"?page=1&page_size=1",
			), http.StatusOK)
			if first.SnapshotID != ids[2] || first.Total != 3 || len(first.Items) != 1 || first.Items[0].ID != ids[2] {
				t.Fatalf("first page = %+v, want snapshot/id %d and total 3", first, ids[2])
			}

			insertedID := test.create(t, db, 4, base.Add(3*time.Second))
			secondTarget := fmt.Sprintf("%s?page=2&page_size=1&snapshot_id=%d", test.path, first.SnapshotID)
			second := decodeObservabilityList(t, requestObservabilityConsole(
				t, 10, string(model.UserRoleUser), secondTarget,
			), http.StatusOK)
			if second.SnapshotID != first.SnapshotID || second.Total != 3 || len(second.Items) != 1 || second.Items[0].ID != ids[1] {
				t.Fatalf("second page = %+v, want snapshot %d, id %d and total 3", second, first.SnapshotID, ids[1])
			}

			refreshed := decodeObservabilityList(t, requestObservabilityConsole(
				t, 10, string(model.UserRoleUser), test.path+"?page=1&page_size=1",
			), http.StatusOK)
			if refreshed.SnapshotID != insertedID || refreshed.Total != 4 || len(refreshed.Items) != 1 || refreshed.Items[0].ID != insertedID {
				t.Fatalf("refreshed page = %+v, want snapshot/id %d and total 4", refreshed, insertedID)
			}
		})
	}
}

func TestAccessLogPaginationDoesNotPersistItsOwnQueries(t *testing.T) {
	db := setupObservabilityConsoleTestDB(t)
	now := time.Now().Truncate(time.Second)
	older := model.APIAccessLog{
		RequestID: "req-page-older", UserID: 10, ActorType: "user",
		Method: http.MethodGet, Path: "/api/tokens", Route: "/api/tokens",
		StatusCode: http.StatusOK, CreatedAt: now,
	}
	newer := model.APIAccessLog{
		RequestID: "req-page-newer", UserID: 10, ActorType: "user",
		Method: http.MethodGet, Path: "/api/tasks", Route: "/api/tasks",
		StatusCode: http.StatusOK, CreatedAt: now.Add(time.Second),
	}
	createObservabilityRecord(t, db, &older)
	createObservabilityRecord(t, db, &newer)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestID(), middleware.PersistentAccessLogger())
	group := router.Group("/api")
	group.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, uint(10))
		c.Set(middleware.ContextKeyUserRole, string(model.UserRoleUser))
		c.Next()
	})
	RegisterRoutes(group)

	requestPage := func(page int, snapshotID uint64) observabilityListEnvelope {
		t.Helper()
		recorder := httptest.NewRecorder()
		target := fmt.Sprintf("/api/observability/access-logs?page=%d&page_size=1", page)
		if snapshotID > 0 {
			target += fmt.Sprintf("&snapshot_id=%d", snapshotID)
		}
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		return decodeObservabilityList(t, recorder, http.StatusOK)
	}

	firstPage := requestPage(1, 0)
	secondPage := requestPage(2, firstPage.SnapshotID)
	if firstPage.Total != 2 || len(firstPage.Items) != 1 || firstPage.Items[0].ID != newer.ID {
		t.Fatalf("first page = %+v", firstPage)
	}
	if secondPage.Total != 2 || len(secondPage.Items) != 1 || secondPage.Items[0].ID != older.ID {
		t.Fatalf("second page = %+v", secondPage)
	}

	var count int64
	if err := db.Model(&model.APIAccessLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("access log count = %d, want 2", count)
	}
}

func setupObservabilityConsoleTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:observability-console-%d?mode=memory&cache=shared", atomic.AddInt64(&observabilityConsoleDBCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.APIAccessLog{},
		&model.AuditEvent{},
		&model.BalanceEntry{},
	); err != nil {
		t.Fatalf("migrate observability tables: %v", err)
	}
	model.SetDB(db)
	return db
}

func createObservabilityRecord(t *testing.T, db *gorm.DB, record any) {
	t.Helper()
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("create observability record: %v", err)
	}
}

func requestObservabilityConsole(t *testing.T, userID uint, role, target string) *httptest.ResponseRecorder {
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
	request := httptest.NewRequest(http.MethodGet, target, nil)
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeObservabilityList(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
) observabilityListEnvelope {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body.String())
	}
	var envelope observabilityEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var list observabilityListEnvelope
	if err := json.Unmarshal(envelope.Data, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return list
}

func assertObservabilityError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
	wantCode int,
) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, wantStatus, response.Body.String())
	}
	var envelope observabilityEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if envelope.Code != wantCode {
		t.Fatalf("code = %d, want %d; body=%s", envelope.Code, wantCode, response.Body.String())
	}
}
