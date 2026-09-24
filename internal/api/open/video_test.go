package open

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/video"
	perrors "github.com/mirainya/Prism/pkg/errors"
)

func TestVideoGenerationEndpointsRejectLegacyInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handlers := map[string]gin.HandlerFunc{
		"create":   CreateVideoGeneration,
		"estimate": EstimateVideoGeneration,
	}
	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewBufferString(
				`{"model":"video-fast","prompt":"test","content":[]}`,
			))

			handler(context)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestVideoGenerationEndpointsRejectOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := append([]byte(`{"model":"m","prompt":"`), bytes.Repeat([]byte{'x'}, maxVideoRequestBytes)...)
	body = append(body, []byte(`"}`)...)
	for name, handler := range map[string]gin.HandlerFunc{"create": CreateVideoGeneration, "estimate": EstimateVideoGeneration} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
			handler(context)
			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestUnifiedVideoPublicIDFitsResourceSchema(t *testing.T) {
	id := newUnifiedVideoPublicID()
	if len(id) != 36 {
		t.Fatalf("public ID length = %d, want 36", len(id))
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("public ID is not a UUID: %q: %v", id, err)
	}
}

func TestUnifiedVideoCreationRejectsPrivateCallbackBeforePersistence(t *testing.T) {
	_, _, err := createUnifiedVideoGeneration(context.Background(), &video.CreateTaskRequest{
		Callback: "http://127.0.0.1:8080/callback",
	}, "")
	if !errors.Is(err, repository.ErrInvalidInput) {
		t.Fatalf("err=%v, want invalid input", err)
	}
}

