package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
)

type CallLease struct {
	CallID, AttemptID uint64
	Owner             string
	ExpiresAt         time.Time
}

func (s *Store) ClaimCallLease(ctx context.Context, tx *sql.Tx, callID, attemptID uint64, owner string, lease time.Duration) (CallLease, error) {
	if tx == nil || callID == 0 || attemptID == 0 || owner == "" || len(owner) > 128 || lease <= 0 {
		return CallLease{}, ErrInvalidInput
	}
	expiresAt := nowUTC().Add(lease).Truncate(time.Millisecond)
	return s.claimCallLeaseUntil(ctx, tx, callID, attemptID, owner, expiresAt)
}

func (s *Store) claimCallLeaseUntil(ctx context.Context, tx *sql.Tx, callID, attemptID uint64, owner string, expiresAt time.Time) (CallLease, error) {
	if tx == nil || callID == 0 || attemptID == 0 || owner == "" || len(owner) > 128 || expiresAt.IsZero() {
		return CallLease{}, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_api_calls
SET lease_owner=?,lease_expires_at=?,next_action_at=NULL,updated_at=?
WHERE id=? AND current_attempt_id=? AND status='in_progress'
  AND (lease_owner='' OR lease_expires_at IS NULL OR lease_expires_at<=CURRENT_TIMESTAMP(3))`,
		owner, expiresAt.UTC(), nowUTC(), callID, attemptID)
	if err != nil {
		return CallLease{}, fmt.Errorf("claim call lease: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return CallLease{}, err
	}
	if !ok {
		return CallLease{}, ErrConflict
	}
	return CallLease{CallID: callID, AttemptID: attemptID, Owner: owner, ExpiresAt: expiresAt.UTC()}, nil
}

// AssertCallLease locks and verifies the exact lease generation. ExpiresAt is
// part of the fence so reusing an owner name cannot authorize an older worker.
func (s *Store) AssertCallLease(ctx context.Context, tx *sql.Tx, lease CallLease) error {
	if tx == nil || lease.CallID == 0 || lease.AttemptID == 0 || lease.Owner == "" || len(lease.Owner) > 128 || lease.ExpiresAt.IsZero() {
		return ErrInvalidInput
	}
	var id uint64
	err := tx.QueryRowContext(ctx, `SELECT id FROM gw_api_calls
WHERE id=? AND current_attempt_id=? AND status='in_progress'
  AND lease_owner=? AND lease_expires_at=? AND lease_expires_at>CURRENT_TIMESTAMP(3)
FOR UPDATE`, lease.CallID, lease.AttemptID, lease.Owner, lease.ExpiresAt.UTC()).Scan(&id)
	if err == sql.ErrNoRows {
		return ErrConflict
	}
	return err
}

func (s *Store) RenewCallLease(ctx context.Context, tx *sql.Tx, lease CallLease, duration time.Duration) (CallLease, error) {
	if duration <= 0 {
		return CallLease{}, ErrInvalidInput
	}
	expiresAt := nowUTC().Add(duration).Truncate(time.Millisecond)
	return s.renewCallLeaseUntil(ctx, tx, lease, expiresAt)
}

func (s *Store) renewCallLeaseUntil(ctx context.Context, tx *sql.Tx, lease CallLease, expiresAt time.Time) (CallLease, error) {
	if err := s.AssertCallLease(ctx, tx, lease); err != nil {
		return CallLease{}, err
	}
	if !expiresAt.After(lease.ExpiresAt) {
		return CallLease{}, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_api_calls SET lease_expires_at=?,updated_at=?
WHERE id=? AND current_attempt_id=? AND status='in_progress' AND lease_owner=? AND lease_expires_at=?`,
		expiresAt, nowUTC(), lease.CallID, lease.AttemptID, lease.Owner, lease.ExpiresAt.UTC())
	if err != nil {
		return CallLease{}, fmt.Errorf("renew call lease: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return CallLease{}, err
	}
	if !ok {
		return CallLease{}, ErrConflict
	}
	lease.ExpiresAt = expiresAt
	return lease, nil
}

// ReleaseCallLease matches the exact lease even after a terminal transition
// has cleared current_attempt_id. This lets result, Outbox, and lease updates
// commit atomically without weakening the earlier assertion.
func (s *Store) ReleaseCallLease(ctx context.Context, tx *sql.Tx, lease CallLease) error {
	if tx == nil || lease.CallID == 0 || lease.AttemptID == 0 || lease.Owner == "" || len(lease.Owner) > 128 || lease.ExpiresAt.IsZero() {
		return ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_api_calls SET lease_owner='',lease_expires_at=NULL,updated_at=?
WHERE id=? AND lease_owner=? AND lease_expires_at=?
  AND EXISTS (SELECT 1 FROM gw_api_call_attempts a WHERE a.id=? AND a.call_id=gw_api_calls.id)`,
		nowUTC(), lease.CallID, lease.Owner, lease.ExpiresAt.UTC(), lease.AttemptID)
	if err != nil {
		return fmt.Errorf("release call lease: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

type CreateCallInput struct {
	PublicID                                                       string
	UserID, TokenID                                                uint64
	OperationContractID, CatalogReleaseID, ModelOperationID, SKUID uint64
	RequestPayloadID, ResultPayloadID                              *uint64
	QuotedAmount                                                   string
	Currency                                                       string
	CurrencyVersion                                                uint32
	DeliveryMode                                                   string
	XFSAPIKey                                                      string
}

// CreateCall inserts a call and its immutable identity. Quoted amount is a
// canonical decimal string; the database DECIMAL column performs no float
// conversion.
func (s *Store) CreateCall(ctx context.Context, db DB, in CreateCallInput) (uint64, error) {
	if db == nil || in.PublicID == "" || in.UserID == 0 || in.TokenID == 0 || in.OperationContractID == 0 || in.CatalogReleaseID == 0 || in.ModelOperationID == 0 || in.SKUID == 0 || in.Currency == "" || in.CurrencyVersion == 0 || in.DeliveryMode == "" {
		return 0, ErrInvalidInput
	}
	if in.QuotedAmount == "" {
		in.QuotedAmount = "0"
	}
	quoted, err := billing.ParseAmount(in.QuotedAmount, 18, true)
	if err != nil {
		return 0, ErrInvalidInput
	}
	if err := validateCurrencyAmount(ctx, db, in.Currency, in.CurrencyVersion, quoted); err != nil {
		return 0, err
	}
	if in.DeliveryMode != "reference" && in.DeliveryMode != "managed_copy" {
		return 0, ErrInvalidInput
	}
	in.XFSAPIKey = strings.TrimSpace(in.XFSAPIKey)
	if len(in.XFSAPIKey) > 512 || in.XFSAPIKey != "" && in.DeliveryMode != "managed_copy" {
		return 0, ErrInvalidInput
	}
	now := nowUTC()
	result, err := db.ExecContext(ctx, `INSERT INTO gw_api_calls
	 (public_id,user_id,token_id,operation_contract_id,catalog_release_id,model_operation_id,sku_id,request_payload_id,result_payload_id,status,state_version,quoted_amount,price_currency,price_currency_version,delivery_mode,xfs_api_key,created_at,updated_at)
	 VALUES (?,?,?,?,?,?,?,?,?,'received',1,?,?,?,?,?,?,?)`, in.PublicID, in.UserID, in.TokenID, in.OperationContractID, in.CatalogReleaseID, in.ModelOperationID, in.SKUID, nullableID(in.RequestPayloadID), nullableID(in.ResultPayloadID), quoted.String(), in.Currency, in.CurrencyVersion, in.DeliveryMode, in.XFSAPIKey, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert call: %w", err)
	}
	return lastID(result)
}

type BeginAttemptInput struct {
	CallID, CatalogReleaseID, SKUID, RouteID, OfferingID, CostPlanID, ProductTransportID uint64
	CredentialPoolID, CredentialID, CredentialVersionID, PurposeGrantID                  uint64
}

// BeginAttempt and the conditional call update must run in the same SQL
// transaction. The generated unique active marker is the final database guard
// against two workers owning one call concurrently.
func (s *Store) BeginAttempt(ctx context.Context, tx *sql.Tx, in BeginAttemptInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.CatalogReleaseID == 0 || in.SKUID == 0 || in.RouteID == 0 || in.OfferingID == 0 || in.CostPlanID == 0 || in.ProductTransportID == 0 || in.CredentialPoolID == 0 || in.CredentialID == 0 || in.CredentialVersionID == 0 || in.PurposeGrantID == 0 {
		return 0, ErrInvalidInput
	}
	var callStatus string
	var releaseID, skuID uint64
	var currentAttempt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT status,catalog_release_id,sku_id,current_attempt_id FROM gw_api_calls WHERE id=? FOR UPDATE`, in.CallID).Scan(&callStatus, &releaseID, &skuID, &currentAttempt); err != nil {
		return 0, err
	}
	if currentAttempt.Valid || releaseID != in.CatalogReleaseID || skuID != in.SKUID || (callStatus != "received" && callStatus != "retry_pending" && callStatus != "in_progress") {
		return 0, ErrConflict
	}
	// The call row serializes attempts; read the latest number before pool locks.
	var attemptNo uint64
	var previousState string
	if err := tx.QueryRowContext(ctx, `SELECT attempt_no,state FROM gw_api_call_attempts WHERE call_id=? ORDER BY attempt_no DESC LIMIT 1 FOR UPDATE`, in.CallID).Scan(&attemptNo, &previousState); err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("allocate attempt number: %w", err)
	}
	if previousState != "" && previousState != "failed" && previousState != "not_created" {
		return 0, ErrConflict
	}
	attemptNo++
	if err := lockAdmissionChannel(ctx, tx, in.CredentialPoolID); err != nil {
		return 0, err
	}
	var poolStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gw_credential_pools WHERE id=? FOR UPDATE`, in.CredentialPoolID).Scan(&poolStatus); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if poolStatus != "active" {
		return 0, ErrConflict
	}
	var credentialStatus string
	var currentVersion sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT status,current_version_id FROM gw_credentials WHERE id=? AND credential_pool_id=? FOR UPDATE`, in.CredentialID, in.CredentialPoolID).Scan(&credentialStatus, &currentVersion); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if credentialStatus != "active" || !currentVersion.Valid || uint64(currentVersion.Int64) != in.CredentialVersionID {
		return 0, ErrConflict
	}
	var versionStatus, secretStatus string
	var validUntil sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT v.status,v.valid_until,i.status FROM gw_credential_versions v JOIN gw_credential_secret_identities i ON i.id=v.secret_identity_id WHERE v.id=? AND v.credential_id=? FOR SHARE`, in.CredentialVersionID, in.CredentialID).Scan(&versionStatus, &validUntil, &secretStatus); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if versionStatus != "active" || secretStatus != "active" || validUntil.Valid && !validUntil.Time.After(nowUTC()) {
		return 0, ErrConflict
	}
	var grantStatus, grantPurpose string
	if err := tx.QueryRowContext(ctx, `SELECT status,purpose FROM gw_credential_purpose_grants WHERE id=? AND credential_id=? FOR SHARE`, in.PurposeGrantID, in.CredentialID).Scan(&grantStatus, &grantPurpose); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if grantStatus != "active" || grantPurpose != "execution" {
		return 0, ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_api_call_attempts
	 (call_id,attempt_no,catalog_release_id,sku_id,route_id,offering_id,cost_plan_id,product_transport_id,credential_pool_id,credential_id,credential_version_id,purpose_grant_id,state,state_version,created_at,updated_at)
	 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,'started',1,?,?)`, in.CallID, attemptNo, in.CatalogReleaseID, in.SKUID, in.RouteID, in.OfferingID, in.CostPlanID, in.ProductTransportID, in.CredentialPoolID, in.CredentialID, in.CredentialVersionID, in.PurposeGrantID, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert attempt: %w", err)
	}
	attemptID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE gw_api_calls SET current_attempt_id=?,status='in_progress',state_version=state_version+1,updated_at=? WHERE id=? AND current_attempt_id IS NULL AND status IN ('received','retry_pending','in_progress')`, attemptID, now, in.CallID)
	if err != nil {
		return 0, fmt.Errorf("claim call: %w", err)
	}
	ok, err := affected(updated)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrConflict
	}
	return attemptID, nil
}

func (s *Store) TransitionCall(ctx context.Context, tx *sql.Tx, callID uint64, from execution.CallState, to execution.CallState, expectedVersion uint64, reason string, finalAttemptID *uint64) error {
	if tx == nil || callID == 0 || reason == "" {
		return ErrInvalidInput
	}
	if err := execution.TransitionCall(from, to); err != nil {
		return err
	}
	if !isTerminalCall(to) {
		finalAttemptID = nil
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `UPDATE gw_api_calls SET status=?,state_version=state_version+1,current_attempt_id=CASE WHEN ? THEN NULL ELSE current_attempt_id END,final_attempt_id=COALESCE(?,final_attempt_id),updated_at=? WHERE id=? AND status=? AND state_version=?`, string(to), isTerminalCall(to) || to == execution.CallRetryPending, nullableID(finalAttemptID), now, callID, string(from), expectedVersion)
	if err != nil {
		return fmt.Errorf("transition call: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(call_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)`, callID, string(from), string(to), expectedVersion+1, reason, now); err != nil {
		return fmt.Errorf("call transition event: %w", err)
	}
	return nil
}

func (s *Store) TransitionAttempt(ctx context.Context, tx *sql.Tx, attemptID uint64, from execution.AttemptState, to execution.AttemptState, expectedVersion uint64, reason string) error {
	if tx == nil || attemptID == 0 || reason == "" {
		return ErrInvalidInput
	}
	if err := execution.TransitionAttempt(from, to); err != nil {
		return err
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `UPDATE gw_api_call_attempts SET state=?,state_version=state_version+1,updated_at=? WHERE id=? AND state=? AND state_version=?`, string(to), now, attemptID, string(from), expectedVersion)
	if err != nil {
		return fmt.Errorf("transition attempt: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(attempt_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)`, attemptID, string(from), string(to), expectedVersion+1, reason, now); err != nil {
		return fmt.Errorf("attempt transition event: %w", err)
	}
	return nil
}

func isTerminalCall(state execution.CallState) bool {
	return state == execution.CallCompleted || state == execution.CallFailed || state == execution.CallCancelled || state == execution.CallIndeterminate
}

func nullableID(value *uint64) any {
	if value == nil {
		return nil
	}
	return *value
}
