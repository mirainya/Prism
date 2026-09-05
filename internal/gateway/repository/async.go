package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/execution"
)

type CreateAsyncInput struct {
	AttemptID uint64
	ScopeKind string
	ScopeKey  string
}

func (s *Store) CreateAsyncExecution(ctx context.Context, tx *sql.Tx, in CreateAsyncInput) (uint64, error) {
	if tx == nil || in.AttemptID == 0 || in.ScopeKind == "" || in.ScopeKey == "" || len(in.ScopeKey) > 255 {
		return 0, ErrInvalidInput
	}
	if !validScope(in.ScopeKind) {
		return 0, ErrInvalidInput
	}
	var attemptID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_api_call_attempts WHERE id=? FOR SHARE`, in.AttemptID).Scan(&attemptID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_async_executions(attempt_id,upstream_scope_kind,upstream_scope_key,state,state_version,action_seq,created_at,updated_at) VALUES (?,?,?,'allocated',1,0,?,?)`, in.AttemptID, in.ScopeKind, in.ScopeKey, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert async execution: %w", err)
	}
	return lastID(result)
}

func (s *Store) TransitionAsync(ctx context.Context, tx *sql.Tx, asyncID uint64, from execution.AsyncState, to execution.AsyncState, expectedVersion uint64, reason string, action string) (uint64, error) {
	if tx == nil || asyncID == 0 || reason == "" {
		return 0, ErrInvalidInput
	}
	if action != "" && !validAsyncAction(action) {
		return 0, ErrInvalidInput
	}
	if err := execution.TransitionAsync(from, to); err != nil {
		return 0, err
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `UPDATE gw_async_executions SET state=?,state_version=state_version+1,action_seq=action_seq+1,updated_at=? WHERE id=? AND state=? AND state_version=?`, string(to), now, asyncID, string(from), expectedVersion)
	if err != nil {
		return 0, fmt.Errorf("transition async execution: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrConflict
	}
	var actionSeq uint64
	if err = tx.QueryRowContext(ctx, `SELECT action_seq FROM gw_async_executions WHERE id=?`, asyncID).Scan(&actionSeq); err != nil {
		return 0, fmt.Errorf("read async action sequence: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(async_execution_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)`, asyncID, string(from), string(to), expectedVersion+1, reason, now); err != nil {
		return 0, fmt.Errorf("async transition event: %w", err)
	}
	if action != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO gw_async_outbox(async_execution_id,action_seq,action,status,state_version,available_at,attempt_count,created_at,updated_at) VALUES (?,?,?,'pending',?,?,0,?,?)`, asyncID, actionSeq, action, expectedVersion+1, now, now, now); err != nil {
			return 0, fmt.Errorf("async outbox: %w", err)
		}
	}
	return actionSeq, nil
}

type TaskIdentityInput struct {
	AsyncExecutionID    uint64
	ScopeKind, ScopeKey string
	EncryptedBlobID     uint64
	ExpiresAt           time.Time
}

// BindTaskIdentity performs the one-time async->upstream identity binding.
// The unique async_execution_id and alias constraints make duplicate callback
// delivery harmless; a different identity never overwrites the first one.
func (s *Store) BindTaskIdentity(ctx context.Context, tx *sql.Tx, in TaskIdentityInput, aliases []TaskAliasInput) (uint64, error) {
	if tx == nil || in.AsyncExecutionID == 0 || in.ScopeKind == "" || in.ScopeKey == "" || len(in.ScopeKey) > 255 || in.EncryptedBlobID == 0 || in.ExpiresAt.IsZero() || !in.ExpiresAt.After(nowUTC()) {
		return 0, ErrInvalidInput
	}
	if !validScope(in.ScopeKind) {
		return 0, ErrInvalidInput
	}
	for _, alias := range aliases {
		if alias.ScopeKind == "" || alias.ScopeKey == "" || len(alias.ScopeKey) > 255 || alias.HMACKeyVersion == 0 || !validHexDigest(alias.ValueHMAC, 32) || !validScope(alias.ScopeKind) {
			return 0, ErrInvalidInput
		}
	}
	var existingID, existingBlobID uint64
	var existingScopeKind, existingScopeKey string
	err := tx.QueryRowContext(ctx, `SELECT id,scope_kind,scope_key,encrypted_blob_id FROM gw_upstream_task_identities WHERE async_execution_id=? FOR UPDATE`, in.AsyncExecutionID).Scan(&existingID, &existingScopeKind, &existingScopeKey, &existingBlobID)
	if err == nil {
		if existingScopeKind != in.ScopeKind || existingScopeKey != in.ScopeKey || existingBlobID != in.EncryptedBlobID {
			return 0, ErrConflict
		}
		return existingID, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_task_identities(async_execution_id,scope_kind,scope_key,encrypted_blob_id,status,state_version,expires_at,created_at,updated_at) VALUES (?,?,?,?, 'bound',1,?,?,?)`, in.AsyncExecutionID, in.ScopeKind, in.ScopeKey, in.EncryptedBlobID, in.ExpiresAt.UTC(), now, now)
	if err != nil {
		if readErr := tx.QueryRowContext(ctx, `SELECT id,scope_kind,scope_key,encrypted_blob_id FROM gw_upstream_task_identities WHERE async_execution_id=? FOR UPDATE`, in.AsyncExecutionID).Scan(&existingID, &existingScopeKind, &existingScopeKey, &existingBlobID); readErr == nil {
			if existingScopeKind == in.ScopeKind && existingScopeKey == in.ScopeKey && existingBlobID == in.EncryptedBlobID {
				return existingID, nil
			}
			return 0, ErrConflict
		}
		return 0, fmt.Errorf("bind task identity: %w", err)
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	for _, alias := range aliases {
		if _, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_task_id_aliases(task_identity_id,scope_kind,scope_key,hmac_key_version,value_hmac,matchable,created_at) VALUES (?,?,?,?,?,true,?)`, id, alias.ScopeKind, alias.ScopeKey, alias.HMACKeyVersion, alias.ValueHMAC, now); err != nil {
			return 0, fmt.Errorf("bind task alias: %w", err)
		}
	}
	return id, nil
}

type TaskAliasInput struct {
	ScopeKind, ScopeKey string
	HMACKeyVersion      uint32
	ValueHMAC           string
}

type CallbackBindingAliasInput struct {
	AsyncExecutionID uint64
	HMACKeyVersion   uint32
	ValueHMAC        string
	EncryptedBlobID  uint64
}

func (s *Store) AddCallbackBindingAlias(ctx context.Context, tx *sql.Tx, in CallbackBindingAliasInput) (uint64, error) {
	if tx == nil || in.AsyncExecutionID == 0 || in.HMACKeyVersion == 0 || !validHexDigest(in.ValueHMAC, 32) || in.EncryptedBlobID == 0 {
		return 0, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_callback_binding_token_aliases(async_execution_id,hmac_key_version,value_hmac,encrypted_blob_id,created_at) VALUES (?,?,?,?,?)`, in.AsyncExecutionID, in.HMACKeyVersion, in.ValueHMAC, in.EncryptedBlobID, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("add callback binding alias: %w", err)
	}
	return lastID(result)
}

func validScope(value string) bool {
	switch value {
	case "global", "channel", "product_transport", "credential_pool", "credential":
		return true
	default:
		return false
	}
}

func validAsyncAction(value string) bool {
	switch value {
	case "submit", "recover", "query", "cancel", "reconcile_delivery", "callback":
		return true
	default:
		return false
	}
}

type OutboxItem struct {
	ID, AsyncExecutionID, AttemptID, CallID, ActionSeq, StateVersion uint64
	Action                                                           string
	Attempts                                                         uint64
	LeaseOwner                                                       string
	LeaseExpiresAt                                                   time.Time
	// MayHaveDispatched is true for a reclaimed lease or a retry. Handlers
	// must recover the upstream fact before sending again.
	MayHaveDispatched bool
	AvailableAt       time.Time
}

func (s *Store) ClaimAsyncOutbox(ctx context.Context, tx *sql.Tx, owner string, lease time.Duration) (OutboxItem, error) {
	if tx == nil || owner == "" || lease <= 0 {
		return OutboxItem{}, ErrInvalidInput
	}
	var item OutboxItem
	var attemptID, callID, asyncID sql.NullInt64
	var available time.Time
	var previousExpiry sql.NullTime
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id,call_id,attempt_id,async_execution_id,action_seq,action,state_version,attempt_count,available_at,status,lease_expires_at FROM gw_async_outbox WHERE ((status='pending' AND available_at<=UTC_TIMESTAMP(3)) OR (status='dispatching' AND lease_expires_at<=UTC_TIMESTAMP(3))) ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&item.ID, &callID, &attemptID, &asyncID, &item.ActionSeq, &item.Action, &item.StateVersion, &item.Attempts, &available, &status, &previousExpiry)
	if err == sql.ErrNoRows {
		return OutboxItem{}, ErrNotFound
	}
	if err != nil {
		return OutboxItem{}, fmt.Errorf("claim outbox: %w", err)
	}
	item.CallID, item.AttemptID, item.AsyncExecutionID = uint64(callID.Int64), uint64(attemptID.Int64), uint64(asyncID.Int64)
	item.AvailableAt = available
	item.MayHaveDispatched = status == "dispatching" || item.Attempts > 0
	expires := nowUTC().Add(lease)
	result, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='dispatching',lease_owner=?,lease_expires_at=?,attempt_count=attempt_count+1,updated_at=? WHERE id=? AND (status='pending' OR (status='dispatching' AND lease_expires_at<=UTC_TIMESTAMP(3)))`, owner, expires, nowUTC(), item.ID)
	if err != nil {
		return OutboxItem{}, fmt.Errorf("lease outbox: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return OutboxItem{}, err
	}
	if !ok {
		return OutboxItem{}, ErrConflict
	}
	item.Attempts++
	item.LeaseOwner = owner
	item.LeaseExpiresAt = expires
	return item, nil
}

// AssertAsyncOutboxLease is the fence handlers use in the same transaction as
// their state mutation. A late worker can never write after another worker
// reclaimed the row.
func (s *Store) AssertAsyncOutboxLease(ctx context.Context, tx *sql.Tx, item OutboxItem) error {
	if tx == nil || item.ID == 0 || item.LeaseOwner == "" || item.Attempts == 0 {
		return ErrInvalidInput
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM gw_async_outbox WHERE id=? AND status='dispatching' AND lease_owner=? AND attempt_count=? AND lease_expires_at>UTC_TIMESTAMP(3) FOR UPDATE`, item.ID, item.LeaseOwner, item.Attempts).Scan(&n)
	if err == sql.ErrNoRows {
		return ErrConflict
	}
	return err
}

