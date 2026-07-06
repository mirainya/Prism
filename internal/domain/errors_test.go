package domain

import (
	"fmt"
	"testing"
	"time"
)

func TestClassifyUpstreamError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantBreak  bool
		wantBackrf time.Duration
	}{
		{"401 structured", &UpstreamError{StatusCode: 401}, true, 6 * time.Hour},
		{"403 structured", &UpstreamError{StatusCode: 403}, true, 6 * time.Hour},
		{"404 structured", &UpstreamError{StatusCode: 404}, true, 6 * time.Hour},
		{"429 structured", &UpstreamError{StatusCode: 429}, true, 3 * time.Minute},
		{"400 not broken", &UpstreamError{StatusCode: 400}, false, 0},
		{"422 not broken", &UpstreamError{StatusCode: 422}, false, 0},
		{"500 not broken", &UpstreamError{StatusCode: 500}, false, 0},
		{"503 not broken", &UpstreamError{StatusCode: 503}, false, 0},
		{"string 401 fallback", fmt.Errorf("http error: 401, body: no access"), true, 6 * time.Hour},
		{"string 429 fallback", fmt.Errorf("http error: 429, body: rate limited"), true, 3 * time.Minute},
		{"string 5xx fallback", fmt.Errorf("http error: 503, body: bad gateway"), false, 0},
		{"wrapped structured", fmt.Errorf("submit: %w", &UpstreamError{StatusCode: 403}), true, 6 * time.Hour},
		{"unrelated error", fmt.Errorf("connection refused"), false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotBreak, gotBackoff := ClassifyUpstreamError(c.err)
			if gotBreak != c.wantBreak || gotBackoff != c.wantBackrf {
				t.Errorf("ClassifyUpstreamError(%v) = (%v, %v), want (%v, %v)",
					c.err, gotBreak, gotBackoff, c.wantBreak, c.wantBackrf)
			}
		})
	}
}

func TestUpstreamStatusCode(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{&UpstreamError{StatusCode: 401}, 401},
		{fmt.Errorf("http error: 404, body: x"), 404},
		{fmt.Errorf("upstream returned status 503"), 503},
		{fmt.Errorf("submit: %w", &UpstreamError{StatusCode: 429}), 429},
		{fmt.Errorf("no code here"), 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := UpstreamStatusCode(c.err); got != c.want {
			t.Errorf("UpstreamStatusCode(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}
