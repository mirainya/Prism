package service

import (
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/model"
)

func TestUpdateTaskFailSanitizesStoredUpstreamError(t *testing.T) {
	db := setupTestDB(t)
	task := &model.Task{
		TaskNo: GenerateTaskNo(), Status: model.TaskStatusProcessing,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	message := "GET https://example.test/result?X-Amz-Signature=secret Authorization: Bearer abc api_key=xyz"
	committed, err := NewTaskService().UpdateTaskFail(task.ID, message)
	if err != nil || !committed {
		t.Fatalf("update task failure: committed=%v err=%v", committed, err)
	}

	var stored model.Task
	if err := db.First(&stored, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.ErrorMessage, "secret") || strings.Contains(stored.ErrorMessage, "abc") ||
		strings.Contains(stored.ErrorMessage, "xyz") || !strings.Contains(stored.ErrorMessage, "[REDACTED]") {
		t.Fatalf("stored task error was not sanitized: %q", stored.ErrorMessage)
	}
}
