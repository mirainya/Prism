package responses

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
)

const (
	responseIdempotencyReplayTTL = 24 * time.Hour
	responseIdempotencyReuseTTL  = 48 * time.Hour
	responseIdempotencyPollDelay = 25 * time.Millisecond
)

func prepareResponseIdempotency(ctx context.Context, tokenID uint, key string, intent []byte) (*repository.IdempotencyInput, error) {
	if key == "" {
		return nil, nil
	}
	if tokenID == 0 || len(key) > 128 || !utf8.ValidString(key) || strings.TrimSpace(key) == "" || strings.IndexFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return nil, domain.ErrBadRequest("Idempotency-Key is invalid or exceeds 128 bytes")
	}
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	operationContractID, err := store.ResolveOperationContract(ctx, http.MethodPost, "/v1/responses")
	if err != nil {
		return nil, err
	}
	var keyVersion uint32
	if err := db.QueryRowContext(ctx, `SELECT k.current_version FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current' WHERE k.purpose='gateway-payload'`).Scan(&keyVersion); err != nil {
		return nil, err
	}
	keys, err := loadBackgroundKeys()
	if err != nil {
		return nil, err
	}
	defer clearBackgroundKeys(&keys)
	keyDigest := security.HMACSHA256(keys.PayloadHMAC, []byte("gateway-idempotency-v1:"+key))
	requestDigest := security.HMACSHA256(keys.PayloadHMAC, intent)
	now := time.Now().UTC()
	replayExpiresAt, keyReuseAfter := now.Add(responseIdempotencyReplayTTL), now.Add(responseIdempotencyReuseTTL)
	input := &repository.IdempotencyInput{
		TokenID: uint64(tokenID), OperationContractID: operationContractID,
		KeyHMAC: hex.EncodeToString(keyDigest[:]), HMACKeyVersion: keyVersion,
		RequestHMAC:     hex.EncodeToString(requestDigest[:]),
		ReplayExpiresAt: &replayExpiresAt, KeyReuseAfter: &keyReuseAfter,
	}
	// Add aliases for every other readable payload-HMAC version so a rotation
	// window does not split idempotency facts and cause double execution of
	// the same Idempotency-Key.
	if versions, err := store.PayloadHMACKeyVersions(ctx, db); err == nil && len(versions) > 1 {
		for _, version := range versions {
			if version == keyVersion {
				continue
			}
			aliasKey, err := security.LoadVersionedHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64", version)
			if err != nil {
				continue
			}
			aliasKeyDigest := security.HMACSHA256(aliasKey, []byte("gateway-idempotency-v1:"+key))
			aliasRequestDigest := security.HMACSHA256(aliasKey, intent)
			clear(aliasKey)
			input.KeyAliases = append(input.KeyAliases, repository.IdempotencyKeyAlias{
				KeyHMAC: hex.EncodeToString(aliasKeyDigest[:]), RequestHMAC: hex.EncodeToString(aliasRequestDigest[:]), HMACKeyVersion: version,
			})
		}
	}
	return input, nil
}

func findResponseIdempotentReplay(ctx context.Context, userID uint, input *repository.IdempotencyInput) (*Result, error) {
	if input == nil {
		return nil, nil
	}
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	fact, err := store.FindIdempotency(ctx, *input)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, responseIdempotencyError(err)
	}
	if fact.CallID == nil {
		return nil, repository.ErrConflict
	}
	return waitResponseIdempotentReplay(ctx, userID, uint(input.TokenID), *fact.CallID)
}

func waitResponseIdempotentReplay(ctx context.Context, userID, tokenID uint, callID uint64) (*Result, error) {
	for {
		row, err := readUnifiedResponseRowByCallID(ctx, userID, tokenID, callID)
		if err != nil {
			return nil, err
		}
		if unifiedResponseReplayReady(row) {
			response, record, err := unifiedResponseReplay(ctx, row)
			if err != nil {
				return nil, err
			}
			return &Result{
				Response: response, Record: record, CallID: row.CallPublicID,
				PublicPreviousResponseID: record.PreviousResponseID,
				IdempotentReplay:         true, unifiedResource: true,
			}, nil
		}
		timer := time.NewTimer(responseIdempotencyPollDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func responseReplayFromExecutionError(ctx context.Context, userID, tokenID uint, err error) (*Result, bool, error) {
	var replay *engine.IdempotentReplayError
	if !errors.As(err, &replay) {
		return nil, false, nil
	}
	result, replayErr := waitResponseIdempotentReplay(ctx, userID, tokenID, replay.CallID)
	return result, true, replayErr
}

func unifiedResponseReplayReady(row unifiedResponseRow) bool {
	switch row.CallStatus {
	case "completed", "failed", "cancelled", "indeterminate":
		return true
	default:
		return false
	}
}

func responseIdempotencyError(err error) error {
	switch {
	case errors.Is(err, repository.ErrIdempotencyConflict):
		return domain.ErrBadRequest("Idempotency-Key was already used with a different request")
	case errors.Is(err, repository.ErrIdempotencyExpired):
		return domain.ErrBadRequest("Idempotency-Key replay window has expired")
	default:
		return err
	}
}