func TestUnifiedVideoCreationRequiresActiveCatalog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previousStore, previousRouter := unifiedVideoStore, unifiedVideoRouter
	unifiedVideoStore, unifiedVideoRouter = store, routing.NewRouter()
	t.Cleanup(func() { unifiedVideoStore, unifiedVideoRouter = previousStore, previousRouter })
	mock.ExpectQuery("SELECT active_release_id FROM gw_catalog_runtime_state").
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(nil))

	_, handled, err := planUnifiedVideoGeneration(context.Background(), &video.CreateTaskRequest{Model: "seedance-2.0"})
	if !handled || !errors.Is(err, gatewayruntime.ErrNotReady) {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUnifiedVideoCreationRequiresPublishedActiveCatalog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previousStore, previousRouter := unifiedVideoStore, unifiedVideoRouter
	unifiedVideoStore, unifiedVideoRouter = store, routing.NewRouter()
	t.Cleanup(func() { unifiedVideoStore, unifiedVideoRouter = previousStore, previousRouter })
	mock.ExpectQuery("SELECT active_release_id FROM gw_catalog_runtime_state").
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(uint64(7)))
	mock.ExpectQuery("SELECT active_release_id FROM gw_catalog_runtime_state").
		WillReturnRows(sqlmock.NewRows([]string{"active_release_id"}).AddRow(uint64(7)))
	mock.ExpectQuery("SELECT status FROM gw_catalog_releases").WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("draft"))

	_, handled, err := planUnifiedVideoGeneration(context.Background(), &video.CreateTaskRequest{Model: "seedance-2.0"})
	if !handled || !errors.Is(err, gatewayruntime.ErrNotReady) {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUnifiedVideoAssetsUsesOwnedActiveMedia(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previous })
	mock.ExpectQuery("SELECT COALESCE\\(NULLIF\\(storage_locator,''\\),object_key\\),state,retention_until,content_type FROM gw_media_assets").
		WithArgs(uint64(19), uint(41), uint(7)).
		WillReturnRows(sqlmock.NewRows([]string{"locator", "state", "retention_until", "content_type"}).AddRow("https://1.1.1.1/input.png", "active", nil, "image/png"))
	req := &video.CreateTaskRequest{UserID: 41, TokenID: 7, Content: []video.ContentItem{{AssetID: "19", Type: "image_url"}}}

	assetIDs, err := resolveUnifiedVideoAssets(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(assetIDs) != 1 || assetIDs[0] != 19 {
		t.Fatalf("asset IDs = %v", assetIDs)
	}
	if req.Content[0].AssetID != "" || req.Content[0].URL != "https://1.1.1.1/input.png" {
		t.Fatalf("resolved content = %+v", req.Content[0])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUnifiedVideoAssetsRejectsExpiredMedia(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previous })
	mock.ExpectQuery("SELECT COALESCE\\(NULLIF\\(storage_locator,''\\),object_key\\),state,retention_until,content_type FROM gw_media_assets").
		WithArgs(uint64(19), uint(41), uint(7)).
		WillReturnRows(sqlmock.NewRows([]string{"locator", "state", "retention_until", "content_type"}).AddRow("https://1.1.1.1/input.png", "active", time.Now().UTC().Add(-time.Minute), "image/png"))

	_, err = resolveUnifiedVideoAssets(context.Background(), &video.CreateTaskRequest{UserID: 41, TokenID: 7, Content: []video.ContentItem{{AssetID: "19"}}})
	if !errors.Is(err, video.ErrAssetNotReady) {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUnifiedVideoAssetsRejectsMismatchedMediaType(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previous })
	mock.ExpectQuery("SELECT COALESCE\\(NULLIF\\(storage_locator,''\\),object_key\\),state,retention_until,content_type FROM gw_media_assets").
		WithArgs(uint64(19), uint(41), uint(7)).
		WillReturnRows(sqlmock.NewRows([]string{"locator", "state", "retention_until", "content_type"}).AddRow("https://1.1.1.1/input.png", "active", nil, "image/png"))

	_, err = resolveUnifiedVideoAssets(context.Background(), &video.CreateTaskRequest{
		UserID: 41, TokenID: 7, Content: []video.ContentItem{{AssetID: "19", Type: "video_url"}},
	})
	if !errors.Is(err, video.ErrInvalidAsset) {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUnifiedVideoAssetsRejectsProviderObjectReference(t *testing.T) {
	_, err := resolveUnifiedVideoAssets(context.Background(), &video.CreateTaskRequest{
		Content: []video.ContentItem{{StorageObjectID: "object-1"}},
	})
	if !errors.Is(err, video.ErrInvalidAsset) {
		t.Fatalf("err=%v, want invalid asset", err)
	}
	if got := video.AssetErrorDetail(err); got != "provider object references are not supported for video assets" {
		t.Fatalf("detail=%q", got)
	}
}

func TestClassifyVideoCreateErrorTreatsMissingRouteAsUnavailable(t *testing.T) {
	for _, err := range []error{gatewayruntime.ErrNotReady, routing.ErrNoRoute, routing.ErrNoCompatibleTransport, routing.ErrCapabilityUnavailable} {
		status, errorType, errorCode := classifyVideoCreateError(errors.Join(err, errors.New("route unavailable")))
		if status != http.StatusServiceUnavailable || errorType != "service_unavailable_error" || errorCode != "video_channel_unavailable" {
			t.Fatalf("classification for %v = %d/%s/%s", err, status, errorType, errorCode)
		}
	}
}

func TestClassifyVideoCreateErrorTreatsIdempotencyReuseAsConflict(t *testing.T) {
	status, errorType, errorCode := classifyVideoCreateError(repository.ErrIdempotencyConflict)
	if status != http.StatusConflict || errorType != "invalid_request_error" || errorCode != "idempotency_conflict" {
		t.Fatalf("classification = %d/%s/%s", status, errorType, errorCode)
	}
}

func TestWriteVideoCreateErrorReportsInsufficientQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)

	writeVideoCreateError(ctx, http.StatusBadRequest, fmt.Errorf("submit gateway call: reserve billing: %w", repository.ErrInsufficient))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body resp.Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != perrors.ErrInsufficientQuota.Code || body.Message != perrors.ErrInsufficientQuota.Message {
		t.Fatalf("response = %#v", body)
	}
}

