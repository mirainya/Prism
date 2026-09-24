package runtime

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/payloadview"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

// ErrCallbackNeedsQuery indicates that the callback was valid but cannot
// safely materialize a result locally (for example managed-copy delivery).
// The caller should schedule the normal authoritative query path.
var ErrCallbackNeedsQuery = errors.New("gateway callback requires authoritative query")

// CallbackObservation contains the provider-independent facts decoded from a
// callback body. It is committed with the Receipt so callback processing has
// the same state, delivery and billing guarantees as polling.
type CallbackObservation struct {
	AsyncExecutionID     uint64
	ReceiptID            uint64
	State                execution.AsyncState
	TaskID               string
	Result               []byte
	Facts                billing.Facts
	Sources              []delivery.RemoteResult
	SourceURLPolicy      string
	PayloadHMAC          string
	ProviderErrorCode    string
	ProviderErrorMessage string
	ProviderHTTPStatus   *uint16
}

// CallbackReceiptInput is the trusted envelope produced by the HTTP callback
// ingress. The ingress must authenticate the binding token before constructing
// this value; the payload is represented only by its HMAC.
type CallbackReceiptInput struct {
	AsyncExecutionID           uint64
	CredentialVersionID        uint64
	EventScope                 string
	EventID                    string
	EventHMAC                  string
	EventAliases               []repository.CallbackReceiptAlias
	PayloadHMAC                string
	PayloadHMACs               map[uint32]string
	BindingTokenHMAC           string
	BindingTokenHMACKeyVer     uint32
	Payload                    []byte
	PayloadKeyringID           uint64
	PayloadKEKVersion          uint32
	PayloadKEK, PayloadHMACKey []byte
	ExpiresAt                  time.Time
}

// callbackProcessingRows is the lock-ordered view used by every callback
// consumer. The callback receipt is the durable entry point, but its async
// parent is locked first so a terminal poll/callback transaction cannot form
// an AsyncExecution -> Outbox -> Receipt / Outbox -> AsyncExecution cycle.
type callbackProcessingRows struct {
	AsyncID       uint64
	State         execution.AsyncState
	ReceiptStatus string
}

// lockCallbackProcessing acquires callback rows in the single order shared by
// terminal state application and callback-query scheduling:
// AsyncExecution -> callback Outbox -> Receipt. The initial lookup is a
// non-locking read because async_execution_id is immutable after receipt
// insertion; all mutable values are re-read under the ordered locks.
func (s *Service) lockCallbackProcessing(ctx context.Context, tx *sql.Tx, item repository.OutboxItem) (callbackProcessingRows, error) {
	if s == nil || s.Store == nil || tx == nil || item.ID == 0 || item.CallbackReceiptID == 0 || item.Action != "callback" {
		return callbackProcessingRows{}, repository.ErrInvalidInput
	}
	var asyncID uint64
	if err := tx.QueryRowContext(ctx, `SELECT async_execution_id FROM gw_upstream_callback_receipts WHERE id=?`, item.CallbackReceiptID).Scan(&asyncID); err == sql.ErrNoRows {
		return callbackProcessingRows{}, repository.ErrNotFound
	} else if err != nil {
		return callbackProcessingRows{}, err
	}
	if asyncID == 0 {
		return callbackProcessingRows{}, repository.ErrConflict
	}
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM gw_async_executions WHERE id=? FOR UPDATE`, asyncID).Scan(&state); err == sql.ErrNoRows {
		return callbackProcessingRows{}, repository.ErrNotFound
	} else if err != nil {
		return callbackProcessingRows{}, err
	}
	if err := s.Store.AssertCallbackOutboxLease(ctx, tx, item); err != nil {
		return callbackProcessingRows{}, err
	}
	var receiptAsyncID, receiptVersion uint64
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT async_execution_id,status,state_version FROM gw_upstream_callback_receipts WHERE id=? FOR UPDATE`, item.CallbackReceiptID).Scan(&receiptAsyncID, &status, &receiptVersion); err == sql.ErrNoRows {
		return callbackProcessingRows{}, repository.ErrNotFound
	} else if err != nil {
		return callbackProcessingRows{}, err
	}
	if receiptAsyncID != asyncID || status == "received" && receiptVersion != item.StateVersion {
		return callbackProcessingRows{}, repository.ErrConflict
	}
	return callbackProcessingRows{AsyncID: asyncID, State: execution.AsyncState(state), ReceiptStatus: status}, nil
}

