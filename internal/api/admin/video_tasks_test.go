package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var videoTaskAdminDBCounter int64

func TestListVideoTasksFiltersAndUsesLightweightRows(t *testing.T) {
	db := setupVideoTaskAdminTestDB(t)
	createdAt := time.Date(2026, 8, 25, 12, 0, 0, 0, time.Local)
	tasks := []video.VideoTask{
		{
			ID: "video_match", CallID: "call_match", ProviderTaskID: "provider_match",
			UserID: 3, TokenID: 4, ChannelID: 2, KeyID: 5,
			Model: "minimax-h3", VendorModel: "h3", Status: video.VideoTaskStatusCompleted,
			TaskMode: "multimodal", ServiceTier: "priority", AdapterType: video.AdapterTypeGeneric,
			ContentJSON: datatypes.JSON(`{"large":"content"}`), ParamsJSON: datatypes.JSON(`{"seed":7}`),
			ResultJSON: datatypes.JSON(`{"video_url":"https://example.com/result.mp4"}`), CreatedAt: createdAt,
		},
		{
			ID: "video_other", CallID: "call_other", ProviderTaskID: "provider_other",
			UserID: 3, TokenID: 4, ChannelID: 2, KeyID: 5,
			Model: "seedance-2.0", VendorModel: "seedance-2.0", Status: video.VideoTaskStatusTracking,
			TaskMode: "text", ServiceTier: "standard", AdapterType: video.AdapterTypeGeneric, CreatedAt: createdAt,
		},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatalf("create video tasks: %v", err)
	}

	target := "/tasks?keyword=provider_match&model=minimax&status=completed&task_mode=multimodal&service_tier=priority&channel_id=2&user_id=3&token_id=4&start_date=2026-08-25&end_date=2026-08-25"
	response := requestVideoTaskAdmin(t, http.MethodGet, target, ListVideoTasks)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			Total      int64               `json:"total"`
			Items      []videoTaskListItem `json:"items"`
			SnapshotAt string              `json:"snapshot_at"`
		} `json:"data"`
	}
	decodeVideoTaskAdminResponse(t, response, &payload)
	if payload.Code != 0 || payload.Data.Total != 1 || len(payload.Data.Items) != 1 || payload.Data.Items[0].ID != "video_match" {
		t.Fatalf("unexpected list response: %+v", payload)
	}
	for _, field := range []string{"content_json", "params_json", "result_json", "provider_response"} {
		if strings.Contains(response.Body.String(), `"`+field+`"`) {
			t.Fatalf("list response contains large field %q: %s", field, response.Body.String())
		}
	}
	if _, err := time.Parse(time.RFC3339Nano, payload.Data.SnapshotAt); err != nil {
		t.Fatalf("invalid snapshot_at %q: %v", payload.Data.SnapshotAt, err)
	}
}

func TestListVideoTasksRejectsInvalidNumericFilter(t *testing.T) {
	setupVideoTaskAdminTestDB(t)
	response := requestVideoTaskAdmin(t, http.MethodGet, "/tasks?channel_id=invalid", ListVideoTasks)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGetVideoTaskIncludesAdminUpstreamFields(t *testing.T) {
	db := setupVideoTaskAdminTestDB(t)
	task := video.VideoTask{
		ID: "video_detail", CallID: "call_video_detail", UserID: 1, TokenID: 2, Model: "minimax-h3", VendorModel: "h3",
		Status: video.VideoTaskStatusTracking, TaskMode: "multimodal", ServiceTier: "standard",
		AdapterType: video.AdapterTypeGeneric, ProviderResponse: datatypes.JSON(`{"code":0}`),
		ProviderMetadata: datatypes.JSON(`{"queue":"standard"}`), PollCount: 6, CreatedAt: time.Now(),
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create video task: %v", err)
	}
	call := model.APICall{
		ID: task.CallID, UserID: task.UserID, TokenID: task.TokenID, Model: task.Model,
		RetainPayload: true, StartedAt: time.Now(), CreatedAt: time.Now(),
	}
	if err := db.Create(&call).Error; err != nil {
		t.Fatalf("create API call: %v", err)
	}
	attempt := model.APICallAttempt{CallID: call.ID, AttemptNo: 1, StartedAt: time.Now()}
	if err := db.Create(&attempt).Error; err != nil {
		t.Fatalf("create API call attempt: %v", err)
	}
	if err := db.Create(&model.APICallPayload{
		CallID: call.ID, AttemptID: attempt.ID, Kind: model.APICallPayloadUpstreamRequest,
		ContentType: "application/json", Data: []byte(`{"model":"h3","seed":7}`), OriginalBytes: 23,
	}).Error; err != nil {
		t.Fatalf("create API call payload: %v", err)
	}

	response := requestVideoTaskAdmin(t, http.MethodGet, "/tasks/video_detail", GetVideoTask)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code int `json:"code"`
		Data struct {
			ProviderResponse json.RawMessage `json:"provider_response"`
			PollCount        int             `json:"poll_count"`
			CallPayloads     []struct {
				Kind string `json:"kind"`
				Data string `json:"data"`
			} `json:"call_payloads"`
		} `json:"data"`
	}
	decodeVideoTaskAdminResponse(t, response, &payload)
	if string(payload.Data.ProviderResponse) != `{"code":0}` || payload.Data.PollCount != 6 {
		t.Fatalf("unexpected detail response: %+v", payload.Data)
	}
	if len(payload.Data.CallPayloads) != 1 || payload.Data.CallPayloads[0].Kind != model.APICallPayloadUpstreamRequest ||
		payload.Data.CallPayloads[0].Data != `{"model":"h3","seed":7}` {
		t.Fatalf("unexpected call payloads: %+v", payload.Data.CallPayloads)
	}
}

func setupVideoTaskAdminTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:video-task-admin-%d?mode=memory&cache=shared", atomic.AddInt64(&videoTaskAdminDBCounter, 1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&video.VideoTask{}, &model.APICall{}, &model.APICallAttempt{}, &model.APICallPayload{}, &model.BillingLog{},
	); err != nil {
		t.Fatalf("migrate video tasks: %v", err)
	}
	model.SetDB(db)
	return db
}

func requestVideoTaskAdmin(t *testing.T, method, target string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/tasks", handler)
	router.GET("/tasks/:id", handler)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

func decodeVideoTaskAdminResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}
