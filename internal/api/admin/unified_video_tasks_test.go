package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func unifiedVideoResolutionRouter(actor uint) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != 0 {
			c.Set("user_id", actor)
		}
	})
	group := router.Group("/api/admin")
	RegisterRoutes(group)
	return router
}

func stubUnifiedVideoResolution(t *testing.T, resolve func(context.Context, *repository.Store, uint64) error) {
	t.Helper()
	original := finishUnifiedVideoTaskManualReview
	finishUnifiedVideoTaskManualReview = resolve
	t.Cleanup(func() { finishUnifiedVideoTaskManualReview = original })
}

func TestListUnifiedVideoTasksUsesProjectionOnly(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM gw_api_resources`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT r.id,c.id,r.public_id`).
		WithArgs(sqlmock.AnyArg(), 20, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"resource_id", "call_id", "public_id", "user_id", "token_id", "call_status", "task_status", "progress", "summary",
			"channel_id", "credential_id", "adapter_code", "quoted_amount", "final_amount", "billing_status", "created_at", "submitted_at", "completed_at",
			"request_payload_id", "result_payload_id", "async_execution_id",
		}).AddRow(1, 2, "video-public", 3, 4, "in_progress", "running", 45,
			[]byte(`{"model":"seedance-2.0","vendor_model":"seedance-vendor","task_mode":"text","service_tier":"standard","resolution":"1080p","ratio":"16:9","duration":5,"generate_audio":true}`),
			5, 6, "seedance", "1.25", "0", "active", now, now, nil, 7, 0, 8))
	router := gin.New()
	router.GET("/tasks", ListUnifiedVideoTasks)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tasks", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			Total    int              `json:"total"`
			Page     int              `json:"page"`
			PageSize int              `json:"page_size"`
			Items    []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Total != 1 || envelope.Data.Page != 1 || envelope.Data.PageSize != 20 || len(envelope.Data.Items) != 1 || envelope.Data.Items[0]["status"] != "tracking" || envelope.Data.Items[0]["model"] != "seedance-2.0" {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "prompt") || strings.Contains(response.Body.String(), "request_payload") {
		t.Fatal("list response exposed executable payload metadata")
	}
}

