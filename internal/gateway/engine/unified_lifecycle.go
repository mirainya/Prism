package engine

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
)

// unifiedLifecycle mirrors the execution facts into the unified gateway
// ledger. Legacy lifecycle calls remain the compatibility source for routes
// that are not selected from the published unified catalog.
type unifiedLifecycle struct {
	store     *repository.Store
	callID    uint64
	attemptID uint64
	asyncID   uint64
	pending   map[string][]byte
}

func unifiedRoute(route *routing.RouteResult) bool {
	return route != nil && route.ReleaseID != 0 && route.OperationContractID != 0 && route.ModelOperationID != 0 && route.SKUID != 0 && route.RouteID != 0 && route.OfferingID != 0 && route.ProductTransportID != 0 && route.CredentialPoolID != 0 && route.CredentialID != 0 && route.CredentialVersionID != 0 && route.PurposeGrantID != 0
}

func newUnifiedLifecycle(route *routing.RouteResult, request canonical.Request, publicID string, userID, tokenID uint, requestStore bool) (*unifiedLifecycle, error) {
	if !unifiedRoute(route) || strings.TrimSpace(publicID) == "" || userID == 0 || tokenID == 0 {
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
	l := &unifiedLifecycle{store: store, pending: make(map[string][]byte)}
	currency := strings.ToUpper(strings.TrimSpace(route.Currency))
	if currency == "" {
		currency = strings.ToUpper(strings.TrimSpace(os.Getenv("PRISM_GATEWAY_CURRENCY")))
	}
	if currency == "" {
		currency = "USD"
	}
	currencyVersion := route.CurrencyVersion
	if currencyVersion == 0 {
		currencyVersion = 1
	}
	// The catalog quote is deliberately zero when no usage has been measured;
	// the published SKU still supplies the immutable currency/version boundary.
	delivery := "reference"
	if requestStore {
		delivery = "managed_copy"
	}
	quotedAmount := estimate(route, request).String()
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		var existing uint64
		if err := tx.QueryRowContext(context.Background(), "SELECT id FROM gw_api_calls WHERE public_id=? FOR UPDATE", publicID).Scan(&existing); err == nil {
			l.callID = existing
			return nil
		} else if err != sql.ErrNoRows {
			return err
		}
		id, err := store.CreateCall(context.Background(), tx, repository.CreateCallInput{
			PublicID: publicID, UserID: uint64(userID), TokenID: uint64(tokenID),
			OperationContractID: uint64(route.OperationContractID), CatalogReleaseID: uint64(route.ReleaseID),
			ModelOperationID: uint64(route.ModelOperationID), SKUID: uint64(route.SKUID), Currency: currency,
			CurrencyVersion: uint32(currencyVersion), DeliveryMode: delivery, QuotedAmount: quotedAmount,
		})
		if err != nil {
			return err
		}
		l.callID = id
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create unified call: %w", err)
	}
	return l, nil
}

func (l *unifiedLifecycle) recordPayload(ctx context.Context, kind string, data []byte) {
	if l == nil || len(data) == 0 || l.store == nil || l.callID == 0 || (kind != "request" && kind != "result") {
		return
	}
	// Payload encryption is mandatory. Missing payload keys disable retention,
	// never cause a plaintext fallback.
	kek, err := decodeGatewayKey("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return
	}
	hmacKey, err := decodeGatewayHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return
	}
	_ = l.store.WithTx(ctx, func(tx *sql.Tx) error {
		var keyringID uint64
		var version uint32
		if err := tx.QueryRowContext(ctx, "SELECT id,current_version FROM crypto_keyring_state WHERE purpose='gateway-payload' FOR SHARE").Scan(&keyringID, &version); err != nil {
			return nil
		}
		if version == 0 {
			return nil
		}
		owner := []byte(fmt.Sprintf("call:%d:%s", l.callID, kind))
		blobID, err := l.store.PutEncryptedBlob(ctx, tx, repository.BlobInput{KeyringID: keyringID, KEKVersion: version, Purpose: "gateway-payload", SchemaVersion: 1, Owner: owner, Plaintext: data, KEK: kek, HMACKey: hmacKey})
		if err != nil {
			return err
		}
		digest := security.HMACSHA256(hmacKey, data)
		contentHMAC := fmt.Sprintf("%x", digest[:])
		payloadID, err := l.store.CreateCallPayload(ctx, tx, repository.CallPayloadInput{CallID: l.callID, Kind: kind, SchemaVersion: 1, EncryptedBlobID: &blobID, ContentHMAC: contentHMAC, ContentLength: uint64(len(data))})
		if err != nil {
			return err
		}
		column := "request_payload_id"
		if kind == "result" {
			column = "result_payload_id"
		}
		_, err = tx.ExecContext(ctx, "UPDATE gw_api_calls SET "+column+"=? WHERE id=? AND "+column+" IS NULL", payloadID, l.callID)
		return err
	})
}

func decodeGatewayKey(name string) ([]byte, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil, fmt.Errorf("%s is not configured", name)
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, fmt.Errorf("%s must be a base64 encoded 32 byte key", name)
	}
	return decoded, nil
}

func decodeGatewayHMACKey(name string) ([]byte, error) { return decodeGatewayKey(name) }

