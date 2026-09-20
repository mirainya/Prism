// Package runtime is the transaction-level orchestration for unified gateway
// calls. Network transports are deliberately outside these methods: workers
// record dispatch facts here before and after every exchange.
package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

type Service struct {
	Store        *repository.Store
	callbackKeys *CallbackDeliveryKeys
	readiness    ReadinessCheck
}

func New(store *repository.Store) (*Service, error) {
	if store == nil {
		return nil, repository.ErrInvalidInput
	}
	return NewWithReadiness(store, func(ctx context.Context) error {
		return RequireConfiguredReadiness(ctx, store.DB())
	})
}

// NewWithReadiness constructs a service whose claim boundaries share the
// supplied process readiness gate.
func NewWithReadiness(store *repository.Store, readiness ReadinessCheck) (*Service, error) {
	if store == nil {
		return nil, repository.ErrInvalidInput
	}
	if readiness == nil {
		return nil, repository.ErrInvalidInput
	}
	return &Service{Store: store, readiness: readiness}, nil
}

func (s *Service) requireReadiness(ctx context.Context) error {
	if s == nil || s.Store == nil {
		return repository.ErrInvalidInput
	}
	return s.readiness.Require(ctx)
}

type SubmitInput struct {
	Call                          repository.CreateCallInput
	Reservation                   repository.ReservationInput
	Attempt                       repository.BeginAttemptInput
	RequestPayload                repository.BlobInput
	IdempotencyRequest            []byte
	Idempotency                   *repository.IdempotencyInput
	AsyncScopeKind, AsyncScopeKey string
	Asynchronous                  bool
	Background                    bool
	ResourceKind                  string
	ResourceSummary               any
	PreviousResponseResourceID    *uint64
	MediaAssetIDs                 []uint64
	CallbackTarget                *CallbackTargetRegistration
	// CallbackBinding is opt-in.  Only an adapter that explicitly declares
	// callback-token support may provide this material; polling-only adapters
	// must leave it nil so no bearer token is persisted.
	CallbackBinding *repository.CallbackBindingInput
}

type CallbackTargetRegistration struct {
	Config        repository.BlobInput
	Algorithm     string
	PolicyVersion uint32
	// SigningSecret holds the plaintext HMAC key returned to the caller once at
	// registration. It is also encoded inside Config.Plaintext so the delivery
	// worker can sign outbound requests. Callers must clear it after echoing it
	// to the client.
	SigningSecret []byte
}

type Submission struct {
	CallID, AttemptID, AsyncExecutionID, ReservationID, OutboxID uint64
	PublicID                                                     string
	Reused                                                       bool
	// CallbackSigningSecret is the plaintext HMAC key returned once on the
	// initial submission of a call that registered an outbound callback target.
	// It is empty on idempotent replays and on calls without a callback.
	CallbackSigningSecret []byte
}

