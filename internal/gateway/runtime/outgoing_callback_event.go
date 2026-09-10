package runtime

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

const (
	callbackAlgorithmHTTPJSONV1 = "http-post-json-v1"
	callbackPolicyVersion       = uint32(1)
	callbackTargetSchemaVersion = uint32(1)
	callbackEventSchemaVersion  = uint32(1)
	callbackTargetBlobPurpose   = "gateway-callback-target"
	callbackEventBlobPurpose    = "gateway-callback-delivery"
	callbackTargetDigestDomain  = "gateway-callback-target-v1"
	callbackReplayWindow        = 7 * 24 * time.Hour
	maxCallbackURLBytes         = 2048
	callbackSigningSecretBytes  = 32
)

type CallbackDeliveryKeys struct {
	PayloadKEK, PayloadHMAC []byte
}

func (k CallbackDeliveryKeys) valid() bool {
	return len(k.PayloadKEK) == security.KeySize && len(k.PayloadHMAC) == security.KeySize
}

// ConfigureCallbackDelivery enables terminal event creation and outbound
// delivery on a long-lived runtime Service. It must be called before workers
// start.
func (s *Service) ConfigureCallbackDelivery(keys CallbackDeliveryKeys) error {
	if s == nil || s.Store == nil || !keys.valid() || s.callbackKeys != nil {
		return repository.ErrInvalidInput
	}
	s.callbackKeys = &CallbackDeliveryKeys{
		PayloadKEK: bytes.Clone(keys.PayloadKEK), PayloadHMAC: bytes.Clone(keys.PayloadHMAC),
	}
	return nil
}

func (s *Service) Close() {
	if s == nil || s.callbackKeys == nil {
		return
	}
	clear(s.callbackKeys.PayloadKEK)
	clear(s.callbackKeys.PayloadHMAC)
	s.callbackKeys = nil
}

type callbackTargetConfig struct {
	SchemaVersion uint32 `json:"schema_version"`
	URL           string `json:"url"`
	// SigningSecret is a per-target random key that authenticates outbound
	// callback bodies. It is generated at registration, stored encrypted in the
	// target blob, and returned in plaintext to the caller once so the client
	// can verify the X-Prism-Signature header.
	SigningSecret []byte `json:"signing_secret,omitempty"`
}

// callbackBlobIntegrityError marks immutable callback material that cannot be
// authenticated or opened. Database and context errors remain unwrapped so a
// delivery can retry instead of discarding a valid callback during an outage.
type callbackBlobIntegrityError struct {
	err error
}

func (e *callbackBlobIntegrityError) Error() string {
	return "callback blob integrity: " + e.err.Error()
}
func (e *callbackBlobIntegrityError) Unwrap() error { return e.err }

func isCallbackBlobIntegrityError(err error) bool {
	var integrityErr *callbackBlobIntegrityError
	return errors.As(err, &integrityErr)
}

func NormalizeCallbackURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > maxCallbackURLBytes || strings.ContainsRune(raw, '\x00') {
		return "", repository.ErrInvalidInput
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", repository.ErrInvalidInput
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", repository.ErrInvalidInput
	}
	return parsed.String(), nil
}

func newCallbackTargetRegistration(rawURL string, keyringID uint64, kekVersion uint32, kek, hmacKey []byte) (*CallbackTargetRegistration, error) {
	callbackURL, err := NormalizeCallbackURL(rawURL)
	if err != nil || callbackURL == "" {
		return nil, err
	}
	if keyringID == 0 || kekVersion == 0 || len(kek) != security.KeySize || len(hmacKey) != security.KeySize {
		return nil, repository.ErrInvalidInput
	}
	secret := make([]byte, callbackSigningSecretBytes)
	if _, err := cryptorand.Read(secret); err != nil {
		return nil, err
	}
	config, err := json.Marshal(callbackTargetConfig{SchemaVersion: callbackTargetSchemaVersion, URL: callbackURL, SigningSecret: secret})
	if err != nil {
		clear(secret)
		return nil, err
	}
	return &CallbackTargetRegistration{
		Config: repository.BlobInput{
			KeyringID: keyringID, KEKVersion: kekVersion, Plaintext: config,
			KEK: kek, HMACKey: hmacKey,
		},
		Algorithm: callbackAlgorithmHTTPJSONV1, PolicyVersion: callbackPolicyVersion,
		SigningSecret: secret,
	}, nil
}

