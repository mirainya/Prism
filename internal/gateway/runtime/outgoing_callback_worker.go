package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/pkg/safeurl"
)

const (
	callbackHTTPTimeout      = 10 * time.Second
	callbackLeaseDuration    = time.Minute
	callbackResponseMaxBytes = int64(64 << 10)
	callbackPayloadMaxBytes  = 1 << 20
)

type callbackHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type CallbackDeliveryWorker struct {
	service  *Service
	client   callbackHTTPClient
	validate func(context.Context, string) error
	now      func() time.Time
}

func NewCallbackDeliveryWorker(service *Service) (*CallbackDeliveryWorker, error) {
	if service == nil || service.Store == nil || service.callbackKeys == nil || !service.callbackKeys.valid() {
		return nil, repository.ErrInvalidInput
	}
	client := safeurl.NewClient(callbackHTTPTimeout)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &CallbackDeliveryWorker{
		service: service, client: client,
		validate: safeurl.Validate, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

type callbackDispatchResult struct {
	repository.CallbackDeliveryCompletion
}

// ProcessOne performs at most one network delivery. Both the attempt and its
// request HMAC exist before Do is called; completion uses a lease fence.
func (w *CallbackDeliveryWorker) ProcessOne(ctx context.Context, owner string) (bool, error) {
	if w == nil || w.service == nil || w.service.Store == nil || w.client == nil || w.validate == nil || w.now == nil || owner == "" {
		return false, repository.ErrInvalidInput
	}
	if err := w.service.requireReadiness(ctx); err != nil {
		return false, err
	}
	var claim repository.ClaimedCallbackDelivery
	err := w.service.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		claim, err = w.service.Store.ClaimCallbackDelivery(ctx, tx, owner, callbackLeaseDuration)
		return err
	})
	if errors.Is(err, repository.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	result := w.dispatch(ctx, claim)
	markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := w.service.Store.WithTx(markCtx, func(tx *sql.Tx) error {
		return w.service.Store.CompleteCallbackDelivery(markCtx, tx, claim, result.CallbackDeliveryCompletion)
	}); err != nil {
		return true, err
	}
	return true, nil
}

func (w *CallbackDeliveryWorker) dispatch(ctx context.Context, claim repository.ClaimedCallbackDelivery) callbackDispatchResult {
	dead := func(attemptState, code string) callbackDispatchResult {
		return callbackDispatchResult{repository.CallbackDeliveryCompletion{
			AttemptState: attemptState, ErrorCode: code, Outcome: "dead_letter",
		}}
	}
	retry := func(attemptState, code string) callbackDispatchResult {
		outcome := "retry"
		var retryAt time.Time
		if claim.AttemptNo >= claim.MaxAttempts || !claim.ReplayExpiresAt.After(w.now()) {
			outcome = "dead_letter"
		} else {
			retryAt = w.now().Add(outboxRetryDelay(5*time.Second, claim.AttemptNo))
			if !retryAt.Before(claim.ReplayExpiresAt) {
				outcome = "dead_letter"
				retryAt = time.Time{}
			}
		}
		return callbackDispatchResult{repository.CallbackDeliveryCompletion{
			AttemptState: attemptState, ErrorCode: code, Outcome: outcome, RetryAt: retryAt,
		}}
	}
	blobFailure := func(err error, integrityCode, readCode string) callbackDispatchResult {
		if isCallbackBlobIntegrityError(err) {
			return dead("failed", integrityCode)
		}
		return retry("failed", readCode)
	}
	if claim.Algorithm != callbackAlgorithmHTTPJSONV1 || claim.PolicyVersion != callbackPolicyVersion || claim.MaxAttempts == 0 {
		return dead("failed", "unsupported_callback_policy")
	}
	if !claim.ReplayExpiresAt.After(w.now()) {
		return dead("failed", "callback_replay_window_expired")
	}
	targetPlain, err := w.service.openCallbackBlob(ctx, w.service.Store.DB(), claim.TargetBlobID, callbackTargetOwner(claim.CallID), callbackTargetBlobPurpose, callbackTargetSchemaVersion)
	if err != nil {
		return blobFailure(err, "callback_target_decryption_failed", "callback_target_read_failed")
	}
	defer clear(targetPlain)
	if !verifyCallbackTargetHMAC(w.service.callbackKeys.PayloadHMAC, targetPlain, claim.TargetHMAC) {
		return dead("failed", "callback_target_integrity_failed")
	}
	var target callbackTargetConfig
	if err := json.Unmarshal(targetPlain, &target); err != nil || target.SchemaVersion != callbackTargetSchemaVersion {
		return dead("failed", "invalid_callback_target")
	}
	target.URL, err = NormalizeCallbackURL(target.URL)
	if err != nil || target.URL == "" {
		return dead("failed", "invalid_callback_target")
	}
	if err := w.validate(ctx, target.URL); err != nil {
		return dead("failed", "unsafe_callback_target")
	}
	payload, err := w.service.openCallbackBlob(ctx, w.service.Store.DB(), claim.PayloadBlobID, callbackEventOwner(claim.CallID, claim.EventSeq), callbackEventBlobPurpose, callbackEventSchemaVersion)
	if err != nil {
		return blobFailure(err, "callback_payload_decryption_failed", "callback_payload_read_failed")
	}
	defer clear(payload)
	if len(payload) == 0 || len(payload) > callbackPayloadMaxBytes || !json.Valid(payload) {
		return dead("failed", "invalid_callback_payload")
	}
	if !verifyCallbackPayloadHMAC(w.service.callbackKeys.PayloadHMAC, payload, claim.PayloadHMAC) {
		return dead("failed", "callback_payload_integrity_failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(payload))
	if err != nil {
		return dead("failed", "invalid_callback_target")
	}
	eventID := fmt.Sprintf("%s:terminal:%d", claim.CallPublicID, claim.EventSeq)
	digest := sha256.Sum256(payload)
	timestamp := strconv.FormatInt(w.now().Unix(), 10)
	if len(target.SigningSecret) == 0 {
		return dead("failed", "invalid_callback_target")
	}
	signingBase := []byte(timestamp + "." + eventID + ".")
	signingBase = append(signingBase, payload...)
	signature := security.HMACSHA256(target.SigningSecret, signingBase)
	clear(target.SigningSecret)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Prism-Callback/1.0")
	request.Header.Set("Idempotency-Key", eventID)
	request.Header.Set("X-Prism-Event-ID", eventID)
	request.Header.Set("X-Prism-Content-SHA256", hex.EncodeToString(digest[:]))
	request.Header.Set("X-Prism-Callback-Attempt", strconv.FormatUint(claim.AttemptNo, 10))
	request.Header.Set("X-Prism-Timestamp", timestamp)
	request.Header.Set("X-Prism-Signature", "t="+timestamp+",v1="+hex.EncodeToString(signature[:]))
	response, err := w.client.Do(request)
	if err != nil {
		return retry("unknown", callbackTransportErrorCode(err))
	}
	if response == nil {
		return retry("unknown", "callback_transport_unknown")
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, callbackResponseMaxBytes+1))
	responseComplete := readErr == nil && int64(len(body)) <= callbackResponseMaxBytes
	if !responseComplete && int64(len(body)) > callbackResponseMaxBytes {
		body = body[:callbackResponseMaxBytes]
	}
	var responseHMAC string
	if responseComplete {
		digest := security.HMACSHA256(w.service.callbackKeys.PayloadHMAC, body)
		responseHMAC = hex.EncodeToString(digest[:])
	}
	base := repository.CallbackDeliveryCompletion{
		ResponseHMAC: responseHMAC, HTTPStatus: uint32(response.StatusCode),
		RequestComplete: true, ResponseComplete: responseComplete,
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		base.AttemptState, base.Outcome = "succeeded", "succeeded"
		return callbackDispatchResult{base}
	}
	code := callbackHTTPErrorCode(response.StatusCode)
	base.AttemptState, base.ErrorCode = "failed", code
	if callbackHTTPRetryable(response.StatusCode) {
		retryResult := retry("failed", code)
		retryResult.ResponseHMAC = responseHMAC
		retryResult.HTTPStatus = uint32(response.StatusCode)
		retryResult.RequestComplete = true
		retryResult.ResponseComplete = responseComplete
		return retryResult
	}
	base.Outcome = "dead_letter"
	return callbackDispatchResult{base}
}

func callbackHTTPRetryable(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500
}

func callbackHTTPErrorCode(status int) string {
	if callbackHTTPRetryable(status) {
		return "callback_http_retryable"
	}
	return "callback_http_rejected"
}

func callbackTransportErrorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "callback_timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "callback_cancelled"
	}
	return "callback_transport_error"
}

func (w *CallbackDeliveryWorker) Run(ctx context.Context, owner string, report func(error)) error {
	if w == nil || w.service == nil || owner == "" {
		return repository.ErrInvalidInput
	}
	nextMaintenance := time.Time{}
	for ctx.Err() == nil {
		if err := w.service.requireReadiness(ctx); err != nil {
			return err
		}
		if !w.now().Before(nextMaintenance) {
			if _, err := w.service.EnsureTerminalCallbacks(ctx, 100); err != nil && ctx.Err() == nil && report != nil {
				report(err)
			}
			if _, err := w.service.Store.DeadLetterUndeliverableCallbacks(ctx, 100); err != nil && ctx.Err() == nil && report != nil {
				report(err)
			}
			nextMaintenance = w.now().Add(30 * time.Second)
		}
		worked, err := w.ProcessOne(ctx, owner)
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, ErrNotReady) {
			return err
		}
		if err != nil && report != nil {
			report(err)
		}
		if worked && err == nil {
			continue
		}
		delay := 250 * time.Millisecond
		if err != nil {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil
}