// IngestCallback records an authenticated callback as an idempotent Receipt
// and Alias. It intentionally does not lock or mutate AsyncExecution. A
// separate consumer can safely apply the receipt after submit/identity races
// have settled.
func (s *Service) IngestCallback(ctx context.Context, in CallbackReceiptInput) (receiptID uint64, replay bool, err error) {
	if s == nil || s.Store == nil || in.AsyncExecutionID == 0 || in.CredentialVersionID == 0 || in.EventScope == "" || len(in.EventScope) > 255 || in.EventID == "" || len(in.EventID) > 255 || !validHex(in.EventHMAC) || !validHex(in.PayloadHMAC) || !validHex(in.BindingTokenHMAC) || in.BindingTokenHMACKeyVer == 0 || in.ExpiresAt.IsZero() || !in.ExpiresAt.After(time.Now().UTC()) {
		return 0, false, repository.ErrInvalidInput
	}
	aliases := append([]repository.CallbackReceiptAlias(nil), in.EventAliases...)
	if len(aliases) == 0 {
		aliases = []repository.CallbackReceiptAlias{{HMACKeyVersion: in.BindingTokenHMACKeyVer, EventHMAC: in.EventHMAC}}
	}
	// The authenticated version is always the primary alias. This keeps the
	// legacy columns useful while the alias table carries all rotated versions.
	primaryFound := false
	for _, alias := range aliases {
		if alias.HMACKeyVersion == in.BindingTokenHMACKeyVer && alias.EventHMAC == in.EventHMAC {
			primaryFound = true
			break
		}
	}
	if !primaryFound {
		aliases = append(aliases, repository.CallbackReceiptAlias{HMACKeyVersion: in.BindingTokenHMACKeyVer, EventHMAC: in.EventHMAC})
	}
	seenVersions := make(map[uint32]struct{}, len(aliases))
	for _, alias := range aliases {
		if alias.HMACKeyVersion == 0 || !validHex(alias.EventHMAC) {
			return 0, false, repository.ErrInvalidInput
		}
		if _, exists := seenVersions[alias.HMACKeyVersion]; exists {
			return 0, false, repository.ErrInvalidInput
		}
		seenVersions[alias.HMACKeyVersion] = struct{}{}
		if digest := in.PayloadHMACs[alias.HMACKeyVersion]; digest != "" && !validHex(digest) {
			return 0, false, repository.ErrInvalidInput
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		receiptID, replay = 0, false
		err = s.ingestCallbackOnce(ctx, in, aliases, &receiptID, &replay)
		if !errors.Is(err, errCallbackReceiptReplayRace) {
			return receiptID, replay, err
		}
	}
	return 0, false, repository.ErrConflict
}

var errCallbackReceiptReplayRace = errors.New("callback receipt replay won by another transaction")

func (s *Service) ingestCallbackOnce(ctx context.Context, in CallbackReceiptInput, aliases []repository.CallbackReceiptAlias, receiptID *uint64, replay *bool) error {
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var attemptCredential uint64
		if err := tx.QueryRowContext(ctx, `SELECT a.credential_version_id FROM gw_async_executions x JOIN gw_api_call_attempts a ON a.id=x.attempt_id WHERE x.id=? FOR SHARE`, in.AsyncExecutionID).Scan(&attemptCredential); err == sql.ErrNoRows {
			return repository.ErrNotFound
		} else if err != nil {
			return err
		} else if attemptCredential != in.CredentialVersionID {
			return repository.ErrConflict
		}
		// Resolve an existing alias before creating a new encrypted payload. A
		// replay across HMAC rotation must point to the same execution and may
		// only add the missing alias rows.
		for _, alias := range aliases {
			existingID, lookupErr := s.Store.FindCallbackReceiptAlias(ctx, tx, alias.HMACKeyVersion, alias.EventHMAC)
			if lookupErr == repository.ErrNotFound {
				continue
			}
			if lookupErr != nil {
				return lookupErr
			}
			if *receiptID == 0 {
				*receiptID = existingID
			} else if *receiptID != existingID {
				return repository.ErrConflict
			}
		}
		if *receiptID != 0 {
			var existingAsync, existingCredential uint64
			var storedVersion uint32
			var existingPayload string
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(async_execution_id,0),verified_credential_version_id,hmac_key_version,payload_hmac FROM gw_upstream_callback_receipts WHERE id=? FOR UPDATE`, *receiptID).Scan(&existingAsync, &existingCredential, &storedVersion, &existingPayload); err != nil {
				return err
			}
			expectedPayload := in.PayloadHMACs[storedVersion]
			if expectedPayload == "" && storedVersion == in.BindingTokenHMACKeyVer {
				expectedPayload = in.PayloadHMAC
			}
			if existingAsync != in.AsyncExecutionID || existingCredential != in.CredentialVersionID || expectedPayload == "" || existingPayload != expectedPayload {
				return repository.ErrConflict
			}
			*replay = true
			return s.Store.AddCallbackReceiptAliases(ctx, tx, *receiptID, aliases)
		}
		var payloadBlobID uint64
		if len(in.Payload) > 0 {
			if len(in.Payload) > 1<<20 || len(in.PayloadKEK) != security.KeySize || len(in.PayloadHMACKey) != security.KeySize || in.PayloadKeyringID == 0 || in.PayloadKEKVersion == 0 {
				return repository.ErrInvalidInput
			}
			createdBlobID, err := s.Store.PutEncryptedBlob(ctx, tx, repository.BlobInput{
				KeyringID: in.PayloadKeyringID, KEKVersion: in.PayloadKEKVersion,
				Purpose: "gateway-callback-payload", SchemaVersion: 1,
				Owner: []byte(fmt.Sprintf("async:%d:callback:%s", in.AsyncExecutionID, in.EventHMAC)), Plaintext: in.Payload,
				KEK: in.PayloadKEK, HMACKey: in.PayloadHMACKey,
			})
			if err != nil {
				return err
			}
			payloadBlobID = createdBlobID
		}
		createdID, wasReplay, err := s.Store.CreateCallbackReceipt(ctx, tx, repository.CallbackReceiptInput{
			AsyncExecutionID: &in.AsyncExecutionID, EventScope: in.EventScope, HMACKeyVersion: in.BindingTokenHMACKeyVer,
			EventHMAC: in.EventHMAC, PayloadHMAC: in.PayloadHMAC,
			EncryptedPayloadBlobID:      payloadBlobID,
			VerifiedCredentialVersionID: in.CredentialVersionID, ExpiresAt: in.ExpiresAt,
		})
		if err != nil {
			return err
		}
		if wasReplay && payloadBlobID != 0 {
			// Roll back the just-created Blob and retry against the Receipt that
			// won the concurrent unique-key race.
			return errCallbackReceiptReplayRace
		}
		*receiptID, *replay = createdID, wasReplay
		if err = s.Store.AddCallbackReceiptAliases(ctx, tx, createdID, aliases); err != nil {
			return err
		}
		if wasReplay {
			return nil
		}
		return s.Store.CreateCallbackProcessingOutbox(ctx, tx, createdID)
	})
}

func validHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ApplyCallbackObservation commits a decoded terminal callback. Progress
// callbacks are converted to a fenced query; terminal reference results are
// materialized immediately. Managed-copy results deliberately fall back to a
// query because downloading provider media cannot occur inside this SQL tx.
func (s *Service) ApplyCallbackObservation(ctx context.Context, item repository.OutboxItem, in CallbackObservation, dispatch repository.AsyncDispatch, payload repository.BlobInput) error {
	if s == nil || s.Store == nil || item.ID == 0 || item.CallbackReceiptID != in.ReceiptID || item.Action != "callback" || in.ReceiptID == 0 || in.AsyncExecutionID == 0 || in.State == "" || in.PayloadHMAC == "" {
		return repository.ErrInvalidInput
	}
	if in.State == execution.AsyncSucceeded && (len(in.Result) == 0 || len(in.Sources) == 0) {
		return repository.ErrInvalidInput
	}
	if in.State == execution.AsyncSucceeded && dispatch.DeliveryMode == "managed_copy" {
		return ErrCallbackNeedsQuery
	}
	var current execution.AsyncState
	if err := s.Store.DB().QueryRowContext(ctx, `SELECT x.state FROM gw_upstream_callback_receipts r JOIN gw_async_executions x ON x.id=r.async_execution_id WHERE r.id=? AND r.async_execution_id=?`, in.ReceiptID, in.AsyncExecutionID).Scan(&current); err == sql.ErrNoRows {
		return repository.ErrNotFound
	} else if err != nil {
		return err
	}
	if callbackTerminalState(current) {
		return s.completeLateCallback(ctx, item, in, current)
	}
	if current == execution.AsyncNotCreated {
		return &PermanentDispatchError{Code: "callback_conflicts_with_terminal_execution"}
	}
	if !callbackObservationApplicableState(current) {
		return repository.ErrConflict
	}
	return s.finishAsync(ctx, in.AsyncExecutionID, in.State, "provider_callback", in.Facts, func(ctx context.Context, tx *sql.Tx) error {
		locked, err := s.lockCallbackProcessing(ctx, tx, item)
		if err != nil {
			return err
		}
		if locked.AsyncID != in.AsyncExecutionID || locked.ReceiptStatus != "received" {
			return repository.ErrConflict
		}
		if !callbackObservationApplicableState(locked.State) {
			return repository.ErrConflict
		}
		attemptID := dispatch.AttemptID
		if attemptID == 0 {
			return repository.ErrConflict
		}
		var requestSeq uint64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(request_seq),0)+1 FROM gw_channel_request_logs WHERE attempt_id=? FOR UPDATE`, attemptID).Scan(&requestSeq); err != nil {
			return err
		}
		requestID, err := s.beginRequest(ctx, tx, repository.RequestLogInput{AttemptID: &attemptID, RequestSeq: requestSeq, Action: "query", MappingHMAC: in.PayloadHMAC, RequestBytesHMAC: in.PayloadHMAC})
		if err != nil {
			return err
		}
		requestResult, err := callbackRequestLogResult(in, payload)
		if err != nil {
			return err
		}
		if err := s.finishRequest(ctx, tx, requestID, "response_recorded", requestResult); err != nil {
			return err
		}
		var result *repository.BlobInput
		if in.State == execution.AsyncSucceeded {
			payload.Plaintext = in.Result
			result = &payload
		}
		if result != nil {
			if err := s.persistAsyncResult(ctx, tx, attemptID, requestID, *result, in.Sources, in.SourceURLPolicy, nil); err != nil {
				return err
			}
		}
		if err := s.Store.TransitionCallbackReceipt(ctx, tx, in.ReceiptID, "received", "processed", "provider_callback"); err != nil {
			return err
		}
		return s.Store.CompleteCallbackOutbox(ctx, tx, item, true, "")
	})
}

