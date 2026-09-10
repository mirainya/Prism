package repository

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/security"
)

type ResultDeliveryInput struct {
	CallID, AttemptID uint64
	Ordinal           uint32
	UserID, TokenID   uint64
	Mode, SourceKind  string
}

type CallbackTargetInput struct {
	CallID, UserID, TokenID, EncryptedConfigBlobID uint64
	TargetHMAC, Algorithm                          string
	PolicyVersion                                  uint32
}

func (s *Store) CreateCallbackTarget(ctx context.Context, tx *sql.Tx, in CallbackTargetInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.UserID == 0 || in.TokenID == 0 || in.EncryptedConfigBlobID == 0 || !validHexDigest(in.TargetHMAC, 32) || in.Algorithm == "" || in.PolicyVersion == 0 {
		return 0, ErrInvalidInput
	}
	var callUserID, callTokenID uint64
	if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id FROM gw_api_calls WHERE id=? FOR SHARE`, in.CallID).Scan(&callUserID, &callTokenID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if callUserID != in.UserID || callTokenID != in.TokenID {
		return 0, ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_callback_targets(call_id,user_id,token_id,encrypted_config_blob_id,target_hmac,algorithm,policy_version,created_at) VALUES (?,?,?,?,?,?,?,?)`, in.CallID, in.UserID, in.TokenID, in.EncryptedConfigBlobID, in.TargetHMAC, in.Algorithm, in.PolicyVersion, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create callback target: %w", err)
	}
	return lastID(result)
}

func (s *Store) CreateResultDelivery(ctx context.Context, tx *sql.Tx, in ResultDeliveryInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.AttemptID == 0 || in.UserID == 0 || in.TokenID == 0 || (in.Mode != "reference" && in.Mode != "managed_copy") || (in.SourceKind != "remote_url" && in.SourceKind != "inline_response") {
		return 0, ErrInvalidInput
	}
	if in.Mode == "reference" && in.SourceKind != "remote_url" {
		return 0, ErrInvalidInput
	}
	var callUserID, callTokenID uint64
	if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id FROM gw_api_calls WHERE id=? FOR SHARE`, in.CallID).Scan(&callUserID, &callTokenID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if callUserID != in.UserID || callTokenID != in.TokenID {
		return 0, ErrConflict
	}
	var attemptCallID uint64
	if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_api_call_attempts WHERE id=? FOR SHARE`, in.AttemptID).Scan(&attemptCallID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if attemptCallID != in.CallID {
		return 0, ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_result_deliveries(call_id,attempt_id,result_ordinal,user_id,token_id,delivery_mode,source_kind,state,state_version,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'pending',1,?,?)`, in.CallID, in.AttemptID, in.Ordinal, in.UserID, in.TokenID, in.Mode, in.SourceKind, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert result delivery: %w", err)
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(result_delivery_id,new_state,state_version,reason_code,created_at) VALUES (?,'pending',1,'result_observed',?)`, id, now)
	return id, err
}

type ResultSourceInput struct {
	DeliveryID                    uint64
	Sequence                      uint64
	EncryptedURLBlobID            uint64
	URLHMAC, ProviderIdentityHMAC string
	ExpiresAt                     *time.Time
	ObservedRequestLogID          uint64
}

