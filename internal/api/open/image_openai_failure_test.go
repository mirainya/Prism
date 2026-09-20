package open

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/payloadview"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestWaitUnifiedOpenAIImageResponseReturnsRecordedProviderFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previousStore := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previousStore })

	const callID, userID, tokenID = uint64(10), uint64(41), uint64(7)
	expectPendingImageResult(mock, callID, userID, tokenID, "failed")
	kek, hmacKey := configureUnifiedPayloadKeys(t)
	expectImageRequestFailure(t, mock, callID, 31, 44, http.StatusUnprocessableEntity,
		"provider_task_rejected", "bad_image", "custom mapped failure", kek, hmacKey)

	_, err = waitUnifiedOpenAIImageResponse(context.Background(), callID, userID, tokenID, "url", time.Second)
	var providerErr *gatewayruntime.ProviderCapabilityError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %v, want provider failure", err)
	}
	if providerErr.HTTPStatus != http.StatusUnprocessableEntity || providerErr.Code != "provider_task_rejected" || providerErr.Message != "custom mapped failure" {
		t.Fatalf("provider error = %+v", providerErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitUnifiedOpenAIImageResponseDoesNotReadRawResponseWhenDiagnosticFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previousStore := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previousStore })

	const callID, userID, tokenID = uint64(10), uint64(41), uint64(7)
	const requestLogID, responseBlobID, diagnosticBlobID = uint64(31), uint64(43), uint64(44)
	const rawMessage = "raw response detail must stay private"
	expectPendingImageResult(mock, callID, userID, tokenID, "failed")
	kek, hmacKey := configureUnifiedPayloadKeys(t)
	mock.ExpectQuery("SELECT l.id,l.error_code,l.http_status,l.response_payload_blob_id,l.diagnostic_blob_id").
		WithArgs(callID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "error_code", "http_status", "response_payload_blob_id", "diagnostic_blob_id"}).
			AddRow(requestLogID, "provider_task_rejected", http.StatusOK, responseBlobID, diagnosticBlobID))
	mock.ExpectQuery("SELECT b.keyring_id,b.purpose,b.schema_version").
		WithArgs(diagnosticBlobID).
		WillReturnError(errors.New("diagnostic unavailable"))
	expectEncryptedRequestLogBlob(t, mock, requestLogID, responseBlobID, "response", []byte(`{"error":{"message":"`+rawMessage+`"}}`), kek, hmacKey)

	_, err = waitUnifiedOpenAIImageResponse(context.Background(), callID, userID, tokenID, "url", time.Second)
	var providerErr *gatewayruntime.ProviderCapabilityError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %v, want provider failure", err)
	}
	if providerErr.Message == rawMessage {
		t.Fatalf("raw provider response was exposed: %q", providerErr.Message)
	}

	// Consume the still-pending response expectation to prove the failure reader
	// did not open the raw response after the diagnostic read failed.
	plain, err := payloadview.ReadRequestLogPayload(context.Background(), store, requestLogID, responseBlobID, "response")
	if err != nil {
		t.Fatalf("raw response was already read by the failure path: %v", err)
	}
	clear(plain)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitUnifiedOpenAIImageResponseFallsBackWhenFailureEvidenceCannotBeRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	previousStore := unifiedVideoStore
	unifiedVideoStore = store
	t.Cleanup(func() { unifiedVideoStore = previousStore })

	const callID, userID, tokenID = uint64(10), uint64(41), uint64(7)
	expectPendingImageResult(mock, callID, userID, tokenID, "failed")
	mock.ExpectQuery("SELECT l.id,l.error_code,l.http_status,l.response_payload_blob_id,l.diagnostic_blob_id").
		WithArgs(callID).
		WillReturnError(errors.New("failure evidence unavailable"))

	_, err = waitUnifiedOpenAIImageResponse(context.Background(), callID, userID, tokenID, "url", time.Second)
	var providerErr *gatewayruntime.ProviderCapabilityError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %v, want provider failure", err)
	}
	if providerErr.HTTPStatus != http.StatusBadGateway || providerErr.Code != "provider_request_failed" || providerErr.Message != "" {
		t.Fatalf("provider error = %+v", providerErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectPendingImageResult(mock sqlmock.Sqlmock, callID, userID, tokenID uint64, status string) {
	mock.ExpectQuery("SELECT c.result_payload_id,c.created_at FROM gw_api_calls").
		WithArgs(callID, userID, tokenID).
		WillReturnRows(sqlmock.NewRows([]string{"result_payload_id", "created_at"}))
	mock.ExpectQuery("SELECT status FROM gw_api_calls").
		WithArgs(callID, userID, tokenID).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(status))
}

func expectImageRequestFailure(t *testing.T, mock sqlmock.Sqlmock, callID, requestLogID, blobID uint64, status int, code, providerCode, providerMessage string, kek, hmacKey []byte) {
	t.Helper()
	providerStatus := uint16(status)
	diagnostic, err := payloadview.EncodeFailureDiagnostic(providerCode, providerMessage, &providerStatus)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT l.id,l.error_code,l.http_status,l.response_payload_blob_id,l.diagnostic_blob_id").
		WithArgs(callID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "error_code", "http_status", "response_payload_blob_id", "diagnostic_blob_id"}).
			AddRow(requestLogID, code, http.StatusOK, nil, blobID))
	owner := []byte(fmt.Sprintf("request-log:%d:diagnostic", requestLogID))
	aad, err := security.CanonicalAAD(blobID, "gateway-request-diagnostic", 1, owner)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := security.Seal(diagnostic, aad, kek, 1)
	if err != nil {
		t.Fatal(err)
	}
	digest := security.HMACSHA256(hmacKey, diagnostic)
	mock.ExpectQuery("SELECT b.keyring_id,b.purpose,b.schema_version").
		WithArgs(blobID).
		WillReturnRows(sqlmock.NewRows([]string{"keyring_id", "purpose", "schema_version", "aad_hash", "nonce", "ciphertext", "content_hmac", "kek_version", "wrap_nonce", "wrapped_dek"}).
			AddRow(uint64(1), "gateway-request-diagnostic", uint32(1), "", sealed.Nonce, sealed.Ciphertext,
				hex.EncodeToString(digest[:]), uint32(1), sealed.WrapNonce, sealed.WrappedDEK))
}

func expectEncryptedRequestLogBlob(t *testing.T, mock sqlmock.Sqlmock, requestLogID, blobID uint64, kind string, plaintext, kek, hmacKey []byte) {
	t.Helper()
	purpose := "gateway-upstream-" + kind
	owner := []byte(fmt.Sprintf("request-log:%d:%s", requestLogID, kind))
	aad, err := security.CanonicalAAD(blobID, purpose, 1, owner)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := security.Seal(plaintext, aad, kek, 1)
	if err != nil {
		t.Fatal(err)
	}
	digest := security.HMACSHA256(hmacKey, plaintext)
	mock.ExpectQuery("SELECT b.keyring_id,b.purpose,b.schema_version").
		WithArgs(blobID).
		WillReturnRows(sqlmock.NewRows([]string{"keyring_id", "purpose", "schema_version", "aad_hash", "nonce", "ciphertext", "content_hmac", "kek_version", "wrap_nonce", "wrapped_dek"}).
			AddRow(uint64(1), purpose, uint32(1), "", sealed.Nonce, sealed.Ciphertext,
				hex.EncodeToString(digest[:]), uint32(1), sealed.WrapNonce, sealed.WrappedDEK))
}