func callbackTargetOwner(callID uint64) []byte {
	return []byte(fmt.Sprintf("call:%d:callback-target", callID))
}

func callbackEventOwner(callID, eventSeq uint64) []byte {
	return []byte(fmt.Sprintf("call:%d:callback-event:%d", callID, eventSeq))
}

// enqueueTerminalCallbackTx persists the exact JSON body that the delivery
// worker will send. It runs in the same transaction as the terminal call state.
func (s *Service) enqueueTerminalCallbackTx(ctx context.Context, tx *sql.Tx, callID uint64, reason string) error {
	if s == nil || s.Store == nil || tx == nil || callID == 0 || s.callbackKeys == nil || !s.callbackKeys.valid() {
		return repository.ErrInvalidInput
	}
	var eventSeq uint64
	if err := tx.QueryRowContext(ctx, `SELECT callback_event_seq FROM gw_api_calls WHERE id=? FOR UPDATE`, callID).Scan(&eventSeq); err != nil {
		return err
	}
	if eventSeq != 0 {
		return nil
	}
	var targetID uint64
	var targetAlgorithm string
	var targetPolicy uint32
	err := tx.QueryRowContext(ctx, `SELECT id,algorithm,policy_version FROM gw_callback_targets WHERE call_id=? FOR SHARE`, callID).
		Scan(&targetID, &targetAlgorithm, &targetPolicy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if targetAlgorithm != callbackAlgorithmHTTPJSONV1 || targetPolicy != callbackPolicyVersion {
		return repository.ErrConflict
	}
	eventSeq = 1
	payload, err := s.buildVideoCallbackPayload(ctx, tx, callID, reason)
	if err != nil {
		return err
	}
	var keyringID uint64
	var kekVersion uint32
	if err := tx.QueryRowContext(ctx, `SELECT k.id,k.current_version
FROM crypto_keyring_state k
JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current'
WHERE k.purpose='gateway-payload'`).Scan(&keyringID, &kekVersion); err != nil {
		return err
	}
	now := time.Now().UTC()
	replayExpiresAt := now.Add(callbackReplayWindow)
	blobID, err := s.Store.PutEncryptedBlob(ctx, tx, repository.BlobInput{
		KeyringID: keyringID, KEKVersion: kekVersion,
		Purpose: callbackEventBlobPurpose, SchemaVersion: callbackEventSchemaVersion,
		Owner: callbackEventOwner(callID, eventSeq), Plaintext: payload,
		KEK: s.callbackKeys.PayloadKEK, HMACKey: s.callbackKeys.PayloadHMAC,
		RetentionUntil: &replayExpiresAt,
	})
	if err != nil {
		return err
	}
	digest := security.HMACSHA256(s.callbackKeys.PayloadHMAC, payload)
	if _, err := s.Store.CreateCallbackDelivery(ctx, tx, repository.CallbackDeliveryInput{
		CallID: callID, TargetID: targetID, EventSeq: eventSeq, PayloadBlobID: blobID,
		PayloadHMAC: hex.EncodeToString(digest[:]), ReplayExpiresAt: &replayExpiresAt,
	}); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE gw_api_calls SET callback_event_seq=?,updated_at=updated_at WHERE id=? AND callback_event_seq=0`, eventSeq, callID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return repository.ErrConflict
	}
	return nil
}

func (s *Service) buildVideoCallbackPayload(ctx context.Context, tx *sql.Tx, callID uint64, reason string) ([]byte, error) {
	var publicID, callStatus, taskStatus string
	var progress uint8
	var createdAt, updatedAt time.Time
	var specification []byte
	var resultPayloadID sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT c.public_id,c.status,c.created_at,c.updated_at,c.result_payload_id,v.status,v.progress,v.specification_summary
FROM gw_api_calls c
JOIN gw_api_resources r ON r.call_id=c.id AND r.resource_kind='video_task'
JOIN gw_video_tasks v ON v.resource_id=r.id
WHERE c.id=?`, callID).Scan(&publicID, &callStatus, &createdAt, &updatedAt, &resultPayloadID, &taskStatus, &progress, &specification)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	status := callbackPublicVideoStatus(callStatus, taskStatus)
	payload := map[string]any{
		"schema_version": callbackEventSchemaVersion,
		"id":             publicID, "status": status, "progress": progress,
		"created_at":   createdAt.UTC().Format(time.RFC3339Nano),
		"updated_at":   updatedAt.UTC().Format(time.RFC3339Nano),
		"completed_at": updatedAt.UTC().Format(time.RFC3339Nano),
	}
	var summary map[string]any
	if len(specification) > 0 && json.Unmarshal(specification, &summary) == nil {
		for _, key := range []string{"model", "service_tier", "resolution", "ratio", "duration", "generate_audio", "task_mode"} {
			if value, exists := summary[key]; exists {
				payload[key] = value
			}
		}
	}
	if callStatus == "completed" {
		if !resultPayloadID.Valid || resultPayloadID.Int64 <= 0 {
			return nil, repository.ErrConflict
		}
		result, err := s.callbackVideoResult(ctx, tx, callID, uint64(resultPayloadID.Int64))
		if err != nil {
			return nil, err
		}
		payload["result"] = result
	} else {
		if len(reason) == 0 || len(reason) > 128 {
			reason = "video_generation_terminal"
		}
		payload["error"] = map[string]any{"code": reason}
	}
	return json.Marshal(payload)
}

