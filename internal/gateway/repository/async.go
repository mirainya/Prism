package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/security"
)

type CreateAsyncInput struct {
	AttemptID       uint64
	ScopeKind       string
	ScopeKey        string
	CallbackBinding *CallbackBindingInput
}

// CallbackBindingInput contains the payload key material required to create
// an execution-scoped callback token. HMACKeys must contain every current or
// readable payload HMAC version; the current version is also accepted through
// HMACKey for callers that have not yet started key rotation.
type CallbackBindingInput struct {
	KeyringID  uint64
	KEKVersion uint32
	KEK        []byte
	HMACKey    []byte
	HMACKeys   map[uint32][]byte
}

const callbackBindingTokenDomain = "gateway-callback-binding-token-v1"

// Callback binding tokens are intentionally short-lived.  The provider
// callback endpoint is an execution ingress, not a permanent capability.
// CallbackBindingTokenHMAC computes the non-reversible alias stored in the
// database. The domain separator prevents a token digest from being reused as
// an identity in another gateway subsystem.
func CallbackBindingTokenHMAC(key []byte, token string) string {
	digest := security.DomainDigest(key, callbackBindingTokenDomain, []byte(token))
	return fmt.Sprintf("%x", digest[:])
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
	asyncID, err := lastID(result)
	if err != nil {
		return 0, err
	}
	if in.CallbackBinding == nil {
		// Keep the low-level repository API usable by migration/import callers;
		// production submission paths always provide the binding material.
		return asyncID, nil
	}
	if err := s.CreateCallbackBindingToken(ctx, tx, asyncID, *in.CallbackBinding); err != nil {
		return 0, err
	}
	return asyncID, nil
}