func (s *Store) AddResultSource(ctx context.Context, tx *sql.Tx, in ResultSourceInput) (uint64, error) {
	if tx == nil || in.DeliveryID == 0 || in.Sequence == 0 || in.EncryptedURLBlobID == 0 || !validHexDigest(in.URLHMAC, 32) {
		return 0, ErrInvalidInput
	}
	if in.ProviderIdentityHMAC != "" && !validHexDigest(in.ProviderIdentityHMAC, 32) {
		return 0, ErrInvalidInput
	}
	var evidence uint64
	if err := tx.QueryRowContext(ctx, `SELECT l.id FROM gw_channel_request_logs l JOIN gw_result_deliveries d ON d.attempt_id=l.attempt_id WHERE d.id=? AND l.id=? AND l.status='response_recorded' AND l.response_bytes_complete=true AND l.http_status BETWEEN 200 AND 299`, in.DeliveryID, in.ObservedRequestLogID).Scan(&evidence); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_result_delivery_sources(result_delivery_id,source_seq,encrypted_url_blob_id,url_hmac,provider_result_identity_hmac,observed_at,expires_at,state,observed_request_log_id) VALUES (?,?,?,?,?,?,?,'superseded',?)`, in.DeliveryID, in.Sequence, in.EncryptedURLBlobID, in.URLHMAC, emptyAsNull(in.ProviderIdentityHMAC), nowUTC(), in.ExpiresAt, in.ObservedRequestLogID)
	if err != nil {
		return 0, fmt.Errorf("insert result source: %w", err)
	}
	return lastID(result)
}

func (s *Store) ActivateResultSource(ctx context.Context, tx *sql.Tx, deliveryID, sourceID uint64) error {
	if tx == nil || deliveryID == 0 || sourceID == 0 {
		return ErrInvalidInput
	}
	now := nowUTC()
	var state, mode, sourcePolicy string
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT d.state,d.delivery_mode,d.state_version,pt.source_url_policy FROM gw_result_deliveries d JOIN gw_api_call_attempts a ON a.id=d.attempt_id JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id WHERE d.id=? FOR UPDATE`, deliveryID).Scan(&state, &mode, &version, &sourcePolicy); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "pending" || mode != "reference" {
		return ErrConflict
	}
	var expires sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT expires_at FROM gw_result_delivery_sources WHERE id=? AND result_delivery_id=? AND state='superseded' FOR UPDATE`, sourceID, deliveryID).Scan(&expires); err != nil {
		return err
	}
	if sourcePolicy != "fixed" && (!expires.Valid || !expires.Time.After(now)) {
		return ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_result_delivery_sources SET state='active' WHERE id=? AND result_delivery_id=? AND state='superseded'`, sourceID, deliveryID)
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
	var expiry any
	if expires.Valid {
		expiry = expires.Time
	}
	res, err = tx.ExecContext(ctx, `UPDATE gw_result_deliveries SET current_source_id=?,expires_at=?,state='ready',reason_code='reference_available',state_version=state_version+1,updated_at=? WHERE id=? AND state='pending' AND state_version=?`, sourceID, expiry, now, deliveryID, version)
	if err != nil {
		return err
	}
	ok, err = affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(result_delivery_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,'pending','ready',?,'reference_available',?)`, deliveryID, version+1, now)
	return err
}

// FailResultDelivery records delivery failure without changing generation or billing.
func (s *Store) FailResultDelivery(ctx context.Context, tx *sql.Tx, deliveryID uint64, reason string) error {
	if tx == nil || deliveryID == 0 || reason == "" || len(reason) > 64 {
		return ErrInvalidInput
	}
	var state string
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT state,state_version FROM gw_result_deliveries WHERE id=? FOR UPDATE`, deliveryID).Scan(&state, &version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "pending" {
		return ErrConflict
	}
	now := nowUTC()
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_result_deliveries SET state='delivery_failed',reason_code=?,state_version=state_version+1,updated_at=? WHERE id=? AND state='pending' AND state_version=?`, reason, now, deliveryID, version)); err != nil {
		return err
	}
	return requireOneRow(tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(result_delivery_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,'pending','delivery_failed',?,?,?)`, deliveryID, version+1, reason, now))
}

