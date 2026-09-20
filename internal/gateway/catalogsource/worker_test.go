package catalogsource

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestCatalogResponseLogResultClassifiesHTTPAndBodyFailures(t *testing.T) {
	key := make([]byte, security.KeySize)
	tests := []struct {
		name      string
		status    uint16
		body      []byte
		readErr   error
		code      string
		complete  bool
		hasDigest bool
	}{
		{name: "success", status: 200, body: []byte(`{"ok":true}`), complete: true, hasDigest: true},
		{name: "provider status", status: 429, body: []byte(`{"error":"limited"}`), code: "provider_http_error", complete: true, hasDigest: true},
		{name: "read failure", status: 200, body: []byte("partial"), readErr: errors.New("read failed"), code: "response_read_failed"},
		{name: "oversize", status: 200, body: make([]byte, maxDiscoveryResponse+1), code: "response_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := catalogResponseLogResult(test.status, 7, test.body, test.readErr, key)
			if result.ErrorCode != test.code || result.ResponseComplete != test.complete || (result.ResponseBytesHMAC != "") != test.hasDigest || result.HTTPStatus == nil || *result.HTTPStatus != test.status || result.DurationMS == nil || *result.DurationMS != 7 || !result.RequestComplete {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestDiscoveryFailureCodeIsStable(t *testing.T) {
	if got := discoveryFailureCode(ErrProviderResponse); got != "invalid_provider_response" {
		t.Fatalf("provider response code=%q", got)
	}
	if got := discoveryFailureCode(ErrProviderRequest); got != "provider_request_failed" {
		t.Fatalf("provider request code=%q", got)
	}
	if got := discoveryFailureCode(ErrProviderRequestLimit); got != "provider_request_limit" {
		t.Fatalf("provider request limit code=%q", got)
	}
}

func TestLimitProviderExchangeRejectsExcessRequests(t *testing.T) {
	requests := 0
	exchange := limitProviderExchange(2, func(context.Context, string, string, []byte, http.Header) ([]byte, error) {
		requests++
		return []byte(`{"ok":true}`), nil
	})
	for index := 0; index < 2; index++ {
		if _, err := exchange(context.Background(), http.MethodGet, "https://example.test", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := exchange(context.Background(), http.MethodGet, "https://example.test", nil, nil); err != ErrProviderRequestLimit {
		t.Fatalf("limit error=%v", err)
	}
	if requests != 2 {
		t.Fatalf("requests=%d", requests)
	}
}