func (s *Store) CompleteAsyncOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, success bool, errorCode string) error {
	if tx == nil || item.ID == 0 || item.LeaseOwner == "" || item.Attempts == 0 {
		return ErrInvalidInput
	}
	status := "failed"
	if success {
		status = "succeeded"
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status=?,last_error_code=?,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='dispatching' AND lease_owner=? AND attempt_count=? AND lease_expires_at>UTC_TIMESTAMP(3)`, status, errorCode, nowUTC(), item.ID, item.LeaseOwner, item.Attempts)
	if err != nil {
		return fmt.Errorf("complete outbox: %w", err)
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

func (s *Store) RetryAsyncOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, errorCode string, availableAt time.Time) error {
	if tx == nil || item.ID == 0 || item.LeaseOwner == "" || item.Attempts == 0 || availableAt.IsZero() {
		return ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='pending',last_error_code=?,available_at=?,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='dispatching' AND lease_owner=? AND attempt_count=?`, errorCode, availableAt.UTC(), nowUTC(), item.ID, item.LeaseOwner, item.Attempts)
	if err != nil {
		return fmt.Errorf("retry outbox: %w", err)
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

func (s *Store) DeadLetterAsyncOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, errorCode string) error {
	if tx == nil || item.ID == 0 || item.LeaseOwner == "" || item.Attempts == 0 || errorCode == "" {
		return ErrInvalidInput
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='dead_letter',last_error_code=?,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='dispatching' AND lease_owner=? AND attempt_count=?`, errorCode, nowUTC(), item.ID, item.LeaseOwner, item.Attempts)
	if err != nil {
		return err
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}
