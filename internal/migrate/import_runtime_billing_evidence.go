package migrate

import (
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

type legacyBillingEvidenceInput struct {
	row                               runtimeSourceRow
	recordKind                        string
	userID, tokenID, accountID        uint64
	accountType, legacyType           string
	legacyStatus, direction, category string
	amount                            billing.Amount
	balanceBefore, balanceAfter       *billing.Amount
	targetCallID, targetAttemptID     int64
	evidenceHMACs                     map[string]any
	sourceCreatedAt                   time.Time
}

func (m *runtimeImporter) createLegacyBillingEvidence(in legacyBillingEvidenceInput) error {
	var accountType, accountID, legacyType, legacyStatus, direction, category any
	if in.accountType != "" {
		accountType = in.accountType
		accountID = in.accountID
	}
	if in.legacyType != "" {
		legacyType = in.legacyType
	}
	if in.legacyStatus != "" {
		legacyStatus = in.legacyStatus
	}
	if in.direction != "" {
		direction = in.direction
	}
	if in.category != "" {
		category = in.category
	}
	var before, after, callID, attemptID any
	if in.balanceBefore != nil {
		before = in.balanceBefore.String()
	}
	if in.balanceAfter != nil {
		after = in.balanceAfter.String()
	}
	if in.targetCallID > 0 {
		callID = in.targetCallID
	}
	if in.targetAttemptID > 0 {
		attemptID = in.targetAttemptID
	}
	result, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_legacy_billing_evidence(migration_run_id,source_table,source_pk,record_kind,user_id,token_id,account_type,account_id,legacy_type,legacy_status,direction,category,amount,balance_before,balance_after,target_call_id,target_attempt_id,evidence_hmacs,source_hmac,source_created_at,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.runID, in.row.Table, in.row.PrimaryKey, in.recordKind, in.userID, in.tokenID,
		accountType, accountID, legacyType, legacyStatus, direction, category, in.amount.String(), before, after,
		callID, attemptID, runtimeSummary(in.evidenceHMACs), in.row.Revision, in.sourceCreatedAt, m.now)
	if err != nil {
		return err
	}
	targetID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	return m.createMapping(in.row, "legacy_billing_evidence", targetID, in.recordKind)
}