// Submit creates call, reservation, and attempt in one transaction. No HTTP
// request is sent before this method commits; a worker consumes the outbox
// created for asynchronous calls afterwards.
func (s *Service) Submit(ctx context.Context, in SubmitInput) (Submission, error) {
	if len(in.RequestPayload.Plaintext) == 0 || len(in.RequestPayload.KEK) != security.KeySize || len(in.RequestPayload.HMACKey) != security.KeySize || in.RequestPayload.KeyringID == 0 || in.RequestPayload.KEKVersion == 0 || in.Call.RequestPayloadID != nil || in.Call.ResultPayloadID != nil {
		return Submission{}, fmt.Errorf("validate request payload: %w", repository.ErrInvalidInput)
	}
	if in.Asynchronous && (in.AsyncScopeKind == "" || in.AsyncScopeKey == "") {
		return Submission{}, fmt.Errorf("validate async scope: %w", repository.ErrInvalidInput)
	}
	if in.Background && (in.Asynchronous || in.ResourceKind != "response") || in.ResourceKind == "response" && in.Asynchronous || in.PreviousResponseResourceID != nil && in.ResourceKind != "response" {
		return Submission{}, fmt.Errorf("validate execution mode: %w", repository.ErrInvalidInput)
	}
	if in.Idempotency != nil && (in.Idempotency.TokenID != in.Call.TokenID || in.Idempotency.OperationContractID != in.Call.OperationContractID) {
		return Submission{}, fmt.Errorf("validate idempotency scope: %w", repository.ErrInvalidInput)
	}
	if in.Reservation.TokenID != in.Call.TokenID || in.Reservation.Currency != in.Call.Currency || in.Reservation.CurrencyVersion != in.Call.CurrencyVersion {
		return Submission{}, fmt.Errorf("validate billing reservation: %w", repository.ErrInvalidInput)
	}
	if in.CallbackTarget != nil {
		callback := in.CallbackTarget
		if callback.Algorithm != callbackAlgorithmHTTPJSONV1 || callback.PolicyVersion != callbackPolicyVersion ||
			callback.Config.KeyringID != in.RequestPayload.KeyringID || callback.Config.KEKVersion != in.RequestPayload.KEKVersion ||
			len(callback.Config.Plaintext) == 0 || len(callback.Config.KEK) != security.KeySize || len(callback.Config.HMACKey) != security.KeySize {
			return Submission{}, fmt.Errorf("validate callback target: %w", repository.ErrInvalidInput)
		}
	}
	reservation := in.Reservation
	var out Submission
	err := s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var keyringID uint64
		if err := tx.QueryRowContext(ctx, `SELECT k.id FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id AND v.key_version=k.current_version WHERE k.id=? AND k.purpose='gateway-payload' AND k.current_version=? AND v.status='current' FOR SHARE`, in.RequestPayload.KeyringID, in.RequestPayload.KEKVersion).Scan(&keyringID); err != nil {
			return fmt.Errorf("verify payload keyring: %w", err)
		}
		var idempotencyID uint64
		if in.Idempotency != nil {
			idempotency := *in.Idempotency
			// The caller may have pre-computed RequestHMAC (and its rotation
			// aliases) to keep an Idempotency-Key resolvable across a payload
			// HMAC rotation. Only fall back to the current-version RequestHMAC
			// when the caller did not supply one.
			if idempotency.RequestHMAC == "" {
				idempotencyRequest := in.IdempotencyRequest
				if len(idempotencyRequest) == 0 {
					idempotencyRequest = in.RequestPayload.Plaintext
				}
				digest := security.HMACSHA256(in.RequestPayload.HMACKey, idempotencyRequest)
				idempotency.RequestHMAC = fmt.Sprintf("%x", digest[:])
			}
			reserved, err := s.Store.ReserveIdempotency(ctx, tx, idempotency)
			if err != nil {
				return fmt.Errorf("reserve idempotency key: %w", err)
			}
			if reserved.Reused {
				if reserved.CallID == nil {
					return repository.ErrConflict
				}
				out.CallID = *reserved.CallID
				out.Reused = true
				if err := tx.QueryRowContext(ctx, `SELECT public_id FROM gw_api_calls WHERE id=?`, out.CallID).Scan(&out.PublicID); err != nil {
					return err
				}
				var asyncID, outboxID sql.NullInt64
				if err := tx.QueryRowContext(ctx, `SELECT a.id,x.id,r.id,COALESCE(xo.id,ao.id)
FROM gw_api_call_attempts a
JOIN billing_reservations r ON r.call_id=a.call_id
LEFT JOIN gw_async_executions x ON x.attempt_id=a.id
LEFT JOIN gw_async_outbox xo ON xo.async_execution_id=x.id AND xo.action='submit'
LEFT JOIN gw_async_outbox ao ON ao.attempt_id=a.id AND ao.action='submit'
WHERE a.call_id=? ORDER BY a.attempt_no DESC LIMIT 1`, out.CallID).Scan(&out.AttemptID, &asyncID, &out.ReservationID, &outboxID); err != nil {
					return err
				}
				if asyncID.Valid {
					out.AsyncExecutionID = uint64(asyncID.Int64)
				}
				if outboxID.Valid {
					out.OutboxID = uint64(outboxID.Int64)
				}
				return nil
			}
			idempotencyID = reserved.ID
		}
		if err := s.configureMediaDelivery(ctx, tx, &in); err != nil {
			return fmt.Errorf("configure media delivery: %w", err)
		}
		// Resolve the current billing ancestors only after the idempotency
		// replay branch. A valid replay must not depend on today's open
		// account or budget window.
		if reservation.BillingAccountID == 0 || reservation.BudgetWindowID == 0 {
			if reservation.BillingAccountID != 0 || reservation.BudgetWindowID != 0 {
				return repository.ErrInvalidInput
			}
			accountID, windowID, err := s.Store.ResolveCurrentBillingContext(ctx, tx, in.Call.UserID, in.Call.TokenID, in.Call.Currency, in.Call.CurrencyVersion)
			if err != nil {
				return fmt.Errorf("resolve billing context: %w", err)
			}
			reservation.BillingAccountID, reservation.BudgetWindowID = accountID, windowID
		}
		callID, err := s.Store.CreateCall(ctx, tx, in.Call)
		if err != nil {
			return fmt.Errorf("create call: %w", err)
		}
		out.CallID = callID
		out.PublicID = in.Call.PublicID
		if _, err := s.Store.PutCallPayload(ctx, tx, callID, "request", in.RequestPayload); err != nil {
			return fmt.Errorf("store request payload: %w", err)
		}
		if in.CallbackTarget != nil {
			configBlob := in.CallbackTarget.Config
			configBlob.Purpose, configBlob.SchemaVersion = callbackTargetBlobPurpose, callbackTargetSchemaVersion
			configBlob.Owner = callbackTargetOwner(callID)
			blobID, err := s.Store.PutEncryptedBlob(ctx, tx, configBlob)
			if err != nil {
				return err
			}
			targetDigest := security.DomainDigest(configBlob.HMACKey, callbackTargetDigestDomain, configBlob.Plaintext)
			if _, err := s.Store.CreateCallbackTarget(ctx, tx, repository.CallbackTargetInput{
				CallID: callID, UserID: in.Call.UserID, TokenID: in.Call.TokenID,
				EncryptedConfigBlobID: blobID, TargetHMAC: fmt.Sprintf("%x", targetDigest[:]),
				Algorithm: in.CallbackTarget.Algorithm, PolicyVersion: in.CallbackTarget.PolicyVersion,
			}); err != nil {
				return err
			}
			out.CallbackSigningSecret = in.CallbackTarget.SigningSecret
		}
		if in.ResourceKind != "" {
			resourceID, err := s.Store.CreateResource(ctx, tx, repository.ResourceInput{PublicID: in.Call.PublicID, Kind: in.ResourceKind, CallID: callID, UserID: in.Call.UserID, TokenID: in.Call.TokenID})
			if err != nil {
				return fmt.Errorf("create %s resource: %w", in.ResourceKind, err)
			}
			switch in.ResourceKind {
			case "video_task":
				if err := s.Store.CreateVideoTask(ctx, tx, repository.VideoTaskInput{ResourceID: resourceID, TaskNo: in.Call.PublicID, Status: "queued", Progress: 0, Specification: in.ResourceSummary}); err != nil {
					return fmt.Errorf("create video task projection: %w", err)
				}
			case "capability_task":
				if err := s.Store.CreateCapabilityTask(ctx, tx, repository.CapabilityTaskInput{ResourceID: resourceID, TaskNo: in.Call.PublicID, Status: "queued", Progress: 0, Parameters: in.ResourceSummary}); err != nil {
					return err
				}
			case "response":
				status := "in_progress"
				if in.Background {
					status = "queued"
				}
				if err := s.Store.CreateAIResponse(ctx, tx, repository.AIResponseInput{ResourceID: resourceID, ResponseNo: in.Call.PublicID, Status: status, PreviousResourceID: in.PreviousResponseResourceID, Summary: in.ResourceSummary}); err != nil {
					return err
				}
			case "file":
				return repository.ErrInvalidInput
			default:
				return repository.ErrInvalidInput
			}
		}
		if len(in.MediaAssetIDs) > 32 {
			return repository.ErrInvalidInput
		}
		seenAssets := make(map[uint64]struct{}, len(in.MediaAssetIDs))
		for ordinal, assetID := range in.MediaAssetIDs {
			if assetID == 0 {
				return repository.ErrInvalidInput
			}
			if _, exists := seenAssets[assetID]; exists {
				return repository.ErrInvalidInput
			}
			seenAssets[assetID] = struct{}{}
			if _, err := s.Store.AddMediaAssetRef(ctx, tx, repository.MediaAssetRefInput{MediaAssetID: assetID, UserID: in.Call.UserID, TokenID: in.Call.TokenID, Role: "input", Ordinal: uint32(ordinal), CallID: &callID}); err != nil {
				return err
			}
		}
		if idempotencyID != 0 {
			if err := s.Store.AttachIdempotencyCall(ctx, tx, idempotencyID, callID); err != nil {
				return err
			}
		}
		reservation.CallID = callID
		reservationID, err := s.Store.ReserveBilling(ctx, tx, reservation)
		if err != nil {
			return fmt.Errorf("reserve billing: %w", err)
		}
		out.ReservationID = reservationID
		in.Attempt.CallID = callID
		attemptID, err := s.Store.BeginAttempt(ctx, tx, in.Attempt)
		if err != nil {
			return fmt.Errorf("begin execution attempt: %w", err)
		}
		out.AttemptID = attemptID
		var taskScope string
		if err := tx.QueryRowContext(ctx, `SELECT task_scope FROM gw_product_transports WHERE id=? AND release_id=?`, in.Attempt.ProductTransportID, in.Attempt.CatalogReleaseID).Scan(&taskScope); err != nil {
			return fmt.Errorf("read task scope: %w", err)
		}
		if taskScope != "none" && taskScope != "request" && taskScope != "task" || taskScope == "task" && !in.Asynchronous {
			return fmt.Errorf("validate task scope: %w", repository.ErrInvalidInput)
		}
		if in.Background {
			item, err := s.Store.CreateAttemptOutbox(ctx, tx, repository.AttemptOutboxInput{AttemptID: attemptID, Action: "submit", ExpectedStateVersion: 1, AvailableAt: time.Now().UTC()})
			if err != nil {
				return err
			}
			out.OutboxID = item.ID
			return nil
		}
		if in.Asynchronous {
			if taskScope == "task" {
				if _, err := s.Store.AcquireCredentialSlot(ctx, tx, repository.SlotInput{CredentialID: in.Attempt.CredentialID, CredentialPoolID: in.Attempt.CredentialPoolID, Scope: "task", AttemptID: &attemptID}); err != nil {
					return fmt.Errorf("acquire task credential slot: %w", err)
				}
			}
			asyncID, err := s.Store.CreateAsyncExecution(ctx, tx, repository.CreateAsyncInput{
				AttemptID: attemptID, ScopeKind: in.AsyncScopeKind, ScopeKey: in.AsyncScopeKey,
				CallbackBinding: in.CallbackBinding,
			})
			if err != nil {
				return fmt.Errorf("create async execution: %w", err)
			}
			out.AsyncExecutionID = asyncID
			actionSeq, err := s.Store.TransitionAsync(ctx, tx, asyncID, execution.AsyncAllocated, execution.AsyncSubmitting, 1, "submit", "submit")
			if err != nil {
				return fmt.Errorf("queue async submission: %w", err)
			}
			if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_async_outbox WHERE async_execution_id=? AND action_seq=? AND action='submit'`, asyncID, actionSeq).Scan(&out.OutboxID); err != nil {
				return fmt.Errorf("read async submission: %w", err)
			}
			if err := s.updateAsyncResourceProjection(ctx, tx, asyncID, execution.AsyncSubmitting); err != nil {
				return fmt.Errorf("update async resource projection: %w", err)
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return Submission{}, fmt.Errorf("submit gateway call: %w", err)
	}
	return out, nil
}

// FinishAttempt applies the terminal attempt and call transition together.
// Settlement/release is part of the same transaction, so a retry cannot
// charge a call twice.
func (s *Service) FinishAttempt(ctx context.Context, attemptID uint64, attemptState execution.AttemptState, callState execution.CallState, reason string, facts billing.Facts) error {
	if attemptID == 0 || reason == "" || !validAttemptOutcome(attemptState, callState) {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var callID, attemptVersion uint64
		var attemptFrom string
		if err := tx.QueryRowContext(ctx, `SELECT call_id FROM gw_api_call_attempts WHERE id=?`, attemptID).Scan(&callID); err != nil {
			return err
		}
		reservationID, err := s.Store.LockCallBillingAncestors(ctx, tx, callID)
		if err != nil {
			return err
		}
		var callFrom string
		var callVersion uint64
		if err := tx.QueryRowContext(ctx, `SELECT status,state_version FROM gw_api_calls WHERE id=? FOR UPDATE`, callID).Scan(&callFrom, &callVersion); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT call_id,state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, attemptID).Scan(&callID, &attemptFrom, &attemptVersion); err == sql.ErrNoRows {
			return repository.ErrNotFound
		} else if err != nil {
			return err
		}
		var asyncID uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_async_executions WHERE attempt_id=? FOR UPDATE`, attemptID).Scan(&asyncID); err == nil {
			return repository.ErrConflict
		} else if err != sql.ErrNoRows {
			return err
		}
		attemptCurrent := execution.AttemptState(attemptFrom)
		if attemptCurrent != attemptState {
			if err := s.Store.TransitionAttempt(ctx, tx, attemptID, attemptCurrent, attemptState, attemptVersion, reason); err != nil {
				return err
			}
		}
		callCurrent := execution.CallState(callFrom)
		if callCurrent != callState {
			if err := s.Store.TransitionCall(ctx, tx, callID, callCurrent, callState, callVersion, reason, &attemptID); err != nil {
				return err
			}
		}
		if err := s.Store.FinalizeAttemptSlot(ctx, tx, attemptID); err != nil {
			return err
		}
		return s.finishBilling(ctx, tx, callID, reservationID, callState, facts)
	})
}

