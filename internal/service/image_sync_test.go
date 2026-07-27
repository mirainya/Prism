package service

import (
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
)

func TestBuildImageResultURLsArray(t *testing.T) {
	task := &model.Task{
		TaskNo: "t-1",
		Status: model.TaskStatusSuccess,
		Result: datatypes.JSON(`{"url":"https://a.png","urls":["https://a.png","https://b.png"],"revised_prompt":"a cat"}`),
	}

	res := buildImageResult(task)

	if !res.Done || !res.Success {
		t.Fatalf("Done/Success = %v/%v, want true/true", res.Done, res.Success)
	}
	if len(res.URLs) != 2 || res.URLs[0] != "https://a.png" || res.URLs[1] != "https://b.png" {
		t.Errorf("URLs = %#v, want [a b]", res.URLs)
	}
	if res.RevisedPrompt != "a cat" {
		t.Errorf("RevisedPrompt = %q", res.RevisedPrompt)
	}
}

func TestBuildImageResultSingleURLFallback(t *testing.T) {
	// 只有 url 无 urls 数组时,回退到单 url
	task := &model.Task{
		TaskNo: "t-2",
		Status: model.TaskStatusSuccess,
		Result: datatypes.JSON(`{"url":"https://only.png"}`),
	}

	res := buildImageResult(task)

	if len(res.URLs) != 1 || res.URLs[0] != "https://only.png" {
		t.Errorf("URLs = %#v, want [only]", res.URLs)
	}
}

func TestBuildImageResultEmptyResult(t *testing.T) {
	// Result 为空不应 panic,URLs 为空
	task := &model.Task{TaskNo: "t-3", Status: model.TaskStatusSuccess}

	res := buildImageResult(task)

	if !res.Success || len(res.URLs) != 0 {
		t.Errorf("empty result: Success=%v URLs=%#v", res.Success, res.URLs)
	}
}

func TestBuildFailedImageResultExtractsUpstreamStatus(t *testing.T) {
	task := &model.Task{
		TaskNo:       "t-failed",
		Status:       model.TaskStatusFailed,
		ErrorMessage: `API Error: openai returned 451: {"error":{"message":"unsafe image"}}`,
	}

	res := buildFailedImageResult(task, task.TaskNo, string(task.Status))

	if !res.Done || res.Success || res.Status != string(model.TaskStatusFailed) {
		t.Fatalf("result = %#v", res)
	}
	if res.HTTPStatus != 451 {
		t.Fatalf("HTTPStatus = %d, want 451", res.HTTPStatus)
	}
	if res.Error != task.ErrorMessage {
		t.Fatalf("Error = %q", res.Error)
	}
}

func TestBuildFailedImageResultIgnoresNonErrorStatus(t *testing.T) {
	task := &model.Task{
		TaskNo:       "t-failed-no-http",
		Status:       model.TaskStatusFailed,
		ErrorMessage: "render failed without an HTTP status",
	}

	res := buildFailedImageResult(task, task.TaskNo, string(task.Status))

	if res.HTTPStatus != 0 {
		t.Fatalf("HTTPStatus = %d, want 0", res.HTTPStatus)
	}
}