// ExpireResultDelivery advances a ready reference after its fixed expiry.
// Expiration is an independent delivery fact and never changes the parent
// generation or billing state.
func (s *Store) ExpireResultDelivery(ctx context.Context, tx *sql.Tx, deliveryID uint64) error {
	if tx == nil || deliveryID == 0 {
		return ErrInvalidInput
	}
	var state, mode, sourcePolicy string
	var version uint64
	var expires sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT d.state,d.state_version,d.expires_at,d.delivery_mode,pt.source_url_policy FROM gw_result_deliveries d JOIN gw_api_call_attempts a ON a.id=d.attempt_id JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id WHERE d.id=? FOR UPDATE`, deliveryID).Scan(&state, &version, &expires, &mode, &sourcePolicy); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if state != "ready" || mode != "reference" || !expires.Valid || expires.Time.After(nowUTC()) {
		return ErrConflict
	}
	now := nowUTC()
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_result_deliveries SET state='expired',reason_code='source_expired',state_version=state_version+1,updated_at=? WHERE id=? AND state='ready' AND state_version=?`, now, deliveryID, version)); err != nil {
		return err
	}
	if err := requireOneRow(tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(result_delivery_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,'ready','expired',?,'source_expired',?)`, deliveryID, version+1, now)); err != nil {
		return err
	}
	if sourcePolicy == "refreshable" {
		_, err := s.ScheduleDeliveryReconciliation(ctx, tx, deliveryID, now)
		return err
	}
	return nil
}

// ExpireReadyDeliveries advances a bounded batch of expired reference
// deliveries. Each row is processed in its own transaction so one malformed
// or concurrently changed delivery cannot block the rest of the scan.
func (s *Store) ExpireReadyDeliveries(ctx context.Context, limit int) (int, error) {
	if s == nil || s.db == nil || limit <= 0 || limit > 1000 {
		return 0, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM gw_result_deliveries WHERE state='ready' AND expires_at IS NOT NULL AND expires_at<=UTC_TIMESTAMP(3) ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	processed := 0
	for _, id := range ids {
		err := s.WithTx(ctx, func(tx *sql.Tx) error {
			return s.ExpireResultDelivery(ctx, tx, id)
		})
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

// CreateReferenceDelivery stores a remote result URL only in an encrypted
// source blob. The caller receives a stable delivery ID suitable for the
// public result payload; no provider URL is persisted in that payload.
func (s *Store) CreateReferenceDelivery(ctx context.Context, tx *sql.Tx, in ResultDeliveryInput, source BlobInput, requestID uint64, expiresAt *time.Time, sourcePolicy string) (uint64, error) {
	if !delivery.ValidRemoteURL(string(source.Plaintext)) || source.KeyringID == 0 || source.KEKVersion == 0 || len(source.KEK) != security.KeySize || len(source.HMACKey) != security.KeySize || requestID == 0 || sourcePolicy != "fixed" && sourcePolicy != "refreshable" {
		return 0, ErrInvalidInput
	}
	if in.Mode != "reference" || in.SourceKind != "remote_url" {
		return 0, ErrInvalidInput
	}
	deliveryID, err := s.CreateResultDelivery(ctx, tx, in)
	if err != nil {
		return 0, err
	}
	source.Purpose, source.SchemaVersion = "gateway-result-source", 1
	source.Owner = []byte(fmt.Sprintf("delivery:%d:source:%d", deliveryID, 1))
	blobID, err := s.PutEncryptedBlob(ctx, tx, source)
	if err != nil {
		return 0, err
	}
	hmac := security.HMACSHA256(source.HMACKey, source.Plaintext)
	sourceID, err := s.AddResultSource(ctx, tx, ResultSourceInput{DeliveryID: deliveryID, Sequence: 1, EncryptedURLBlobID: blobID, URLHMAC: hex.EncodeToString(hmac[:]), ObservedRequestLogID: requestID, ExpiresAt: expiresAt})
	if err != nil {
		return 0, err
	}
	if sourcePolicy != "fixed" && (expiresAt == nil || !expiresAt.After(nowUTC())) {
		reason := "source_expiry_unknown"
		if expiresAt != nil {
			reason = "source_expired"
		}
		if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_result_delivery_sources SET state='invalid' WHERE id=? AND state='superseded'`, sourceID)); err != nil {
			return 0, err
		}
		if err := s.FailResultDelivery(ctx, tx, deliveryID, reason); err != nil {
			return 0, err
		}
		if _, err := s.ScheduleDeliveryReconciliation(ctx, tx, deliveryID, nowUTC()); err != nil {
			return 0, err
		}
		return deliveryID, nil
	}
	if err := s.ActivateResultSource(ctx, tx, deliveryID, sourceID); err != nil {
		return 0, err
	}
	return deliveryID, nil
}