func validAttemptOutcome(attempt execution.AttemptState, call execution.CallState) bool {
	switch attempt {
	case execution.AttemptCompleted:
		return call == execution.CallCompleted
	case execution.AttemptFailed, execution.AttemptNotCreated:
		return call == execution.CallFailed || call == execution.CallRetryPending
	case execution.AttemptCancelled:
		return call == execution.CallCancelled
	case execution.AttemptTerminatedUnknown:
		return call == execution.CallIndeterminate
	default:
		return false
	}
}

// FinishAsync applies the provider terminal fact to AsyncExecution, its
// parent Attempt and the Call in one transaction. This prevents a callback
// or poller from leaving an accepted task with a terminal parent mismatch.
func (s *Service) FinishAsync(ctx context.Context, asyncID uint64, target execution.AsyncState, reason string, facts billing.Facts) error {
	return s.finishAsync(ctx, asyncID, target, reason, facts, nil)
}

func (s *Service) finishAsync(ctx context.Context, asyncID uint64, target execution.AsyncState, reason string, facts billing.Facts, record func(context.Context, *sql.Tx) error) error {
	if asyncID == 0 || reason == "" {
		return repository.ErrInvalidInput
	}
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		return s.finishAsyncTx(ctx, tx, asyncID, target, reason, facts, record)
	})
}

