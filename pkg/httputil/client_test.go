package httputil

import (
	"errors"
	"strings"
	"testing"
)

func TestHTTPErrorIsStructuredAndBounded(t *testing.T) {
	err := newHTTPError(429, []byte(`{"error":{"message":"rate limited","type":"rate_limit_error","code":"rate_limit_exceeded","param":"model"}}`))
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPStatus() != 429 {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("message missing: %v", err)
	}
	if httpErr.Type != "rate_limit_error" || httpErr.Code != "rate_limit_exceeded" || httpErr.Param == nil || *httpErr.Param != "model" {
		t.Fatalf("error detail missing: %#v", httpErr)
	}
	long := newHTTPError(500, []byte(`{"message":"`+strings.Repeat("x", 1000)+`"}`))
	if len([]rune(long.Error())) > 550 {
		t.Fatalf("error was not bounded: %d", len([]rune(long.Error())))
	}
}
