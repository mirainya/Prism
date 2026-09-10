//go:build integration

package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/tokenauth"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func verifyLegacyImportInitializedRuntime(t *testing.T, db *gorm.DB) {
	t.Helper()
	channel := model.GwChannel{Name: "Import fixture", Protocol: "openai", BaseURL: "https://example.invalid", Status: 1}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	key := model.GwChannelKey{ChannelID: channel.ID, Name: "Import fixture", APIKey: "test-import-secret", Weight: 1, Status: 1}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	ability := model.GwAbility{ChannelID: channel.ID, KeyID: key.ID, ModelName: "qa-import-model", VendorModel: "qa-import-model", Status: 1}
	if err := db.Create(&ability).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	options := ImportOptions{KEK: bytes.Repeat([]byte{71}, 32), HMACKey: bytes.Repeat([]byte{72}, 32)}
	legacyReport, err := importLegacyGatewayV1(context.Background(), sqlDB, options)
	if err != nil || legacyReport.ReleaseID == 0 || legacyReport.Channels != 1 || legacyReport.Credentials != 1 {
		t.Fatalf("seed V1 catalog draft: report=%+v err=%v", legacyReport, err)
	}
	if _, err := sqlDB.Exec(`UPDATE gw_channel_transports
		SET transport_code=CASE protocol
			WHEN 'anthropic' THEN CONCAT('anthropic_messages-',channel_id)
			WHEN 'google' THEN CONCAT('google_generate_content-',channel_id)
			WHEN 'volcengine' THEN CONCAT('volcengine_responses_v3-',channel_id)
			ELSE CONCAT('openai_chat-',channel_id)
		END
		WHERE release_id=?`, legacyReport.ReleaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`UPDATE gw_offering_runtime_state
		SET reason_code='legacy_import_backfill',updated_at=?
		WHERE release_id=?`, time.Now().UTC(), legacyReport.ReleaseID); err != nil {
		t.Fatal(err)
	}
	report, err := ImportLegacyGateway(context.Background(), sqlDB, options)
	if err != nil || report.Channels != 2 || report.Credentials != 1 {
		t.Fatalf("import after initialized runtime: report=%+v err=%v issues=%v", report, err, loadRuntimeMigrationIssues(t, sqlDB, report.RunID))
	}
	if report.ReleaseID == legacyReport.ReleaseID {
		t.Fatalf("verified V1 draft was not replaced: release=%d", report.ReleaseID)
	}
	var oldReleaseCount, oldChannelCount int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM gw_catalog_releases WHERE id=?`, legacyReport.ReleaseID).Scan(&oldReleaseCount); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM gateway_channels WHERE channel_code LIKE 'legacy-channel-%'`).Scan(&oldChannelCount); err != nil {
		t.Fatal(err)
	}
	if oldReleaseCount != 0 || oldChannelCount != 0 {
		t.Fatalf("V1 target graph remains after replacement: releases=%d channels=%d", oldReleaseCount, oldChannelCount)
	}
	var runCount, mappingCount, revisionCount, proofCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM gw_migration_runs WHERE operation=? AND status='succeeded'`, catalogImportOperation).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*),COUNT(sr.id) FROM gw_migration_object_map m LEFT JOIN gw_migration_source_revisions sr ON sr.object_map_id=m.id WHERE m.run_id=(SELECT id FROM gw_migration_runs WHERE operation=? LIMIT 1)`, catalogImportOperation).Scan(&mappingCount, &revisionCount); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM gw_migration_mapping_proofs p JOIN gw_migration_runs r ON r.id=p.run_id WHERE r.operation=? AND r.status='succeeded'`, catalogImportOperation).Scan(&proofCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 || mappingCount < int(report.SourceRows)+1 || revisionCount != mappingCount || proofCount != mappingCount {
		t.Fatalf("legacy import audit evidence: runs=%d mappings=%d revisions=%d proofs=%d", runCount, mappingCount, revisionCount, proofCount)
	}
	var active sql.NullInt64
	var version uint64
	if err := sqlDB.QueryRow(`SELECT active_release_id,state_version FROM gw_catalog_runtime_state WHERE id=1`).Scan(&active, &version); err != nil || active.Valid || version != 1 {
		t.Fatalf("import unexpectedly activated catalog: active=%+v version=%d err=%v", active, version, err)
	}
	repeated, err := ImportLegacyGateway(context.Background(), sqlDB, options)
	if err != nil || !repeated.Reused || repeated.RunID != report.RunID || repeated.Skipped != repeated.SourceRows {
		t.Fatalf("idempotent catalog import: report=%+v err=%v", repeated, err)
	}
	verifyLegacyRuntimeRows(t, db, sqlDB, report, ability, key, options)
}

func verifyLegacyRuntimeRows(t *testing.T, db *gorm.DB, sqlDB *sql.DB, catalog ImportReport, ability model.GwAbility, key model.GwChannelKey, options ImportOptions) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	user := model.User{Username: "runtime-import-user", Password: "not-a-login-secret", Role: model.UserRoleUser, Balance: decimal.RequireFromString("10"), Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	_, selector, secretDigest, err := tokenauth.Generate()
	if err != nil {
		t.Fatal(err)
	}
	token := model.Token{UserID: user.ID, Selector: selector, SecretDigest: secretDigest, SecretDigestVersion: tokenauth.DigestVersion, AuthVersion: 1, KeyHint: "test", Name: "runtime import", Balance: decimal.RequireFromString("6"), TotalUsed: decimal.RequireFromString("4"), Status: 1}
	if err := db.Create(&token).Error; err != nil {
		t.Fatal(err)
	}
	store, err := repository.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return store.InitializeBilling(context.Background(), tx, billing.Currency{Code: "CREDIT", Version: 1, FractionDigits: 8, RoundingMode: "half_even", MaxAmount: "99999999999999999999.99999999"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`UPDATE gw_catalog_releases SET status='published',updated_at=? WHERE id=? AND status='draft'`, now, catalog.ReleaseID); err != nil {
		t.Fatal(err)
	}

	const sourceCallID = "runtime-call-1"
	if _, err := sqlDB.Exec(`INSERT INTO api_calls(id,request_id,user_id,token_id,endpoint,operation,model,status,reserved_amount,started_at,completed_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'completed','0',?,?,?,?)`, sourceCallID, "runtime-request-1", user.ID, token.ID, "/v1/chat/completions", "chat.completions", ability.ModelName, now, now, now, now); err != nil {
		t.Fatal(err)
	}
	attemptResult, err := sqlDB.Exec(`INSERT INTO api_call_attempts(call_id,attempt_no,route_kind,stage,ability_id,channel_id,key_id,protocol,vendor_model,transport,request_path,status,started_at,completed_at,created_at,updated_at) VALUES (?,1,'gateway_v2','submit',?,?,?,'openai',?,'chat_completions','/v1/chat/completions','completed',?,?,?,?)`, sourceCallID, ability.ID, ability.ChannelID, key.ID, ability.VendorModel, now, now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := attemptResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`UPDATE api_calls SET final_attempt_id=?,attempt_count=1 WHERE id=?`, attemptID, sourceCallID); err != nil {
		t.Fatal(err)
	}
	requestBody := []byte(`{"model":"qa-import-model","messages":[{"role":"user","content":"hello"}]}`)
	if _, err := sqlDB.Exec(`INSERT INTO api_call_payloads(call_id,attempt_id,kind,content_type,data,original_bytes,created_at,updated_at) VALUES (?,?,'request','application/json',?,?,?,?)`, sourceCallID, attemptID, requestBody, len(requestBody), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO channel_request_logs(call_id,attempt_id,channel_id,request_type,model_code,vendor_model,upstream_transport,request_path,method,url,request_body,status_code,response_body,duration_ms,request_at,created_at,updated_at) VALUES (?,?,?,'chat',?,?, 'chat_completions','/v1/chat/completions','POST','https://example.invalid/v1/chat/completions',?,200,'{"id":"upstream-result"}',12,?,?,?)`, sourceCallID, attemptID, ability.ChannelID, ability.ModelName, ability.VendorModel, string(requestBody), now, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO billing_logs(idempotent_key,token_id,user_id,call_id,attempt_id,phase,pricing_snapshot,amount,type,status,remark,created_at,updated_at) VALUES ('runtime-billing-1',?,?,?,?,'settle',JSON_OBJECT('kind','legacy'),2,'deduct','success','verified fixture',?,?)`, token.ID, user.ID, sourceCallID, attemptID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO balance_entries(entry_key,source_key,account_type,account_id,user_id,token_id,direction,category,amount,balance_before,balance_after,call_id,attempt_id,metadata,created_at) VALUES ('runtime-token-entry','runtime-billing-1','token',?,?,?, 'debit','deduction',2,8,6,?,?,JSON_OBJECT('kind','legacy'),?),('runtime-user-entry','runtime-billing-1','user',?,?,?, 'debit','deduction',2,12,10,?,?,JSON_OBJECT('kind','legacy'),?)`, token.ID, user.ID, token.ID, sourceCallID, attemptID, now, user.ID, user.ID, token.ID, sourceCallID, attemptID, now); err != nil {
		t.Fatal(err)
	}

	report, err := ImportLegacyRuntime(context.Background(), sqlDB, options)
	if err != nil {
		t.Fatalf("runtime import: report=%+v err=%v issues=%v", report, err, loadRuntimeMigrationIssues(t, sqlDB, report.RunID))
	}
	if report.SourceRows != 9 || report.BillingAccounts != 1 || report.BudgetWindows != 1 || report.Calls != 1 || report.Attempts != 1 || report.Payloads != 1 || report.RequestLogs != 1 || report.BillingEvidence != 1 || report.BalanceEvidence != 2 || report.Issues != 0 {
		t.Fatalf("unexpected runtime report: %+v", report)
	}
	var attemptCostPlanID int64
	if err := sqlDB.QueryRow(`SELECT a.cost_plan_id
		FROM gw_api_call_attempts a
		JOIN gw_cost_plans cp ON cp.id=a.cost_plan_id
		  AND cp.release_id=a.catalog_release_id
		  AND cp.offering_id=a.offering_id`).Scan(&attemptCostPlanID); err != nil {
		t.Fatal(err)
	}
	if attemptCostPlanID == 0 {
		t.Fatal("runtime attempt has no immutable cost-plan identity")
	}
	verifyLegacyOpeningPosition(t, sqlDB, user.ID, token.ID)
	var blobID sql.NullInt64
	var contentLength uint64
	var contentHMAC string
	var purgedAt sql.NullTime
	if err := sqlDB.QueryRow(`SELECT encrypted_blob_id,content_length,content_hmac,purged_at FROM gw_api_call_payloads`).Scan(&blobID, &contentLength, &contentHMAC, &purgedAt); err != nil {
		t.Fatal(err)
	}
	if blobID.Valid || !purgedAt.Valid || contentLength != uint64(len(requestBody)) || len(contentHMAC) != 64 {
		t.Fatalf("unsafe payload projection: blob=%+v length=%d hmac=%q purged=%+v", blobID, contentLength, contentHMAC, purgedAt)
	}
	var requestHMAC, responseHMAC string
	var requestComplete, responseComplete bool
	if err := sqlDB.QueryRow(`SELECT request_bytes_hmac,response_bytes_hmac,request_bytes_complete,response_bytes_complete FROM gw_channel_request_logs`).Scan(&requestHMAC, &responseHMAC, &requestComplete, &responseComplete); err != nil {
		t.Fatal(err)
	}
	if len(requestHMAC) != 64 || len(responseHMAC) != 64 || !requestComplete || !responseComplete {
		t.Fatalf("unexpected request evidence: request=%q response=%q complete=%t/%t", requestHMAC, responseHMAC, requestComplete, responseComplete)
	}
	repeated, err := ImportLegacyRuntime(context.Background(), sqlDB, options)
	if err != nil || repeated.RunID != report.RunID || repeated.Skipped != repeated.SourceRows {
		t.Fatalf("idempotent runtime import: report=%+v err=%v", repeated, err)
	}
	var provedMappings int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM gw_migration_mapping_proofs WHERE run_id=?`, report.RunID).Scan(&provedMappings); err != nil {
		t.Fatal(err)
	}
	if provedMappings != report.SourceRows {
		t.Fatalf("proved runtime mappings=%d, want %d", provedMappings, report.SourceRows)
	}
	if _, err := sqlDB.Exec(`UPDATE channel_request_logs SET response_body='{"id":"changed"}' WHERE call_id=?`, sourceCallID); err != nil {
		t.Fatal(err)
	}
	changed, err := ImportLegacyRuntime(context.Background(), sqlDB, options)
	if !errors.Is(err, ErrRuntimeImportIssues) || changed.Issues == 0 {
		t.Fatalf("changed source must fail: report=%+v err=%v", changed, err)
	}
	var targetLogs int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM gw_channel_request_logs`).Scan(&targetLogs); err != nil || targetLogs != 1 {
		t.Fatalf("source conflict changed target rows: count=%d err=%v", targetLogs, err)
	}
}

