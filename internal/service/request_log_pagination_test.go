package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
)

func TestListRequestLogsKeepsSnapshotStableAcrossPages(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&model.ChannelRequestLog{}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		log := model.ChannelRequestLog{
			TaskNo:      fmt.Sprintf("task-%d", i),
			RequestType: model.RequestTypeChat,
			RequestAt:   now.Add(time.Duration(i) * time.Second),
		}
		if err := db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}

	service := NewRequestLogService()
	first, err := service.ListRequestLogs(&ListRequestLogsRequest{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotID == 0 || first.Total != 5 {
		t.Fatalf("first page snapshot=%d total=%d, want non-zero snapshot and total 5", first.SnapshotID, first.Total)
	}
	assertRequestLogIDs(t, first.Items, 5, 4)

	newLog := model.ChannelRequestLog{TaskNo: "task-6", RequestType: model.RequestTypeChat, RequestAt: now.Add(6 * time.Second)}
	if err := db.Create(&newLog).Error; err != nil {
		t.Fatal(err)
	}

	second, err := service.ListRequestLogs(&ListRequestLogsRequest{
		Page: 2, PageSize: 2, SnapshotID: first.SnapshotID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.SnapshotID != first.SnapshotID || second.Total != 5 {
		t.Fatalf("second page snapshot=%d total=%d, want snapshot=%d total=5", second.SnapshotID, second.Total, first.SnapshotID)
	}
	assertRequestLogIDs(t, second.Items, 3, 2)

	refreshed, err := service.ListRequestLogs(&ListRequestLogsRequest{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.SnapshotID != newLog.ID || refreshed.Total != 6 {
		t.Fatalf("refreshed snapshot=%d total=%d, want snapshot=%d total=6", refreshed.SnapshotID, refreshed.Total, newLog.ID)
	}
	assertRequestLogIDs(t, refreshed.Items, 6, 5)
}

func assertRequestLogIDs(t *testing.T, logs []model.ChannelRequestLog, want ...uint) {
	t.Helper()
	if len(logs) != len(want) {
		t.Fatalf("got %d logs, want %d", len(logs), len(want))
	}
	for i := range want {
		if logs[i].ID != want[i] {
			t.Fatalf("log[%d].ID=%d, want %d", i, logs[i].ID, want[i])
		}
	}
}
