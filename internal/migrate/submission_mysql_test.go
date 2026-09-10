//go:build integration

package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
)

func verifyAtomicGatewaySubmission(t *testing.T, db *sql.DB, store *repository.Store, call repository.CreateCallInput, attempt repository.BeginAttemptInput) {
	t.Helper()
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32)))
	ctx := context.Background()
	service, _ := gatewayruntime.New(store)
	var accountID, windowID, keyringID uint64
	if err := store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		accountID, err = store.OpenBillingAccount(ctx, tx, call.UserID)
		if err != nil {
			return err
		}
		if err := store.CreditBillingAccount(ctx, tx, accountID, "100", "qa-submission-funding"); err != nil {
			return err
		}
		policyID, err := store.CreateBudgetPolicy(ctx, tx, repository.BudgetPolicyInput{TokenID: call.TokenID, PolicyCode: "qa-submission", WindowKind: "unlimited", AlgorithmVersion: 1})
		if err != nil {
			return err
		}
		start := time.Now().UTC().Add(-time.Hour)
		activationID, err := store.ActivateBudgetPolicy(ctx, tx, call.TokenID, policyID, start)
		if err != nil {
			return err
		}
		windowID, err = store.CreateBudgetWindow(ctx, tx, repository.BudgetWindowInput{TokenID: call.TokenID, PolicyID: policyID, ActivationID: activationID, StartAt: start})
		if err != nil {
			return err
		}
		return tx.QueryRow(`SELECT id FROM crypto_keyring_state WHERE purpose='gateway-payload'`).Scan(&keyringID)
	}); err != nil {
		t.Fatal(err)
	}
	retentionUntil := time.Now().UTC().Add(time.Hour)
	call.PublicID, call.QuotedAmount = "qa-atomic-submit", "1"
	in := gatewayruntime.SubmitInput{Call: call, Attempt: attempt, Asynchronous: true, AsyncScopeKind: "credential", AsyncScopeKey: fmt.Sprintf("credential:%d", attempt.CredentialID),
		Reservation:    repository.ReservationInput{TokenID: call.TokenID, BillingAccountID: accountID, BudgetWindowID: windowID, Amount: "1", Currency: call.Currency, CurrencyVersion: call.CurrencyVersion},
		RequestPayload: repository.BlobInput{KeyringID: keyringID, KEKVersion: 1, Plaintext: []byte(`{"model":"qa","prompt":"immutable test prompt"}`), KEK: bytes.Repeat([]byte{41}, 32), HMACKey: bytes.Repeat([]byte{42}, 32), RetentionUntil: &retentionUntil},
		Idempotency:    &repository.IdempotencyInput{TokenID: call.TokenID, OperationContractID: call.OperationContractID, KeyHMAC: strings.Repeat("f", 64), HMACKeyVersion: 1},
	}
	t.Run("submission_payload_and_idempotency", func(t *testing.T) {
		// A local background job using a request-scoped transport must not consume
		// an upstream task slot, even while the pool retains an unknown task.
		created, err := service.Submit(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if created.CallID == 0 || created.AttemptID == 0 || created.AsyncExecutionID == 0 || created.ReservationID == 0 || created.OutboxID == 0 || created.Reused {
			t.Fatalf("incomplete submission: %+v", created)
		}
		reused, err := service.Submit(ctx, in)
		if err != nil || !reused.Reused || reused.CallID != created.CallID || reused.AttemptID != created.AttemptID || reused.AsyncExecutionID != created.AsyncExecutionID || reused.ReservationID != created.ReservationID || reused.OutboxID != created.OutboxID {
			t.Fatalf("replay=%+v err=%v", reused, err)
		}
		var blobID uint64
		if err := db.QueryRow(`SELECT p.encrypted_blob_id FROM gw_api_calls c JOIN gw_api_call_payloads p ON p.id=c.request_payload_id WHERE c.id=?`, created.CallID).Scan(&blobID); err != nil {
			t.Fatal(err)
		}
		envelope, err := store.ReadEncryptedBlob(ctx, db, blobID)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := repository.OpenBlob(envelope, blobID, []byte(fmt.Sprintf("call:%d:request", created.CallID)), in.RequestPayload.KEK, in.RequestPayload.HMACKey)
		if err != nil || !bytes.Equal(plain, in.RequestPayload.Plaintext) || bytes.Contains(envelope.Ciphertext, in.RequestPayload.Plaintext) {
			t.Fatalf("payload encryption round trip: %v", err)
		}
		clear(plain)
		changed := in
		changed.RequestPayload.Plaintext = []byte(`{"model":"qa","prompt":"changed"}`)
		if _, err := service.Submit(ctx, changed); !errors.Is(err, repository.ErrIdempotencyConflict) {
			t.Fatalf("changed replay accepted: %v", err)
		}
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM gw_async_outbox WHERE async_execution_id=?`, created.AsyncExecutionID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("duplicate submit actions: count=%d err=%v", count, err)
		}

		t.Run("outbox_acceptance_and_stale_worker_fence", func(t *testing.T) {
			var item repository.OutboxItem
			if err := store.WithTx(ctx, func(tx *sql.Tx) error {
				var err error
				item, err = store.ClaimAsyncOutbox(ctx, tx, "qa-outbox-worker", time.Minute)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			requestID, err := service.BeginAsyncRequest(ctx, item, gatewayruntime.AsyncRequestEvidence{MappingHMAC: strings.Repeat("a", 64), RequestHMAC: strings.Repeat("b", 64)})
			if err != nil || requestID == 0 {
				t.Fatalf("begin async request: id=%d err=%v", requestID, err)
			}
			status := uint16(200)
			identity, err := service.AcceptAsyncSubmission(ctx, gatewayruntime.AcceptAsyncInput{
				Item: item, RequestID: requestID,
				TaskIdentity:      repository.BlobInput{KeyringID: keyringID, KEKVersion: 1, Plaintext: []byte("provider-task-1"), KEK: bytes.Repeat([]byte{41}, 32), HMACKey: bytes.Repeat([]byte{42}, 32)},
				IdentityExpiresAt: time.Now().UTC().Add(time.Hour), QueryAt: time.Now().UTC().Add(time.Minute),
				Response: repository.RequestLogResult{ResponseBytesHMAC: strings.Repeat("c", 64), HTTPStatus: &status, ResponseComplete: true},
			})
			if err != nil || identity == 0 {
				t.Fatalf("accept async submission: identity=%d err=%v", identity, err)
			}
			var state string
			if err := db.QueryRow(`SELECT state FROM gw_async_executions WHERE id=?`, created.AsyncExecutionID).Scan(&state); err != nil || state != "accepted" {
				t.Fatalf("async state=%s err=%v", state, err)
			}
			if _, err := service.BeginAsyncRequest(ctx, item, gatewayruntime.AsyncRequestEvidence{MappingHMAC: strings.Repeat("d", 64), RequestHMAC: strings.Repeat("e", 64)}); !errors.Is(err, repository.ErrConflict) {
				t.Fatalf("stale submit worker was accepted: %v", err)
			}
			var queryCount int
			if err := db.QueryRow(`SELECT COUNT(*) FROM gw_async_outbox WHERE async_execution_id=? AND action='query' AND status='pending'`, created.AsyncExecutionID).Scan(&queryCount); err != nil || queryCount != 1 {
				t.Fatalf("query outbox count=%d err=%v", queryCount, err)
			}
		})
	})
	t.Run("submission_capacity_failure_rolls_back_every_fact", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE gw_product_transports SET task_scope='task' WHERE id=?`, attempt.ProductTransportID); err != nil {
			t.Fatal(err)
		}
		var beforeBlob, beforeCall, beforeAttempt, beforeOutbox, beforeReservation int
		counts := func(blobs, calls, attempts, outbox, reservations *int) {
			t.Helper()
			if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM encrypted_blobs),(SELECT COUNT(*) FROM gw_api_calls),(SELECT COUNT(*) FROM gw_api_call_attempts),(SELECT COUNT(*) FROM gw_async_outbox),(SELECT COUNT(*) FROM billing_reservations)`).Scan(blobs, calls, attempts, outbox, reservations); err != nil {
				t.Fatal(err)
			}
		}
		counts(&beforeBlob, &beforeCall, &beforeAttempt, &beforeOutbox, &beforeReservation)
		failed := in
		failed.Call.PublicID, failed.Idempotency = "qa-atomic-denied", nil
		synchronous := failed
		synchronous.Asynchronous = false
		if _, err := service.Submit(ctx, synchronous); !errors.Is(err, repository.ErrInvalidInput) {
			t.Fatalf("task transport bypassed async capacity: %v", err)
		}
		if _, err := service.Submit(ctx, failed); !errors.Is(err, repository.ErrConcurrencyLimit) {
			t.Fatalf("task capacity failure: %v", err)
		}
		var afterBlob, afterCall, afterAttempt, afterOutbox, afterReservation int
		counts(&afterBlob, &afterCall, &afterAttempt, &afterOutbox, &afterReservation)
		if beforeBlob != afterBlob || beforeCall != afterCall || beforeAttempt != afterAttempt || beforeOutbox != afterOutbox || beforeReservation != afterReservation {
			t.Fatal("failed submission committed partial state")
		}
		var held string
		if err := db.QueryRow(`SELECT held_amount FROM billing_accounts WHERE id=?`, accountID).Scan(&held); err != nil || held != "1.000000000000000000" {
			t.Fatalf("failed submission changed balance: held=%s err=%v", held, err)
		}
	})
	t.Run("async_terminal_and_billing_are_atomic", func(t *testing.T) {
		for _, query := range []string{
			`UPDATE gw_credential_pools SET task_limit=2 WHERE id=?`,
			`UPDATE gw_credentials SET task_limit=2 WHERE credential_pool_id=?`,
		} {
			if _, err := db.Exec(query, attempt.CredentialPoolID); err != nil {
				t.Fatal(err)
			}
		}
		asyncInput := in
		asyncInput.Call.PublicID, asyncInput.Idempotency = "qa-async-terminal", nil
		created, err := service.Submit(ctx, asyncInput)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.FinishAttempt(ctx, created.AttemptID, execution.AttemptFailed, execution.CallFailed, "qa_wrong_entry", billing.Facts{}); !errors.Is(err, repository.ErrConflict) {
			t.Fatalf("async parent finalized without child: %v", err)
		}
		// The imported catalog is intentionally unpriced. A settlement error must
		// roll back the terminal state and its slot release, not just the money.
		if err := service.FinishAsync(ctx, created.AsyncExecutionID, execution.AsyncNotCreated, "qa_provider_rejected", billing.Facts{}); err == nil {
			t.Fatal("unpriced settlement accepted")
		}
		assertStates := func(want string) {
			t.Helper()
			var states string
			if err := db.QueryRow(`SELECT CONCAT(c.status,':',a.state,':',x.state,':',s.state,':',r.state)
FROM gw_api_calls c JOIN gw_api_call_attempts a ON a.call_id=c.id JOIN gw_async_executions x ON x.attempt_id=a.id
JOIN gw_credential_slots s ON s.attempt_id=a.id JOIN billing_reservations r ON r.call_id=c.id WHERE c.id=?`, created.CallID).Scan(&states); err != nil || states != want {
				t.Fatalf("states=%s want=%s error=%v", states, want, err)
			}
		}
		assertStates("in_progress:started:submitting:active:active")
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			_, err := store.TransitionAsync(ctx, tx, created.AsyncExecutionID, execution.AsyncSubmitting, execution.AsyncSubmissionUnknown, 2, "qa_connection_lost", "")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := service.FinishAsync(ctx, created.AsyncExecutionID, execution.AsyncTerminatedUnknown, "qa_unknown_outcome", billing.Facts{}); err != nil {
				t.Fatal(err)
			}
		}
		assertStates("indeterminate:terminated_unknown:terminated_unknown:recovery_required:unknown_hold")
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM billing_events WHERE reservation_id=? AND event_type='reservation_held_unknown'`, created.ReservationID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("unknown hold events=%d err=%v", count, err)
		}
	})
	t.Run("retry_releases_attempt_ownership_not_reservation", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE gw_product_transports SET task_scope='none' WHERE id=?`, attempt.ProductTransportID); err != nil {
			t.Fatal(err)
		}
		retryInput := in
		retryInput.Call.PublicID, retryInput.Idempotency, retryInput.Asynchronous = "qa-retry-boundary", nil, false
		created, err := service.Submit(ctx, retryInput)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.FinishAttempt(ctx, created.AttemptID, execution.AttemptNotCreated, execution.CallRetryPending, "qa_not_created", billing.Facts{}); err != nil {
			t.Fatal(err)
		}
		var current, final sql.NullInt64
		var reservationState string
		if err := db.QueryRow(`SELECT c.current_attempt_id,c.final_attempt_id,r.state FROM gw_api_calls c JOIN billing_reservations r ON r.call_id=c.id WHERE c.id=?`, created.CallID).Scan(&current, &final, &reservationState); err != nil || current.Valid || final.Valid || reservationState != "active" {
			t.Fatalf("retry current=%v final=%v reservation=%s error=%v", current, final, reservationState, err)
		}
		retry := attempt
		retry.CallID = created.CallID
		var nextID uint64
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			nextID, err = store.BeginAttempt(ctx, tx, retry)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if nextID == created.AttemptID {
			t.Fatal("retry reused immutable attempt")
		}
		if err := service.FinishAttempt(ctx, nextID, execution.AttemptTerminatedUnknown, execution.CallIndeterminate, "qa_uncertain_retry", billing.Facts{}); err != nil {
			t.Fatal(err)
		}
		err = store.WithTx(ctx, func(tx *sql.Tx) error { _, err := store.BeginAttempt(ctx, tx, retry); return err })
		if !errors.Is(err, repository.ErrConflict) {
			t.Fatalf("unknown outcome accepted another attempt: %v", err)
		}
	})
	verifyAsyncHTTPDispatch(t, db, store, in)
}