func verifyLegacyOpeningPosition(t *testing.T, db *sql.DB, userID, tokenID uint) {
	t.Helper()
	var posted, userLedger, clearingLedger, limit, used string
	var accountSnapshots, evidenceRows, evidenceMappings int64
	if err := db.QueryRow(`SELECT a.posted_balance,l.balance FROM billing_accounts a JOIN ledger_accounts l ON l.billing_account_id=a.id WHERE a.user_id=?`, userID).Scan(&posted, &userLedger); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT balance FROM ledger_accounts WHERE system_account_code='funding_clearing'`).Scan(&clearingLedger); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT limit_amount,used_amount FROM token_budget_windows WHERE token_id=?`, tokenID).Scan(&limit, &used); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_legacy_account_snapshots WHERE user_id=?`, userID).Scan(&accountSnapshots); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_legacy_billing_evidence WHERE user_id=?`, userID).Scan(&evidenceRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM gw_migration_object_map WHERE target_type='legacy_billing_evidence'`).Scan(&evidenceMappings); err != nil {
		t.Fatal(err)
	}
	if posted != "10.000000000000000000" || userLedger != "-10.000000000000000000" || clearingLedger != "10.000000000000000000" || limit != "10.000000000000000000" || used != "4.000000000000000000" || accountSnapshots != 2 || evidenceRows != 3 || evidenceMappings != 3 {
		t.Fatalf("opening position posted=%s user_ledger=%s clearing=%s budget=%s/%s snapshots=%d evidence=%d mappings=%d", posted, userLedger, clearingLedger, limit, used, accountSnapshots, evidenceRows, evidenceMappings)
	}
}

func loadRuntimeMigrationIssues(t *testing.T, db *sql.DB, runID int64) []string {
	t.Helper()
	rows, err := db.Query(`SELECT source_table,source_pk,issue_code,detail,status FROM gw_migration_issues WHERE run_id=? ORDER BY id`, runID)
	if err != nil {
		t.Fatalf("load runtime migration issues: %v", err)
	}
	defer rows.Close()
	var issues []string
	for rows.Next() {
		var table, primaryKey, code, detail, status string
		if err := rows.Scan(&table, &primaryKey, &code, &detail, &status); err != nil {
			t.Fatalf("scan runtime migration issue: %v", err)
		}
		issues = append(issues, fmt.Sprintf("%s[%s] %s (%s): %s", table, primaryKey, code, status, detail))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate runtime migration issues: %v", err)
	}
	return issues
}
