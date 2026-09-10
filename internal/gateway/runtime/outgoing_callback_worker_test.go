package runtime

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/pkg/safeurl"
)

type callbackHTTPClientFunc func(*http.Request) (*http.Response, error)

func (f callbackHTTPClientFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type callbackRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f callbackRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCallbackDeliveryWorkerRejectsAllRedirects(t *testing.T) {
	service, _, _ := callbackWorkerTestService(t)
	worker, err := NewCallbackDeliveryWorker(service)
	if err != nil {
		t.Fatal(err)
	}
	client, ok := worker.client.(*http.Client)
	if !ok || client.CheckRedirect == nil {
		t.Fatal("callback worker HTTP client has no redirect policy")
	}
	var requests atomic.Int32
	client.Transport = callbackRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if requests.Add(1) != 1 {
			t.Fatalf("redirect target was requested: %s", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     http.Header{"Location": []string{"https://other.example/hook"}},
			Body:       io.NopCloser(strings.NewReader("redirect")),
			Request:    request,
		}, nil
	})
	request, err := http.NewRequest(http.MethodPost, "https://callback.example/hook", strings.NewReader(`{"event":"done"}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || requests.Load() != 1 {
		t.Fatalf("status=%d requests=%d", response.StatusCode, requests.Load())
	}
	redirect, err := http.NewRequest(http.MethodPost, "https://other.example/hook", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(redirect, []*http.Request{request}); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy error = %v, want http.ErrUseLastResponse", err)
	}
}

func TestCallbackDeliveryDispatchSendsPersistedPayloadWithStableIdentity(t *testing.T) {
	service, mock, key := callbackWorkerTestService(t)
	signingSecret := []byte("0123456789abcdef0123456789abcdef")
	target := []byte(`{"schema_version":1,"url":"https://callback.example/hook","signing_secret":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	payload := []byte(`{"id":"video-1","status":"completed"}`)
	expectCallbackWorkerBlob(t, mock, 11, callbackTargetBlobPurpose, callbackTargetOwner(7), target, key)
	expectCallbackWorkerBlob(t, mock, 12, callbackEventBlobPurpose, callbackEventOwner(7, 1), payload, key)
	targetDigest := security.DomainDigest(key, callbackTargetDigestDomain, target)
	payloadDigest := security.HMACSHA256(key, payload)
	claim := repository.ClaimedCallbackDelivery{
		ID: 5, CallID: 7, EventSeq: 1, TargetBlobID: 11, PayloadBlobID: 12,
		TargetHMAC: hex.EncodeToString(targetDigest[:]), PayloadHMAC: hex.EncodeToString(payloadDigest[:]),
		Algorithm: callbackAlgorithmHTTPJSONV1, PolicyVersion: callbackPolicyVersion,
		AttemptNo: 1, MaxAttempts: 5, CallPublicID: "video-1", ReplayExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	fixedNow := time.Unix(1_700_000_000, 0).UTC()
	worker := &CallbackDeliveryWorker{
		service:  service,
		validate: func(context.Context, string) error { return nil },
		now:      func() time.Time { return fixedNow },
		client: callbackHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != string(payload) {
				t.Fatalf("body = %s", body)
			}
			timestamp := request.Header.Get("X-Prism-Timestamp")
			signatureHeader := request.Header.Get("X-Prism-Signature")
			if request.Method != http.MethodPost || request.URL.String() != "https://callback.example/hook" || request.Header.Get("X-Prism-Event-ID") != "video-1:terminal:1" || request.Header.Get("Idempotency-Key") != "video-1:terminal:1" || request.Header.Get("X-Prism-Callback-Attempt") != "1" || timestamp == "" || signatureHeader == "" {
				t.Fatalf("unexpected request: %s %s %#v", request.Method, request.URL, request.Header)
			}
			signingBase := []byte(timestamp + ".video-1:terminal:1.")
			signingBase = append(signingBase, payload...)
			expected := security.HMACSHA256(signingSecret, signingBase)
			if signatureHeader != "t="+timestamp+",v1="+hex.EncodeToString(expected[:]) {
				t.Fatalf("signature mismatch: got %s", signatureHeader)
			}
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}),
	}
	result := worker.dispatch(context.Background(), claim)
	if result.Outcome != "succeeded" || result.AttemptState != "succeeded" || result.HTTPStatus != http.StatusNoContent || !result.RequestComplete || !result.ResponseComplete || result.ResponseHMAC == "" {
		t.Fatalf("result = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCallbackDeliveryDispatchRejectsUnsafeTargetBeforeHTTP(t *testing.T) {
	service, mock, key := callbackWorkerTestService(t)
	target := []byte(`{"schema_version":1,"url":"https://callback.example/hook","signing_secret":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	expectCallbackWorkerBlob(t, mock, 11, callbackTargetBlobPurpose, callbackTargetOwner(7), target, key)
	targetDigest := security.DomainDigest(key, callbackTargetDigestDomain, target)
	var sent atomic.Bool
	worker := &CallbackDeliveryWorker{
		service:  service,
		validate: func(context.Context, string) error { return safeurl.ErrUnsafeURL },
		now:      func() time.Time { return time.Now().UTC() },
		client: callbackHTTPClientFunc(func(*http.Request) (*http.Response, error) {
			sent.Store(true)
			return nil, nil
		}),
	}
	result := worker.dispatch(context.Background(), repository.ClaimedCallbackDelivery{
		CallID: 7, EventSeq: 1, TargetBlobID: 11, TargetHMAC: hex.EncodeToString(targetDigest[:]),
		Algorithm: callbackAlgorithmHTTPJSONV1, PolicyVersion: callbackPolicyVersion,
		AttemptNo: 1, MaxAttempts: 5, ReplayExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if result.Outcome != "dead_letter" || result.ErrorCode != "unsafe_callback_target" || sent.Load() {
		t.Fatalf("result=%+v sent=%t", result, sent.Load())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCallbackDeliveryDispatchRetriesRetryableHTTPStatus(t *testing.T) {
	service, mock, key := callbackWorkerTestService(t)
	target := []byte(`{"schema_version":1,"url":"https://callback.example/hook","signing_secret":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	payload := []byte(`{"id":"video-1","status":"failed"}`)
	expectCallbackWorkerBlob(t, mock, 11, callbackTargetBlobPurpose, callbackTargetOwner(7), target, key)
	expectCallbackWorkerBlob(t, mock, 12, callbackEventBlobPurpose, callbackEventOwner(7, 1), payload, key)
	targetDigest := security.DomainDigest(key, callbackTargetDigestDomain, target)
	payloadDigest := security.HMACSHA256(key, payload)
	now := time.Now().UTC()
	worker := &CallbackDeliveryWorker{
		service:  service,
		validate: func(context.Context, string) error { return nil }, now: func() time.Time { return now },
		client: callbackHTTPClientFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("try later")), Header: make(http.Header)}, nil
		}),
	}
	result := worker.dispatch(context.Background(), repository.ClaimedCallbackDelivery{
		CallID: 7, EventSeq: 1, TargetBlobID: 11, PayloadBlobID: 12,
		TargetHMAC: hex.EncodeToString(targetDigest[:]), PayloadHMAC: hex.EncodeToString(payloadDigest[:]),
		Algorithm: callbackAlgorithmHTTPJSONV1, PolicyVersion: callbackPolicyVersion,
		AttemptNo: 2, MaxAttempts: 5, CallPublicID: "video-1", ReplayExpiresAt: now.Add(time.Hour),
	})
	if result.Outcome != "retry" || result.AttemptState != "failed" || result.ErrorCode != "callback_http_retryable" || result.HTTPStatus != http.StatusServiceUnavailable || !result.RetryAt.After(now) {
		t.Fatalf("result = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCallbackDeliveryDispatchRetriesTransientTargetBlobReadError(t *testing.T) {
	service, mock, _ := callbackWorkerTestService(t)
	mock.ExpectQuery("SELECT b.keyring_id,b.purpose,b.schema_version").WithArgs(uint64(11)).
		WillReturnError(errors.New("temporary database failure"))
	now := time.Now().UTC()
	worker := &CallbackDeliveryWorker{
		service: service, validate: func(context.Context, string) error { return nil },
		now: func() time.Time { return now }, client: callbackHTTPClientFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("HTTP request must not be sent when the target blob cannot be read")
			return nil, nil
		}),
	}
	result := worker.dispatch(context.Background(), repository.ClaimedCallbackDelivery{
		CallID: 7, EventSeq: 1, TargetBlobID: 11,
		Algorithm: callbackAlgorithmHTTPJSONV1, PolicyVersion: callbackPolicyVersion,
		AttemptNo: 1, MaxAttempts: 5, ReplayExpiresAt: now.Add(time.Hour),
	})
	if result.Outcome != "retry" || result.AttemptState != "failed" || result.ErrorCode != "callback_target_read_failed" || !result.RetryAt.After(now) {
		t.Fatalf("result = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCallbackDeliveryDispatchRetriesTransientPayloadBlobReadError(t *testing.T) {
	service, mock, key := callbackWorkerTestService(t)
	target := []byte(`{"schema_version":1,"url":"https://callback.example/hook","signing_secret":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	expectCallbackWorkerBlob(t, mock, 11, callbackTargetBlobPurpose, callbackTargetOwner(7), target, key)
	mock.ExpectQuery("SELECT b.keyring_id,b.purpose,b.schema_version").WithArgs(uint64(12)).
		WillReturnError(errors.New("temporary database failure"))
	targetDigest := security.DomainDigest(key, callbackTargetDigestDomain, target)
	now := time.Now().UTC()
	worker := &CallbackDeliveryWorker{
		service: service, validate: func(context.Context, string) error { return nil },
		now: func() time.Time { return now }, client: callbackHTTPClientFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("HTTP request must not be sent when the payload blob cannot be read")
			return nil, nil
		}),
	}
	result := worker.dispatch(context.Background(), repository.ClaimedCallbackDelivery{
		CallID: 7, EventSeq: 1, TargetBlobID: 11, PayloadBlobID: 12,
		TargetHMAC: hex.EncodeToString(targetDigest[:]),
		Algorithm:  callbackAlgorithmHTTPJSONV1, PolicyVersion: callbackPolicyVersion,
		AttemptNo: 1, MaxAttempts: 5, ReplayExpiresAt: now.Add(time.Hour),
	})
	if result.Outcome != "retry" || result.AttemptState != "failed" || result.ErrorCode != "callback_payload_read_failed" || !result.RetryAt.After(now) {
		t.Fatalf("result = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCallbackDeliveryDispatchDeadLettersCorruptTargetBlob(t *testing.T) {
	service, mock, key := callbackWorkerTestService(t)
	target := []byte(`{"schema_version":1,"url":"https://callback.example/hook","signing_secret":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	wrongKey := []byte("abcdefghijklmnopqrstuvwxyzABCDEF")
	expectCallbackWorkerBlob(t, mock, 11, callbackTargetBlobPurpose, callbackTargetOwner(7), target, wrongKey)
	targetDigest := security.DomainDigest(key, callbackTargetDigestDomain, target)
	now := time.Now().UTC()
	worker := &CallbackDeliveryWorker{
		service: service, validate: func(context.Context, string) error { return nil },
		now: func() time.Time { return now }, client: callbackHTTPClientFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("HTTP request must not be sent for corrupt callback material")
			return nil, nil
		}),
	}
	result := worker.dispatch(context.Background(), repository.ClaimedCallbackDelivery{
		CallID: 7, EventSeq: 1, TargetBlobID: 11, TargetHMAC: hex.EncodeToString(targetDigest[:]),
		Algorithm: callbackAlgorithmHTTPJSONV1, PolicyVersion: callbackPolicyVersion,
		AttemptNo: 1, MaxAttempts: 5, ReplayExpiresAt: now.Add(time.Hour),
	})
	if result.Outcome != "dead_letter" || result.AttemptState != "failed" || result.ErrorCode != "callback_target_decryption_failed" || !result.RetryAt.IsZero() {
		t.Fatalf("result = %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func callbackWorkerTestService(t *testing.T) (*Service, sqlmock.Sqlmock, []byte) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	if err := service.ConfigureCallbackDelivery(CallbackDeliveryKeys{PayloadKEK: key, PayloadHMAC: key}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	return service, mock, key
}

func expectCallbackWorkerBlob(t *testing.T, mock sqlmock.Sqlmock, blobID uint64, purpose string, owner, plaintext, key []byte) {
	t.Helper()
	aad, err := security.CanonicalAAD(blobID, purpose, 1, owner)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := security.Seal(plaintext, aad, key, 1)
	if err != nil {
		t.Fatal(err)
	}
	digest := security.HMACSHA256(key, plaintext)
	mock.ExpectQuery("SELECT b.keyring_id,b.purpose,b.schema_version").WithArgs(blobID).
		WillReturnRows(sqlmock.NewRows([]string{"keyring_id", "purpose", "schema_version", "aad_hash", "nonce", "ciphertext", "content_hmac", "kek_version", "wrap_nonce", "wrapped_dek"}).
			AddRow(uint64(1), purpose, uint32(1), "", envelope.Nonce, envelope.Ciphertext, hex.EncodeToString(digest[:]), uint32(1), envelope.WrapNonce, envelope.WrappedDEK))
}
