package migrate

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/security"
)

var (
	ErrRuntimeImportIssues  = errors.New("legacy runtime import has unresolved issues")
	ErrRuntimeImportRunning = errors.New("legacy runtime import is already running")
)

const runtimeImportOperation = "legacy_runtime_import"

type RuntimeImportReport struct {
	RunID              int64
	SourceRows         int64
	BillingAccounts    int64
	BudgetWindows      int64
	Calls              int64
	Attempts           int64
	Payloads           int64
	RequestLogs        int64
	CapabilityTasks    int64
	Responses          int64
	VideoTasks         int64
	AsyncExecutions    int64
	BillingEvidence    int64
	BalanceEvidence    int64
	Skipped            int64
	Issues             int64
	SourceRevisionHMAC string
}

type runtimeOwner struct {
	UserID                    uint64
	TokenStatus, UserStatus   int64
	TokenDeleted, UserDeleted bool
}

type runtimeImporter struct {
	ctx      context.Context
	tx       *sql.Tx
	options  ImportOptions
	runID    int64
	now      time.Time
	report   RuntimeImportReport
	snapshot runtimeSnapshot
	owners   map[uint64]runtimeOwner
	users    map[uint64]runtimeSourceRow
	calls    map[string]runtimeSourceRow
	attempts map[string][]runtimeSourceRow
	issueErr error
}

// ImportLegacyRuntime imports only facts whose ownership and immutable target
// configuration can be proven from the stopped legacy snapshot. Rows that
// cannot be proved are retained in the source tables and receive an explicit
// migration issue; the importer never edits or deletes a legacy row.
func ImportLegacyRuntime(ctx context.Context, db *sql.DB, options ImportOptions) (RuntimeImportReport, error) {
	if db == nil || len(options.HMACKey) != security.KeySize {
		return RuntimeImportReport{}, ErrImportRequiresKeyring
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return RuntimeImportReport{}, err
	}
	defer conn.Close()
	var locked int
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK('prism_legacy_runtime_import',60)`).Scan(&locked); err != nil {
		return RuntimeImportReport{}, fmt.Errorf("acquire runtime import lock: %w", err)
	}
	if locked != 1 {
		return RuntimeImportReport{}, ErrRuntimeImportRunning
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('prism_legacy_runtime_import')`)
	}()

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return RuntimeImportReport{}, err
	}
	snapshot, err := loadRuntimeSnapshot(ctx, tx, options.HMACKey)
	if err != nil {
		_ = tx.Rollback()
		return RuntimeImportReport{}, err
	}
	runID, alreadySucceeded, err := beginRuntimeImportRun(ctx, tx, snapshot.HMAC)
	if err != nil {
		_ = tx.Rollback()
		return RuntimeImportReport{}, err
	}
	report := RuntimeImportReport{RunID: runID, SourceRows: snapshot.Rows, SourceRevisionHMAC: snapshot.HMAC}
	if alreadySucceeded {
		report.Skipped = snapshot.Rows
		if err := tx.Commit(); err != nil {
			return report, err
		}
		return report, nil
	}
	importer := &runtimeImporter{
		ctx: ctx, tx: tx, options: options, runID: runID, now: time.Now().UTC(),
		report: report, snapshot: snapshot, users: map[uint64]runtimeSourceRow{}, calls: map[string]runtimeSourceRow{}, attempts: map[string][]runtimeSourceRow{},
	}
	if err := importer.run(); err != nil {
		_ = tx.Rollback()
		_ = recordRuntimeImportFailure(ctx, conn, snapshot.HMAC, "runtime_import_error")
		return importer.report, err
	}
	var openIssues int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_migration_issues WHERE run_id=? AND status='open'`, runID).Scan(&openIssues); err != nil {
		_ = tx.Rollback()
		return importer.report, err
	}
	status, errorCode := "succeeded", ""
	if openIssues > 0 {
		status, errorCode = "failed", "unresolved_migration_issues"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status=?,finished_at=?,error_code=? WHERE id=? AND status='running'`, status, importer.now, errorCode, runID); err != nil {
		_ = tx.Rollback()
		return importer.report, err
	}
	if err := tx.Commit(); err != nil {
		return importer.report, err
	}
	importer.report.Issues = openIssues
	if openIssues > 0 {
		return importer.report, fmt.Errorf("%w: run_id=%d open_issues=%d", ErrRuntimeImportIssues, runID, openIssues)
	}
	return importer.report, nil
}