func TestWriteVideoCreateErrorPreservesSafeValidationReason(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	err := fmt.Errorf("%w: duration must be one of 5, 10, 15", repository.ErrInvalidInput)

	writeVideoCreateError(ctx, http.StatusBadRequest, err)

	var body resp.Response
	if decodeErr := json.Unmarshal(response.Body.Bytes(), &body); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if body.Code != perrors.ErrInvalidParams.Code || body.Message != err.Error() {
		t.Fatalf("response = %#v", body)
	}
}

func TestUnifiedVideoPublicStatus(t *testing.T) {
	tests := []struct {
		name, call, task, want string
	}{
		{name: "new queued projection", call: "in_progress", task: "queued", want: "queued"},
		{name: "new submitted projection", call: "in_progress", task: "submitted", want: "submitted"},
		{name: "new tracking projection", call: "in_progress", task: "tracking", want: "tracking"},
		{name: "submission unknown", call: "in_progress", task: "submission_unknown", want: "submission_unknown"},
		{name: "manual review", call: "in_progress", task: "manual_review", want: "submission_unknown"},
		{name: "internal accepted", call: "in_progress", task: "accepted", want: "submitted"},
		{name: "call terminal wins", call: "completed", task: "accepted", want: "completed"},
		{name: "failed call wins", call: "failed", task: "tracking", want: "failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := unifiedVideoPublicStatus(test.call, test.task); got != test.want {
				t.Fatalf("status %s/%s = %s, want %s", test.call, test.task, got, test.want)
			}
		})
	}
}