// RefreshReferenceDelivery replaces an expired or failed reference with a
// newly observed provider URL. The old source remains immutable evidence;
// only the new source becomes active after all ownership and expiry checks.
func (s *Store) RefreshReferenceDelivery(ctx context.Context, tx *sql.Tx, deliveryID uint64, source BlobInput, requestID uint64, expiresAt *time.Time) error {
	if tx == nil || deliveryID == 0 || requestID == 0 || source.KeyringID == 0 || source.KEKVersion == 0 || !delivery.ValidRemoteURL(string(source.Plaintext)) || len(source.KEK) != security.KeySize || len(source.HMACKey) != security.KeySize {
		return ErrInvalidInput
	}
	var state, mode, sourcePolicy string
	var callIDNum, attemptIDNum, currentSourceID uint64
	var version uint64
	var currentSource sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT call_id,attempt_id,state,delivery_mode,state_version,current_source_id FROM gw_result_deliveries WHERE id=? FOR UPDATE`, deliveryID).Scan(&callIDNum, &attemptIDNum, &state, &mode, &version, &currentSource); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if callIDNum == 0 || attemptIDNum == 0 {
		return ErrConflict
	}
	if mode != "reference" || (state != "expired" && state != "delivery_failed") {
		return ErrConflict
	}
	if currentSource.Valid {
		currentSourceID = uint64(currentSource.Int64)
	}
	if err := tx.QueryRowContext(ctx, `SELECT pt.source_url_policy FROM gw_result_deliveries d JOIN gw_api_call_attempts a ON a.id=d.attempt_id JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id WHERE d.id=?`, deliveryID).Scan(&sourcePolicy); err != nil {
		return err
	}
	if sourcePolicy != "refreshable" || expiresAt == nil || !expiresAt.After(nowUTC()) {
		return ErrConflict
	}
	var evidence uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_channel_request_logs WHERE id=? AND attempt_id=? AND result_delivery_id=? AND action='reconcile_delivery' AND status='response_recorded' AND response_bytes_complete=true AND http_status BETWEEN 200 AND 299 FOR UPDATE`, requestID, attemptIDNum, deliveryID).Scan(&evidence); err != nil {
		return err
	}
	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(source_seq) FROM gw_result_delivery_sources WHERE result_delivery_id=? FOR UPDATE`, deliveryID).Scan(&maxSeq); err != nil {
		return err
	}
	seq := uint64(1)
	if maxSeq.Valid {
		seq = uint64(maxSeq.Int64) + 1
	}
	if seq == 0 {
		return ErrConflict
	}
	source.Purpose, source.SchemaVersion = "gateway-result-source", 1
	source.Owner = []byte(fmt.Sprintf("delivery:%d:source:%d", deliveryID, seq))
	blobID, err := s.PutEncryptedBlob(ctx, tx, source)
	if err != nil {
		return err
	}
	hmac := security.HMACSHA256(source.HMACKey, source.Plaintext)
	if _, err = s.AddResultSource(ctx, tx, ResultSourceInput{DeliveryID: deliveryID, Sequence: seq, EncryptedURLBlobID: blobID, URLHMAC: hex.EncodeToString(hmac[:]), ObservedRequestLogID: requestID, ExpiresAt: expiresAt}); err != nil {
		return err
	}
	now := nowUTC()
	if currentSourceID != 0 {
		if err = requireOneRow(tx.ExecContext(ctx, `UPDATE gw_result_delivery_sources SET state='superseded' WHERE id=? AND result_delivery_id=? AND state='active'`, currentSourceID, deliveryID)); err != nil {
			return err
		}
	}
	if err = requireOneRow(tx.ExecContext(ctx, `UPDATE gw_result_delivery_sources SET state='active' WHERE result_delivery_id=? AND source_seq=? AND state='superseded'`, deliveryID, seq)); err != nil {
		return err
	}
	if err = requireOneRow(tx.ExecContext(ctx, `UPDATE gw_result_deliveries SET current_source_id=(SELECT id FROM gw_result_delivery_sources WHERE result_delivery_id=? AND source_seq=?),expires_at=?,retry_at=NULL,state='ready',reason_code='reference_refreshed',state_version=state_version+1,updated_at=? WHERE id=? AND state=? AND state_version=?`, deliveryID, seq, expiresAt.UTC(), now, deliveryID, state, version)); err != nil {
		return err
	}
	return requireOneRow(tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(result_delivery_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)`, deliveryID, state, "ready", version+1, "reference_refreshed", now))
}

type ResultDeliveryRecord struct {
	ID, CallID, AttemptID, UserID, TokenID, SourceBlobID uint64
	MediaAssetRefID, MediaAssetID                        uint64
	SourceSequence                                       uint64
	SourceURLPolicy                                      string
	MediaLocator                                         string
	Mode, SourceKind, State, ContentType, ReasonCode     string
	ExpiresAt                                            *time.Time
}