func beginRuntimeImportRun(ctx context.Context, tx *sql.Tx, revision string) (int64, bool, error) {
	var id int64
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id,status FROM gw_migration_runs WHERE operation=? AND source_revision_hmac=? FOR UPDATE`, runtimeImportOperation, revision).Scan(&id, &status)
	if err == sql.ErrNoRows {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_migration_runs(operation,source_revision_hmac,status,started_at) VALUES (?,?,'running',?)`, runtimeImportOperation, revision, time.Now().UTC())
		if insertErr != nil {
			return 0, false, insertErr
		}
		id, insertErr = result.LastInsertId()
		return id, false, insertErr
	}
	if err != nil {
		return 0, false, err
	}
	switch status {
	case "succeeded":
		return id, true, nil
	case "running":
		return 0, false, ErrRuntimeImportRunning
	case "failed":
		if _, err = tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status='running',started_at=?,finished_at=NULL,error_code='' WHERE id=? AND status='failed'`, time.Now().UTC(), id); err != nil {
			return 0, false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE gw_migration_issues SET status='resolved' WHERE run_id=? AND source_table='*' AND source_pk='*' AND issue_code='runtime_import_error' AND status='open'`, id)
		return id, false, err
	default:
		return 0, false, fmt.Errorf("runtime import run %d has invalid status %q", id, status)
	}
}