func TestVideoListPaginationValidation(t *testing.T) {
	for _, target := range []string{
		"/v1/videos/generations?page=0",
		"/v1/videos/generations?page=abc",
		"/v1/videos/generations?page_size=0",
		"/v1/videos/generations?page_size=101",
		"/v1/videos/generations?page=9223372036854775807&page_size=100",
	} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
		if _, _, ok := videoListPagination(ctx); ok || response.Code != http.StatusBadRequest {
			t.Fatalf("target=%s ok=%t status=%d body=%s", target, ok, response.Code, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/generations?page=3&page_size=25", nil)
	page, size, ok := videoListPagination(ctx)
	if !ok || page != 3 || size != 25 {
		t.Fatalf("page=%d size=%d ok=%t", page, size, ok)
	}
}

func TestListUnifiedVideoGenerationsFiltersAndPaginatesPublicProjection(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previous })

	mock.ExpectQuery("SELECT COUNT\\(\\*\\).*gw_api_resources.*CASE").
		WithArgs(uint(41), uint(7), "submitted", "seedance-2.0").
		WillReturnRows(sqlmock.NewRows([]string{"total"}).AddRow(3))
	createdAt := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT c.id,r.public_id,c.status,v.status,v.progress,v.specification_summary,c.created_at,c.updated_at.*CASE").
		WithArgs(uint(41), uint(7), "submitted", "seedance-2.0", 2, 2).
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "public_id", "call_status", "task_status", "progress", "specification", "created_at", "updated_at"}).
			AddRow(uint64(10), "video-1", "in_progress", "submitted", 0, []byte(`{"model":"seedance-2.0","service_tier":"standard","task_mode":"references"}`), createdAt, createdAt.Add(time.Second)))

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/generations", nil)
	handled, err := listUnifiedVideoGenerations(ctx, 41, 7, 2, 2, "submitted", "seedance-2.0")
	if err != nil || !handled {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	var envelope struct {
		Data struct {
			Items []struct {
				ID          string `json:"id"`
				Status      string `json:"status"`
				Model       string `json:"model"`
				ServiceTier string `json:"service_tier"`
				TaskMode    string `json:"task_mode"`
			}
			Total    int64 `json:"total"`
			Page     int   `json:"page"`
			PageSize int   `json:"page_size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].ID != "video-1" || envelope.Data.Items[0].Status != "submitted" || envelope.Data.Items[0].Model != "seedance-2.0" || envelope.Data.Items[0].ServiceTier != "standard" || envelope.Data.Items[0].TaskMode != "references" || envelope.Data.Total != 3 || envelope.Data.Page != 2 || envelope.Data.PageSize != 2 {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetUnifiedVideoGenerationReadsOwnedRequestPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previous })
	kek, hmacKey := configureUnifiedPayloadKeys(t)
	createdAt := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT c.id,r.user_id,r.token_id,c.user_id,c.token_id,c.status").
		WithArgs("video-owned").
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "resource_user_id", "resource_token_id", "call_user_id", "call_token_id", "status", "created_at", "updated_at", "request_payload_id", "result_payload_id", "task_status", "progress", "specification_summary"}).
			AddRow(uint64(10), uint64(41), uint64(7), uint64(41), uint64(7), "in_progress", createdAt, createdAt.Add(time.Second), uint64(21), nil, "submitted", 0, []byte(`{"model":"seedance-2.0","resolution":"1080p","task_mode":"references"}`)))
	expectUnifiedPayload(t, mock, 21, 31, 10, "request", []byte(`{"model":"seedance-2.0","prompt":"ocean","task_mode":"references","resolution":"1080p","ratio":"16:9","duration":5}`), kek, hmacKey)

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Params = gin.Params{{Key: "id", Value: "video-owned"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/generations/video-owned", nil)
	handled, err := getUnifiedVideoGeneration(ctx, 41, 7)
	if err != nil || !handled {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	var envelope struct {
		Data struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			Prompt     string `json:"prompt"`
			TaskMode   string `json:"task_mode"`
			Resolution string `json:"resolution"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ID != "video-owned" || envelope.Data.Status != "submitted" || envelope.Data.Prompt != "ocean" || envelope.Data.TaskMode != "references" || envelope.Data.Resolution != "1080p" {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReadUnifiedVideoResultReturnsOwnedManagedCopy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previous })
	kek, hmacKey := configureUnifiedPayloadKeys(t)
	resultPayload, err := json.Marshal(delivery.VideoResult{SchemaVersion: 1, Duration: "5", VideoDeliveryID: 17})
	if err != nil {
		t.Fatal(err)
	}
	expectUnifiedPayload(t, mock, 22, 32, 10, "result", resultPayload, kek, hmacKey)
	mock.ExpectQuery("SELECT d.id,d.call_id,d.attempt_id,d.user_id,d.token_id").
		WithArgs(uint64(17), uint64(10), uint64(41), uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "attempt_id", "user_id", "token_id", "delivery_mode", "source_kind", "state", "content_type", "reason_code", "expires_at", "source_url_policy", "source_seq", "encrypted_url_blob_id", "media_asset_ref_id", "media_asset_id", "object_key"}).
			AddRow(uint64(17), uint64(10), uint64(11), uint64(41), uint64(7), "managed_copy", "remote_url", "ready", "video/mp4", "managed_copy_available", nil, "fixed", nil, nil, uint64(20), uint64(21), "https://storage.example/video.mp4"))
	mock.ExpectQuery("SELECT c.user_id,c.token_id,COALESCE\\(c.xfs_api_key,''\\)").
		WithArgs(uint64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "token_id", "xfs_api_key"}).
			AddRow(uint64(41), uint64(7), testManagedResultAPIKey))
	previousPresign := presignManagedResult
	presignManagedResult = func(_ context.Context, apiKey, locator string) (string, error) {
		if apiKey != testManagedResultAPIKey || locator != "https://storage.example/video.mp4" {
			t.Fatalf("presign key=%q locator=%q", apiKey, locator)
		}
		return locator, nil
	}
	t.Cleanup(func() { presignManagedResult = previousPresign })

	result, err := readUnifiedVideoResult(context.Background(), 22, 10, 41, 7)
	if err != nil {
		t.Fatal(err)
	}
	if result["video_delivery_id"] != "17" || result["video_url"] != "https://storage.example/video.mp4" || result["duration"] != "5" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestReadUnifiedVideoResultExposesDeliveryFailureReason(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previous })
	kek, hmacKey := configureUnifiedPayloadKeys(t)
	resultPayload, err := json.Marshal(delivery.VideoResult{SchemaVersion: 1, VideoDeliveryID: 17})
	if err != nil {
		t.Fatal(err)
	}
	expectUnifiedPayload(t, mock, 22, 32, 10, "result", resultPayload, kek, hmacKey)
	mock.ExpectQuery("SELECT d.id,d.call_id,d.attempt_id,d.user_id,d.token_id").
		WithArgs(uint64(17), uint64(10), uint64(41), uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "call_id", "attempt_id", "user_id", "token_id", "delivery_mode", "source_kind", "state", "content_type", "reason_code", "expires_at", "source_url_policy", "source_seq", "encrypted_url_blob_id", "media_asset_ref_id", "media_asset_id", "object_key"}).
			AddRow(uint64(17), uint64(10), uint64(11), uint64(41), uint64(7), "managed_copy", "remote_url", "delivery_failed", nil, "managed_copy_size_exceeded", nil, "fixed", nil, nil, nil, nil, nil))

	result, err := readUnifiedVideoResult(context.Background(), 22, 10, 41, 7)
	if err != nil {
		t.Fatal(err)
	}
	if result["delivery_status"] != "unavailable" || result["delivery_error_code"] != "managed_copy_size_exceeded" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

