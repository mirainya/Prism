package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type testAsyncCodec struct{}

func (testAsyncCodec) Prepare(context.Context, string, repository.AsyncDispatch, []byte, string) (AsyncRequest, error) {
	return AsyncRequest{}, nil
}
func (testAsyncCodec) Decode(string, []byte, []byte) (AsyncObservation, error) {
	return AsyncObservation{}, nil
}

type testFailureAwareAsyncCodec struct{ testAsyncCodec }

func (testFailureAwareAsyncCodec) DecodeFailureWithDispatch(repository.AsyncDispatch, []byte, uint16) (AsyncObservation, error) {
	status := uint16(http.StatusUnprocessableEntity)
	return AsyncObservation{
		State:                execution.AsyncFailed,
		ProviderErrorCode:    "bad_image",
		ProviderErrorMessage: "mapped failure",
		ProviderHTTPStatus:   &status,
	}, nil
}

func testDispatcher(t *testing.T, client *http.Client) (*AsyncDispatcher, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store, _ := repository.New(db)
	service, _ := New(store)
	key := bytes.Repeat([]byte{13}, 32)
	dispatcher, err := NewAsyncDispatcher(service, client, AsyncKeys{key, key, key, key}, map[string]AsyncCodec{"test@1": testAsyncCodec{}})
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher, mock
}

func TestAsyncDispatcherDoesNotFollowRedirectsOrExposeResponseBodies(t *testing.T) {
	var redirected atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		fmt.Fprint(w, "provider echoed credential: isolated-secret")
	}))
	defer source.Close()
	dispatcher, _ := testDispatcher(t, source.Client())
	request, _ := http.NewRequest(http.MethodPost, source.URL, strings.NewReader(`{"prompt":"isolated-prompt"}`))
	request.Header.Set("Authorization", "Bearer isolated-secret")
	body, response := dispatcher.exchange(request)
	defer clear(body)
	if redirected.Load() != 0 || response.HTTPStatus == nil || *response.HTTPStatus != 307 || response.ErrorCode != "provider_http_error" || !response.ResponseComplete {
		t.Fatalf("redirect was followed or incorrectly recorded: %+v", response)
	}
	encoded, _ := json.Marshal(response)
	for _, sensitive := range []string{"isolated-secret", "isolated-prompt", target.URL} {
		if strings.Contains(string(encoded), sensitive) {
			t.Fatal("request metadata contains sensitive input")
		}
	}
}

func TestAsyncDispatcherBoundsProviderResponse(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, (4<<20)+2))
	}))
	defer source.Close()
	dispatcher, _ := testDispatcher(t, source.Client())
	request, _ := http.NewRequest(http.MethodGet, source.URL, nil)
	body, response := dispatcher.exchange(request)
	defer clear(body)
	if len(body) != 4<<20 || response.ResponseComplete || response.ErrorCode != "provider_response_incomplete" {
		t.Fatalf("oversized response was not bounded: length=%d response=%+v", len(body), response)
	}
}

func TestDecodeMappedDispatchFailureUsesFailureAwareCodec(t *testing.T) {
	observed, ok := decodeMappedDispatchFailure(testFailureAwareAsyncCodec{}, repository.AsyncDispatch{}, []byte(`{"failure":true}`), http.StatusBadRequest)
	if !ok || observed.State != execution.AsyncFailed || observed.ProviderErrorMessage != "mapped failure" ||
		observed.ProviderHTTPStatus == nil || *observed.ProviderHTTPStatus != http.StatusUnprocessableEntity {
		t.Fatalf("mapped failure = %+v, ok=%t", observed, ok)
	}
	if _, ok := decodeMappedDispatchFailure(testAsyncCodec{}, repository.AsyncDispatch{}, nil, http.StatusBadRequest); ok {
		t.Fatal("codec without failure mapping was accepted")
	}
}

func TestAsyncResultCommitContextUsesRemainingLease(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	leaseExpiresAt := time.Now().Add(2 * time.Minute)
	ctx, cancel := asyncResultCommitContext(parent, leaseExpiresAt)
	t.Cleanup(cancel)

	cancelParent()
	select {
	case <-ctx.Done():
		t.Fatalf("commit context inherited parent cancellation: %v", ctx.Err())
	default:
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("commit context has no lease deadline")
	}
	want := leaseExpiresAt.Add(-asyncResultCommitMargin)
	if delta := deadline.Sub(want); delta < -time.Millisecond || delta > time.Millisecond {
		t.Fatalf("commit deadline = %v, want %v", deadline, want)
	}
	if remaining := time.Until(deadline); remaining < time.Minute {
		t.Fatalf("commit context retained only %v, want the remaining lease", remaining)
	}
}