func recordRuntimeImportFailure(ctx context.Context, conn *sql.Conn, revision, code string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var runID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM gw_migration_runs WHERE operation=? AND source_revision_hmac=? FOR UPDATE`, runtimeImportOperation, revision).Scan(&runID)
	if err == sql.ErrNoRows {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_migration_runs(operation,source_revision_hmac,status,started_at,finished_at,error_code) VALUES (?,?,'failed',?,?,?)`, runtimeImportOperation, revision, time.Now().UTC(), time.Now().UTC(), code)
		if insertErr != nil {
			return insertErr
		}
		runID, err = result.LastInsertId()
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE gw_migration_runs SET status='failed',finished_at=?,error_code=? WHERE id=?`, time.Now().UTC(), code, runID)
	}
	if err != nil {
		return err
	}
	if err := insertRuntimeIssue(ctx, tx, runID, "*", "*", code, "runtime import stopped before committing target changes"); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *runtimeImporter) run() error {
	owners, err := loadRuntimeOwners(m.ctx, m.tx)
	if err != nil {
		return fmt.Errorf("load legacy ownership: %w", err)
	}
	m.owners = owners
	if table := m.snapshot.Tables["users"]; table.Present {
		for _, row := range table.Rows {
			if id, err := runtimeUint(row, "id"); err == nil && id > 0 {
				m.users[id] = row
			}
		}
	}
	if table := m.snapshot.Tables["api_calls"]; table.Present {
		for _, row := range table.Rows {
			m.calls[row.PrimaryKey] = row
		}
	}
	if table := m.snapshot.Tables["api_call_attempts"]; table.Present {
		for _, row := range table.Rows {
			m.attempts[row.text("call_id")] = append(m.attempts[row.text("call_id")], row)
		}
	}
	processors := []func() error{
		m.importAccountSnapshots,
		m.importCalls,
		m.importAttempts,
		m.importPayloads,
		m.importCapabilityTasks,
		m.importVideoTasks,
		m.importResponses,
		m.linkPreviousResponses,
		m.importRequestLogs,
		m.importRuntimeBillingEvidence,
	}
	for _, process := range processors {
		if err := process(); err != nil {
			return err
		}
		if m.issueErr != nil {
			return fmt.Errorf("record migration issue: %w", m.issueErr)
		}
	}
	return nil
}

func loadRuntimeOwners(ctx context.Context, tx *sql.Tx) (map[uint64]runtimeOwner, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.id,t.user_id,t.status,t.deleted_at,u.id,u.status,u.deleted_at FROM tokens t LEFT JOIN users u ON u.id=t.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := map[uint64]runtimeOwner{}
	for rows.Next() {
		var tokenID, tokenUserID uint64
		var tokenStatus sql.NullInt64
		var tokenDeleted sql.NullTime
		var userID, userStatus sql.NullInt64
		var userDeleted sql.NullTime
		if err := rows.Scan(&tokenID, &tokenUserID, &tokenStatus, &tokenDeleted, &userID, &userStatus, &userDeleted); err != nil {
			return nil, err
		}
		owner := runtimeOwner{UserID: tokenUserID, TokenDeleted: tokenDeleted.Valid, UserDeleted: userDeleted.Valid}
		if tokenStatus.Valid {
			owner.TokenStatus = tokenStatus.Int64
		} else {
			owner.TokenStatus = -1
		}
		if userID.Valid && uint64(userID.Int64) == tokenUserID && userStatus.Valid {
			owner.UserStatus = userStatus.Int64
		} else {
			owner.UserStatus = -1
		}
		owners[tokenID] = owner
	}
	return owners, rows.Err()
}

func (m *runtimeImporter) validateOwner(row runtimeSourceRow, requireUser bool) (uint64, uint64, bool) {
	tokenID, err := runtimeUint(row, "token_id")
	if err != nil || tokenID == 0 {
		m.issue(row, "invalid_token_owner", "token_id is missing or invalid")
		return 0, 0, false
	}
	owner, ok := m.owners[tokenID]
	if !ok || owner.UserID == 0 {
		m.issue(row, "invalid_token_owner", "token does not resolve to a user")
		return 0, 0, false
	}
	if owner.TokenStatus != 0 && owner.TokenStatus != 1 || owner.UserStatus != 0 && owner.UserStatus != 1 {
		m.issue(row, "invalid_owner_status", "user or token has an unrecognized status")
		return 0, 0, false
	}
	if value := row.text("user_id"); value != "" {
		userID, parseErr := strconv.ParseUint(value, 10, 64)
		if parseErr != nil || userID == 0 || userID != owner.UserID {
			m.issue(row, "owner_mismatch", "source user_id does not own the token")
			return 0, 0, false
		}
	} else if requireUser {
		m.issue(row, "missing_user_owner", "source row does not contain user_id")
		return 0, 0, false
	}
	return owner.UserID, tokenID, true
}

func (m *runtimeImporter) issue(row runtimeSourceRow, code, detail string) {
	if m.issueErr != nil {
		return
	}
	if row.PrimaryKey == "" {
		row.PrimaryKey = "*"
	}
	if err := insertRuntimeIssue(m.ctx, m.tx, m.runID, row.Table, row.PrimaryKey, code, detail); err != nil {
		m.issueErr = err
	}
}

func insertRuntimeIssue(ctx context.Context, tx *sql.Tx, runID int64, table, primaryKey, code, detail string) error {
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gw_migration_issues WHERE run_id=? AND source_table=? AND source_pk=? AND issue_code=? AND detail=? AND status='open')`, runID, table, primaryKey, code, detail).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO gw_migration_issues(run_id,source_table,source_pk,issue_code,detail,status,created_at) VALUES (?,?,?,?,?,'open',?)`, runID, table, primaryKey, code, detail, time.Now().UTC())
	return err
}

func (m *runtimeImporter) resolveIssues(row runtimeSourceRow) error {
	_, err := m.tx.ExecContext(m.ctx, `UPDATE gw_migration_issues SET status='resolved' WHERE run_id=? AND source_table=? AND source_pk=? AND status='open'`, m.runID, row.Table, row.PrimaryKey)
	return err
}

type runtimeMapping struct {
	ID, TargetID  int64
	Discriminator string
}

func (m *runtimeImporter) existingMapping(row runtimeSourceRow, targetType, targetTable string) (runtimeMapping, bool, error) {
	rows, err := m.tx.QueryContext(m.ctx, `SELECT id,target_id,target_discriminator FROM gw_migration_object_map WHERE source_table=? AND source_pk=? AND target_type=? ORDER BY id FOR UPDATE`, row.Table, row.PrimaryKey, targetType)
	if err != nil {
		return runtimeMapping{}, false, err
	}
	defer rows.Close()
	var mappings []runtimeMapping
	for rows.Next() {
		var mapping runtimeMapping
		if err := rows.Scan(&mapping.ID, &mapping.TargetID, &mapping.Discriminator); err != nil {
			return runtimeMapping{}, false, err
		}
		mappings = append(mappings, mapping)
	}
	if err := rows.Err(); err != nil {
		return runtimeMapping{}, false, err
	}
	if len(mappings) == 0 {
		return runtimeMapping{}, false, nil
	}
	if len(mappings) != 1 {
		m.issue(row, "duplicate_object_mapping", "more than one target mapping exists for the source row")
		return runtimeMapping{}, true, nil
	}
	mapping := mappings[0]
	var targetExists bool
	query, ok := runtimeTargetExistenceQuery(targetTable)
	if !ok {
		return runtimeMapping{}, false, fmt.Errorf("unsupported runtime mapping target %s", targetTable)
	}
	if err := m.tx.QueryRowContext(m.ctx, query, mapping.TargetID).Scan(&targetExists); err != nil {
		return runtimeMapping{}, false, err
	}
	if !targetExists {
		m.issue(row, "missing_mapping_target", "object mapping points to a missing target row")
		return runtimeMapping{}, true, nil
	}
	var revisionExists bool
	if err := m.tx.QueryRowContext(m.ctx, `SELECT EXISTS(SELECT 1 FROM gw_migration_source_revisions WHERE object_map_id=? AND source_hmac=?)`, mapping.ID, row.Revision).Scan(&revisionExists); err != nil {
		return runtimeMapping{}, false, err
	}
	if !revisionExists {
		m.issue(row, "source_revision_conflict", "source row changed after its target identity was created")
		return runtimeMapping{}, true, nil
	}
	if err := recordMappingProof(m.ctx, m.tx, m.runID, mapping.ID, row.Revision, m.now); err != nil {
		return runtimeMapping{}, false, err
	}
	if err := m.resolveIssues(row); err != nil {
		return runtimeMapping{}, false, err
	}
	m.report.Skipped++
	return mapping, true, nil
}

func runtimeTargetExistenceQuery(table string) (string, bool) {
	switch table {
	case "gw_api_calls", "gw_api_call_attempts", "gw_api_call_payloads", "gw_api_resources", "gw_async_executions", "gw_channel_request_logs", "gw_legacy_account_snapshots", "gw_legacy_billing_evidence":
		return "SELECT EXISTS(SELECT 1 FROM `" + table + "` WHERE id=?)", true
	default:
		return "", false
	}
}

func (m *runtimeImporter) createMapping(row runtimeSourceRow, targetType string, targetID int64, discriminator string) error {
	result, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_migration_object_map(run_id,source_table,source_pk,target_type,target_id,target_discriminator,created_at) VALUES (?,?,?,?,?,?,?)`, m.runID, row.Table, row.PrimaryKey, targetType, targetID, discriminator, m.now)
	if err != nil {
		return err
	}
	mapID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_migration_source_revisions(object_map_id,source_hmac,observed_at) VALUES (?,?,?)`, mapID, row.Revision, m.now); err != nil {
		return err
	}
	if err := recordMappingProof(m.ctx, m.tx, m.runID, mapID, row.Revision, m.now); err != nil {
		return err
	}
	return m.resolveIssues(row)
}

func (m *runtimeImporter) lookupTarget(sourceTable, sourcePK, targetType string) (int64, bool, error) {
	var count, targetID int64
	if err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*),COALESCE(MIN(target_id),0) FROM gw_migration_object_map WHERE source_table=? AND source_pk=? AND target_type=?`, sourceTable, sourcePK, targetType).Scan(&count, &targetID); err != nil {
		return 0, false, err
	}
	return targetID, count == 1 && targetID > 0, nil
}

