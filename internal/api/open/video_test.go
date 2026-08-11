package open

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/video"
	"github.com/shopspring/decimal"
	"gorm.io/datatypes"
)

func TestBuildVideoCreateRequestDefaultsAudioAndAllowsReferenceOnly(t *testing.T) {
	req, err := buildVideoCreateRequest(map[string]any{
		"model": "seedance-2.0",
		"content": []any{map[string]any{
			"type": "image_url", "role": "reference_image", "url": "https://cdn.example/image.png",
		}},
	}, 3, 7, "seedance-2.0", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !req.Audio || req.Prompt != "" || len(req.Content) != 1 {
		t.Fatalf("request = %#v", req)
	}
}

func TestMapLegacyVideoStatus(t *testing.T) {
	tests := []struct {
		status model.TaskStatus
		want   string
	}{
		{status: model.TaskStatusPending, want: string(video.VideoTaskStatusQueued)},
		{status: model.TaskStatusProcessing, want: string(video.VideoTaskStatusTracking)},
		{status: model.TaskStatusFinalizing, want: string(video.VideoTaskStatusTracking)},
		{status: model.TaskStatusSuccess, want: string(video.VideoTaskStatusCompleted)},
		{status: model.TaskStatusFailed, want: string(video.VideoTaskStatusFailed)},
		{status: model.TaskStatusCancelled, want: string(video.VideoTaskStatusCancelled)},
	}
	for _, test := range tests {
		if got := mapLegacyVideoStatus(test.status); got != test.want {
			t.Errorf("status %q mapped to %q, want %q", test.status, got, test.want)
		}
	}
}

func TestClassifyVideoCreateErrorTreatsMissingRouteAsUnavailable(t *testing.T) {
	for _, err := range []error{video.ErrNoChannel, video.ErrNoKey, video.ErrEngineUnavailable} {
		status, errorType, errorCode := classifyVideoCreateError(errors.Join(err, errors.New("route unavailable")))
		if status != http.StatusServiceUnavailable || errorType != "service_unavailable_error" || errorCode != "video_channel_unavailable" {
			t.Fatalf("classification for %v = %d/%s/%s", err, status, errorType, errorCode)
		}
	}
}

func TestLegacyVideoTaskToResponse(t *testing.T) {
	createdAt := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	completedAt := createdAt.Add(time.Minute)
	task := &model.Task{
		BaseModel: model.BaseModel{CreatedAt: createdAt},
		TaskNo:    "legacy-video", ModelCode: "seedance-2.0", Status: model.TaskStatusSuccess,
		Progress: 100, Cost: decimal.NewFromFloat(1.25), CompletedAt: &completedAt,
		RequestParams: datatypes.JSON([]byte(`{"prompt":"ocean","resolution":"1080p","ratio":"16:9","task_mode":"references","duration":5,"generate_audio":true}`)),
		Result:        datatypes.JSON([]byte(`{"video_url":"https://cdn.example/video.mp4"}`)),
	}
	response := legacyVideoTaskToResponse(task)
	if response.ID != task.TaskNo || response.Status != string(video.VideoTaskStatusCompleted) || response.BillingStatus != "charged" {
		t.Fatalf("response identity/status = %#v", response)
	}
	if response.Prompt != "ocean" || response.Duration != 5 || !response.GenerateAudio || response.TaskMode != "references" {
		t.Fatalf("response request fields = %#v", response)
	}
	if response.Result == nil || response.CompletedAt == nil || *response.CompletedAt != completedAt.Format(time.RFC3339) {
		t.Fatalf("response result/completion = %#v", response)
	}
}
