//go:build integration

package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
)

type expiringSeedanceCodec struct{ adapter.Seedance }

func (c expiringSeedanceCodec) Decode(action string, request, response []byte) (gatewayruntime.AsyncObservation, error) {
	observation, err := c.Seedance.Decode(action, request, response)
	if err != nil || observation.State != execution.AsyncSucceeded {
		return observation, err
	}
	expiresAt := time.Now().UTC().Add(time.Hour)
	for index := range observation.Sources {
		observation.Sources[index].ExpiresAt = &expiresAt
	}
	return observation, nil
}

func verifyAsyncHTTPDispatch(t *testing.T, db *sql.DB, store *repository.Store, input gatewayruntime.SubmitInput) {
	t.Helper()
	ctx := context.Background()
	// Async result-source validation is resource-specific; this fixture models
	// the video task contract used by the Seedance codec.
	input.ResourceKind = "video_task"
	input.ResourceSummary = map[string]any{"fixture": "async-http"}
	// This test isolates the HTTP dispatch protocol. Deployment admission is
	// covered by the dedicated readiness and worker-claim tests; inject a
	// successful gate here so the fixture does not need to manufacture a full
	// published catalog deployment and cryptographic proof set.
	service, err := gatewayruntime.NewWithReadiness(store, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var submits, polls atomic.Int64
	var mode atomic.Value
	mode.Store("normal")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-import-secret" {
			t.Error("request did not use the pinned credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			submits.Add(1)
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["model"] != "qa-import-model" {
				t.Error("invalid pinned provider request")
			}
			if mode.Load() == "timeout" {
				select {
				case <-r.Context().Done():
				case <-time.After(250 * time.Millisecond):
				}
				return
			}
			if mode.Load() == "malformed" {
				fmt.Fprint(w, `{not valid JSON`)
				return
			}
			if mode.Load() == "reject" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":"request rejected"}`)
				return
			}
			id := r.Header.Get("X-Request-ID")
			if mode.Load() == "duplicate_id" {
				id = "qa-http-normal"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "upstream-" + id})
			return
		}
		count := polls.Add(1)
		if mode.Load() == "query_error" {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":"temporary provider outage"}`)
			return
		}
		if mode.Load() == "wrong_id" {
			fmt.Fprint(w, `{"id":"another-task","status":"succeeded","duration":6.125,"content":{"video_url":"https://example.invalid/wrong.mp4"}}`)
			return
		}
		status := "running"
		if count == 1 {
			status = "queued"
		} else if count >= 3 {
			status = "succeeded"
		}
		videoURL := "https://example.invalid/video.mp4"
		if mode.Load() == "delivery_refresh" {
			status = "succeeded"
			videoURL = "https://example.invalid/refreshed.mp4"
		}
		fmt.Fprintf(w, `{"status":%q,"duration":6.125,"content":{"video_url":%q}}`, status, videoURL)
	}))
	defer upstream.Close()
	var transportID, adapterID uint64
	if err := db.QueryRow(`SELECT ct.id,ct.adapter_implementation_id FROM gw_product_transports pt JOIN gw_channel_transports ct ON ct.id=pt.channel_transport_id WHERE pt.id=?`, input.Attempt.ProductTransportID).Scan(&transportID, &adapterID); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(upstream.URL)
	if err := store.WithTx(ctx, func(tx *sql.Tx) error {
		statements := []struct {
			query string
			args  []any
		}{
			{`UPDATE gw_async_outbox SET available_at=DATE_ADD(UTC_TIMESTAMP(3),INTERVAL 1 DAY) WHERE status='pending'`, nil},
			{`UPDATE gw_product_transports SET task_scope='task',timeout_ms=30000 WHERE id=?`, []any{input.Attempt.ProductTransportID}},
			{`UPDATE gw_credential_pools SET request_limit=NULL,task_limit=NULL WHERE id=?`, []any{input.Attempt.CredentialPoolID}},
			{`UPDATE gw_credentials SET request_limit=NULL,task_limit=NULL WHERE id=?`, []any{input.Attempt.CredentialID}},
			{`UPDATE gw_adapter_implementations SET adapter_code='seedance',contract_version=1 WHERE id=?`, []any{adapterID}},
			{`UPDATE gw_channel_transports SET base_url=?,protocol='seedance',request_path='/tasks',timeout_ms=30000 WHERE id=?`, []any{upstream.URL, transportID}},
			{`INSERT INTO gw_transport_allowed_hosts(release_id,channel_transport_id,protocol,host_pattern,port,created_at) VALUES (?,?,'http',?,?,UTC_TIMESTAMP(3))`, []any{input.Call.CatalogReleaseID, transportID, parsed.Hostname(), parsed.Port()}},
			{`INSERT INTO gw_transport_allowed_hosts(release_id,channel_transport_id,protocol,host_pattern,port,created_at) VALUES (?,?,'https','example.invalid',443,UTC_TIMESTAMP(3)) ON DUPLICATE KEY UPDATE protocol=VALUES(protocol)`, []any{input.Call.CatalogReleaseID, transportID}},
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement.query, statement.args...); err != nil {
				return err
			}
		}
		evidence, err := tx.Exec(`INSERT INTO gw_rate_evidence(source_type,authority_level,source_reference,observed_at,fact_hmac,unit_code,unit_price,currency_code,currency_version,created_at) VALUES ('qa','manual','qa-http-rate',UTC_TIMESTAMP(3),?,'second',0.1,?,?,UTC_TIMESTAMP(3))`, strings.Repeat("a", 64), input.Call.Currency, input.Call.CurrencyVersion)
		if err != nil {
			return err
		}
		evidenceID, _ := evidence.LastInsertId()
		review, err := tx.Exec(`INSERT INTO gw_rate_evidence_review_events(rate_evidence_id,review_seq,decision,reviewer_user_id,reason_code,created_at) VALUES (?,1,'accepted',?,'qa_http_rate',UTC_TIMESTAMP(3))`, evidenceID, input.Call.UserID)
		if err != nil {
			return err
		}
		reviewID, _ := review.LastInsertId()
		if _, err := tx.Exec(`INSERT INTO gw_rate_evidence_review_state(rate_evidence_id,state,state_version,latest_review_event_id) VALUES (?,'accepted',1,?)`, evidenceID, reviewID); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO gw_sell_rates(release_id,sku_id,rate_evidence_review_event_id,unit_code,unit_price,currency_code,currency_version,created_at,component_code,quantity_source,charge_event,unit_scale,quantity_step,max_quantity) VALUES (?,?,?,'second',0.1,?,?,UTC_TIMESTAMP(3),'video_seconds','result.seconds','call.succeeded',0,0,10)`, input.Call.CatalogReleaseID, input.Call.SKUID, reviewID, input.Call.Currency, input.Call.CurrencyVersion)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := gatewayruntime.NewAsyncDispatcher(service, upstream.Client(), gatewayruntime.AsyncKeys{
		CredentialKEK: bytes.Repeat([]byte{71}, 32), CredentialHMAC: bytes.Repeat([]byte{72}, 32),
		PayloadKEK: input.RequestPayload.KEK, PayloadHMAC: input.RequestPayload.HMACKey,
	}, map[string]gatewayruntime.AsyncCodec{adapter.SeedanceAdapter: expiringSeedanceCodec{}})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) gatewayruntime.Submission {
		t.Helper()
		in := input
		in.Call.PublicID, in.Idempotency, in.Asynchronous = "qa-http-"+name, nil, true
		in.RequestPayload.Plaintext = []byte(`{"model":"public-model","prompt":"test","duration":5}`)
		created, err := service.Submit(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	process := func(created gatewayruntime.Submission) error {
		t.Helper()
		if _, err := db.Exec(`UPDATE gw_async_outbox SET available_at=UTC_TIMESTAMP(3) WHERE async_execution_id=? AND status='pending'`, created.AsyncExecutionID); err != nil {
			t.Fatal(err)
		}
		worked, err := service.ProcessOne(ctx, "qa-http-worker", time.Minute, time.Second, dispatcher)
		if !worked {
			t.Fatalf("no outbox action processed: %v", err)
		}
		return err
	}
	assertState := func(created gatewayruntime.Submission, want string) {
		t.Helper()
		var state string
		if err := db.QueryRow(`SELECT state FROM gw_async_executions WHERE id=?`, created.AsyncExecutionID).Scan(&state); err != nil || state != want {
			t.Fatalf("state=%s want=%s err=%v", state, want, err)
		}
	}
	t.Run("http_submit_poll_and_exact_settlement", func(t *testing.T) {
		created := create("normal")
		for _, state := range []string{"accepted", "accepted", "running", "succeeded"} {
			if err := process(created); err != nil {
				t.Fatal(err)
			}
			assertState(created, state)
		}
		if submits.Load() != 1 || polls.Load() != 3 {
			t.Fatalf("submit=%d poll=%d", submits.Load(), polls.Load())
		}
		var amount, reservation, callState string
		var requests, activeSlots, settlements int
		if err := db.QueryRow(`SELECT c.status,s.actual_amount,r.state FROM gw_api_calls c JOIN billing_reservations r ON r.call_id=c.id JOIN billing_settlements s ON s.reservation_id=r.id WHERE c.id=?`, created.CallID).Scan(&callState, &amount, &reservation); err != nil || callState != "completed" || amount != "0.612500000000000000" || reservation != "settled" {
			t.Fatalf("call=%s amount=%s reservation=%s err=%v", callState, amount, reservation, err)
		}
		if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM gw_channel_request_logs WHERE attempt_id=? AND status='response_recorded'),(SELECT COUNT(*) FROM gw_credential_slots WHERE (attempt_id=? OR request_log_id IN (SELECT id FROM gw_channel_request_logs WHERE attempt_id=?)) AND state<>'released'),(SELECT COUNT(*) FROM billing_events WHERE reservation_id=? AND event_type='reservation_settled')`, created.AttemptID, created.AttemptID, created.AttemptID, created.ReservationID).Scan(&requests, &activeSlots, &settlements); err != nil || requests != 4 || activeSlots != 0 || settlements != 1 {
			t.Fatalf("requests=%d slots=%d settlements=%d err=%v", requests, activeSlots, settlements, err)
		}
	})
	for _, scenario := range []string{"timeout", "malformed"} {
		t.Run("http_"+scenario+"_never_resubmits", func(t *testing.T) {
			mode.Store(scenario)
			if _, err := db.Exec(`UPDATE gw_channel_transports SET timeout_ms=40 WHERE id=?`, transportID); err != nil {
				t.Fatal(err)
			}
			before := submits.Load()
			created := create(scenario)
			if err := process(created); err != nil {
				t.Fatal(err)
			}
			assertState(created, "submission_unknown")
			if err := process(created); err != nil {
				t.Fatal(err)
			}
			assertState(created, "manual_review")
			if submits.Load() != before+1 {
				t.Fatal("uncertain submission was repeated")
			}
			var requestSlots, taskSlots int
			var reservation string
			if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM gw_credential_slots s JOIN gw_channel_request_logs l ON l.id=s.request_log_id WHERE l.attempt_id=? AND s.state='active'),(SELECT COUNT(*) FROM gw_credential_slots WHERE attempt_id=? AND state='active'),(SELECT state FROM billing_reservations WHERE id=?)`, created.AttemptID, created.AttemptID, created.ReservationID).Scan(&requestSlots, &taskSlots, &reservation); err != nil || requestSlots != 0 || taskSlots != 1 || reservation != "active" {
				t.Fatalf("requests=%d tasks=%d reservation=%s err=%v", requestSlots, taskSlots, reservation, err)
			}
		})
	}
	t.Run("crash_after_sending_does_not_duplicate_generation", func(t *testing.T) {
		mode.Store("normal")
		if _, err := db.Exec(`UPDATE gw_channel_transports SET timeout_ms=30000 WHERE id=?`, transportID); err != nil {
			t.Fatal(err)
		}
		created := create("crashed")
		var item repository.OutboxItem
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			item, err = store.ClaimAsyncOutbox(ctx, tx, "qa-crashed-worker", time.Minute)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.BeginAsyncRequest(ctx, item, gatewayruntime.AsyncRequestEvidence{MappingHMAC: strings.Repeat("b", 64), RequestHMAC: strings.Repeat("c", 64)}); err != nil {
			t.Fatal(err)
		}
		request, _ := http.NewRequest(http.MethodPost, upstream.URL+"/tasks", strings.NewReader(`{"model":"qa-import-model"}`))
		request.Header.Set("Authorization", "Bearer test-import-secret")
		request.Header.Set("X-Request-ID", "qa-http-crashed")
		response, err := upstream.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		before := submits.Load()
		if _, err := db.Exec(`UPDATE gw_async_outbox SET lease_expires_at=DATE_SUB(UTC_TIMESTAMP(3),INTERVAL 1 SECOND) WHERE id=?`, item.ID); err != nil {
			t.Fatal(err)
		}
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "submission_unknown")
		if err := dispatcher.Dispatch(ctx, item); err == nil {
			t.Fatal("stale worker obtained another send authorization")
		}
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "manual_review")
		if submits.Load() != before {
			t.Fatal("crashed submit was sent again")
		}
	})
	t.Run("query_outage_beyond_ten_attempts_keeps_recovering", func(t *testing.T) {
		mode.Store("normal")
		created := create("query-outage")
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		before := submits.Load()
		mode.Store("query_error")
		for range 12 {
			if err := process(created); err == nil {
				t.Fatal("provider outage was not reported")
			}
			assertState(created, "accepted")
		}
		var status string
		var attempts, requestSlots int
		if err := db.QueryRow(`SELECT status,attempt_count FROM gw_async_outbox WHERE async_execution_id=? AND action='query'`, created.AsyncExecutionID).Scan(&status, &attempts); err != nil || status != "pending" || attempts != 12 {
			t.Fatalf("status=%s attempts=%d err=%v", status, attempts, err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM gw_credential_slots s JOIN gw_channel_request_logs l ON l.id=s.request_log_id WHERE l.attempt_id=? AND s.state='active'`, created.AttemptID).Scan(&requestSlots); err != nil || requestSlots != 0 {
			t.Fatalf("request slots leaked after retry: %d %v", requestSlots, err)
		}
		mode.Store("normal")
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "succeeded")
		if submits.Load() != before {
			t.Fatal("poll retries caused another submit")
		}
	})
	t.Run("duplicate_provider_identity_requires_review", func(t *testing.T) {
		mode.Store("duplicate_id")
		created := create("duplicate-id")
		before := submits.Load()
		if err := process(created); err == nil {
			t.Fatal("provider task identity was bound to two executions")
		}
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "submission_unknown")
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "manual_review")
		if submits.Load() != before+1 {
			t.Fatal("identity conflict caused another generation")
		}
	})
	t.Run("query_cannot_complete_a_different_provider_task", func(t *testing.T) {
		mode.Store("normal")
		created := create("wrong-id")
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		mode.Store("wrong_id")
		if err := process(created); err == nil {
			t.Fatal("another task's result was accepted")
		}
		assertState(created, "accepted")
		mode.Store("normal")
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "succeeded")
	})
	t.Run("terminal_billing_failure_rolls_back_response_and_retries_query", func(t *testing.T) {
		mode.Store("normal")
		created := create("billing-retry")
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE gw_sell_rates SET max_quantity=NULL WHERE sku_id=?`, input.Call.SKUID); err != nil {
			t.Fatal(err)
		}
		before := submits.Load()
		if err := process(created); err == nil {
			t.Fatal("invalid billing configuration committed a partial terminal result")
		}
		assertState(created, "accepted")
		var resultID sql.NullInt64
		if err := db.QueryRow(`SELECT result_payload_id FROM gw_api_calls WHERE id=?`, created.CallID).Scan(&resultID); err != nil || resultID.Valid {
			t.Fatalf("partial result remained after rollback: %v %v", resultID, err)
		}
		if _, err := db.Exec(`UPDATE gw_sell_rates SET max_quantity=10 WHERE sku_id=?`, input.Call.SKUID); err != nil {
			t.Fatal(err)
		}
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "succeeded")
		if submits.Load() != before {
			t.Fatal("billing retry repeated the generation request")
		}
	})
	t.Run("obsolete_submit_is_acknowledged_without_network", func(t *testing.T) {
		created := create("obsolete")
		if err := service.FinishAsync(ctx, created.AsyncExecutionID, execution.AsyncNotCreated, "qa_confirmed_not_sent", billing.Facts{}); err != nil {
			t.Fatal(err)
		}
		before := submits.Load()
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "not_created")
		var status, reason string
		if err := db.QueryRow(`SELECT status,last_error_code FROM gw_async_outbox WHERE async_execution_id=?`, created.AsyncExecutionID).Scan(&status, &reason); err != nil || status != "succeeded" || reason != "superseded_action" || submits.Load() != before {
			t.Fatalf("obsolete action status=%s reason=%s err=%v", status, reason, err)
		}
	})
	t.Run("complete_submit_rejection_is_not_created", func(t *testing.T) {
		mode.Store("reject")
		created := create("rejected")
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "not_created")
		var attemptState, callState, requestStatus string
		if err := db.QueryRow(`SELECT a.state,c.status,l.status FROM gw_api_call_attempts a JOIN gw_api_calls c ON c.id=a.call_id JOIN gw_channel_request_logs l ON l.attempt_id=a.id AND l.action='submit' WHERE a.id=?`, created.AttemptID).Scan(&attemptState, &callState, &requestStatus); err != nil {
			t.Fatal(err)
		}
		if attemptState != "not_created" || callState != "failed" || requestStatus != "response_recorded" {
			t.Fatalf("attempt=%s call=%s request=%s", attemptState, callState, requestStatus)
		}
		mode.Store("normal")
	})
	t.Run("delivery_refresh_retries_and_recovers_lost_exchange", func(t *testing.T) {
		mode.Store("normal")
		if _, err := db.Exec(`UPDATE gw_product_transports SET source_url_policy='refreshable' WHERE id=?`, input.Attempt.ProductTransportID); err != nil {
			t.Fatal(err)
		}
		defer db.Exec(`UPDATE gw_product_transports SET source_url_policy='fixed' WHERE id=?`, input.Attempt.ProductTransportID)
		created := create("delivery-recovery")
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		if err := process(created); err != nil {
			t.Fatal(err)
		}
		assertState(created, "succeeded")
		var deliveryID uint64
		if err := db.QueryRow(`SELECT id FROM gw_result_deliveries WHERE attempt_id=? AND result_ordinal=0`, created.AttemptID).Scan(&deliveryID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE gw_result_deliveries SET expires_at=DATE_SUB(UTC_TIMESTAMP(3),INTERVAL 1 SECOND) WHERE id=?`, deliveryID); err != nil {
			t.Fatal(err)
		}
		if err := store.WithTx(ctx, func(tx *sql.Tx) error { return store.ExpireResultDelivery(ctx, tx, deliveryID) }); err != nil {
			t.Fatal(err)
		}
		processDelivery := func(owner string) error {
			t.Helper()
			if _, err := db.Exec(`UPDATE gw_async_outbox SET available_at=UTC_TIMESTAMP(3) WHERE result_delivery_id=? AND status='pending'`, deliveryID); err != nil {
				t.Fatal(err)
			}
			worked, err := service.ProcessDeliveryOne(ctx, owner, time.Minute, time.Millisecond, dispatcher)
			if !worked {
				t.Fatalf("no delivery action processed: %v", err)
			}
			return err
		}
		beforeSubmits := submits.Load()
		mode.Store("query_error")
		if err := processDelivery("qa-delivery-retry"); err == nil {
			t.Fatal("temporary provider failure was not retried")
		}
		var outboxStatus string
		var attempts uint64
		if err := db.QueryRow(`SELECT status,attempt_count FROM gw_async_outbox WHERE result_delivery_id=?`, deliveryID).Scan(&outboxStatus, &attempts); err != nil || outboxStatus != "pending" || attempts != 1 {
			t.Fatalf("delivery retry status=%s attempts=%d err=%v", outboxStatus, attempts, err)
		}

		mode.Store("delivery_refresh")
		if _, err := db.Exec(`UPDATE gw_async_outbox SET available_at=UTC_TIMESTAMP(3) WHERE result_delivery_id=?`, deliveryID); err != nil {
			t.Fatal(err)
		}
		var crashed repository.OutboxItem
		if err := store.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			crashed, err = store.ClaimDeliveryOutbox(ctx, tx, "qa-delivery-crashed", time.Minute)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.BeginDeliveryReconcileRequest(ctx, crashed, strings.Repeat("d", 64), strings.Repeat("e", 64)); err != nil {
			var deliveryState, outboxStatus, owner string
			var deliveryVersion, deliverySequence, outboxVersion, outboxSequence, outboxAttempts uint64
			_ = db.QueryRow(`SELECT d.state,d.state_version,d.action_seq,o.status,o.state_version,o.action_seq,o.attempt_count,o.lease_owner FROM gw_result_deliveries d JOIN gw_async_outbox o ON o.result_delivery_id=d.id WHERE d.id=?`, deliveryID).Scan(&deliveryState, &deliveryVersion, &deliverySequence, &outboxStatus, &outboxVersion, &outboxSequence, &outboxAttempts, &owner)
			t.Fatalf("begin crashed delivery request: %v item=%+v delivery=%s/%d/%d outbox=%s/%d/%d/%d/%s", err, crashed, deliveryState, deliveryVersion, deliverySequence, outboxStatus, outboxVersion, outboxSequence, outboxAttempts, owner)
		}
		request, _ := http.NewRequest(http.MethodGet, upstream.URL+"/tasks/lost-delivery-query", nil)
		request.Header.Set("Authorization", "Bearer test-import-secret")
		response, err := upstream.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if _, err := db.Exec(`UPDATE gw_async_outbox SET lease_expires_at=DATE_SUB(UTC_TIMESTAMP(3),INTERVAL 1 SECOND) WHERE id=?`, crashed.ID); err != nil {
			t.Fatal(err)
		}
		if err := processDelivery("qa-delivery-reclaimed"); err != nil {
			t.Fatal(err)
		}
		var deliveryState, activeURLHMAC, completedStatus, lostStatus string
		var sourceCount, activeCount int
		if err := db.QueryRow(`SELECT d.state,(SELECT COUNT(*) FROM gw_result_delivery_sources WHERE result_delivery_id=d.id),(SELECT COUNT(*) FROM gw_result_delivery_sources WHERE result_delivery_id=d.id AND state='active'),s.url_hmac,o.status,(SELECT status FROM gw_channel_request_logs WHERE outbox_id=o.id AND outbox_attempt_count=2) FROM gw_result_deliveries d JOIN gw_result_delivery_sources s ON s.id=d.current_source_id JOIN gw_async_outbox o ON o.result_delivery_id=d.id WHERE d.id=?`, deliveryID).Scan(&deliveryState, &sourceCount, &activeCount, &activeURLHMAC, &completedStatus, &lostStatus); err != nil {
			t.Fatal(err)
		}
		expectedDigest := repositoryURLDigest(input.RequestPayload.HMACKey, "https://example.invalid/refreshed.mp4")
		if deliveryState != "ready" || sourceCount != 2 || activeCount != 1 || activeURLHMAC != expectedDigest || completedStatus != "succeeded" || lostStatus != "unknown" {
			t.Fatalf("delivery state=%s sources=%d active=%d hmac=%s outbox=%s lost=%s", deliveryState, sourceCount, activeCount, activeURLHMAC, completedStatus, lostStatus)
		}
		if submits.Load() != beforeSubmits {
			t.Fatal("delivery recovery submitted another generation")
		}
	})
}

func repositoryURLDigest(key []byte, value string) string {
	digest := security.HMACSHA256(key, []byte(value))
	return fmt.Sprintf("%x", digest[:])
}
