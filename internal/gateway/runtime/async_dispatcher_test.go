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

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type testAsyncCodec struct{}

func (testAsyncCodec) Prepare(context.Context, string, repository.AsyncDispatch, []byte, string) (AsyncRequest, error) {
	return AsyncRequest{}, nil
}
func (testAsyncCodec) Decode(string, []byte, []byte) (AsyncObservation, error) {
	return AsyncObservation{}, nil
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