// CreateCallbackBindingToken creates the one execution-scoped token and all
// aliases in the same transaction as the AsyncExecution. The plaintext is
// only returned to the caller through the encrypted Blob's normal dispatch
// path; this method deliberately returns no token value.
func (s *Store) CreateCallbackBindingToken(ctx context.Context, tx *sql.Tx, asyncID uint64, in CallbackBindingInput) error {
	if s == nil || tx == nil || asyncID == 0 || in.KeyringID == 0 || in.KEKVersion == 0 || len(in.KEK) != security.KeySize {
		return ErrInvalidInput
	}
	hmacKeys := cloneHMACKeyMap(in.HMACKeys)
	defer clearHMACKeyMap(hmacKeys)
	if len(in.HMACKey) == security.KeySize {
		if _, ok := hmacKeys[in.KEKVersion]; !ok {
			hmacKeys[in.KEKVersion] = append([]byte(nil), in.HMACKey...)
		}
	}
	if len(hmacKeys) == 0 {
		return ErrInvalidInput
	}
	var parentID uint64
	if err := tx.QueryRowContext(ctx, `SELECT attempt_id FROM gw_async_executions WHERE id=? FOR UPDATE`, asyncID).Scan(&parentID); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var currentKeyringID uint64
	var currentVersion uint32
	if err := tx.QueryRowContext(ctx, `SELECT id,current_version FROM crypto_keyring_state WHERE id=? AND purpose='gateway-payload' AND current_version IS NOT NULL FOR SHARE`, in.KeyringID).Scan(&currentKeyringID, &currentVersion); err != nil {
		return err
	}
	if currentVersion != in.KEKVersion {
		return ErrConflict
	}
	rows, err := tx.QueryContext(ctx, `SELECT key_version,status FROM crypto_key_versions WHERE keyring_id=? AND status IN ('current','readable') ORDER BY key_version`, in.KeyringID)
	if err != nil {
		return err
	}
	versions := make([]uint32, 0, 2)
	for rows.Next() {
		var version uint32
		var status string
		if err := rows.Scan(&version, &status); err != nil {
			rows.Close()
			return err
		}
		if version == 0 {
			rows.Close()
			return ErrConflict
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(versions) == 0 {
		return ErrConflict
	}
	for _, version := range versions {
		if len(hmacKeys[version]) != security.KeySize {
			return fmt.Errorf("payload HMAC version %d is unavailable: %w", version, ErrConflict)
		}
	}
	// A retry inside the same transaction is harmless. This also makes the
	// method safe for callers that recover after an interrupted pre-commit
	// operation in databases supporting nested application retries.
	var existingBlob uint64
	if err := tx.QueryRowContext(ctx, `SELECT encrypted_blob_id FROM gw_callback_binding_token_aliases WHERE async_execution_id=? ORDER BY hmac_key_version LIMIT 1 FOR UPDATE`, asyncID).Scan(&existingBlob); err == nil {
		if existingBlob == 0 {
			return ErrConflict
		}
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	var tokenBytes [32]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return fmt.Errorf("generate callback binding token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes[:])
	owner := []byte(fmt.Sprintf("async:%d:callback-binding-token", asyncID))
	blobID, err := s.PutEncryptedBlob(ctx, tx, BlobInput{
		KeyringID: in.KeyringID, KEKVersion: in.KEKVersion,
		Purpose: "gateway-callback-binding-token", SchemaVersion: 1,
		Owner: owner, Plaintext: []byte(token), KEK: in.KEK, HMACKey: hmacKeys[in.KEKVersion],
	})
	if err != nil {
		return err
	}
	for _, version := range versions {
		valueHMAC := CallbackBindingTokenHMAC(hmacKeys[version], token)
		if _, err := s.AddCallbackBindingAlias(ctx, tx, CallbackBindingAliasInput{AsyncExecutionID: asyncID, HMACKeyVersion: version, ValueHMAC: valueHMAC, EncryptedBlobID: blobID}); err != nil {
			return err
		}
	}
	return nil
}

func cloneHMACKeyMap(source map[uint32][]byte) map[uint32][]byte {
	if len(source) == 0 {
		return make(map[uint32][]byte)
	}
	out := make(map[uint32][]byte, len(source))
	for version, key := range source {
		out[version] = append([]byte(nil), key...)
	}
	return out
}

func clearHMACKeyMap(keys map[uint32][]byte) {
	for version, key := range keys {
		for i := range key {
			key[i] = 0
		}
		delete(keys, version)
	}
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
	if tx == nil || in.AsyncExecutionID == 0 || in.ScopeKind == "" || in.ScopeKey == "" || len(in.ScopeKey) > 255 || in.EncryptedBlobID == 0 || in.ExpiresAt.IsZero() || !in.ExpiresAt.After(nowUTC()) || len(aliases) == 0 {
		return 0, ErrInvalidInput
	}
	if !validScope(in.ScopeKind) {
		return 0, ErrInvalidInput
	}
	for _, alias := range aliases {
		if alias.ScopeKind != in.ScopeKind || alias.ScopeKey != in.ScopeKey || alias.HMACKeyVersion == 0 || !validHexDigest(alias.ValueHMAC, 32) {
			return 0, ErrInvalidInput
		}
	}
	var parentKind, parentKey string
	if err := tx.QueryRowContext(ctx, `SELECT upstream_scope_kind,upstream_scope_key FROM gw_async_executions WHERE id=? FOR UPDATE`, in.AsyncExecutionID).Scan(&parentKind, &parentKey); err != nil {
		return 0, err
	}
	if parentKind != in.ScopeKind || parentKey != in.ScopeKey {
		return 0, ErrConflict
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

// CreateCallbackProcessingOutbox schedules exactly one consumer action for a
// newly received callback Receipt. The receipt row is the sole parent.
func (s *Store) CreateCallbackProcessingOutbox(ctx context.Context, tx *sql.Tx, receiptID uint64) error {
	if tx == nil || receiptID == 0 {
		return ErrInvalidInput
	}
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT state_version FROM gw_upstream_callback_receipts WHERE id=? FOR SHARE`, receiptID).Scan(&version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	now := nowUTC()
	_, err := tx.ExecContext(ctx, `INSERT INTO gw_async_outbox(callback_receipt_id,action_seq,action,status,state_version,available_at,attempt_count,created_at,updated_at) VALUES (?,1,'callback','pending',?,?,0,?,?)`, receiptID, version, now, now, now)
	if err != nil {
		return fmt.Errorf("create callback processing outbox: %w", err)
	}
	return nil
}

type CallbackReceiptRecord struct {
	ID, AsyncExecutionID, EncryptedPayloadBlobID uint64
	EventScope, EventHMAC, PayloadHMAC, Status   string
	StateVersion                                 uint64
	ExpiresAt                                    time.Time
}

func (s *Store) ReadCallbackReceipt(ctx context.Context, db DB, receiptID uint64) (CallbackReceiptRecord, error) {
	if db == nil || receiptID == 0 {
		return CallbackReceiptRecord{}, ErrInvalidInput
	}
	var out CallbackReceiptRecord
	var payloadID sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT id,async_execution_id,event_scope,event_hmac,payload_hmac,status,state_version,encrypted_payload_blob_id,expires_at FROM gw_upstream_callback_receipts WHERE id=? AND status='received' AND expires_at>CURRENT_TIMESTAMP(3)`, receiptID).
		Scan(&out.ID, &out.AsyncExecutionID, &out.EventScope, &out.EventHMAC, &out.PayloadHMAC, &out.Status, &out.StateVersion, &payloadID, &out.ExpiresAt)
	if err == sql.ErrNoRows {
		return CallbackReceiptRecord{}, ErrNotFound
	}
	if payloadID.Valid {
		out.EncryptedPayloadBlobID = uint64(payloadID.Int64)
	}
	return out, err
}

// ScheduleCallbackQuery marks the Receipt processed and creates a fenced
// query action without changing the provider-reported execution state.
func (s *Store) ScheduleCallbackQuery(ctx context.Context, tx *sql.Tx, item OutboxItem, asyncID uint64, at time.Time) error {
	if tx == nil || item.ID == 0 || item.CallbackReceiptID == 0 || item.Action != "callback" || asyncID == 0 || at.IsZero() {
		return ErrInvalidInput
	}
	var state string
	var version, sequence uint64
	if err := tx.QueryRowContext(ctx, `SELECT state,state_version,action_seq FROM gw_async_executions WHERE id=? FOR UPDATE`, asyncID).Scan(&state, &version, &sequence); err != nil {
		return err
	}
	if state != "accepted" && state != "running" {
		return ErrConflict
	}
	// Callback consumers share one lock order with terminal application:
	// AsyncExecution -> callback Outbox -> Receipt. Keeping the lease proof in
	// this repository method prevents a future caller from skipping the Outbox
	// lock and recreating the inverse Receipt -> Outbox dependency.
	if err := s.AssertCallbackOutboxLease(ctx, tx, item); err != nil {
		return err
	}
	var status string
	var receiptVersion uint64
	var payloadBlob sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT status,state_version,encrypted_payload_blob_id FROM gw_upstream_callback_receipts WHERE id=? AND async_execution_id=? FOR UPDATE`, item.CallbackReceiptID, asyncID).Scan(&status, &receiptVersion, &payloadBlob); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "received" || receiptVersion != item.StateVersion {
		return ErrConflict
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_async_executions SET action_seq=action_seq+1,updated_at=? WHERE id=? AND state_version=? AND action_seq=?`, nowUTC(), asyncID, version, sequence)); err != nil {
		return err
	}
	now := nowUTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_async_outbox(async_execution_id,action_seq,action,status,state_version,available_at,attempt_count,created_at,updated_at) VALUES (?,?, 'query','pending',?,?,?,?,?)`, asyncID, sequence+1, version, at.UTC(), 0, now, now); err != nil {
		return fmt.Errorf("schedule callback query: %w", err)
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_upstream_callback_receipts SET status='processed',state_version=state_version+1 WHERE id=? AND status='received' AND state_version=?`, item.CallbackReceiptID, receiptVersion)); err != nil {
		return err
	}
	if payloadBlob.Valid && payloadBlob.Int64 > 0 {
		return s.DetachCallbackReceiptPayload(ctx, tx, item.CallbackReceiptID, uint64(payloadBlob.Int64))
	}
	return nil
}

type CallbackBindingRecord struct {
	AsyncExecutionID, CredentialVersionID, EncryptedBlobID uint64
}

// FindCallbackBinding resolves a per-execution callback token alias. A
// binding token authenticates itself and therefore does not depend on an
// upstream_callback_verify Grant, which is reserved for provider signatures.
// The caller must still open EncryptedBlobID using the execution owner AAD and
// compare the recovered token before accepting the callback.
func (s *Store) FindCallbackBinding(ctx context.Context, keyVersion uint32, valueHMAC string) (CallbackBindingRecord, error) {
	if s == nil || keyVersion == 0 || valueHMAC == "" || !validHexDigest(valueHMAC, 32) {
		return CallbackBindingRecord{}, ErrInvalidInput
	}
	var out CallbackBindingRecord
	err := s.db.QueryRowContext(ctx, `SELECT a.async_execution_id,ca.credential_version_id,a.encrypted_blob_id
FROM gw_callback_binding_token_aliases a
JOIN gw_async_executions x ON x.id=a.async_execution_id
JOIN gw_api_call_attempts ca ON ca.id=x.attempt_id
	JOIN gw_credential_versions cv ON cv.id=ca.credential_version_id AND cv.credential_id=ca.credential_id
JOIN gw_credential_secret_identities si ON si.id=cv.secret_identity_id AND si.status='active'
JOIN encrypted_blobs b ON b.id=a.encrypted_blob_id AND b.purpose='gateway-callback-binding-token' AND b.schema_version=1 AND b.purged_at IS NULL
	WHERE a.hmac_key_version=? AND a.value_hmac=? AND a.status='active' AND a.expires_at>CURRENT_TIMESTAMP(3)
	  AND x.state NOT IN ('succeeded','failed','cancelled','not_created','terminated_unknown')`, keyVersion, valueHMAC).
		Scan(&out.AsyncExecutionID, &out.CredentialVersionID, &out.EncryptedBlobID)
	if err == sql.ErrNoRows {
		return CallbackBindingRecord{}, ErrNotFound
	}
	return out, err
}

// FindCallbackExecution is retained for repository callers that only need the
// execution and credential identities. New ingress code must use
// FindCallbackBinding so it can validate the encrypted token owner.
func (s *Store) FindCallbackExecution(ctx context.Context, keyVersion uint32, valueHMAC string) (asyncID, credentialVersionID uint64, err error) {
	record, err := s.FindCallbackBinding(ctx, keyVersion, valueHMAC)
	return record.AsyncExecutionID, record.CredentialVersionID, err
}

func (s *Store) AddCallbackBindingAlias(ctx context.Context, tx *sql.Tx, in CallbackBindingAliasInput) (uint64, error) {
	if tx == nil || in.AsyncExecutionID == 0 || in.HMACKeyVersion == 0 || !validHexDigest(in.ValueHMAC, 32) || in.EncryptedBlobID == 0 {
		return 0, ErrInvalidInput
	}
	// Keep the expiry and active state in the database so a token cannot be
	// revived by an application restart. CURRENT_TIMESTAMP follows the same
	// session clock used by DATETIME values written through the MySQL driver.
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_callback_binding_token_aliases(async_execution_id,hmac_key_version,value_hmac,encrypted_blob_id,created_at,status,expires_at) VALUES (?,?,?,?,?,'active',DATE_ADD(CURRENT_TIMESTAMP(3), INTERVAL 24 HOUR))`, in.AsyncExecutionID, in.HMACKeyVersion, in.ValueHMAC, in.EncryptedBlobID, nowUTC())
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
	ID, AsyncExecutionID, AttemptID, CallID, CallbackReceiptID, ResultDeliveryID uint64
	ActionSeq, StateVersion                                                      uint64
	Action                                                                       string
	Attempts                                                                     uint64
	CallLease                                                                    CallLease
	LeaseOwner                                                                   string
	LeaseExpiresAt                                                               time.Time
	// MayHaveDispatched is true for a reclaimed lease or a retry. Handlers
	// must recover the upstream fact before sending again.
	MayHaveDispatched bool
	AvailableAt       time.Time
}

type AttemptOutboxInput struct {
	AttemptID            uint64
	Action               string
	ExpectedStateVersion uint64
	AvailableAt          time.Time
}

// CreateAttemptOutbox schedules a deferred synchronous exchange. It never
// creates an AsyncExecution: the Attempt remains the sole provider execution
// owner and its state version fences every later send.
func (s *Store) CreateAttemptOutbox(ctx context.Context, tx *sql.Tx, in AttemptOutboxInput) (OutboxItem, error) {
	if tx == nil || in.AttemptID == 0 || in.ExpectedStateVersion == 0 || in.AvailableAt.IsZero() || (in.Action != "submit" && in.Action != "recover") {
		return OutboxItem{}, ErrInvalidInput
	}
	var callID, stateVersion uint64
	var currentAttemptID sql.NullInt64
	var attemptState, callState string
	if err := tx.QueryRowContext(ctx, `SELECT a.call_id,a.state,a.state_version,c.current_attempt_id,c.status
FROM gw_api_call_attempts a
JOIN gw_api_calls c ON c.id=a.call_id
WHERE a.id=? FOR UPDATE`, in.AttemptID).Scan(&callID, &attemptState, &stateVersion, &currentAttemptID, &callState); err == sql.ErrNoRows {
		return OutboxItem{}, ErrNotFound
	} else if err != nil {
		return OutboxItem{}, err
	}
	if !currentAttemptID.Valid || uint64(currentAttemptID.Int64) != in.AttemptID || callState != "in_progress" || stateVersion != in.ExpectedStateVersion || !attemptOutboxActionAllowed(attemptState, in.Action) {
		return OutboxItem{}, ErrConflict
	}
	var actionSeq uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(action_seq),0) FROM gw_async_outbox WHERE attempt_id=?`, in.AttemptID).Scan(&actionSeq); err != nil {
		return OutboxItem{}, fmt.Errorf("read attempt outbox sequence: %w", err)
	}
	actionSeq++
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_async_outbox(attempt_id,action_seq,action,status,state_version,available_at,attempt_count,created_at,updated_at) VALUES (?,?,?,'pending',?,?,0,?,?)`, in.AttemptID, actionSeq, in.Action, stateVersion, in.AvailableAt.UTC(), now, now)
	if err != nil {
		return OutboxItem{}, fmt.Errorf("create attempt outbox: %w", err)
	}
	id, err := lastID(result)
	if err != nil {
		return OutboxItem{}, err
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_api_calls SET next_action_at=?,updated_at=? WHERE id=? AND current_attempt_id=? AND status='in_progress'`, in.AvailableAt.UTC(), now, callID, in.AttemptID)); err != nil {
		return OutboxItem{}, fmt.Errorf("schedule deferred call: %w", err)
	}
	return OutboxItem{ID: id, AttemptID: in.AttemptID, ActionSeq: actionSeq, StateVersion: stateVersion, Action: in.Action, AvailableAt: in.AvailableAt.UTC()}, nil
}

