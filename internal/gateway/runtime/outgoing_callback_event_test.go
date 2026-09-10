package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/delivery"
)

func TestBuildVideoCallbackPayloadIncludesPublicManagedResult(t *testing.T) {
	service, mock, key := callbackWorkerTestService(t)
	createdAt := time.Date(2026, 9, 8, 8, 0, 0, 0, time.UTC)
	resultBody, err := json.Marshal(delivery.VideoResult{SchemaVersion: 1, Duration: "5", VideoDeliveryID: 17})
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT c.public_id,c.status,c.created_at,c.updated_at,c.result_payload_id").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"public_id", "call_status", "created_at", "updated_at", "result_payload_id", "task_status", "progress", "specification_summary"}).
			AddRow("video-1", "completed", createdAt, createdAt.Add(time.Minute), uint64(21), "completed", uint8(100), []byte(`{"model":"seedance-2.0","resolution":"1080p"}`)))
	mock.ExpectQuery("SELECT encrypted_blob_id FROM gw_api_call_payloads").
		WithArgs(uint64(21), uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"encrypted_blob_id"}).AddRow(uint64(31)))
	expectCallbackWorkerBlob(t, mock, 31, "gateway-payload", []byte("call:7:result"), resultBody, key)
	mock.ExpectQuery("SELECT d.delivery_mode,d.state,src.source_seq,src.encrypted_url_blob_id").
		WithArgs(uint64(17), uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"delivery_mode", "state", "source_seq", "source_blob_id", "storage_locator"}).
			AddRow("managed_copy", "ready", nil, nil, "https://media.example/video.mp4"))
	mock.ExpectCommit()

	var encoded []byte
	err = service.Store.WithTx(context.Background(), func(tx *sql.Tx) error {
		var buildErr error
		encoded, buildErr = service.buildVideoCallbackPayload(context.Background(), tx, 7, "provider_terminal")
		return buildErr
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		Model      string `json:"model"`
		Resolution string `json:"resolution"`
		Result     struct {
			VideoURL string `json:"video_url"`
			Duration string `json:"duration"`
		} `json:"result"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ID != "video-1" || payload.Status != "completed" || payload.Model != "seedance-2.0" || payload.Resolution != "1080p" || payload.Result.VideoURL != "https://media.example/video.mp4" || payload.Result.Duration != "5" {
		t.Fatalf("payload=%s", encoded)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCallbackManagedCopyWithoutPublicLocatorIsUnavailable(t *testing.T) {
	service, mock, _ := callbackWorkerTestService(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT d.delivery_mode,d.state,src.source_seq,src.encrypted_url_blob_id").
		WithArgs(uint64(17), uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"delivery_mode", "state", "source_seq", "source_blob_id", "storage_locator"}).
			AddRow("managed_copy", "ready", nil, nil, nil))
	mock.ExpectCommit()
	var value string
	var available bool
	err := service.Store.WithTx(context.Background(), func(tx *sql.Tx) error {
		var readErr error
		value, available, readErr = service.callbackDeliveryURL(context.Background(), tx, 7, 17)
		return readErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if available || value != "" {
		t.Fatalf("value=%q available=%t", value, available)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