// finishAsyncTx is the transaction-bound form used by callback processing.
// Keeping the state, delivery and billing writes in the caller's transaction
// prevents a callback Receipt from being acknowledged before its facts commit.
func (s *Service) finishAsyncTx(ctx context.Context, tx *sql.Tx, asyncID uint64, target execution.AsyncState, reason string, facts billing.Facts, record func(context.Context, *sql.Tx) error) error {
	if tx == nil || asyncID == 0 || reason == "" {
		return repository.ErrInvalidInput
	}
	var attemptState execution.AttemptState
	var callState execution.CallState
	switch target {
	case execution.AsyncSucceeded:
		attemptState, callState = execution.AttemptCompleted, execution.CallCompleted
	case execution.AsyncFailed:
		attemptState, callState = execution.AttemptFailed, execution.CallFailed
	case execution.AsyncCancelled:
		attemptState, callState = execution.AttemptCancelled, execution.CallCancelled
	case execution.AsyncNotCreated:
		attemptState, callState = execution.AttemptNotCreated, execution.CallFailed
	case execution.AsyncTerminatedUnknown:
		attemptState, callState = execution.AttemptTerminatedUnknown, execution.CallIndeterminate
	default:
		return repository.ErrInvalidInput
	}
	var attemptID, asyncVersion, attemptVersion uint64
	var fromAsync, fromAttempt string
	var callID, callVersion uint64
	var fromCall string
	if err := tx.QueryRowContext(ctx, `SELECT a.call_id,a.id FROM gw_async_executions x JOIN gw_api_call_attempts a ON a.id=x.attempt_id WHERE x.id=?`, asyncID).Scan(&callID, &attemptID); err != nil {
		return err
	}
	reservationID, err := s.Store.LockCallBillingAncestors(ctx, tx, callID)
	if err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT status,state_version FROM gw_api_calls WHERE id=? FOR UPDATE`, callID).Scan(&fromCall, &callVersion); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT state,state_version FROM gw_api_call_attempts WHERE id=? FOR UPDATE`, attemptID).Scan(&fromAttempt, &attemptVersion); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT attempt_id,state,state_version FROM gw_async_executions WHERE id=? FOR UPDATE`, asyncID).Scan(&attemptID, &fromAsync, &asyncVersion); err == sql.ErrNoRows {
		return repository.ErrNotFound
	} else if err != nil {
		return err
	}
	if record != nil {
		if err := record(ctx, tx); err != nil {
			return err
		}
	}
	fromAsyncState := execution.AsyncState(fromAsync)
	// A terminal submit result proves that the provider accepted the work even
	// when it completed before the first poll. Preserve that fact in the state
	// history before recording success or cancellation.
	if immediateTerminalProvesAcceptance(fromAsyncState, target) {
		if _, err := s.Store.TransitionAsync(ctx, tx, asyncID, fromAsyncState, execution.AsyncAccepted, asyncVersion, "provider_accepted", ""); err != nil {
			return err
		}
		fromAsyncState, asyncVersion = execution.AsyncAccepted, asyncVersion+1
	}
	if fromAsyncState != target {
		if _, err := s.Store.TransitionAsync(ctx, tx, asyncID, fromAsyncState, target, asyncVersion, reason, ""); err != nil {
			return err
		}
	}
	if err := s.updateAsyncResourceProjection(ctx, tx, asyncID, target); err != nil {
		return err
	}
	if execution.AttemptState(fromAttempt) != attemptState {
		if err := s.Store.TransitionAttempt(ctx, tx, attemptID, execution.AttemptState(fromAttempt), attemptState, attemptVersion, reason); err != nil {
			return err
		}
	}
	if execution.CallState(fromCall) != callState {
		if err := s.Store.TransitionCall(ctx, tx, callID, execution.CallState(fromCall), callState, callVersion, reason, &attemptID); err != nil {
			return err
		}
	}
	if err := s.Store.FinalizeAttemptSlot(ctx, tx, attemptID); err != nil {
		return err
	}
	if err := s.finishBilling(ctx, tx, callID, reservationID, callState, facts); err != nil {
		return err
	}
	if s.callbackKeys != nil {
		return s.enqueueTerminalCallbackTx(ctx, tx, callID, reason)
	}
	return nil
}

func immediateTerminalProvesAcceptance(from, target execution.AsyncState) bool {
	return from == execution.AsyncSubmitting && (target == execution.AsyncSucceeded || target == execution.AsyncCancelled)
}

func (s *Service) finishBilling(ctx context.Context, tx *sql.Tx, callID, reservationID uint64, state execution.CallState, facts billing.Facts) error {
	if reservationID == 0 {
		return repository.ErrConflict
	}
	if state == execution.CallRetryPending {
		return nil
	}
	if state == execution.CallIndeterminate {
		return s.Store.ResolveReservation(ctx, tx, reservationID, "unknown_hold", "")
	}
	if state != execution.CallCompleted && state != execution.CallFailed && state != execution.CallCancelled {
		return repository.ErrInvalidInput
	}
	events := make(map[billing.ChargeEvent]bool, len(facts.Events)+3)
	for key, value := range facts.Events {
		events[key] = value
	}
	events[billing.ChargeSucceeded] = state == execution.CallCompleted
	events[billing.ChargeFailed] = state == execution.CallFailed
	events[billing.ChargeCancelled] = state == execution.CallCancelled
	facts.Events = events
	err := s.Store.SettleCallRates(ctx, tx, callID, reservationID, facts)
	if errors.Is(err, billing.ErrMissingFact) {
		return s.Store.ResolveReservation(ctx, tx, reservationID, "unknown_hold", "")
	}
	return err
}