func attemptOutboxActionAllowed(state, action string) bool {
	switch action {
	case "submit":
		return state == "started"
	case "recover":
		return state == "recovery_pending"
	default:
		return false
	}
}

// ClaimAttemptOutbox leases both the durable action and its owning Call in one
// transaction. Rolling back either update releases both claims.
func (s *Store) ClaimAttemptOutbox(ctx context.Context, tx *sql.Tx, owner string, lease time.Duration) (OutboxItem, error) {
	if tx == nil || owner == "" || len(owner) > 128 || lease <= 0 {
		return OutboxItem{}, ErrInvalidInput
	}
	var item OutboxItem
	var available time.Time
	var previousExpiry sql.NullTime
	var status string
	var callID uint64
	err := tx.QueryRowContext(ctx, `SELECT o.id,o.attempt_id,o.action_seq,o.action,o.state_version,o.attempt_count,o.available_at,o.status,o.lease_expires_at,a.call_id
FROM gw_async_outbox o
JOIN gw_api_call_attempts a ON a.id=o.attempt_id
WHERE o.attempt_id IS NOT NULL AND o.action IN ('submit','recover')
  AND ((o.status='pending' AND o.available_at<=CURRENT_TIMESTAMP(3)) OR (o.status='dispatching' AND o.lease_expires_at<=CURRENT_TIMESTAMP(3)))
ORDER BY o.id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&item.ID, &item.AttemptID, &item.ActionSeq, &item.Action, &item.StateVersion, &item.Attempts, &available, &status, &previousExpiry, &callID)
	if err == sql.ErrNoRows {
		return OutboxItem{}, ErrNotFound
	}
	if err != nil {
		return OutboxItem{}, fmt.Errorf("claim attempt outbox: %w", err)
	}
	item.AvailableAt = available
	item.MayHaveDispatched = status == "dispatching" || item.Attempts > 0
	expiresAt := nowUTC().Add(lease).Truncate(time.Millisecond)
	callLease, err := s.claimCallLeaseUntil(ctx, tx, callID, item.AttemptID, owner, expiresAt)
	if err != nil {
		return OutboxItem{}, err
	}
	var lockedCallID, attemptVersion uint64
	var attemptState string
	if err := tx.QueryRowContext(ctx, `SELECT call_id,state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, item.AttemptID).Scan(&lockedCallID, &attemptState, &attemptVersion); err == sql.ErrNoRows {
		return OutboxItem{}, ErrNotFound
	} else if err != nil {
		return OutboxItem{}, err
	}
	if lockedCallID != callID || attemptVersion != item.StateVersion || !attemptOutboxActionAllowed(attemptState, item.Action) {
		return OutboxItem{}, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='dispatching',lease_owner=?,lease_expires_at=?,attempt_count=attempt_count+1,updated_at=? WHERE id=? AND attempt_id=? AND action_seq=? AND state_version=? AND action=? AND (status='pending' OR (status='dispatching' AND lease_expires_at<=CURRENT_TIMESTAMP(3)))`, owner, expiresAt, nowUTC(), item.ID, item.AttemptID, item.ActionSeq, item.StateVersion, item.Action)
	if err != nil {
		return OutboxItem{}, fmt.Errorf("lease attempt outbox: %w", err)
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
	item.LeaseExpiresAt = expiresAt
	item.CallLease = callLease
	return item, nil
}

// AssertAttemptOutboxLease is the send/result fence for deferred synchronous
// work. It proves the exact Outbox claim, Call lease, current Attempt, action,
// and Attempt state version in the caller's transaction.
func (s *Store) AssertAttemptOutboxLease(ctx context.Context, tx *sql.Tx, item OutboxItem) error {
	if tx == nil || item.AttemptID == 0 || item.CallID != 0 || item.AsyncExecutionID != 0 || item.CallbackReceiptID != 0 || item.ResultDeliveryID != 0 || item.CallLease.AttemptID != item.AttemptID || item.CallLease.Owner != item.LeaseOwner || !item.CallLease.ExpiresAt.Equal(item.LeaseExpiresAt) {
		return ErrInvalidInput
	}
	if err := s.AssertCallLease(ctx, tx, item.CallLease); err != nil {
		return err
	}
	var callID, version uint64
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT call_id,state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, item.AttemptID).Scan(&callID, &state, &version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if callID != item.CallLease.CallID || version != item.StateVersion || !attemptOutboxActionAllowed(state, item.Action) {
		return ErrConflict
	}
	return s.AssertAsyncOutboxLease(ctx, tx, item)
}

func (s *Store) RenewAttemptOutboxLease(ctx context.Context, tx *sql.Tx, item OutboxItem, lease time.Duration) (OutboxItem, error) {
	if lease <= 0 {
		return OutboxItem{}, ErrInvalidInput
	}
	if err := s.AssertAttemptOutboxLease(ctx, tx, item); err != nil {
		return OutboxItem{}, err
	}
	expiresAt := nowUTC().Add(lease).Truncate(time.Millisecond)
	if !expiresAt.After(item.LeaseExpiresAt) {
		return OutboxItem{}, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET lease_expires_at=?,updated_at=? WHERE id=? AND status='dispatching' AND lease_owner=? AND attempt_count=? AND lease_expires_at=?`, expiresAt, nowUTC(), item.ID, item.LeaseOwner, item.Attempts, item.LeaseExpiresAt.UTC())
	if err != nil {
		return OutboxItem{}, fmt.Errorf("renew attempt outbox lease: %w", err)
	}
	ok, err := affected(result)
	if err != nil {
		return OutboxItem{}, err
	}
	if !ok {
		return OutboxItem{}, ErrConflict
	}
	renewedCall, err := s.renewCallLeaseUntil(ctx, tx, item.CallLease, expiresAt)
	if err != nil {
		return OutboxItem{}, err
	}
	// Both leases use the same deadline; retain a single worker deadline.
	if !renewedCall.ExpiresAt.Equal(expiresAt) {
		return OutboxItem{}, ErrConflict
	}
	item.LeaseExpiresAt = expiresAt
	item.CallLease = renewedCall
	return item, nil
}

