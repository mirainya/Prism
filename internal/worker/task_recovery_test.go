package worker

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestRecoverPendingTaskSubmissionsUsesTaskRowsAsIntent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	future := time.Now().Add(time.Hour)
	tasks := []model.Task{
		{TaskNo: "pending", Status: model.TaskStatusPending},
		{TaskNo: "checkpoint", Status: model.TaskStatusProcessing, SubmitCheckpoint: datatypes.JSON(`{"state":"succeeded"}`)},
		{TaskNo: "already-running", Status: model.TaskStatusProcessing},
		{TaskNo: "leased", Status: model.TaskStatusPending, WorkerLeaseExpiresAt: &future},
		{TaskNo: "done", Status: model.TaskStatusSuccess},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}

	previousRecover := recoverTaskSubmit
	var recovered []uint
	recoverTaskSubmit = func(taskID uint) error {
		recovered = append(recovered, taskID)
		return nil
	}
	t.Cleanup(func() { recoverTaskSubmit = previousRecover })

	count, err := RecoverPendingTaskSubmissions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(recovered) != 2 || recovered[0] != tasks[0].ID || recovered[1] != tasks[1].ID {
		t.Fatalf("recovered count=%d ids=%v", count, recovered)
	}
}

func TestRecoverPendingTaskSubmissionsPropagatesQueueFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	task := model.Task{TaskNo: "pending-error", Status: model.TaskStatusPending}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	previousRecover := recoverTaskSubmit
	recoverTaskSubmit = func(uint) error { return fmt.Errorf("redis unavailable") }
	t.Cleanup(func() { recoverTaskSubmit = previousRecover })

	if _, err := RecoverPendingTaskSubmissions(context.Background()); err == nil {
		t.Fatal("expected queue recovery error")
	}
}