func callbackPublicVideoStatus(callStatus, taskStatus string) string {
	switch callStatus {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "indeterminate":
		return "submission_unknown"
	}
	if taskStatus == "not_created" {
		return "failed"
	}
	return taskStatus
}

func (s *Service) callbackVideoResult(ctx context.Context, tx *sql.Tx, callID, payloadID uint64) (map[string]any, error) {
	var blobID uint64
	if err := tx.QueryRowContext(ctx, `SELECT encrypted_blob_id FROM gw_api_call_payloads WHERE id=? AND call_id=? AND kind='result'`, payloadID, callID).Scan(&blobID); err != nil {
		return nil, err
	}
	plain, err := s.openCallbackBlob(ctx, tx, blobID, []byte(fmt.Sprintf("call:%d:result", callID)), "gateway-payload", callbackEventSchemaVersion)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	var result delivery.VideoResult
	if err := json.Unmarshal(plain, &result); err != nil || result.SchemaVersion != delivery.ResultSchemaVersion || result.VideoDeliveryID == 0 {
		return nil, delivery.ErrInvalidResult
	}
	out := map[string]any{
		"schema_version":    result.SchemaVersion,
		"video_delivery_id": fmt.Sprintf("%d", result.VideoDeliveryID),
	}
	if result.Duration != "" {
		out["duration"] = result.Duration
	}
	videoURL, available, err := s.callbackDeliveryURL(ctx, tx, callID, result.VideoDeliveryID)
	if err != nil {
		return nil, err
	}
	if available {
		out["video_url"] = videoURL
	} else {
		out["delivery_status"] = "unavailable"
	}
	if result.ThumbnailDeliveryID != 0 {
		out["thumbnail_delivery_id"] = fmt.Sprintf("%d", result.ThumbnailDeliveryID)
		thumbnailURL, thumbnailAvailable, err := s.callbackDeliveryURL(ctx, tx, callID, result.ThumbnailDeliveryID)
		if err != nil {
			return nil, err
		}
		if thumbnailAvailable {
			out["thumbnail_url"] = thumbnailURL
		}
	}
	return out, nil
}