func (s *Store) CompleteAttemptOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, errorCode string) error {
	if err := s.AssertAttemptOutboxLease(ctx, tx, item); err != nil {
		return err
	}
	if err := s.CompleteAsyncOutbox(ctx, tx, item, true, errorCode); err != nil {
		return err
	}
	return s.ReleaseCallLease(ctx, tx, item.CallLease)
}

func (s *Store) RetryAttemptOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, errorCode string, availableAt time.Time) error {
	if availableAt.IsZero() {
		return ErrInvalidInput
	}
	if err := s.AssertAttemptOutboxLease(ctx, tx, item); err != nil {
		return err
	}
	if err := s.RetryAsyncOutbox(ctx, tx, item, errorCode, availableAt); err != nil {
		return err
	}
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_api_calls SET next_action_at=? WHERE id=? AND current_attempt_id=? AND lease_owner=? AND lease_expires_at=?`, availableAt.UTC(), item.CallLease.CallID, item.AttemptID, item.CallLease.Owner, item.CallLease.ExpiresAt.UTC())); err != nil {
		return err
	}
	return s.ReleaseCallLease(ctx, tx, item.CallLease)
}

// ClaimDeliveryOutbox leases one delivery reconciliation independently from
// generation polling. A recovered lease may safely repeat only the provider
// query; it can never submit another generation.
func (s *Store) ClaimDeliveryOutbox(ctx context.Context, tx *sql.Tx, owner string, lease time.Duration) (OutboxItem, error) {
	if tx == nil || owner == "" || lease <= 0 {
		return OutboxItem{}, ErrInvalidInput
	}
	var item OutboxItem
	var available time.Time
	var previousExpiry sql.NullTime
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id,result_delivery_id,action_seq,action,state_version,attempt_count,available_at,status,lease_expires_at FROM gw_async_outbox WHERE result_delivery_id IS NOT NULL AND action='reconcile_delivery' AND ((status='pending' AND available_at<=CURRENT_TIMESTAMP(3)) OR (status='dispatching' AND lease_expires_at<=CURRENT_TIMESTAMP(3))) ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&item.ID, &item.ResultDeliveryID, &item.ActionSeq, &item.Action, &item.StateVersion, &item.Attempts, &available, &status, &previousExpiry)
	if err == sql.ErrNoRows {
		return OutboxItem{}, ErrNotFound
	}
	if err != nil {
		return OutboxItem{}, fmt.Errorf("claim delivery outbox: %w", err)
	}
	item.AvailableAt = available
	item.MayHaveDispatched = status == "dispatching" || item.Attempts > 0
	expires := nowUTC().Add(lease)
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='dispatching',lease_owner=?,lease_expires_at=?,attempt_count=attempt_count+1,updated_at=? WHERE id=? AND result_delivery_id=? AND action='reconcile_delivery' AND (status='pending' OR (status='dispatching' AND lease_expires_at<=CURRENT_TIMESTAMP(3)))`, owner, expires, nowUTC(), item.ID, item.ResultDeliveryID)); err != nil {
		return OutboxItem{}, fmt.Errorf("lease delivery outbox: %w", err)
	}
	item.Attempts++
	item.LeaseOwner, item.LeaseExpiresAt = owner, expires
	return item, nil
}