func (s *Store) ReadResultDelivery(ctx context.Context, callID, userID, tokenID uint64) (ResultDeliveryRecord, error) {
	if callID == 0 || userID == 0 || tokenID == 0 {
		return ResultDeliveryRecord{}, ErrInvalidInput
	}
	return s.readResultDelivery(ctx, `d.call_id=? AND d.user_id=? AND d.token_id=?`, callID, userID, tokenID)
}

func (s *Store) ReadResultDeliveryByID(ctx context.Context, deliveryID, callID, userID, tokenID uint64) (ResultDeliveryRecord, error) {
	if deliveryID == 0 || callID == 0 || userID == 0 || tokenID == 0 {
		return ResultDeliveryRecord{}, ErrInvalidInput
	}
	return s.readResultDelivery(ctx, `d.id=? AND d.call_id=? AND d.user_id=? AND d.token_id=?`, deliveryID, callID, userID, tokenID)
}

func (s *Store) readResultDelivery(ctx context.Context, predicate string, args ...any) (ResultDeliveryRecord, error) {
	var out ResultDeliveryRecord
	var sourceID sql.NullInt64
	var expires sql.NullTime
	var contentType, reasonCode sql.NullString
	var sourceSequence sql.NullInt64
	var mediaRefID, mediaAssetID sql.NullInt64
	var mediaLocator sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT d.id,d.call_id,d.attempt_id,d.user_id,d.token_id,d.delivery_mode,d.source_kind,d.state,d.content_type,d.reason_code,d.expires_at,pt.source_url_policy,s.source_seq,s.encrypted_url_blob_id,d.media_asset_ref_id,mr.media_asset_id,COALESCE(NULLIF(ma.storage_locator,''),ma.object_key) FROM gw_result_deliveries d JOIN gw_api_call_attempts a ON a.id=d.attempt_id JOIN gw_product_transports pt ON pt.id=a.product_transport_id AND pt.release_id=a.catalog_release_id LEFT JOIN gw_result_delivery_sources s ON s.id=d.current_source_id AND s.state='active' LEFT JOIN gw_media_asset_refs mr ON mr.id=d.media_asset_ref_id LEFT JOIN gw_media_assets ma ON ma.id=mr.media_asset_id AND ma.state='active' WHERE `+predicate+` ORDER BY d.result_ordinal LIMIT 1`, args...).Scan(&out.ID, &out.CallID, &out.AttemptID, &out.UserID, &out.TokenID, &out.Mode, &out.SourceKind, &out.State, &contentType, &reasonCode, &expires, &out.SourceURLPolicy, &sourceSequence, &sourceID, &mediaRefID, &mediaAssetID, &mediaLocator)
	if err == sql.ErrNoRows {
		return ResultDeliveryRecord{}, ErrNotFound
	}
	if err != nil {
		return ResultDeliveryRecord{}, err
	}
	if contentType.Valid {
		out.ContentType = contentType.String
	}
	if reasonCode.Valid {
		out.ReasonCode = reasonCode.String
	}
	if sourceSequence.Valid {
		out.SourceSequence = uint64(sourceSequence.Int64)
	}
	if sourceID.Valid {
		out.SourceBlobID = uint64(sourceID.Int64)
	}
	if mediaRefID.Valid {
		out.MediaAssetRefID = uint64(mediaRefID.Int64)
	}
	if mediaAssetID.Valid {
		out.MediaAssetID = uint64(mediaAssetID.Int64)
	}
	if mediaLocator.Valid {
		out.MediaLocator = mediaLocator.String
	}
	if expires.Valid {
		out.ExpiresAt = &expires.Time
	}
	if out.State == "ready" && out.Mode == "reference" && (out.SourceBlobID == 0 || out.SourceSequence == 0 || out.SourceURLPolicy == "" || out.SourceURLPolicy != "fixed" && (out.ExpiresAt == nil || !out.ExpiresAt.After(nowUTC()))) {
		return ResultDeliveryRecord{}, ErrConflict
	}
	if out.State == "ready" && out.Mode == "managed_copy" && (out.MediaAssetRefID == 0 || out.MediaAssetID == 0 || out.MediaLocator == "") {
		return ResultDeliveryRecord{}, ErrConflict
	}
	return out, nil
}

// ManagedCopyDeliveryInput binds a verified staging object to a delivery. The
// provider source URL is deliberately absent from this record.
type ManagedCopyDeliveryInput struct {
	ResultDeliveryInput
	MediaAssetID        uint64
	ContentType, SHA256 string
	ContentLength       uint64
}

func (s *Store) CreateManagedCopyDelivery(ctx context.Context, tx *sql.Tx, in ManagedCopyDeliveryInput) (uint64, error) {
	if in.Mode != "managed_copy" || in.SourceKind != "remote_url" && in.SourceKind != "inline_response" || in.MediaAssetID == 0 || in.ContentType == "" || in.ContentLength == 0 || !validHexDigest(in.SHA256, 32) {
		return 0, ErrInvalidInput
	}
	deliveryID, err := s.CreateResultDelivery(ctx, tx, in.ResultDeliveryInput)
	if err != nil {
		return 0, err
	}
	var userID, tokenID, contentLength uint64
	var purpose, state, contentType, digest string
	var locator sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT user_id,token_id,purpose,state,content_type,content_length,sha256,storage_locator FROM gw_media_assets WHERE id=? FOR UPDATE`, in.MediaAssetID).
		Scan(&userID, &tokenID, &purpose, &state, &contentType, &contentLength, &digest, &locator); err != nil {
		return 0, err
	}
	if userID != in.UserID || tokenID != in.TokenID || purpose != "result" || state != "staging" || !locator.Valid || locator.String == "" || contentType != in.ContentType || contentLength != in.ContentLength || !strings.EqualFold(digest, in.SHA256) {
		return 0, ErrConflict
	}
	if err := s.TransitionMediaAssetWithReason(ctx, tx, in.MediaAssetID, "staging", "active", "managed_copy_published"); err != nil {
		return 0, err
	}
	refID, err := s.AddMediaAssetRef(ctx, tx, MediaAssetRefInput{MediaAssetID: in.MediaAssetID, UserID: in.UserID, TokenID: in.TokenID, Role: "result", Ordinal: in.Ordinal, ResultDeliveryID: &deliveryID})
	if err != nil {
		return 0, err
	}
	now := nowUTC()
	if err := requireOneRow(tx.ExecContext(ctx, `UPDATE gw_result_deliveries SET media_asset_ref_id=?,content_sha256=?,content_length=?,content_type=?,state='ready',reason_code='managed_copy_available',state_version=state_version+1,updated_at=? WHERE id=? AND state='pending'`, refID, in.SHA256, in.ContentLength, in.ContentType, now, deliveryID)); err != nil {
		return 0, err
	}
	if err := requireOneRow(tx.ExecContext(ctx, `INSERT INTO gw_state_transition_events(result_delivery_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,'pending','ready',2,'managed_copy_available',?)`, deliveryID, now)); err != nil {
		return 0, err
	}
	return deliveryID, nil
}

