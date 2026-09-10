package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
)

// unifiedLifecycle mirrors the execution facts into the unified gateway
// ledger. Legacy lifecycle calls remain the compatibility source for routes
// that are not selected from the published unified catalog.
type unifiedLifecycle struct {
	store        *repository.Store
	publicID     string
	callID       uint64
	attemptID    uint64
	asyncID      uint64
	resourceID   uint64
	resourceKind string
	releaseID    uint
	skuID        uint
	pricing      billing.RateSchedule

	resourceMu     sync.Mutex
	responseStatus string
}

func unifiedRoute(route *routing.RouteResult) bool {
	return route != nil && route.ReleaseID != 0 && route.OperationContractID != 0 && route.ModelOperationID != 0 && route.SKUID != 0 && route.RouteID != 0 && route.OfferingID != 0 && route.CostPlanID != 0 && route.ProductTransportID != 0 && route.CredentialPoolID != 0 && route.CredentialID != 0 && route.CredentialVersionID != 0 && route.PurposeGrantID != 0
}

func newUnifiedLifecycle(ctx context.Context, route *routing.RouteResult, request canonical.Request, publicID string, options ExecuteOptions) (*unifiedLifecycle, error) {
	if !unifiedRoute(route) {
		return nil, nil
	}
	parsedID, parseErr := uuid.Parse(publicID)
	if parseErr != nil || len(publicID) != 36 || parsedID.String() != publicID || options.UserID == 0 || options.TokenID == 0 {
		return nil, repository.ErrInvalidInput
	}
	if (options.ResourceType == "") != (options.ResourceID == "") || options.ResourceType != "" && options.ResourceType != "response" {
		return nil, repository.ErrInvalidInput
	}
	pricing, err := canonicalSellSchedule(route)
	if err != nil {
		return nil, err
	}
	if route.DeliveryMode != "reference" && route.DeliveryMode != "managed_copy" {
		return nil, repository.ErrInvalidInput
	}
	quote, err := pricing.Reserve()
	if err != nil {
		return nil, err
	}
	kek, err := decodeGatewayKey("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return nil, err
	}
	hmacKey, err := decodeGatewayHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return nil, err
	}
	requestBody := append([]byte(nil), options.DownstreamRequest...)
	if len(requestBody) == 0 {
		requestBody, err = json.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("encode canonical call request: %w", err)
		}
	}
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	l := &unifiedLifecycle{store: store, publicID: publicID, pricing: pricing, releaseID: route.ReleaseID, skuID: route.SKUID}
	var replay *IdempotentReplayError
	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		if options.EnforceIdempotencyPolicy || options.Idempotency != nil {
			var mode string
			if err := tx.QueryRowContext(ctx, `SELECT idempotency_mode FROM gw_skus WHERE id=? AND release_id=?`, route.SKUID, route.ReleaseID).Scan(&mode); err != nil {
				return err
			}
			hasKey := options.Idempotency != nil
			if mode == "required" && !hasKey || mode == "forbidden" && hasKey || mode != "required" && mode != "optional" && mode != "forbidden" {
				return repository.ErrInvalidInput
			}
		}
		var idempotencyID uint64
		if options.Idempotency != nil {
			idempotency := *options.Idempotency
			if idempotency.TokenID != uint64(options.TokenID) || idempotency.OperationContractID != uint64(route.OperationContractID) {
				return repository.ErrInvalidInput
			}
			reserved, err := store.ReserveIdempotency(ctx, tx, idempotency)
			if err != nil {
				return err
			}
			if reserved.Reused {
				if reserved.CallID == nil {
					return repository.ErrConflict
				}
				var resourceID uint64
				var callPublicID, resourcePublicID, kind string
				err := tx.QueryRowContext(ctx, `SELECT c.public_id,r.id,r.public_id,r.resource_kind
FROM gw_api_calls c
JOIN gw_api_resources r ON r.call_id=c.id
WHERE c.id=? AND c.user_id=? AND c.token_id=?`, *reserved.CallID, options.UserID, options.TokenID).
					Scan(&callPublicID, &resourceID, &resourcePublicID, &kind)
				if err != nil {
					return err
				}
				if options.ResourceType == "" || kind != options.ResourceType {
					return repository.ErrConflict
				}
				replay = &IdempotentReplayError{CallID: *reserved.CallID, CallPublicID: callPublicID, ResourceID: resourceID, ResourcePublicID: resourcePublicID}
				return nil
			}
			idempotencyID = reserved.ID
		}
		if replay != nil {
			return nil
		}
		var existing, owner, token, release, sku uint64
		if err := tx.QueryRowContext(ctx, "SELECT id,user_id,token_id,catalog_release_id,sku_id FROM gw_api_calls WHERE public_id=? FOR UPDATE", publicID).Scan(&existing, &owner, &token, &release, &sku); err == nil {
			if owner != uint64(options.UserID) || token != uint64(options.TokenID) || release != uint64(route.ReleaseID) || sku != uint64(route.SKUID) {
				return repository.ErrConflict
			}
			l.callID = existing
			if err := l.putPayload(ctx, tx, "request", requestBody, kek, hmacKey); err != nil {
				return err
			}
			return l.attachResource(ctx, tx, options, true)
		} else if err != sql.ErrNoRows {
			return err
		}
		id, err := store.CreateCall(ctx, tx, repository.CreateCallInput{
			PublicID: publicID, UserID: uint64(options.UserID), TokenID: uint64(options.TokenID),
			OperationContractID: uint64(route.OperationContractID), CatalogReleaseID: uint64(route.ReleaseID),
			ModelOperationID: uint64(route.ModelOperationID), SKUID: uint64(route.SKUID), Currency: pricing.Currency.Code,
			CurrencyVersion: pricing.Currency.Version, DeliveryMode: route.DeliveryMode, QuotedAmount: quote.Amount.String(),
		})
		if err != nil {
			return err
		}
		l.callID = id
		if idempotencyID != 0 {
			if err := store.AttachIdempotencyCall(ctx, tx, idempotencyID, id); err != nil {
				return err
			}
		}
		if err := l.putPayload(ctx, tx, "request", requestBody, kek, hmacKey); err != nil {
			return err
		}
		return l.attachResource(ctx, tx, options, false)
	})
	if err != nil {
		return nil, fmt.Errorf("create unified call: %w", err)
	}
	if replay != nil {
		return nil, replay
	}
	return l, nil
}