func (s *Store) ClaimCallbackOutbox(ctx context.Context, tx *sql.Tx, owner string, lease time.Duration) (OutboxItem, error) {
	if tx == nil || owner == "" || lease <= 0 {
		return OutboxItem{}, ErrInvalidInput
	}
	var item OutboxItem
	var available time.Time
	var previousExpiry sql.NullTime
	var status string
	err := tx.QueryRowContext(ctx, `SELECT id,callback_receipt_id,action_seq,action,state_version,attempt_count,available_at,status,lease_expires_at FROM gw_async_outbox WHERE callback_receipt_id IS NOT NULL AND ((status='pending' AND available_at<=CURRENT_TIMESTAMP(3)) OR (status='dispatching' AND lease_expires_at<=CURRENT_TIMESTAMP(3))) ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&item.ID, &item.CallbackReceiptID, &item.ActionSeq, &item.Action, &item.StateVersion, &item.Attempts, &available, &status, &previousExpiry)
	if err == sql.ErrNoRows {
		return OutboxItem{}, ErrNotFound
	}
	if err != nil {
		return OutboxItem{}, fmt.Errorf("claim callback outbox: %w", err)
	}
	item.AvailableAt = available
	item.MayHaveDispatched = status == "dispatching" || item.Attempts > 0
	expires := nowUTC().Add(lease)
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='dispatching',lease_owner=?,lease_expires_at=?,attempt_count=attempt_count+1,updated_at=? WHERE id=? AND callback_receipt_id=? AND (status='pending' OR (status='dispatching' AND lease_expires_at<=CURRENT_TIMESTAMP(3)))`, owner, expires, nowUTC(), item.ID, item.CallbackReceiptID)); err != nil {
		return OutboxItem{}, fmt.Errorf("lease callback outbox: %w", err)
	}
	item.Attempts++
	item.LeaseOwner, item.LeaseExpiresAt = owner, expires
	return item, nil
}