type CallbackDeliveryInput struct {
	CallID, TargetID, EventSeq, PayloadBlobID uint64
	PayloadHMAC                               string
	ReplayExpiresAt                           *time.Time
}

func (s *Store) CreateCallbackDelivery(ctx context.Context, tx *sql.Tx, in CallbackDeliveryInput) (uint64, error) {
	if tx == nil || in.CallID == 0 || in.TargetID == 0 || in.EventSeq == 0 || in.PayloadBlobID == 0 || !validHexDigest(in.PayloadHMAC, 32) || in.ReplayExpiresAt == nil || !in.ReplayExpiresAt.After(nowUTC()) {
		return 0, ErrInvalidInput
	}
	var targetCallID uint64
	if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_callback_targets WHERE id=? FOR SHARE`, in.TargetID).Scan(&targetCallID); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	} else if targetCallID != in.CallID {
		return 0, ErrConflict
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_callback_deliveries(call_id,callback_target_id,callback_event_seq,encrypted_payload_blob_id,payload_hmac,state,state_version,replay_expires_at,created_at,updated_at) VALUES (?,?,?,?,?,'pending',1,?,?,?)`, in.CallID, in.TargetID, in.EventSeq, in.PayloadBlobID, in.PayloadHMAC, in.ReplayExpiresAt, now, now)
	if err != nil {
		return 0, fmt.Errorf("insert callback delivery: %w", err)
	}
	return lastID(result)
}