func (s *Service) callbackDeliveryURL(ctx context.Context, tx *sql.Tx, callID, deliveryID uint64) (string, bool, error) {
	var mode, state string
	var sourceSequence, sourceBlobID sql.NullInt64
	var mediaLocator sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT d.delivery_mode,d.state,src.source_seq,src.encrypted_url_blob_id,NULLIF(asset.storage_locator,'')
FROM gw_result_deliveries d
LEFT JOIN gw_result_delivery_sources src ON src.id=d.current_source_id AND src.state='active'
LEFT JOIN gw_media_asset_refs ref ON ref.id=d.media_asset_ref_id
LEFT JOIN gw_media_assets asset ON asset.id=ref.media_asset_id AND asset.state='active'
WHERE d.id=? AND d.call_id=?`, deliveryID, callID).Scan(&mode, &state, &sourceSequence, &sourceBlobID, &mediaLocator)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, repository.ErrNotFound
	}
	if err != nil {
		return "", false, err
	}
	if state != "ready" {
		return "", false, nil
	}
	if mode == "managed_copy" {
		if !mediaLocator.Valid || strings.TrimSpace(mediaLocator.String) == "" {
			return "", false, nil
		}
		return mediaLocator.String, true, nil
	}
	if mode != "reference" || !sourceSequence.Valid || !sourceBlobID.Valid {
		return "", false, repository.ErrConflict
	}
	if sourceSequence.Int64 <= 0 || sourceBlobID.Int64 <= 0 {
		return "", false, repository.ErrConflict
	}
	sequence, blobID := uint64(sourceSequence.Int64), uint64(sourceBlobID.Int64)
	plain, err := s.openCallbackBlob(ctx, tx, blobID, []byte(fmt.Sprintf("delivery:%d:source:%d", deliveryID, sequence)), "gateway-result-source", callbackEventSchemaVersion)
	if err != nil {
		return "", false, err
	}
	defer clear(plain)
	return string(plain), true, nil
}

func (s *Service) openCallbackBlob(ctx context.Context, db repository.DB, blobID uint64, owner []byte, purpose string, schemaVersion uint32) ([]byte, error) {
	if s == nil || s.callbackKeys == nil || !s.callbackKeys.valid() {
		return nil, repository.ErrInvalidInput
	}
	envelope, err := s.Store.ReadEncryptedBlob(ctx, db, blobID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrInvalidInput) {
			return nil, &callbackBlobIntegrityError{err: err}
		}
		return nil, err
	}
	if envelope.Purpose != purpose || envelope.SchemaVersion != schemaVersion {
		return nil, &callbackBlobIntegrityError{err: repository.ErrConflict}
	}
	plain, err := repository.OpenBlob(envelope, blobID, owner, s.callbackKeys.PayloadKEK, s.callbackKeys.PayloadHMAC)
	if err != nil {
		return nil, &callbackBlobIntegrityError{err: err}
	}
	return plain, nil
}

func verifyCallbackTargetHMAC(key, plaintext []byte, expected string) bool {
	digest := security.DomainDigest(key, callbackTargetDigestDomain, plaintext)
	encoded := make([]byte, hex.EncodedLen(len(digest)))
	hex.Encode(encoded, digest[:])
	return subtle.ConstantTimeCompare(encoded, []byte(expected)) == 1
}

func verifyCallbackPayloadHMAC(key, plaintext []byte, expected string) bool {
	digest := security.HMACSHA256(key, plaintext)
	encoded := make([]byte, hex.EncodedLen(len(digest)))
	hex.Encode(encoded, digest[:])
	return subtle.ConstantTimeCompare(encoded, []byte(expected)) == 1
}

// EnsureTerminalCallbacks repairs terminal calls committed before their
// callback event was persisted. It is also the upgrade path for in-flight
// calls created by an older binary.
func (s *Service) EnsureTerminalCallbacks(ctx context.Context, limit int) (int, error) {
	if s == nil || s.Store == nil || s.callbackKeys == nil || !s.callbackKeys.valid() || limit <= 0 || limit > 1000 {
		return 0, repository.ErrInvalidInput
	}
	rows, err := s.Store.DB().QueryContext(ctx, `SELECT c.id
FROM gw_api_calls c
JOIN gw_callback_targets target ON target.call_id=c.id
JOIN gw_api_resources resource ON resource.call_id=c.id AND resource.resource_kind='video_task'
WHERE c.callback_event_seq=0 AND c.status IN ('completed','failed','cancelled','indeterminate')
ORDER BY c.id LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	var callIDs []uint64
	for rows.Next() {
		var callID uint64
		if err := rows.Scan(&callID); err != nil {
			rows.Close()
			return 0, err
		}
		callIDs = append(callIDs, callID)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	created := 0
	for _, callID := range callIDs {
		err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
			return s.enqueueTerminalCallbackTx(ctx, tx, callID, "terminal_callback_recovery")
		})
		if errors.Is(err, repository.ErrConflict) {
			continue
		}
		if err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}