func (l *unifiedLifecycle) attachResource(ctx context.Context, tx *sql.Tx, options ExecuteOptions, existingCall bool) error {
	if options.ResourceType == "" {
		return nil
	}
	if tx == nil || l == nil || l.store == nil || l.callID == 0 || options.ResourceID == "" {
		return repository.ErrInvalidInput
	}
	if existingCall {
		var resourceID uint64
		var kind, publicID string
		var previous sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT r.id,r.resource_kind,r.public_id,response.previous_response_resource_id
FROM gw_api_resources r
JOIN gw_ai_responses response ON response.resource_id=r.id
WHERE r.call_id=? FOR UPDATE`, l.callID).Scan(&resourceID, &kind, &publicID, &previous)
		if err == sql.ErrNoRows {
			return repository.ErrConflict
		}
		if err != nil {
			return err
		}
		if kind != options.ResourceType || publicID != options.ResourceID || !sameNullableID(previous, options.PreviousResourceID) {
			return repository.ErrConflict
		}
		l.resourceID, l.resourceKind = resourceID, kind
		return nil
	}
	resourceID, err := l.store.CreateResource(ctx, tx, repository.ResourceInput{
		PublicID: options.ResourceID, Kind: options.ResourceType, CallID: l.callID,
		UserID: uint64(options.UserID), TokenID: uint64(options.TokenID),
	})
	if err != nil {
		return err
	}
	if err := l.store.CreateAIResponse(ctx, tx, repository.AIResponseInput{
		ResourceID: resourceID, ResponseNo: options.ResourceID, Status: "in_progress",
		PreviousResourceID: options.PreviousResourceID, Summary: options.ResourceSummary,
	}); err != nil {
		return err
	}
	l.resourceID, l.resourceKind = resourceID, options.ResourceType
	return nil
}

func sameNullableID(value sql.NullInt64, expected *uint64) bool {
	if expected == nil {
		return !value.Valid
	}
	return value.Valid && value.Int64 > 0 && uint64(value.Int64) == *expected
}

func (l *unifiedLifecycle) recordPayload(ctx context.Context, kind string, data []byte) error {
	if l == nil {
		return nil
	}
	if len(data) == 0 || l.store == nil || l.callID == 0 || (kind != "request" && kind != "result") {
		return repository.ErrInvalidInput
	}
	kek, err := decodeGatewayKey("PRISM_GATEWAY_PAYLOAD_KEK_B64")
	if err != nil {
		return err
	}
	hmacKey, err := decodeGatewayHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return err
	}
	err = l.store.WithTx(ctx, func(tx *sql.Tx) error {
		return l.putPayload(ctx, tx, kind, data, kek, hmacKey)
	})
	if err == nil && kind == "result" && l.resourceKind == "response" {
		var response canonical.Response
		if json.Unmarshal(data, &response) == nil && validProjectedResponseStatus(response.Status) {
			l.resourceMu.Lock()
			l.responseStatus = response.Status
			l.resourceMu.Unlock()
		}
	}
	return err
}

func (l *unifiedLifecycle) putPayload(ctx context.Context, tx *sql.Tx, kind string, data, kek, hmacKey []byte) error {
	var keyringID uint64
	var version uint32
	if err := tx.QueryRowContext(ctx, `SELECT k.id,k.current_version FROM crypto_keyring_state k
JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version AND v.status='current'
WHERE k.purpose='gateway-payload' FOR SHARE`).Scan(&keyringID, &version); err != nil {
		return fmt.Errorf("load payload keyring: %w", err)
	}
	if version == 0 {
		return repository.ErrConflict
	}
	retention := config.APICallPayloadRetentionDuration()
	if kind == "result" {
		retention = config.ResourceHistoryRetentionDuration()
	}
	retentionUntil := time.Now().UTC().Add(retention)
	_, err := l.store.PutCallPayload(ctx, tx, l.callID, kind, repository.BlobInput{
		KeyringID: keyringID, KEKVersion: version, Plaintext: data, KEK: kek, HMACKey: hmacKey, RetentionUntil: &retentionUntil,
	})
	return err
}

func decodeGatewayKey(name string) ([]byte, error) {
	decoded, err := security.DecodeBase64Key(os.Getenv(name))
	if err != nil {
		return nil, fmt.Errorf("%s must be a base64 encoded 32 byte key", name)
	}
	return decoded, nil
}

func decodeGatewayHMACKey(name string) ([]byte, error) { return decodeGatewayKey(name) }

func (l *unifiedLifecycle) startAttempt(ctx context.Context, route *routing.RouteResult, asynchronous bool, scopeKey string) error {
	if l == nil || l.store == nil || l.callID == 0 || !unifiedRoute(route) {
		return nil
	}
	if route.ReleaseID != l.releaseID || route.SKUID != l.skuID {
		return fmt.Errorf("%w: retry cannot change the call's catalog or SKU", repository.ErrConflict)
	}
	var attemptID, createdAsyncID uint64
	err := l.store.WithTx(ctx, func(tx *sql.Tx) error {
		id, err := l.store.BeginAttempt(ctx, tx, repository.BeginAttemptInput{
			CallID: l.callID, CatalogReleaseID: uint64(route.ReleaseID), SKUID: uint64(route.SKUID), RouteID: uint64(route.RouteID),
			OfferingID: uint64(route.OfferingID), CostPlanID: uint64(route.CostPlanID), ProductTransportID: uint64(route.ProductTransportID), CredentialPoolID: uint64(route.CredentialPoolID),
			CredentialID: uint64(route.CredentialID), CredentialVersionID: uint64(route.CredentialVersionID), PurposeGrantID: uint64(route.PurposeGrantID),
		})
		if err == nil {
			attemptID = id
			if asynchronous {
				var taskScope string
				if err := tx.QueryRowContext(ctx, `SELECT task_scope FROM gw_product_transports WHERE id=? AND release_id=?`, route.ProductTransportID, route.ReleaseID).Scan(&taskScope); err != nil {
					return err
				}
				if taskScope == "task" {
					if _, err := l.store.AcquireCredentialSlot(ctx, tx, repository.SlotInput{CredentialID: uint64(route.CredentialID), CredentialPoolID: uint64(route.CredentialPoolID), Scope: "task", AttemptID: &id}); err != nil {
						return err
					}
				}
				asyncID, asyncErr := l.store.CreateAsyncExecution(ctx, tx, repository.CreateAsyncInput{
					AttemptID: id, ScopeKind: "credential", ScopeKey: scopeKey,
					// Upstream callback token binding is opt-in and requires an
					// adapter implementing CallbackTokenCodec.  This lifecycle
					// path has no adapter capability proof, so it remains
					// polling-only by construction.
					CallbackBinding: nil,
				})
				if asyncErr != nil {
					return asyncErr
				}
				createdAsyncID = asyncID
				// This engine owns the exchange. Only runtime.Submit queues a worker.
				_, asyncErr = l.store.TransitionAsync(ctx, tx, asyncID, execution.AsyncAllocated, execution.AsyncSubmitting, 1, "submit", "")
				if asyncErr != nil {
					return asyncErr
				}
			}
		}
		return err
	})
	if err == nil {
		l.attemptID, l.asyncID = attemptID, createdAsyncID
	}
	return err
}

func (l *unifiedLifecycle) reserve(ctx context.Context, userID, tokenID uint, route *routing.RouteResult, request canonical.Request) (*unifiedReservation, error) {
	if l == nil || l.store == nil || l.callID == 0 || route == nil {
		return nil, repository.ErrInvalidInput
	}
	if route.ReleaseID != l.releaseID || route.SKUID != l.skuID {
		return nil, repository.ErrConflict
	}
	quote, err := l.pricing.Reserve()
	if err != nil {
		return nil, err
	}
	currency, version := l.pricing.Currency.Code, l.pricing.Currency.Version
	reservation := &unifiedReservation{store: l.store, callID: l.callID, pricing: l.pricing}
	err = l.store.WithTx(ctx, func(tx *sql.Tx) error {
		accountID, windowID, err := l.store.ResolveCurrentBillingContext(ctx, tx, uint64(userID), uint64(tokenID), currency, version)
		if err != nil {
			return err
		}
		id, err := l.store.ReserveBilling(ctx, tx, repository.ReservationInput{CallID: l.callID, TokenID: uint64(tokenID), BillingAccountID: accountID, BudgetWindowID: windowID, Amount: quote.Amount.String(), Currency: currency, CurrencyVersion: version})
		reservation.reservationID = id
		return err
	})
	if err != nil {
		return nil, err
	}
	return reservation, nil
}

func (l *unifiedLifecycle) finish(ctx context.Context, attemptState execution.AttemptState, callState execution.CallState, reason string) error {
	if l == nil || l.store == nil || l.callID == 0 {
		return nil
	}
	return l.store.WithTx(ctx, func(tx *sql.Tx) error {
		var attemptFrom execution.AttemptState
		var attemptVersion, callVersion uint64
		var callFrom execution.CallState
		if err := tx.QueryRowContext(ctx, "SELECT status,state_version FROM gw_api_calls WHERE id=? FOR UPDATE", l.callID).Scan(&callFrom, &callVersion); err != nil {
			return err
		}
		if l.attemptID != 0 {
			if err := tx.QueryRowContext(ctx, "SELECT state,state_version FROM gw_api_call_attempts WHERE id=? AND call_id=? FOR UPDATE", l.attemptID, l.callID).Scan(&attemptFrom, &attemptVersion); err != nil {
				return err
			}
		}
		if l.attemptID != 0 && attemptState != "" && attemptFrom != attemptState {
			if err := l.store.TransitionAttempt(ctx, tx, l.attemptID, attemptFrom, attemptState, attemptVersion, reason); err != nil {
				return err
			}
			attemptFrom = attemptState
		}
		if l.asyncID != 0 && attemptState != "" {
			if err := l.finishAsync(ctx, tx, attemptState, reason); err != nil {
				return err
			}
		}
		if l.attemptID != 0 && (attemptFrom == execution.AttemptStarted || attemptFrom == execution.AttemptRecoveryPending) {
			return repository.ErrConflict
		}
		if callState == execution.CallIndeterminate && attemptFrom != execution.AttemptTerminatedUnknown {
			return repository.ErrConflict
		}
		if l.asyncID != 0 {
			if err := l.store.FinalizeAttemptSlot(ctx, tx, l.attemptID); err != nil {
				return err
			}
		}
		if l.resourceID != 0 && l.resourceKind == "response" {
			status, terminal := l.responseStatusForCall(callState)
			if terminal {
				var summary any
				if callState != execution.CallCompleted {
					summary = map[string]any{"error_code": reason}
				}
				if err := l.store.UpdateAIResponse(ctx, tx, l.resourceID, status, summary); err != nil {
					return err
				}
			}
		}
		if callFrom == callState {
			if callState == execution.CallInProgress {
				_, err := tx.ExecContext(ctx, "UPDATE gw_api_calls SET current_attempt_id=NULL WHERE id=? AND current_attempt_id=?", l.callID, l.attemptID)
				return err
			}
			return nil
		}
		var finalAttempt *uint64
		if l.attemptID != 0 && (callState == execution.CallCompleted || callState == execution.CallFailed || callState == execution.CallCancelled || callState == execution.CallIndeterminate) {
			finalAttempt = &l.attemptID
		}
		return l.store.TransitionCall(ctx, tx, l.callID, callFrom, callState, callVersion, reason, finalAttempt)
	})
}

func (l *unifiedLifecycle) responseStatusForCall(state execution.CallState) (string, bool) {
	switch state {
	case execution.CallCompleted:
		l.resourceMu.Lock()
		status := l.responseStatus
		l.resourceMu.Unlock()
		if validProjectedResponseStatus(status) {
			return status, true
		}
		return "completed", true
	case execution.CallFailed:
		return "failed", true
	case execution.CallCancelled:
		return "cancelled", true
	case execution.CallIndeterminate:
		return "incomplete", true
	default:
		return "", false
	}
}

func validProjectedResponseStatus(status string) bool {
	switch status {
	case "completed", "failed", "cancelled", "incomplete":
		return true
	default:
		return false
	}
}

func (l *unifiedLifecycle) finishAsync(ctx context.Context, tx *sql.Tx, attemptState execution.AttemptState, reason string) error {
	var from execution.AsyncState
	var version uint64
	if err := tx.QueryRowContext(ctx, "SELECT state,state_version FROM gw_async_executions WHERE id=? FOR UPDATE", l.asyncID).Scan(&from, &version); err != nil {
		return err
	}
	target, err := asyncAttemptOutcome(attemptState)
	if err != nil || from == target {
		return err
	}
	// A completed provider operation proves acceptance; a failed submission
	// does not. Never manufacture an acceptance event merely to reach failure.
	if from == execution.AsyncSubmitting && target == execution.AsyncSucceeded {
		if _, err := l.store.TransitionAsync(ctx, tx, l.asyncID, from, execution.AsyncAccepted, version, "provider_accepted", ""); err != nil {
			return err
		}
		from, version = execution.AsyncAccepted, version+1
	}
	_, err = l.store.TransitionAsync(ctx, tx, l.asyncID, from, target, version, reason, "")
	return err
}

func asyncAttemptOutcome(state execution.AttemptState) (execution.AsyncState, error) {
	switch state {
	case execution.AttemptCompleted:
		return execution.AsyncSucceeded, nil
	case execution.AttemptFailed:
		return execution.AsyncFailed, nil
	case execution.AttemptCancelled:
		return execution.AsyncCancelled, nil
	case execution.AttemptNotCreated:
		return execution.AsyncNotCreated, nil
	case execution.AttemptTerminatedUnknown:
		return execution.AsyncTerminatedUnknown, nil
	default:
		return "", repository.ErrInvalidInput
	}
}