const testManagedResultAPIKey = "xfs_0123456789abcdef0123456789abcdef"

func configureUnifiedPayloadKeys(t *testing.T) ([]byte, []byte) {
	t.Helper()
	kek := []byte("01234567890123456789012345678901")
	hmacKey := []byte("abcdefghijklmnopqrstuvwxyzABCDEF")
	t.Setenv("PRISM_GATEWAY_PAYLOAD_KEK_B64", base64.StdEncoding.EncodeToString(kek))
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", base64.StdEncoding.EncodeToString(hmacKey))
	return kek, hmacKey
}

func expectUnifiedPayload(t *testing.T, mock sqlmock.Sqlmock, payloadID, blobID, callID uint64, kind string, plaintext, kek, hmacKey []byte) {
	t.Helper()
	owner := []byte(fmt.Sprintf("call:%d:%s", callID, kind))
	aad, err := security.CanonicalAAD(blobID, "gateway-payload", 1, owner)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := security.Seal(plaintext, aad, kek, 1)
	if err != nil {
		t.Fatal(err)
	}
	digest := security.HMACSHA256(hmacKey, plaintext)
	mock.ExpectQuery("SELECT encrypted_blob_id FROM gw_api_call_payloads").
		WithArgs(payloadID, callID, kind).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_blob_id"}).AddRow(blobID))
	mock.ExpectQuery("SELECT b.keyring_id,b.purpose,b.schema_version").
		WithArgs(blobID).
		WillReturnRows(sqlmock.NewRows([]string{"keyring_id", "purpose", "schema_version", "aad_hash", "nonce", "ciphertext", "content_hmac", "kek_version", "wrap_nonce", "wrapped_dek"}).
			AddRow(uint64(1), "gateway-payload", uint32(1), "", sealed.Nonce, sealed.Ciphertext, hex.EncodeToString(digest[:]), uint32(1), sealed.WrapNonce, sealed.WrappedDEK))
}

func TestUnifiedVideoQueueStatus(t *testing.T) {
	for _, test := range []struct {
		call, async string
		want        string
		queued      bool
	}{
		{call: "in_progress", async: "submitting", want: "queued", queued: true},
		{call: "in_progress", async: "accepted", want: "running"},
		{call: "in_progress", async: "running", want: "running"},
		{call: "received", async: "", want: "queued", queued: true},
	} {
		got, queued := unifiedVideoQueueStatus(test.call, test.async)
		if got != test.want || queued != test.queued {
			t.Fatalf("status %s/%s = %s/%t, want %s/%t", test.call, test.async, got, queued, test.want, test.queued)
		}
	}
}

