package video

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func newVideoWorkerLeaseTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&VideoTask{}); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)
	return db
}

func createVideoWorkerLeaseTask(t *testing.T, db *gorm.DB, id string, status VideoTaskStatus) {
	t.Helper()
	task := &VideoTask{
		ID: id, UserID: 1, TokenID: 1, Model: "seedance-2.0",
		Status: status, TaskMode: "text", AdapterType: "seedance",
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
}

func TestVideoWorkerLeaseMutualExclusionAndRelease(t *testing.T) {
	db := newVideoWorkerLeaseTestDB(t)
	createVideoWorkerLeaseTask(t, db, "lease-mutual", VideoTaskStatusQueued)

	first, acquired, err := AcquireVideoWorkerLeaseWithOptions(
		context.Background(), "lease-mutual", VideoWorkerStageSubmit, time.Second, 100*time.Millisecond,
	)
	if err != nil || !acquired {
		t.Fatalf("first acquire: acquired=%t err=%v", acquired, err)
	}
	second, acquired, err := AcquireVideoWorkerLeaseWithOptions(
		context.Background(), "lease-mutual", VideoWorkerStageSubmit, time.Second, 100*time.Millisecond,
	)
	if err != nil || acquired || second != nil {
		t.Fatalf("competing acquire: lease=%v acquired=%t err=%v", second, acquired, err)
	}
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}

	second, acquired, err = AcquireVideoWorkerLeaseWithOptions(
		context.Background(), "lease-mutual", VideoWorkerStageSubmit, time.Second, 100*time.Millisecond,
	)
	if err != nil || !acquired {
		t.Fatalf("acquire after release: acquired=%t err=%v", acquired, err)
	}
	if err := second.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestVideoWorkerLeaseHeartbeatRenewsExpiry(t *testing.T) {
	db := newVideoWorkerLeaseTestDB(t)
	createVideoWorkerLeaseTask(t, db, "lease-heartbeat", VideoTaskStatusQueued)

	lease, acquired, err := AcquireVideoWorkerLeaseWithOptions(
		context.Background(), "lease-heartbeat", VideoWorkerStageSubmit, 400*time.Millisecond, 50*time.Millisecond,
	)
	if err != nil || !acquired {
		t.Fatalf("acquire: acquired=%t err=%v", acquired, err)
	}
	var before VideoTask
	if err := db.First(&before, "id = ?", "lease-heartbeat").Error; err != nil {
		t.Fatal(err)
	}
	time.Sleep(160 * time.Millisecond)
	var after VideoTask
	if err := db.First(&after, "id = ?", "lease-heartbeat").Error; err != nil {
		t.Fatal(err)
	}
	if before.WorkerLeaseUntil == nil || after.WorkerLeaseUntil == nil || !after.WorkerLeaseUntil.After(*before.WorkerLeaseUntil) {
		t.Fatalf("lease expiry was not renewed: before=%v after=%v", before.WorkerLeaseUntil, after.WorkerLeaseUntil)
	}
	if err := lease.Check(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestVideoWorkerLeaseDoesNotRenewAfterExpiry(t *testing.T) {
	db := newVideoWorkerLeaseTestDB(t)
	createVideoWorkerLeaseTask(t, db, "lease-expired", VideoTaskStatusQueued)

	lease, acquired, err := AcquireVideoWorkerLeaseWithOptions(
		context.Background(), "lease-expired", VideoWorkerStageSubmit, 500*time.Millisecond, 100*time.Millisecond,
	)
	if err != nil || !acquired {
		t.Fatalf("acquire: acquired=%t err=%v", acquired, err)
	}
	expiredAt := time.Now().Add(-time.Second)
	if err := db.Model(&VideoTask{}).Where("id = ?", "lease-expired").
		UpdateColumn("worker_lease_expires_at", expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	select {
	case <-lease.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("lease heartbeat did not detect expiry")
	}
	if !errors.Is(lease.Check(), ErrVideoWorkerLeaseLost) {
		t.Fatalf("check error = %v", lease.Check())
	}
	if err := lease.Stop(); err != nil && !errors.Is(err, ErrVideoWorkerLeaseLost) {
		t.Fatal(err)
	}
}

func TestVideoSubmitCheckpointSaveAndDecode(t *testing.T) {
	db := newVideoWorkerLeaseTestDB(t)
	createVideoWorkerLeaseTask(t, db, "lease-checkpoint", VideoTaskStatusQueued)
	lease, acquired, err := AcquireVideoWorkerLeaseWithOptions(
		context.Background(), "lease-checkpoint", VideoWorkerStageSubmit, time.Second, 100*time.Millisecond,
	)
	if err != nil || !acquired {
		t.Fatalf("acquire: acquired=%t err=%v", acquired, err)
	}
	checkpoint := &VideoSubmitCheckpoint{
		Version: 1, State: "in_flight", RequestID: "lease-checkpoint",
		AttemptID: 9, Attempts: 2, StartedAt: time.Now().UTC(),
	}
	if err := SaveVideoSubmitCheckpoint(context.Background(), "lease-checkpoint", lease.Owner(), checkpoint); err != nil {
		t.Fatal(err)
	}
	var task VideoTask
	if err := db.First(&task, "id = ?", "lease-checkpoint").Error; err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVideoSubmitCheckpoint(task.SubmitCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	if decoded == nil || decoded.RequestID != checkpoint.RequestID || decoded.AttemptID != 9 || decoded.Attempts != 2 {
		t.Fatalf("decoded checkpoint = %#v", decoded)
	}
	if err := lease.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestVideoWorkerLeaseRejectsIneligibleTaskAndStage(t *testing.T) {
	db := newVideoWorkerLeaseTestDB(t)
	createVideoWorkerLeaseTask(t, db, "lease-terminal", VideoTaskStatusCompleted)

	lease, acquired, err := AcquireVideoWorkerLeaseWithOptions(
		context.Background(), "lease-terminal", VideoWorkerStageSubmit, time.Second, 100*time.Millisecond,
	)
	if err != nil || acquired || lease != nil {
		t.Fatalf("terminal acquire: lease=%v acquired=%t err=%v", lease, acquired, err)
	}
	_, _, err = AcquireVideoWorkerLeaseWithOptions(
		context.Background(), "lease-terminal", "invalid", time.Second, 100*time.Millisecond,
	)
	if err == nil {
		t.Fatal("invalid stage should fail")
	}
	_, _, err = AcquireVideoWorkerLeaseWithOptions(
		context.Background(), "missing-task", VideoWorkerStageSubmit, time.Second, 100*time.Millisecond,
	)
	if !errors.Is(err, ErrVideoTaskNotFound) {
		t.Fatalf("missing task error = %v", err)
	}
}
