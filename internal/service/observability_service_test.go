package service

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestObservabilityListsKeepSnapshotStableAcrossPages(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *gorm.DB, *ObservabilityService)
	}{
		{name: "access logs", run: testAccessLogSnapshotPagination},
		{name: "audit events", run: testAuditEventSnapshotPagination},
		{name: "balance entries", run: testBalanceEntrySnapshotPagination},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupTestDB(t)
			if err := db.AutoMigrate(&model.APIAccessLog{}, &model.AuditEvent{}); err != nil {
				t.Fatalf("migrate observability tables: %v", err)
			}
			test.run(t, db, NewObservabilityService())
		})
	}
}

func testAccessLogSnapshotPagination(t *testing.T, db *gorm.DB, service *ObservabilityService) {
	t.Helper()
	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	logs := make([]model.APIAccessLog, 3)
	for i := range logs {
		logs[i] = model.APIAccessLog{
			RequestID: fmt.Sprintf("access-%d", i+1), UserID: 10, ActorType: "user",
			Method: http.MethodGet, Path: "/api/test", Route: "/api/test",
			StatusCode: http.StatusOK, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := db.Create(&logs[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	first, err := service.ListAPIAccessLogs(&ListAPIAccessLogsRequest{
		ObservabilityListFilter: ObservabilityListFilter{Page: 1, PageSize: 1},
	}, ObservabilityScope{ActorUserID: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertObservabilitySnapshotPage(t, first.SnapshotID, first.Total, accessLogIDs(first.Items), logs[2].ID, logs[2].ID, 3)

	inserted := model.APIAccessLog{
		RequestID: "access-4", UserID: 10, ActorType: "user", Method: http.MethodGet,
		Path: "/api/test", Route: "/api/test", StatusCode: http.StatusOK, CreatedAt: base.Add(3 * time.Second),
	}
	if err := db.Create(&inserted).Error; err != nil {
		t.Fatal(err)
	}
	snapshotID := first.SnapshotID
	second, err := service.ListAPIAccessLogs(&ListAPIAccessLogsRequest{
		ObservabilityListFilter: ObservabilityListFilter{Page: 2, PageSize: 1, SnapshotID: &snapshotID},
	}, ObservabilityScope{ActorUserID: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertObservabilitySnapshotPage(t, second.SnapshotID, second.Total, accessLogIDs(second.Items), first.SnapshotID, logs[1].ID, 3)

	refreshed, err := service.ListAPIAccessLogs(&ListAPIAccessLogsRequest{
		ObservabilityListFilter: ObservabilityListFilter{Page: 1, PageSize: 1},
	}, ObservabilityScope{ActorUserID: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertObservabilitySnapshotPage(t, refreshed.SnapshotID, refreshed.Total, accessLogIDs(refreshed.Items), inserted.ID, inserted.ID, 4)
}

func testAuditEventSnapshotPagination(t *testing.T, db *gorm.DB, service *ObservabilityService) {
	t.Helper()
	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	events := make([]model.AuditEvent, 3)
	for i := range events {
		events[i] = model.AuditEvent{
			RequestID: fmt.Sprintf("audit-%d", i+1), ActorType: "user", ActorUserID: 10,
			Action: "POST /api/test", ResourceType: "test", Outcome: "success",
			HTTPStatus: http.StatusOK, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := db.Create(&events[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	first, err := service.ListAuditEvents(&ListAuditEventsRequest{
		ObservabilityListFilter: ObservabilityListFilter{Page: 1, PageSize: 1},
	}, ObservabilityScope{ActorUserID: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertObservabilitySnapshotPage(t, first.SnapshotID, first.Total, auditEventIDs(first.Items), events[2].ID, events[2].ID, 3)

	inserted := model.AuditEvent{
		RequestID: "audit-4", ActorType: "user", ActorUserID: 10, Action: "POST /api/test",
		ResourceType: "test", Outcome: "success", HTTPStatus: http.StatusOK, CreatedAt: base.Add(3 * time.Second),
	}
	if err := db.Create(&inserted).Error; err != nil {
		t.Fatal(err)
	}
	snapshotID := first.SnapshotID
	second, err := service.ListAuditEvents(&ListAuditEventsRequest{
		ObservabilityListFilter: ObservabilityListFilter{Page: 2, PageSize: 1, SnapshotID: &snapshotID},
	}, ObservabilityScope{ActorUserID: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertObservabilitySnapshotPage(t, second.SnapshotID, second.Total, auditEventIDs(second.Items), first.SnapshotID, events[1].ID, 3)

	refreshed, err := service.ListAuditEvents(&ListAuditEventsRequest{
		ObservabilityListFilter: ObservabilityListFilter{Page: 1, PageSize: 1},
	}, ObservabilityScope{ActorUserID: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertObservabilitySnapshotPage(t, refreshed.SnapshotID, refreshed.Total, auditEventIDs(refreshed.Items), inserted.ID, inserted.ID, 4)
}

func testBalanceEntrySnapshotPagination(t *testing.T, db *gorm.DB, service *ObservabilityService) {
	t.Helper()
	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	entries := make([]model.BalanceEntry, 3)
	for i := range entries {
		entries[i] = model.BalanceEntry{
			EntryKey: fmt.Sprintf("entry-%d", i+1), SourceKey: fmt.Sprintf("source-%d", i+1),
			AccountType: model.BalanceAccountUser, AccountID: 10, UserID: 10,
			Direction: model.BalanceDirectionCredit, Category: "recharge",
			Amount: decimal.NewFromInt(1), BalanceBefore: decimal.NewFromInt(int64(i)),
			BalanceAfter: decimal.NewFromInt(int64(i + 1)), CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := db.Create(&entries[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	first, err := service.ListBalanceEntries(&ListBalanceEntriesRequest{
		ObservabilityListFilter: ObservabilityListFilter{Page: 1, PageSize: 1},
	}, ObservabilityScope{ActorUserID: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertObservabilitySnapshotPage(t, first.SnapshotID, first.Total, balanceEntryIDs(first.Items), entries[2].ID, entries[2].ID, 3)

	inserted := model.BalanceEntry{
		EntryKey: "entry-4", SourceKey: "source-4", AccountType: model.BalanceAccountUser,
		AccountID: 10, UserID: 10, Direction: model.BalanceDirectionCredit, Category: "recharge",
		Amount: decimal.NewFromInt(1), BalanceBefore: decimal.NewFromInt(3),
		BalanceAfter: decimal.NewFromInt(4), CreatedAt: base.Add(3 * time.Second),
	}
	if err := db.Create(&inserted).Error; err != nil {
		t.Fatal(err)
	}
	snapshotID := first.SnapshotID
	second, err := service.ListBalanceEntries(&ListBalanceEntriesRequest{
		ObservabilityListFilter: ObservabilityListFilter{Page: 2, PageSize: 1, SnapshotID: &snapshotID},
	}, ObservabilityScope{ActorUserID: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertObservabilitySnapshotPage(t, second.SnapshotID, second.Total, balanceEntryIDs(second.Items), first.SnapshotID, entries[1].ID, 3)

	refreshed, err := service.ListBalanceEntries(&ListBalanceEntriesRequest{
		ObservabilityListFilter: ObservabilityListFilter{Page: 1, PageSize: 1},
	}, ObservabilityScope{ActorUserID: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertObservabilitySnapshotPage(t, refreshed.SnapshotID, refreshed.Total, balanceEntryIDs(refreshed.Items), inserted.ID, inserted.ID, 4)
}

func assertObservabilitySnapshotPage(
	t *testing.T,
	snapshotID uint64,
	total int64,
	ids []uint64,
	wantSnapshotID uint64,
	wantID uint64,
	wantTotal int64,
) {
	t.Helper()
	if snapshotID != wantSnapshotID {
		t.Fatalf("snapshot=%d, want %d", snapshotID, wantSnapshotID)
	}
	if total != wantTotal || len(ids) != 1 || ids[0] != wantID {
		t.Fatalf("snapshot=%d total=%d ids=%v, want total=%d id=%d", snapshotID, total, ids, wantTotal, wantID)
	}
}

func accessLogIDs(items []model.APIAccessLog) []uint64 {
	ids := make([]uint64, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	return ids
}

func auditEventIDs(items []model.AuditEvent) []uint64 {
	ids := make([]uint64, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	return ids
}

func balanceEntryIDs(items []model.BalanceEntry) []uint64 {
	ids := make([]uint64, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	return ids
}