type runtimeCatalogTuple struct {
	ContractID, ReleaseID, ModelOperationID, SKUID int64
	DeliveryMode                                   string
}

func (m *runtimeImporter) resolveCatalog(row runtimeSourceRow) (runtimeCatalogTuple, bool, error) {
	modelName, operation, endpoint := row.text("model"), row.text("operation"), row.text("endpoint")
	if modelName == "" || operation == "" {
		m.issue(row, "catalog_identity_missing", "model or operation is empty")
		return runtimeCatalogTuple{}, false, nil
	}
	rows, err := m.tx.QueryContext(m.ctx, `SELECT DISTINCT oc.id,rel.id,mo.id,sku.id,sku.delivery_mode
		FROM gw_catalog_releases rel
		JOIN gw_migration_runs mr ON mr.status='succeeded' AND (
			(mr.operation='legacy_gateway_import' AND mr.source_revision_hmac=rel.content_hash) OR
			(mr.operation='legacy_catalog_import' AND EXISTS (
				SELECT 1 FROM gw_migration_object_map release_map
				WHERE release_map.run_id=mr.id AND release_map.source_table='legacy_catalog_snapshot'
				  AND release_map.target_type='catalog_release' AND release_map.target_id=rel.id
			)))
		JOIN gw_catalog_models cm ON cm.release_id=rel.id
		JOIN gw_catalog_model_names cmn ON cmn.release_id=cm.release_id AND cmn.catalog_model_id=cm.id
		JOIN gw_model_names mn ON mn.id=cmn.model_name_id AND mn.model_id=cm.model_id
		JOIN gw_model_operations mo ON mo.release_id=cm.release_id AND mo.catalog_model_id=cm.id
		JOIN gw_operation_contracts oc ON oc.id=mo.operation_contract_id
		JOIN gw_operation_routes opr ON opr.operation_contract_id=oc.id
		JOIN gw_skus sku ON sku.release_id=mo.release_id AND sku.model_operation_id=mo.id
		WHERE rel.status IN ('published','retired') AND mn.api_name=?
		  AND (oc.operation_code=? OR (?<>'' AND opr.route_template=?))`, modelName, operation, endpoint, endpoint)
	if err != nil {
		return runtimeCatalogTuple{}, false, err
	}
	defer rows.Close()
	var candidates []runtimeCatalogTuple
	for rows.Next() {
		var candidate runtimeCatalogTuple
		if err := rows.Scan(&candidate.ContractID, &candidate.ReleaseID, &candidate.ModelOperationID, &candidate.SKUID, &candidate.DeliveryMode); err != nil {
			return runtimeCatalogTuple{}, false, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return runtimeCatalogTuple{}, false, err
	}
	if len(candidates) != 1 {
		code := "catalog_mapping_missing"
		if len(candidates) > 1 {
			code = "catalog_mapping_ambiguous"
		}
		m.issue(row, code, "call does not resolve to exactly one imported release, operation contract, model operation, and SKU")
		return runtimeCatalogTuple{}, false, nil
	}
	return candidates[0], true, nil
}

func (m *runtimeImporter) importCalls() error {
	table := m.snapshot.Tables["api_calls"]
	if !table.Present {
		return nil
	}
	required := []string{"id", "user_id", "token_id", "endpoint", "operation", "model", "status", "reserved_amount", "started_at", "created_at", "updated_at"}
	if missing := table.missingColumns(required...); len(missing) > 0 {
		m.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		return nil
	}
	if len(table.Rows) == 0 {
		return nil
	}
	var currency string
	var currencyVersion uint32
	if err := m.tx.QueryRowContext(m.ctx, `SELECT currency_code,currency_version FROM billing_system_state WHERE id=1`).Scan(&currency, &currencyVersion); err != nil {
		for _, row := range table.Rows {
			m.issue(row, "billing_currency_unavailable", "billing system currency is not initialized")
		}
		return nil
	}
	for _, row := range table.Rows {
		if row.PrimaryKey == "" {
			m.issue(row, "invalid_source_identity", "call id is empty")
			continue
		}
		if _, exists, err := m.existingMapping(row, "api_call", "gw_api_calls"); err != nil {
			return err
		} else if exists {
			continue
		}
		userID, tokenID, ok := m.validateOwner(row, true)
		if !ok {
			continue
		}
		status, ok := runtimeCallStatus(row)
		if !ok {
			m.issue(row, "invalid_call_status", "call status is not part of the verified legacy state map")
			continue
		}
		if runtimeCallRequiresReservationReconciliation(status) {
			m.issue(row, "inflight_billing_unreconciled", "active or indeterminate call requires an explicitly reconstructed billing reservation")
			continue
		}
		catalog, ok, err := m.resolveCatalog(row)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		amount, err := billing.ParseAmount(row.text("reserved_amount"), 18, true)
		if err != nil {
			m.issue(row, "invalid_quoted_amount", "reserved_amount is not a non-negative decimal")
			continue
		}
		startedAt, valid := runtimeTime(row, "started_at", "created_at")
		if !valid {
			m.issue(row, "invalid_call_time", "call start time is missing or invalid")
			continue
		}
		createdAt, valid := runtimeTime(row, "created_at", "started_at")
		if !valid {
			createdAt = startedAt
		}
		updatedAt, valid := runtimeTime(row, "updated_at", "completed_at", "created_at")
		if !valid {
			updatedAt = createdAt
		}
		publicID := runtimePublicID("call", row.PrimaryKey, m.options.HMACKey)
		var duplicate bool
		if err := m.tx.QueryRowContext(m.ctx, `SELECT EXISTS(SELECT 1 FROM gw_api_calls WHERE public_id=?)`, publicID).Scan(&duplicate); err != nil {
			return err
		}
		if duplicate {
			m.issue(row, "target_identity_conflict", "call public identity is already owned by an unmapped target")
			continue
		}
		result, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_api_calls(public_id,user_id,token_id,operation_contract_id,catalog_release_id,model_operation_id,sku_id,status,state_version,quoted_amount,price_currency,price_currency_version,delivery_mode,next_action_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,1,?,?,?,?,NULL,?,?)`, publicID, userID, tokenID, catalog.ContractID, catalog.ReleaseID, catalog.ModelOperationID, catalog.SKUID, status, amount.String(), currency, currencyVersion, catalog.DeliveryMode, createdAt, updatedAt)
		if err != nil {
			return fmt.Errorf("import legacy call %s: %w", row.PrimaryKey, err)
		}
		targetID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_state_transition_events(call_id,new_state,state_version,reason_code,created_at) VALUES (?,?,1,'legacy_runtime_import',?)`, targetID, status, createdAt); err != nil {
			return err
		}
		if err := m.createMapping(row, "api_call", targetID, publicID); err != nil {
			return err
		}
		m.report.Calls++
	}
	return nil
}

func runtimeCallStatus(row runtimeSourceRow) (string, bool) {
	status := strings.ToLower(row.text("status"))
	switch status {
	case "received", "retry_pending", "completed", "failed", "cancelled", "indeterminate":
		return status, true
	case "in_progress", "processing":
		if row.text("resource_type") == "" {
			return "indeterminate", true
		}
		return "in_progress", true
	default:
		return "", false
	}
}

func runtimeCallRequiresReservationReconciliation(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return false
	default:
		return true
	}
}

func runtimePublicID(namespace, source string, key []byte) string {
	if len(source) > 0 && len(source) <= 36 {
		valid := true
		for _, char := range source {
			if char < 0x21 || char > 0x7e {
				valid = false
				break
			}
		}
		if valid {
			return source
		}
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("prism-runtime-public-id-v1\x00" + namespace + "\x00" + source))
	value := mac.Sum(nil)[:16]
	value[6] = value[6]&0x0f | 0x50
	value[8] = value[8]&0x3f | 0x80
	hexValue := hex.EncodeToString(value)
	return hexValue[:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:]
}

func runtimeSummary(values map[string]any) []byte {
	encoded, _ := json.Marshal(values)
	return encoded
}
