package migrate

import "testing"

func TestDeepAuditReadyForCleanupRequiresCompleteEvidence(t *testing.T) {
	report := DeepAuditReport{MigrationRunCount: 1, SucceededMigrationRuns: 1}
	if !report.ReadyForCleanup() {
		t.Fatal("complete audit should be ready for cleanup")
	}
	report.OpenMigrationIssues = 1
	if report.ReadyForCleanup() {
		t.Fatal("open migration issues must block cleanup")
	}
	report.OpenMigrationIssues = 0
	report.LegacyTablesPresent = []string{"gw_abilities"}
	if !report.ReadyForCleanup() {
		t.Fatal("mapped legacy tables may remain until the cleanup step")
	}
	report.UnmappedLegacyKeys = 1
	if report.ReadyForCleanup() {
		t.Fatal("unmapped legacy rows must block cleanup")
	}
}
