package service

import (
	"strings"
	"testing"
)

func TestCapabilityAttemptMetadataRemovesURLCredentials(t *testing.T) {
	requestPath := sanitizeCapabilityRequestPath("/v1/tasks/123?api_key=secret#fragment")
	if requestPath != "/v1/tasks/123" {
		t.Fatalf("request path = %q", requestPath)
	}
	upstreamURL := joinUpstreamURL("https://user:password@example.test/api?token=secret", requestPath)
	if strings.Contains(upstreamURL, "secret") || strings.Contains(upstreamURL, "password") ||
		strings.Contains(upstreamURL, "user@") || strings.Contains(upstreamURL, "?") {
		t.Fatalf("upstream URL retained credentials: %q", upstreamURL)
	}
}
