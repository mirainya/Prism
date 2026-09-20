package runtime

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestCallbackFailedObservationCommitsSanitizedDiagnostic(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}

	providerStatus := uint16(http.StatusUnprocessableEntity)
	providerMessage := "invalid prompt at https://private.example/result Authorization: Bearer sk-callback-secret-123456789 from 10.1.2.3"
	observation := callbackObservationFromAsync(
		repository.CallbackReceiptRecord{ID: 17, AsyncExecutionID: 19, PayloadHMAC: strings.Repeat("a", 64)},
		repository.AsyncDispatch{SourceURLPolicy: "fixed"},
		AsyncObservation{
			State:                execution.AsyncFailed,
			ProviderErrorCode:    "invalid_prompt",
			ProviderErrorMessage: providerMessage,
			ProviderHTTPStatus:   &providerStatus,
		},
	)
	key := bytes.Repeat([]byte{7}, 32)
	rawResultMarker := []byte(`{"raw":"provider callback remains in its receipt blob"}`)
	result, err := callbackRequestLogResult(observation, repository.BlobInput{
		KeyringID: 3, KEKVersion: 4, Plaintext: rawResultMarker, KEK: key, HMACKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "provider_task_rejected" {
		t.Fatalf("error code = %q, want provider_task_rejected", result.ErrorCode)
	}
	if result.HTTPStatus == nil || *result.HTTPStatus != http.StatusOK {
		t.Fatalf("callback transport status = %v, want 200", result.HTTPStatus)
	}
	if result.ResponsePayload != nil || result.Diagnostic == nil {
		t.Fatal("callback evidence was copied into the response payload or diagnostic was omitted")
	}
	if bytes.Equal(result.Diagnostic.Plaintext, rawResultMarker) {
		t.Fatal("raw callback evidence was reused as the public diagnostic")
	}
	encoded := string(result.Diagnostic.Plaintext)
	for _, secret := range []string{"private.example", "sk-callback-secret", "10.1.2.3"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("diagnostic contains sensitive value %q: %s", secret, encoded)
		}
	}
	var diagnostic map[string]any
	if err := json.Unmarshal(result.Diagnostic.Plaintext, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if diagnostic["provider_code"] != "invalid_prompt" || diagnostic["provider_http_status"] != float64(providerStatus) {
		t.Fatalf("diagnostic lost provider failure facts: %#v", diagnostic)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT status FROM gw_channel_request_logs").
		WithArgs(uint64(23)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("dispatching"))
	mock.ExpectQuery("SELECT status FROM gw_channel_request_logs").
		WithArgs(uint64(23)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("dispatching"))
	mock.ExpectQuery("SELECT diagnostic_blob_id FROM gw_channel_request_logs").
		WithArgs(uint64(23)).
		WillReturnRows(sqlmock.NewRows([]string{"diagnostic_blob_id"}).AddRow(nil))
	mock.ExpectExec("INSERT INTO encrypted_blobs").WillReturnResult(sqlmock.NewResult(29, 1))
	mock.ExpectExec("UPDATE encrypted_blobs SET aad_hash").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO encrypted_blob_key_wraps").WillReturnResult(sqlmock.NewResult(31, 1))
	mock.ExpectExec("UPDATE gw_channel_request_logs SET diagnostic_blob_id").
		WithArgs(uint64(29), uint64(23)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE gw_channel_request_logs SET status=").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT status FROM gw_channel_request_logs").
		WithArgs(uint64(23)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("sent"))
	mock.ExpectQuery("SELECT diagnostic_blob_id FROM gw_channel_request_logs").
		WithArgs(uint64(23)).
		WillReturnRows(sqlmock.NewRows([]string{"diagnostic_blob_id"}).AddRow(uint64(29)))
	diagnosticHMAC := security.HMACSHA256(key, result.Diagnostic.Plaintext)
	mock.ExpectQuery("SELECT purpose,content_hmac,content_length FROM encrypted_blobs").
		WithArgs(int64(29)).
		WillReturnRows(sqlmock.NewRows([]string{"purpose", "content_hmac", "content_length"}).
			AddRow("gateway-request-diagnostic", fmt.Sprintf("%x", diagnosticHMAC[:]), len(result.Diagnostic.Plaintext)))
	mock.ExpectExec("UPDATE gw_channel_request_logs SET status=").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id,state FROM gw_credential_slots").
		WithArgs(uint64(23)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "state"}).AddRow(uint64(37), "active"))
	mock.ExpectQuery("SELECT state,scope FROM gw_credential_slots").
		WithArgs(uint64(37)).
		WillReturnRows(sqlmock.NewRows([]string{"state", "scope"}).AddRow("active", "request"))
	mock.ExpectExec("UPDATE gw_credential_slots SET state=").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return service.finishRequest(context.Background(), tx, 23, "response_recorded", result)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
