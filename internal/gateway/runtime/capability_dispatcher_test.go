package runtime

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestCapabilityExchangeObservesExactlyTheRecordedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("first"))
		_, _ = w.Write([]byte("-second"))
	}))
	defer server.Close()
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, bytes.NewReader([]byte("request")))
	key := bytes.Repeat([]byte{1}, 32)
	dispatcher := &CapabilityDispatcher{client: server.Client(), network: &AsyncDispatcher{keys: AsyncKeys{PayloadHMAC: key}}}
	var observed []byte
	body, result := dispatcher.exchange(request, func(chunk []byte) error {
		observed = append(observed, chunk...)
		return nil
	})
	if result.ErrorCode != "" || !result.RequestComplete || !result.ResponseComplete || result.HTTPStatus == nil || *result.HTTPStatus != http.StatusOK {
		t.Fatalf("exchange = %+v", result)
	}
	if !bytes.Equal(body, []byte("first-second")) || !bytes.Equal(observed, body) || result.ResponseBytesHMAC == "" {
		t.Fatalf("body=%q observed=%q hmac=%q", body, observed, result.ResponseBytesHMAC)
	}
}

func TestCapabilityExchangeTreatsObserverFailureAsIncomplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("response"))
	}))
	defer server.Close()
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, nil)
	key := bytes.Repeat([]byte{1}, 32)
	dispatcher := &CapabilityDispatcher{client: server.Client(), network: &AsyncDispatcher{keys: AsyncKeys{PayloadHMAC: key}}}
	_, result := dispatcher.exchange(request, func([]byte) error { return errors.New("observer failed") })
	if result.ErrorCode != "provider_response_incomplete" || result.ResponseComplete {
		t.Fatalf("exchange = %+v", result)
	}
}

func TestCapabilityResponsePayloadCaptureIsBounded(t *testing.T) {
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
	key := bytes.Repeat([]byte{1}, 32)
	dispatcher := &CapabilityDispatcher{network: &AsyncDispatcher{
		service: service,
		keys:    AsyncKeys{PayloadKEK: key, PayloadHMAC: key},
	}}
	mock.ExpectQuery("SELECT k.id,k.current_version FROM crypto_keyring_state").
		WillReturnRows(sqlmock.NewRows([]string{"id", "current_version"}).AddRow(7, 3))
	body := []byte(`{"data":[{"url":"https://cdn.example/image.png"}]}`)
	var result repository.RequestLogResult
	if err := dispatcher.attachResponsePayload(context.Background(), body, &result); err != nil {
		t.Fatal(err)
	}
	if result.ResponsePayload == nil || result.ResponsePayload.KeyringID != 7 || result.ResponsePayload.KEKVersion != 3 ||
		!bytes.Equal(result.ResponsePayload.Plaintext, body) {
		t.Fatalf("response payload = %+v", result.ResponsePayload)
	}

	mock.ExpectQuery("SELECT k.id,k.current_version FROM crypto_keyring_state").
		WillReturnRows(sqlmock.NewRows([]string{"id", "current_version"}).AddRow(7, 3))
	largeBody := bytes.Repeat([]byte("x"), maxCapturedExchangeBody+1)
	result.ResponsePayload = nil
	if err := dispatcher.attachResponsePayload(context.Background(), largeBody, &result); err != nil {
		t.Fatal(err)
	}
	if result.ResponsePayload == nil || !bytes.Equal(result.ResponsePayload.Plaintext, largeBody) {
		t.Fatal("response body within the exchange limit was not selected for persistence")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProviderCapabilityErrorIncludesBoundedProviderMessage(t *testing.T) {
	status := uint16(http.StatusBadRequest)
	exchange := repository.RequestLogResult{
		HTTPStatus:       &status,
		ResponseComplete: true,
	}
	failure := providerCapabilityError(exchange, []byte(`{"error":{"message":"invalid image size"}}`), "provider_http_error")
	if failure.HTTPStatus != http.StatusBadRequest || failure.Code != "provider_http_error" || failure.Message != "invalid image size" {
		t.Fatalf("failure = %+v", failure)
	}

	oversized := bytes.Repeat([]byte("x"), maxCapturedExchangeBody+1)
	failure = providerCapabilityError(exchange, oversized, "invalid_provider_response")
	if failure.Message != "" {
		t.Fatalf("oversized response message = %q", failure.Message)
	}
}

func TestCapabilityDispatcherCloseClearsEveryKeyCopy(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, _ := repository.New(db)
	service, _ := New(store)
	key := bytes.Repeat([]byte{7}, 32)
	dispatcher, err := NewCapabilityDispatcher(service, nil, AsyncKeys{
		CredentialKEK: key, CredentialHMAC: key, PayloadKEK: key, PayloadHMAC: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.Close()
	for _, candidate := range [][]byte{
		dispatcher.keys.CredentialKEK, dispatcher.keys.CredentialHMAC,
		dispatcher.keys.PayloadKEK, dispatcher.keys.PayloadHMAC,
		dispatcher.network.keys.CredentialKEK, dispatcher.network.keys.CredentialHMAC,
		dispatcher.network.keys.PayloadKEK, dispatcher.network.keys.PayloadHMAC,
	} {
		if !bytes.Equal(candidate, make([]byte, 32)) {
			t.Fatal("dispatcher retained key material")
		}
	}
}

func TestCapabilityRequestPayloadCaptureExcludesMultipartBytes(t *testing.T) {
	base64Body := []byte("--boundary\r\nContent-Disposition: form-data; name=\"image\"\r\n\r\naW1hZ2UtYnl0ZXM=\r\n--boundary--\r\n")
	if captureCapabilityRequestPayload("multipart/form-data; boundary=boundary", base64Body) {
		t.Fatal("multipart image bytes would be stored as a request-log payload")
	}
	if !captureCapabilityRequestPayload("application/json; charset=utf-8", []byte(`{"model":"image"}`)) {
		t.Fatal("bounded JSON request evidence was not selected")
	}
}