func TestGetUnifiedVideoTaskReturnsProjectedDetail(t *testing.T) {
	mock := unifiedChannelMock(t)
	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT r.id,c.id,r.public_id`).WithArgs("video-public").
		WillReturnRows(sqlmock.NewRows([]string{
			"resource_id", "call_id", "public_id", "user_id", "token_id", "call_status", "task_status", "progress", "summary",
			"channel_id", "credential_id", "adapter_code", "quoted_amount", "final_amount", "billing_status", "created_at", "submitted_at", "completed_at",
			"request_payload_id", "result_payload_id", "async_execution_id",
		}).AddRow(1, 2, "video-public", 3, 4, "completed", "succeeded", 100,
			[]byte(`{"model":"seedance-2.0","task_mode":"multimodal","service_tier":"standard","resolution":"1080p","ratio":"16:9","duration":5}`),
			5, 6, "seedance", "1.25", "1.20", "settled", now, now, now, 0, 0, 8))
	router := gin.New()
	router.GET("/tasks/:id", GetUnifiedVideoTask)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tasks/video-public", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"completed"`) || !strings.Contains(response.Body.String(), `"call_payloads":[]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGetUnifiedVideoTaskKeepsDetailWhenRequestPayloadExpired(t *testing.T) {
	mock := unifiedChannelMock(t)
	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT r.id,c.id,r.public_id`).WithArgs("video-expired").
		WillReturnRows(sqlmock.NewRows([]string{
			"resource_id", "call_id", "public_id", "user_id", "token_id", "call_status", "task_status", "progress", "summary",
			"channel_id", "credential_id", "adapter_code", "quoted_amount", "final_amount", "billing_status", "created_at", "submitted_at", "completed_at",
			"request_payload_id", "result_payload_id", "async_execution_id",
		}).AddRow(1, 2, "video-expired", 3, 4, "completed", "succeeded", 100,
			[]byte(`{"model":"seedance-2.0","task_mode":"text","service_tier":"standard","resolution":"1080p","ratio":"16:9","duration":5}`),
			5, 6, "seedance", "1.25", "1.20", "settled", now, now, now, 7, 0, 8))
	mock.ExpectQuery(`SELECT encrypted_blob_id FROM gw_api_call_payloads`).WithArgs(uint64(7), uint64(2), "request").
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_blob_id"}).AddRow(nil))
	router := gin.New()
	router.GET("/tasks/:id", GetUnifiedVideoTask)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tasks/video-expired", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"request_payload_expired":true`) || !strings.Contains(response.Body.String(), `"call_payloads":[]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestListUnifiedVideoTasksRejectsInvalidStatus(t *testing.T) {
	unifiedChannelMock(t)
	router := gin.New()
	router.GET("/tasks", ListUnifiedVideoTasks)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tasks?status=not-a-state", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestResolveUnifiedVideoTaskTerminatesUnknownSubmission(t *testing.T) {
	for _, state := range []string{"manual_review", "submission_unknown"} {
		t.Run(state, func(t *testing.T) {
			mock := unifiedChannelMock(t)
			mock.ExpectQuery(`SELECT x.id,x.state`).WithArgs("video-public").
				WillReturnRows(sqlmock.NewRows([]string{"id", "state"}).AddRow(96, state))
			called := false
			stubUnifiedVideoResolution(t, func(_ context.Context, _ *repository.Store, asyncID uint64) error {
				called = true
				if asyncID != 96 {
					t.Fatalf("asyncID=%d", asyncID)
				}
				return nil
			})
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/admin/video/tasks/video-public/resolve", strings.NewReader(`{"resolution":"terminated_unknown"}`))
			unifiedVideoResolutionRouter(7).ServeHTTP(response, request)
			if response.Code != http.StatusOK || !called || !strings.Contains(response.Body.String(), `"status":"terminated_unknown"`) {
				t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
			}
		})
	}
}

func TestResolveUnifiedVideoTaskRejectsUnsupportedResolution(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/video/tasks/video-public/resolve", strings.NewReader(`{"resolution":"cancelled"}`))
	unifiedVideoResolutionRouter(7).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestResolveUnifiedVideoTaskRejectsNonReviewState(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery(`SELECT x.id,x.state`).WithArgs("video-public").
		WillReturnRows(sqlmock.NewRows([]string{"id", "state"}).AddRow(96, "running"))
	called := false
	stubUnifiedVideoResolution(t, func(context.Context, *repository.Store, uint64) error {
		called = true
		return nil
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/video/tasks/video-public/resolve", strings.NewReader(`{"resolution":"terminated_unknown"}`))
	unifiedVideoResolutionRouter(7).ServeHTTP(response, request)
	if response.Code != http.StatusConflict || called {
		t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
}

func TestResolveUnifiedVideoTaskReportsMissingTask(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery(`SELECT x.id,x.state`).WithArgs("missing").WillReturnError(sql.ErrNoRows)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/video/tasks/missing/resolve", strings.NewReader(`{"resolution":"terminated_unknown"}`))
	unifiedVideoResolutionRouter(7).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestResolveUnifiedVideoTaskMapsRuntimeConflict(t *testing.T) {
	mock := unifiedChannelMock(t)
	mock.ExpectQuery(`SELECT x.id,x.state`).WithArgs("video-public").
		WillReturnRows(sqlmock.NewRows([]string{"id", "state"}).AddRow(96, "manual_review"))
	stubUnifiedVideoResolution(t, func(context.Context, *repository.Store, uint64) error {
		return repository.ErrConflict
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/video/tasks/video-public/resolve", strings.NewReader(`{"resolution":"terminated_unknown"}`))
	unifiedVideoResolutionRouter(7).ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestResolveUnifiedVideoTaskRequiresAdmin(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/video/tasks/video-public/resolve", strings.NewReader(`{"resolution":"terminated_unknown"}`))
	unifiedVideoResolutionRouter(0).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