func callbackObservationApplicableState(state execution.AsyncState) bool {
	switch state {
	case execution.AsyncAccepted, execution.AsyncRunning, execution.AsyncManualReview, execution.AsyncTerminatedUnknown:
		return true
	default:
		return false
	}
}

func callbackRequestLogResult(in CallbackObservation, payload repository.BlobInput) (repository.RequestLogResult, error) {
	statusCode := uint16(200)
	result := repository.RequestLogResult{
		ResponseBytesHMAC: in.PayloadHMAC,
		HTTPStatus:        &statusCode,
		RequestComplete:   true,
		ResponseComplete:  true,
	}
	if in.State != execution.AsyncFailed {
		return result, nil
	}
	result.ErrorCode = asyncProviderFailureCode(in.ProviderErrorCode, in.ProviderHTTPStatus)
	diagnostic, err := payloadview.EncodeFailureDiagnostic(in.ProviderErrorCode, in.ProviderErrorMessage, in.ProviderHTTPStatus)
	if err != nil {
		return repository.RequestLogResult{}, err
	}
	if len(diagnostic) != 0 {
		payload.Plaintext = diagnostic
		result.Diagnostic = &payload
	}
	return result, nil
}

func (s *Service) completeLateCallback(ctx context.Context, item repository.OutboxItem, in CallbackObservation, state execution.AsyncState) error {
	return s.Store.WithTx(ctx, func(tx *sql.Tx) error {
		locked, err := s.lockCallbackProcessing(ctx, tx, item)
		if err != nil {
			return err
		}
		if locked.AsyncID != in.AsyncExecutionID {
			return repository.ErrConflict
		}
		if locked.ReceiptStatus == "processed" {
			return s.Store.CompleteCallbackOutbox(ctx, tx, item, true, "")
		}
		if locked.ReceiptStatus != "received" {
			return repository.ErrConflict
		}
		if locked.State != state || !callbackTerminalState(locked.State) {
			return repository.ErrConflict
		}
		if err := s.Store.TransitionCallbackReceipt(ctx, tx, in.ReceiptID, "received", "processed", "late_callback"); err != nil {
			return err
		}
		return s.Store.CompleteCallbackOutbox(ctx, tx, item, true, "")
	})
}

func callbackTerminalState(state execution.AsyncState) bool {
	switch state {
	case execution.AsyncSucceeded, execution.AsyncFailed, execution.AsyncCancelled:
		return true
	default:
		return false
	}
}

var ErrCallbackAuthentication = errors.New("gateway callback authentication failed")
