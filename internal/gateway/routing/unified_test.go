package routing

import (
	"testing"

	"github.com/mirainya/Prism/internal/model"
)

func TestUnifiedTransportUsesDeclaredProtocol(t *testing.T) {
	tests := map[string]model.UpstreamTransport{
		"openai":     model.UpstreamTransportOpenAIChat,
		"anthropic":  model.UpstreamTransportAnthropic,
		"google":     model.UpstreamTransportGoogle,
		"volcengine": model.UpstreamTransportVolcengineV3,
	}
	for protocol, want := range tests {
		if got := unifiedTransport(protocol); got != want {
			t.Fatalf("protocol %q mapped to %q, want %q", protocol, got, want)
		}
	}
}

func TestUnifiedKeyMarkerDoesNotReleaseLegacyConcurrency(t *testing.T) {
	router := NewRouter()
	if !router.releaseUnified(unifiedKeyMarker | 42) {
		t.Fatal("unified key marker was not recognized")
	}
	if router.releaseUnified(42) {
		t.Fatal("legacy key was incorrectly marked as unified")
	}
}