func (l *unifiedLifecycle) startAttempt(ctx context.Context, route *routing.RouteResult, asynchronous bool, scopeKey string) error {
	if l == nil || l.store == nil || l.callID == 0 || !unifiedRoute(route) {
		return nil
	}
	err := l.store.WithTx(ctx, func(tx *sql.Tx) error {
		id, err := l.store.BeginAttempt(ctx, tx, repository.BeginAttemptInput{
			CallID: l.callID, CatalogReleaseID: uint64(route.ReleaseID), SKUID: uint64(route.SKUID), RouteID: uint64(route.RouteID),
			OfferingID: uint64(route.OfferingID), ProductTransportID: uint64(route.ProductTransportID), CredentialPoolID: uint64(route.CredentialPoolID),
			CredentialID: uint64(route.CredentialID), CredentialVersionID: uint64(route.CredentialVersionID), PurposeGrantID: uint64(route.PurposeGrantID),
		})
		if err == nil {
			l.attemptID = id
			if asynchronous {
				asyncID, asyncErr := l.store.CreateAsyncExecution(ctx, tx, repository.CreateAsyncInput{AttemptID: id, ScopeKind: "credential", ScopeKey: scopeKey})
				if asyncErr != nil {
					return asyncErr
				}
				l.asyncID = asyncID
				_, asyncErr = l.store.TransitionAsync(ctx, tx, asyncID, execution.AsyncAllocated, execution.AsyncSubmitting, 1, "submit", "submit")
				if asyncErr != nil {
					return asyncErr
				}
			}
		}
		return err
	})
	return err
}

func (l *unifiedLifecycle) finish(ctx context.Context, attemptState execution.AttemptState, callState execution.CallState, reason string) error {
	if l == nil || l.store == nil || l.callID == 0 || l.attemptID == 0 {
		return nil
	}
	return l.store.WithTx(ctx, func(tx *sql.Tx) error {
		if l.asyncID != 0 {
			var asyncState string
			if err := tx.QueryRowContext(ctx, "SELECT state FROM gw_async_executions WHERE id=? FOR UPDATE", l.asyncID).Scan(&asyncState); err == nil {
				target := execution.AsyncFailed
				if callState == execution.CallCompleted {
					target = execution.AsyncSucceeded
				}
				if callState == execution.CallCancelled {
					target = execution.AsyncCancelled
				}
				if execution.AsyncState(asyncState) != target {
					version := uint64(0)
					_ = tx.QueryRowContext(ctx, "SELECT state_version FROM gw_async_executions WHERE id=?", l.asyncID).Scan(&version)
					if execution.AsyncState(asyncState) == execution.AsyncSubmitting {
						if _, err := l.store.TransitionAsync(ctx, tx, l.asyncID, execution.AsyncSubmitting, execution.AsyncAccepted, version, "provider_accepted", ""); err != nil {
							return err
						}
						asyncState = string(execution.AsyncAccepted)
						version++
					}
					if _, err := l.store.TransitionAsync(ctx, tx, l.asyncID, execution.AsyncState(asyncState), target, version, reason, ""); err != nil {
						return err
					}
				}
			}
		}
		var attemptFrom string
		var attemptVersion, callVersion uint64
		var callFrom string
		if err := tx.QueryRowContext(ctx, "SELECT state,state_version FROM gw_api_call_attempts WHERE id=? AND call_id=? FOR UPDATE", l.attemptID, l.callID).Scan(&attemptFrom, &attemptVersion); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		if attemptFrom != string(attemptState) {
			if err := execution.TransitionAttempt(execution.AttemptState(attemptFrom), attemptState); err != nil {
				return nil
			}
			if _, err := tx.ExecContext(ctx, "UPDATE gw_api_call_attempts SET state=?,state_version=state_version+1,updated_at=? WHERE id=? AND state_version=?", attemptState, time.Now().UTC(), l.attemptID, attemptVersion); err != nil {
				return err
			}
			_, _ = tx.ExecContext(ctx, "INSERT INTO gw_state_transition_events(attempt_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)", l.attemptID, attemptFrom, attemptState, attemptVersion+1, reason, time.Now().UTC())
		}
		if err := tx.QueryRowContext(ctx, "SELECT status,state_version FROM gw_api_calls WHERE id=? FOR UPDATE", l.callID).Scan(&callFrom, &callVersion); err != nil {
			return err
		}
		if callFrom == string(callState) {
			return nil
		}
		if err := execution.TransitionCall(execution.CallState(callFrom), callState); err != nil {
			return nil
		}
		res, err := tx.ExecContext(ctx, "UPDATE gw_api_calls SET status=?,current_attempt_id=NULL,final_attempt_id=?,state_version=state_version+1,updated_at=? WHERE id=? AND state_version=?", callState, l.attemptID, time.Now().UTC(), l.callID, callVersion)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			_, _ = tx.ExecContext(ctx, "INSERT INTO gw_state_transition_events(call_id,old_state,new_state,state_version,reason_code,created_at) VALUES (?,?,?,?,?,?)", l.callID, callFrom, callState, callVersion+1, reason, time.Now().UTC())
		}
		return nil
	})
}