func TestListUnifiedVideoQueueUsesPublishedCatalogProjection(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	defer func() { unifiedVideoStore = previous }()

	mock.ExpectQuery("SELECT r.public_id,c.status").
		WithArgs(uint(41), uint(7)).
		WillReturnRows(sqlmock.NewRows([]string{"public_id", "status", "async_state", "async_id", "next_action_at", "specification_summary"}).
			AddRow("video-unified", "in_progress", "submitting", int64(12), time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), []byte(`{"service_tier":"standard"}`)))

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/queue", nil)
	handled, err := listUnifiedVideoQueue(ctx, 41, 7)
	if err != nil || !handled {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			ActiveCount int `json:"active_count"`
			QueuedCount int `json:"queued_count"`
			Items       []struct {
				ID          string `json:"id"`
				Status      string `json:"status"`
				ServiceTier string `json:"service_tier"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ActiveCount != 1 || envelope.Data.QueuedCount != 1 || len(envelope.Data.Items) != 1 || envelope.Data.Items[0].ID != "video-unified" || envelope.Data.Items[0].Status != "queued" || envelope.Data.Items[0].ServiceTier != "standard" {
		t.Fatalf("unexpected queue response: %s", response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetUnifiedVideoGenerationDoesNotFallThroughForForeignResource(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	defer func() { unifiedVideoStore = previous }()

	mock.ExpectQuery("SELECT c.id,r.user_id,r.token_id,c.user_id,c.token_id,c.status").
		WithArgs("video-unified").
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "resource_user_id", "resource_token_id", "call_user_id", "call_token_id", "status", "created_at", "updated_at", "request_payload_id", "result_payload_id", "task_status", "progress", "specification_summary"}).
			AddRow(uint64(10), uint64(99), uint64(17), uint64(99), uint64(17), "in_progress", time.Now(), time.Now(), nil, nil, "running", 25, []byte(`{"model":"seedance-2.0"}`)))

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Params = gin.Params{{Key: "id", Value: "video-unified"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/generations/video-unified", nil)
	handled, err := getUnifiedVideoGeneration(ctx, 41, 7)
	if err != nil || !handled {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetUnifiedVideoQueueDoesNotFallThroughForForeignResource(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previous := unifiedVideoStore
	unifiedVideoStore = store
	defer func() { unifiedVideoStore = previous }()

	mock.ExpectQuery("SELECT c.id,r.user_id,r.token_id,c.user_id,c.token_id,c.status,x.id,x.state,x.next_action_at").
		WithArgs("video-unified").
		WillReturnRows(sqlmock.NewRows([]string{"call_id", "resource_user_id", "resource_token_id", "call_user_id", "call_token_id", "status", "async_id", "async_state", "next_action_at"}).
			AddRow(uint64(10), uint64(99), uint64(17), uint64(99), uint64(17), "in_progress", uint64(12), "accepted", nil))

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Params = gin.Params{{Key: "id", Value: "video-unified"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/generations/video-unified/queue", nil)
	handled, err := getUnifiedVideoGenerationQueue(ctx, 41, 7)
	if err != nil || !handled {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUnifiedVideoMissingUsesUnifiedAuthority(t *testing.T) {
	handled, err := unifiedVideoMissing(context.Background())
	if !handled || !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
}

func TestUnifiedVideoTerminalStateIsDetectedBeforeCancellationPolicy(t *testing.T) {
	if !isUnifiedVideoTerminal("completed", "accepted") || !isUnifiedVideoTerminal("in_progress", "succeeded") {
		t.Fatal("terminal unified video states were not recognized")
	}
	if isUnifiedVideoTerminal("in_progress", "running") {
		t.Fatal("active unified video state was marked terminal")
	}
}