func TestAsyncExchangeTimeoutLeavesCommitWindow(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(2 * time.Minute)
	timeout := asyncExchangeTimeout(5*time.Minute, leaseExpiresAt, now)
	if want := 2*time.Minute - asyncExchangeCompletionMargin; timeout != want {
		t.Fatalf("exchange timeout = %v, want %v", timeout, want)
	}
	commitDeadline := leaseExpiresAt.Add(-asyncResultCommitMargin)
	exchangeDeadline := now.Add(timeout)
	if gap := commitDeadline.Sub(exchangeDeadline); gap <= 0 {
		t.Fatalf("exchange deadline does not precede commit deadline: gap=%v", gap)
	}
}

func TestDefinitiveSubmissionRejectionRequiresCompleteNon2xxExchange(t *testing.T) {
	rejected := uint16(http.StatusBadRequest)
	accepted := uint16(http.StatusAccepted)
	for _, test := range []struct {
		name     string
		response repository.RequestLogResult
		want     bool
	}{
		{name: "complete rejection", response: repository.RequestLogResult{HTTPStatus: &rejected, RequestComplete: true, ResponseComplete: true}, want: true},
		{name: "successful response", response: repository.RequestLogResult{HTTPStatus: &accepted, RequestComplete: true, ResponseComplete: true}},
		{name: "incomplete body", response: repository.RequestLogResult{HTTPStatus: &rejected, RequestComplete: true}},
		{name: "network outcome unknown", response: repository.RequestLogResult{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := definitiveSubmissionRejection(test.response); got != test.want {
				t.Fatalf("definitiveSubmissionRejection() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestDefinitiveQueryFailureRequiresCompleteDeterministicResponse(t *testing.T) {
	for _, test := range []struct {
		name             string
		status           *uint16
		requestComplete  bool
		responseComplete bool
		errorCode        string
		want             bool
	}{
		{name: "bad request", status: uint16Pointer(http.StatusBadRequest), requestComplete: true, responseComplete: true, want: true},
		{name: "unauthorized", status: uint16Pointer(http.StatusUnauthorized), requestComplete: true, responseComplete: true, want: true},
		{name: "forbidden", status: uint16Pointer(http.StatusForbidden), requestComplete: true, responseComplete: true, want: true},
		{name: "not found", status: uint16Pointer(http.StatusNotFound), requestComplete: true, responseComplete: true, want: true},
		{name: "conflict", status: uint16Pointer(http.StatusConflict), requestComplete: true, responseComplete: true, want: true},
		{name: "unprocessable entity", status: uint16Pointer(http.StatusUnprocessableEntity), requestComplete: true, responseComplete: true, want: true},
		{name: "request timeout", status: uint16Pointer(http.StatusRequestTimeout), requestComplete: true, responseComplete: true},
		{name: "too early", status: uint16Pointer(http.StatusTooEarly), requestComplete: true, responseComplete: true},
		{name: "too many requests", status: uint16Pointer(http.StatusTooManyRequests), requestComplete: true, responseComplete: true},
		{name: "server error", status: uint16Pointer(http.StatusInternalServerError), requestComplete: true, responseComplete: true},
		{name: "request incomplete", status: uint16Pointer(http.StatusBadRequest), responseComplete: true},
		{name: "response incomplete", status: uint16Pointer(http.StatusBadRequest), requestComplete: true},
		{name: "missing status", requestComplete: true, responseComplete: true},
		{name: "invalid complete success response", status: uint16Pointer(http.StatusOK), requestComplete: true, responseComplete: true, errorCode: "invalid_provider_response", want: true},
		{name: "valid complete success response", status: uint16Pointer(http.StatusOK), requestComplete: true, responseComplete: true},
		{name: "invalid incomplete success response", status: uint16Pointer(http.StatusOK), requestComplete: true, errorCode: "invalid_provider_response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := repository.RequestLogResult{
				HTTPStatus:       test.status,
				RequestComplete:  test.requestComplete,
				ResponseComplete: test.responseComplete,
				ErrorCode:        test.errorCode,
			}
			if got := definitiveQueryFailure(response); got != test.want {
				t.Fatalf("definitiveQueryFailure() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestExchangeFailureReturnsPermanentErrorForDeterministicQueryFailure(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		errorCode string
	}{
		{name: "provider rejection", status: http.StatusBadRequest, errorCode: "provider_http_error"},
		{name: "invalid success payload", status: http.StatusOK, errorCode: "invalid_provider_response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dispatcher, mock := testDispatcher(t, nil)
			requestID := uint64(17)
			status := uint16(test.status)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT status FROM gw_channel_request_logs").
				WithArgs(requestID).
				WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("response_recorded"))
			mock.ExpectQuery("SELECT id,state FROM gw_credential_slots").
				WithArgs(requestID).
				WillReturnRows(sqlmock.NewRows([]string{"id", "state"}).AddRow(19, "released"))
			mock.ExpectCommit()

			err := dispatcher.exchangeFailure(context.Background(), repository.OutboxItem{Action: "query"}, requestID, repository.RequestLogResult{
				HTTPStatus:       &status,
				RequestComplete:  true,
				ResponseComplete: true,
				ErrorCode:        test.errorCode,
			})
			var permanent *PermanentDispatchError
			if !errors.As(err, &permanent) || permanent.Code != test.errorCode {
				t.Fatalf("exchangeFailure error = %v, want permanent %s", err, test.errorCode)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAsyncProviderFailureCodeUsesSafeProviderValueAndFallbacks(t *testing.T) {
	status := uint16(http.StatusUnprocessableEntity)
	for _, test := range []struct {
		name   string
		code   string
		status *uint16
		want   string
	}{
		{name: "provider code remains evidence only", code: "bad_image", want: "provider_task_failed"},
		{name: "provider rejection", code: "bad_image", status: &status, want: "provider_task_rejected"},
		{name: "generic failure", want: "provider_task_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := asyncProviderFailureCode(test.code, test.status); got != test.want {
				t.Fatalf("asyncProviderFailureCode() = %q, want %q", got, test.want)
			}
		})
	}
}

func uint16Pointer(value int) *uint16 {
	converted := uint16(value)
	return &converted
}

func TestAsyncDispatcherDefaultClientRejectsPrivateDestinations(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	dispatcher, _ := testDispatcher(t, nil)
	request, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, response := dispatcher.exchange(request)
	if requests.Load() != 0 {
		t.Fatal("default async client connected to a private destination")
	}
	if response.ErrorCode != "provider_exchange_unknown" {
		t.Fatalf("response error code = %q, want provider_exchange_unknown", response.ErrorCode)
	}
}

func TestAsyncDispatcherRejectsForeignOriginAndUnlistedHostBeforeSending(t *testing.T) {
	dispatcher, mock := testDispatcher(t, nil)
	fixed := repository.AsyncDispatch{BaseURL: "https://provider.invalid", ChannelTransportID: 2, ReleaseID: 1, AuthScheme: "bearer"}
	for _, path := range []string{"//other.invalid/tasks", "https://other.invalid/tasks", "/tasks#fragment"} {
		if _, err := dispatcher.prepareHTTP(context.Background(), fixed, AsyncRequest{Method: "POST", Path: path}, []byte("test")); err == nil {
			t.Errorf("invalid provider path accepted: %s", path)
		}
	}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gw_transport_allowed_hosts").
		WithArgs(uint64(1), uint64(2), "https", "provider.invalid", "443").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	if _, err := dispatcher.prepareHTTP(context.Background(), fixed, AsyncRequest{Method: "POST", Path: "/tasks"}, []byte("test")); err == nil {
		t.Fatal("unlisted host accepted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncDispatcherInjectsCatalogPinnedGenericCredentialHeader(t *testing.T) {
	dispatcher, mock := testDispatcher(t, nil)
	fixed := repository.AsyncDispatch{
		BaseURL: "https://provider.invalid", ChannelTransportID: 2, ReleaseID: 1,
		AuthScheme: "bearer",
	}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gw_transport_allowed_hosts").
		WithArgs(uint64(1), uint64(2), "https", "provider.invalid", "443").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	request, err := dispatcher.prepareHTTP(context.Background(), fixed, AsyncRequest{
		Method: "POST", Path: "/tasks", CredentialHeader: "X-API-Key", CredentialPrefix: "Token ",
	}, []byte("isolated-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("X-API-Key") != "Token isolated-secret" || request.Header.Get("Authorization") != "" {
		t.Fatalf("credential headers = %#v", request.Header)
	}

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gw_transport_allowed_hosts").
		WithArgs(uint64(1), uint64(2), "https", "provider.invalid", "443").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	_, err = dispatcher.prepareHTTP(context.Background(), fixed, AsyncRequest{
		Method: "POST", Path: "/tasks", Header: http.Header{"X-Api-Key": []string{"untrusted"}},
		CredentialHeader: "X-API-Key",
	}, []byte("isolated-secret"))
	var permanent *PermanentDispatchError
	if !errors.As(err, &permanent) || permanent.Code != "invalid_async_auth_mapping" {
		t.Fatalf("preset credential header error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncDispatcherRequiresEveryResultHostInTransportAllowlist(t *testing.T) {
	dispatcher, mock := testDispatcher(t, nil)
	fixed := repository.AsyncDispatch{CallID: 5, ReleaseID: 7, ChannelTransportID: 9}
	sources := []delivery.RemoteResult{
		{Role: "video", URL: "https://media.provider.example/result.mp4"},
		{Role: "thumbnail", URL: "https://thumb.provider.example:8443/result.jpg"},
	}
	mock.ExpectQuery("SELECT resource_kind FROM gw_api_resources WHERE call_id=\\?").
		WithArgs(uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"resource_kind"}).AddRow(delivery.ResourceVideoTask))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gw_transport_allowed_hosts").
		WithArgs(uint64(7), uint64(9), "https", "media.provider.example", "443").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM gw_transport_allowed_hosts").
		WithArgs(uint64(7), uint64(9), "https", "thumb.provider.example", "8443").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	err := dispatcher.validateResultSources(context.Background(), fixed, sources)
	var permanent *PermanentDispatchError
	if !errors.As(err, &permanent) || permanent.Code != "provider_result_host_not_allowed" {
		t.Fatalf("validateResultSources error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
