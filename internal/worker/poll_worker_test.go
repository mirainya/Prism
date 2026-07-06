package worker

import (
	"fmt"
	"testing"

	"github.com/mirainya/Prism/internal/domain"
)

func TestIsRetryablePollError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"network error no status", fmt.Errorf("connection refused"), true},
		{"read timeout no status", fmt.Errorf("do request: context deadline exceeded"), true},
		{"408 request timeout", &domain.UpstreamError{StatusCode: 408}, true},
		{"429 rate limited", &domain.UpstreamError{StatusCode: 429}, true},
		{"500 upstream error", &domain.UpstreamError{StatusCode: 500}, true},
		{"503 unavailable", &domain.UpstreamError{StatusCode: 503}, true},
		{"400 bad request", &domain.UpstreamError{StatusCode: 400}, false},
		{"401 unauthorized", &domain.UpstreamError{StatusCode: 401}, false},
		{"403 forbidden", &domain.UpstreamError{StatusCode: 403}, false},
		{"404 not found", &domain.UpstreamError{StatusCode: 404}, false},
		{"422 unprocessable", &domain.UpstreamError{StatusCode: 422}, false},
		{"wrapped 500 retryable", fmt.Errorf("poll: %w", &domain.UpstreamError{StatusCode: 500}), true},
		{"wrapped 404 fatal", fmt.Errorf("poll: %w", &domain.UpstreamError{StatusCode: 404}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRetryablePollError(c.err); got != c.want {
				t.Errorf("isRetryablePollError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
