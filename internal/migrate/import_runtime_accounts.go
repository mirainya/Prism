package migrate

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

func (m *runtimeImporter) importAccountSnapshots() error {
	users := m.snapshot.Tables["users"]
	tokens := m.snapshot.Tables["tokens"]
	if users.Present {
		if missing := users.missingColumns("id", "balance", "status", "created_at"); len(missing) > 0 {
			m.issue(runtimeSourceRow{Table: users.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		} else if err := m.importUserAccountSnapshots(users.Rows); err != nil {
			return err
		}
	}
	if tokens.Present {
		if missing := tokens.missingColumns("id", "user_id", "balance", "total_used", "status", "created_at"); len(missing) > 0 {
			m.issue(runtimeSourceRow{Table: tokens.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
		} else if err := m.importTokenBudgetSnapshots(tokens.Rows); err != nil {
			return err
		}
	}
	return nil
}

func (m *runtimeImporter) importUserAccountSnapshots(rows []runtimeSourceRow) error {
	var currency string
	var currencyVersion uint32
	if err := m.tx.QueryRowContext(m.ctx, `SELECT currency_code,currency_version FROM billing_system_state WHERE id=1 FOR SHARE`).Scan(&currency, &currencyVersion); err != nil {
		for _, row := range rows {
			m.issue(row, "billing_currency_unavailable", "billing system currency is not initialized")
		}
		return nil
	}
	for _, row := range rows {
		if _, exists, err := m.existingMapping(row, "account_snapshot", "gw_legacy_account_snapshots"); err != nil {
			return err
		} else if exists {
			continue
		}
		userID, idErr := runtimeUint(row, "id")
		balanceValue, balanceErr := billing.ParseAmount(row.text("balance"), 18, false)
		createdAt, timeOK := runtimeTime(row, "created_at")
		statusValue := row.text("status")
		if idErr != nil || userID == 0 || balanceErr != nil || !timeOK || statusValue != "0" && statusValue != "1" {
			m.issue(row, "account_snapshot_invalid", "user identity, balance, state, or creation time is invalid")
			continue
		}
		if balanceValue.Sign() < 0 {
			m.issue(row, "negative_account_balance", "negative user balance requires manual reconciliation")
			continue
		}
		var existing int64
		if err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*) FROM billing_accounts WHERE user_id=?`, userID).Scan(&existing); err != nil {
			return err
		}
		if existing != 0 {
			m.issue(row, "billing_account_conflict", "a billing account already exists without this verified source mapping")
			continue
		}
		accountStatus := "open"
		if statusValue == "0" || runtimeDeleted(row) {
			accountStatus = "closed"
		}
		stateVersion := uint64(1)
		if balanceValue.Sign() != 0 {
			stateVersion = 2
		}
		result, err := m.tx.ExecContext(m.ctx, `INSERT INTO billing_accounts(user_id,currency_code,currency_version,posted_balance,held_amount,credit_limit,status,state_version,created_at,updated_at) VALUES (?,?,?,?,0,0,?,?,?,?)`, userID, currency, currencyVersion, balanceValue.String(), accountStatus, stateVersion, createdAt, m.now)
		if err != nil {
			return fmt.Errorf("create legacy billing account for user %d: %w", userID, err)
		}
		accountID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		ledgerResult, err := m.tx.ExecContext(m.ctx, `INSERT INTO ledger_accounts(billing_account_id,currency_code,currency_version,balance,created_at) VALUES (?,?,?,0,?)`, accountID, currency, currencyVersion, createdAt)
		if err != nil {
			return err
		}
		userLedgerID, err := ledgerResult.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO billing_account_state_events(billing_account_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,1,NULL,?,'legacy_balance_import',?)`, accountID, accountStatus, createdAt); err != nil {
			return err
		}
		if balanceValue.Sign() != 0 {
			if err := m.postLegacyOpeningBalance(accountID, userLedgerID, userID, currency, currencyVersion, balanceValue); err != nil {
				return err
			}
		}
		snapshotResult, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_legacy_account_snapshots(migration_run_id,subject_type,source_id,user_id,token_id,balance,total_used,billing_account_id,budget_window_id,source_hmac,source_created_at,created_at) VALUES (?,'user',?,?,NULL,?,0,?,NULL,?,?,?)`, m.runID, userID, userID, balanceValue.String(), accountID, row.Revision, createdAt, m.now)
		if err != nil {
			return err
		}
		snapshotID, err := snapshotResult.LastInsertId()
		if err != nil {
			return err
		}
		if err := m.createMapping(row, "account_snapshot", snapshotID, "user:"+strconv.FormatUint(userID, 10)); err != nil {
			return err
		}
		m.report.BillingAccounts++
	}
	return nil
}

func (m *runtimeImporter) importTokenBudgetSnapshots(rows []runtimeSourceRow) error {
	for _, row := range rows {
		if _, exists, err := m.existingMapping(row, "account_snapshot", "gw_legacy_account_snapshots"); err != nil {
			return err
		} else if exists {
			continue
		}
		tokenID, idErr := runtimeUint(row, "id")
		userID, userErr := runtimeUint(row, "user_id")
		balanceValue, balanceErr := billing.ParseAmount(row.text("balance"), 18, false)
		usedValue, usedErr := billing.ParseAmount(row.text("total_used"), 18, true)
		createdAt, timeOK := runtimeTime(row, "created_at")
		statusValue := row.text("status")
		owner, ownerOK := m.owners[tokenID]
		if idErr != nil || userErr != nil || tokenID == 0 || userID == 0 || !ownerOK || owner.UserID != userID || balanceErr != nil || usedErr != nil || !timeOK || statusValue != "0" && statusValue != "1" {
			m.issue(row, "budget_snapshot_invalid", "token ownership, balance, usage, state, or creation time is invalid")
			continue
		}
		if balanceValue.Sign() < 0 {
			m.issue(row, "negative_token_balance", "negative token balance requires manual reconciliation")
			continue
		}
		limitValue := balanceValue.Add(usedValue)
		if limitValue.Sign() < 0 {
			m.issue(row, "budget_snapshot_invalid", "token balance plus total usage is negative")
			continue
		}
		var billingAccountID int64
		if err := m.tx.QueryRowContext(m.ctx, `SELECT id FROM billing_accounts WHERE user_id=?`, userID).Scan(&billingAccountID); err == sql.ErrNoRows {
			m.issue(row, "billing_account_missing", "token owner has no imported billing account")
			continue
		} else if err != nil {
			return err
		}
		var existing int64
		if err := m.tx.QueryRowContext(m.ctx, `SELECT COUNT(*) FROM token_budget_policies WHERE token_id=?`, tokenID).Scan(&existing); err != nil {
			return err
		}
		if existing != 0 {
			m.issue(row, "budget_policy_conflict", "a token budget already exists without this verified source mapping")
			continue
		}
		policyCode := "legacy-opening-" + strconv.FormatUint(tokenID, 10)
		policyResult, err := m.tx.ExecContext(m.ctx, `INSERT INTO token_budget_policies(token_id,policy_code,window_kind,limit_amount,period_seconds,timezone_name,algorithm_version,created_at) VALUES (?,?,'lifetime',?,NULL,'UTC',1,?)`, tokenID, policyCode, limitValue.String(), m.now)
		if err != nil {
			return err
		}
		policyID, err := policyResult.LastInsertId()
		if err != nil {
			return err
		}
		activationResult, err := m.tx.ExecContext(m.ctx, `INSERT INTO token_budget_policy_activations(token_id,policy_id,activation_seq,predecessor_id,effective_at,created_at) VALUES (?,?,1,NULL,?,?)`, tokenID, policyID, m.now, m.now)
		if err != nil {
			return err
		}
		activationID, err := activationResult.LastInsertId()
		if err != nil {
			return err
		}
		windowResult, err := m.tx.ExecContext(m.ctx, `INSERT INTO token_budget_windows(token_id,policy_id,activation_id,window_start,window_end,limit_amount,used_amount,held_amount,created_at) VALUES (?,?,?,?,NULL,?,?,0,?)`, tokenID, policyID, activationID, m.now, limitValue.String(), usedValue.String(), m.now)
		if err != nil {
			return err
		}
		windowID, err := windowResult.LastInsertId()
		if err != nil {
			return err
		}
		snapshotResult, err := m.tx.ExecContext(m.ctx, `INSERT INTO gw_legacy_account_snapshots(migration_run_id,subject_type,source_id,user_id,token_id,balance,total_used,billing_account_id,budget_window_id,source_hmac,source_created_at,created_at) VALUES (?,'token',?,?,?,?,?,?,?,?,?,?)`, m.runID, tokenID, userID, tokenID, balanceValue.String(), usedValue.String(), billingAccountID, windowID, row.Revision, createdAt, m.now)
		if err != nil {
			return err
		}
		snapshotID, err := snapshotResult.LastInsertId()
		if err != nil {
			return err
		}
		if err := m.createMapping(row, "account_snapshot", snapshotID, "token:"+strconv.FormatUint(tokenID, 10)); err != nil {
			return err
		}
		m.report.BudgetWindows++
	}
	return nil
}

func (m *runtimeImporter) postLegacyOpeningBalance(accountID, userLedgerID int64, userID uint64, currency string, currencyVersion uint32, amount billing.Amount) error {
	if amount.Sign() <= 0 {
		return fmt.Errorf("legacy opening balance must be positive")
	}
	const eventType = "funding_credit"
	const expectedDirection = "credit"
	const systemDirection = "debit"
	const userDirection = "credit"
	var ruleID, systemLedgerID int64
	var amountSource, direction, systemAccount, budgetEffect string
	if err := m.tx.QueryRowContext(m.ctx, `SELECT id,amount_source,user_direction,system_account_code,budget_effect FROM billing_posting_rules WHERE event_type=? AND currency_code=? AND currency_version=? ORDER BY rule_version DESC LIMIT 1 FOR SHARE`, eventType, currency, currencyVersion).Scan(&ruleID, &amountSource, &direction, &systemAccount, &budgetEffect); err != nil {
		return err
	}
	if amountSource != "event_amount" || direction != expectedDirection || systemAccount != "funding_clearing" || budgetEffect != "none" {
		return fmt.Errorf("legacy opening balance posting rule is incompatible")
	}
	if err := m.tx.QueryRowContext(m.ctx, `SELECT id FROM ledger_accounts WHERE system_account_code=? AND currency_code=? AND currency_version=? FOR UPDATE`, systemAccount, currency, currencyVersion).Scan(&systemLedgerID); err != nil {
		return err
	}
	eventResult, err := m.tx.ExecContext(m.ctx, `INSERT INTO billing_events(billing_account_id,reservation_id,call_id,event_key,event_type,amount,currency_code,currency_version,state_version,posting_rule_id,created_at) VALUES (?,NULL,NULL,?,?,?,?,?,2,?,?)`, accountID, fmt.Sprintf("legacy-opening:user:%d", userID), eventType, amount.String(), currency, currencyVersion, ruleID, m.now)
	if err != nil {
		return err
	}
	eventID, err := eventResult.LastInsertId()
	if err != nil {
		return err
	}
	transactionResult, err := m.tx.ExecContext(m.ctx, `INSERT INTO ledger_transactions(billing_event_id,currency_code,currency_version,state,created_at,posted_at) VALUES (?,?,?,'posted',?,?)`, eventID, currency, currencyVersion, m.now, m.now)
	if err != nil {
		return err
	}
	transactionID, err := transactionResult.LastInsertId()
	if err != nil {
		return err
	}
	for _, entry := range []struct {
		accountID int64
		code      string
		direction string
	}{
		{userLedgerID, "user", userDirection},
		{systemLedgerID, "system", systemDirection},
	} {
		if _, err := m.tx.ExecContext(m.ctx, `INSERT INTO ledger_entries(ledger_transaction_id,ledger_account_id,currency_code,currency_version,entry_code,direction,amount,created_at) VALUES (?,?,?,?,?,?,?,?)`, transactionID, entry.accountID, currency, currencyVersion, entry.code, entry.direction, amount.String(), m.now); err != nil {
			return err
		}
		operator := "+"
		if entry.direction == "credit" {
			operator = "-"
		}
		if _, err := m.tx.ExecContext(m.ctx, `UPDATE ledger_accounts SET balance=balance`+operator+`? WHERE id=?`, amount.String(), entry.accountID); err != nil {
			return err
		}
	}
	return nil
}

func runtimeDeleted(row runtimeSourceRow) bool {
	value := row.value("deleted_at")
	return value.Valid && strings.TrimSpace(value.String()) != ""
}