func (s *Store) AssertCallbackOutboxLease(ctx context.Context, tx *sql.Tx, item OutboxItem) error {
	if tx == nil || item.ID == 0 || item.CallbackReceiptID == 0 || item.LeaseOwner == "" || item.Attempts == 0 {
		return ErrInvalidInput
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM gw_async_outbox WHERE id=? AND callback_receipt_id=? AND status='dispatching' AND lease_owner=? AND attempt_count=? AND action_seq=? AND state_version=? AND action='callback' AND lease_expires_at>CURRENT_TIMESTAMP(3) FOR UPDATE`, item.ID, item.CallbackReceiptID, item.LeaseOwner, item.Attempts, item.ActionSeq, item.StateVersion).Scan(&n)
	if err == sql.ErrNoRows {
		return ErrConflict
	}
	return err
}

func (s *Store) CompleteCallbackOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, success bool, errorCode string) error {
	if err := s.AssertCallbackOutboxLease(ctx, tx, item); err != nil {
		return err
	}
	status := "failed"
	if success {
		status = "succeeded"
	}
	return requireOneRow(tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status=?,last_error_code=?,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND callback_receipt_id=? AND status='dispatching' AND lease_owner=? AND attempt_count=?`, status, errorCode, nowUTC(), item.ID, item.CallbackReceiptID, item.LeaseOwner, item.Attempts))
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
	err := tx.QueryRowContext(ctx, `SELECT id,call_id,attempt_id,async_execution_id,action_seq,action,state_version,attempt_count,available_at,status,lease_expires_at FROM gw_async_outbox WHERE async_execution_id IS NOT NULL AND ((status='pending' AND available_at<=CURRENT_TIMESTAMP(3)) OR (status='dispatching' AND lease_expires_at<=CURRENT_TIMESTAMP(3))) ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&item.ID, &callID, &attemptID, &asyncID, &item.ActionSeq, &item.Action, &item.StateVersion, &item.Attempts, &available, &status, &previousExpiry)
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
	result, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='dispatching',lease_owner=?,lease_expires_at=?,attempt_count=attempt_count+1,updated_at=? WHERE id=? AND (status='pending' OR (status='dispatching' AND lease_expires_at<=CURRENT_TIMESTAMP(3)))`, owner, expires, nowUTC(), item.ID)
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
	// Every immutable identity carried by the leased item is part of the
	// fence. Checking only the outbox primary key would let a stale or forged
	// worker attach a valid lease to another action after a retry/reclaim.
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM gw_async_outbox
WHERE id=? AND status='dispatching' AND lease_owner=? AND attempt_count=?
  AND action_seq=? AND state_version=? AND action=?
  AND (call_id <=> ?) AND (attempt_id <=> ?) AND (async_execution_id <=> ?)
  AND (callback_receipt_id <=> ?) AND (result_delivery_id <=> ?)
  AND lease_expires_at>CURRENT_TIMESTAMP(3) FOR UPDATE`,
		item.ID, item.LeaseOwner, item.Attempts, item.ActionSeq, item.StateVersion, item.Action,
		outboxNullableID(item.CallID), outboxNullableID(item.AttemptID), outboxNullableID(item.AsyncExecutionID),
		outboxNullableID(item.CallbackReceiptID), outboxNullableID(item.ResultDeliveryID)).Scan(&n)
	if err == sql.ErrNoRows {
		return ErrConflict
	}
	return err
}

func outboxNullableID(value uint64) any {
	if value == 0 {
		return nil
	}
	return value
}

// AsyncOutboxSucceeded recognizes only the exact completed lease, allowing a
// worker to acknowledge a result already committed by its transaction handler.
func (s *Store) AsyncOutboxSucceeded(ctx context.Context, db DB, item OutboxItem) (bool, error) {
	if db == nil || item.ID == 0 || item.Attempts == 0 {
		return false, ErrInvalidInput
	}
	var id uint64
	err := db.QueryRowContext(ctx, `SELECT id FROM gw_async_outbox WHERE id=? AND status='succeeded' AND attempt_count=? AND action_seq=? AND state_version=? AND action=? AND (call_id <=> ?) AND (attempt_id <=> ?) AND (async_execution_id <=> ?) AND (callback_receipt_id <=> ?) AND (result_delivery_id <=> ?)`, item.ID, item.Attempts, item.ActionSeq, item.StateVersion, item.Action, outboxNullableID(item.CallID), outboxNullableID(item.AttemptID), outboxNullableID(item.AsyncExecutionID), outboxNullableID(item.CallbackReceiptID), outboxNullableID(item.ResultDeliveryID)).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) AssertDeliveryOutboxLease(ctx context.Context, tx *sql.Tx, item OutboxItem) error {
	if item.ResultDeliveryID == 0 || item.Action != "reconcile_delivery" || item.CallID != 0 || item.AttemptID != 0 || item.AsyncExecutionID != 0 || item.CallbackReceiptID != 0 {
		return ErrInvalidInput
	}
	return s.AssertAsyncOutboxLease(ctx, tx, item)
}

func (s *Store) DeliveryOutboxSucceeded(ctx context.Context, db DB, item OutboxItem) (bool, error) {
	if item.ResultDeliveryID == 0 || item.Action != "reconcile_delivery" {
		return false, ErrInvalidInput
	}
	return s.AsyncOutboxSucceeded(ctx, db, item)
}

func (s *Store) CompleteDeliveryOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, success bool, errorCode string) error {
	if item.ResultDeliveryID == 0 || item.Action != "reconcile_delivery" {
		return ErrInvalidInput
	}
	if err := s.CompleteAsyncOutbox(ctx, tx, item, success, errorCode); err != nil {
		return err
	}
	return nil
}

func (s *Store) RetryDeliveryOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, errorCode string, availableAt time.Time) error {
	if item.ResultDeliveryID == 0 || item.Action != "reconcile_delivery" {
		return ErrInvalidInput
	}
	if err := s.RetryAsyncOutbox(ctx, tx, item, errorCode, availableAt); err != nil {
		return err
	}
	return requireOneRow(tx.ExecContext(ctx, `UPDATE gw_result_deliveries SET retry_at=?,updated_at=? WHERE id=? AND state IN ('delivery_failed','expired') AND state_version=? AND action_seq=?`, availableAt.UTC(), nowUTC(), item.ResultDeliveryID, item.StateVersion, item.ActionSeq))
}

func (s *Store) DeadLetterDeliveryOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, errorCode string) error {
	if item.ResultDeliveryID == 0 || item.Action != "reconcile_delivery" {
		return ErrInvalidInput
	}
	if err := s.DeadLetterAsyncOutbox(ctx, tx, item, errorCode); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE gw_result_deliveries SET retry_at=NULL,updated_at=? WHERE id=? AND state_version=? AND action_seq=?`, nowUTC(), item.ResultDeliveryID, item.StateVersion, item.ActionSeq)
	return err
}

