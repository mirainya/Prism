package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/mirainya/Prism/internal/model"
)

type notifyRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f notifyRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSuccessfulCallbackRetriesWhenStatusPersistenceFails(t *testing.T) {
	db, task, _, _ := setupCapabilityWorkerTest(t, model.TaskStatusSuccess, model.ModeCallback)
	task.CallbackURL = "https://1.1.1.1/callback"
	if err := db.Model(task).Updates(map[string]any{
		"callback_url":    task.CallbackURL,
		"callback_status": model.CallbackStatusPending,
	}).Error; err != nil {
		t.Fatal(err)
	}

	previousClient := notifyClient
	notifyClient = &http.Client{Transport: notifyRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatalf("get sql db: %v", err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close sql db: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { notifyClient = previousClient })

	payload, err := json.Marshal(TaskNotifyPayload{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	job := asynq.NewTask(TypeTaskNotify, payload)
	err = HandleTaskNotify(context.Background(), job)
	if err == nil || !strings.Contains(err.Error(), "record successful callback delivery") {
		t.Fatalf("notify error = %v, want persistence failure", err)
	}
}

func TestSuccessfulCallbackIsNotDeliveredAgain(t *testing.T) {
	db, task, _, _ := setupCapabilityWorkerTest(t, model.TaskStatusSuccess, model.ModeCallback)
	if err := db.Model(task).Updates(map[string]any{
		"callback_url":    "https://1.1.1.1/callback",
		"callback_status": model.CallbackStatusSuccess,
	}).Error; err != nil {
		t.Fatal(err)
	}

	previousClient := notifyClient
	deliveries := 0
	notifyClient = &http.Client{Transport: notifyRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		deliveries++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	t.Cleanup(func() { notifyClient = previousClient })

	payload, err := json.Marshal(TaskNotifyPayload{TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := HandleTaskNotify(context.Background(), asynq.NewTask(TypeTaskNotify, payload)); err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 {
		t.Fatalf("callback deliveries = %d, want 0", deliveries)
	}
}