func (s *Store) CompleteAsyncOutbox(ctx context.Context, tx *sql.Tx, item OutboxItem, success bool, errorCode string) error {
	if tx == nil || item.ID == 0 || item.LeaseOwner == "" || item.Attempts == 0 {
		return ErrInvalidInput
	}
	if err := s.AssertAsyncOutboxLease(ctx, tx, item); err != nil {
		return err
	}
	status := "failed"
	if success {
		status = "succeeded"
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status=?,last_error_code=?,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='dispatching' AND lease_owner=? AND attempt_count=? AND lease_expires_at>CURRENT_TIMESTAMP(3)`, status, errorCode, nowUTC(), item.ID, item.LeaseOwner, item.Attempts)
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
	if err := s.AssertAsyncOutboxLease(ctx, tx, item); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='pending',last_error_code=?,available_at=?,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='dispatching' AND lease_owner=? AND attempt_count=? AND lease_expires_at>CURRENT_TIMESTAMP(3)`, errorCode, availableAt.UTC(), nowUTC(), item.ID, item.LeaseOwner, item.Attempts)
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
	if err := s.AssertAsyncOutboxLease(ctx, tx, item); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_async_outbox SET status='dead_letter',last_error_code=?,lease_owner='',lease_expires_at=NULL,updated_at=? WHERE id=? AND status='dispatching' AND lease_owner=? AND attempt_count=? AND lease_expires_at>CURRENT_TIMESTAMP(3)`, errorCode, nowUTC(), item.ID, item.LeaseOwner, item.Attempts)
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
