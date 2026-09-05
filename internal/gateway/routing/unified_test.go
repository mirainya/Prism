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

func TestUnifiedAttemptExclusionUsesCredentialAndTransport(t *testing.T) {
	attempts := []TransportAttempt{{KeyID: 7, Transport: model.UpstreamTransportAnthropic}}
	if !excludedUnifiedAttempt(attempts, 7, model.UpstreamTransportAnthropic) {
		t.Fatal("matching unified attempt was not excluded")
	}
	if excludedUnifiedAttempt(attempts, 8, model.UpstreamTransportAnthropic) {
		t.Fatal("different credential was excluded")
	}
	if excludedUnifiedAttempt(attempts, 7, model.UpstreamTransportOpenAIChat) {
		t.Fatal("different transport was excluded")
	}
}

func TestWeightedUnifiedCandidateAcceptsNonPositiveWeights(t *testing.T) {
	candidates := []unifiedCandidate{{AbilityID: 1, Weight: 0}, {AbilityID: 2, Weight: -1}}
	for i := 0; i < 20; i++ {
		chosen := weightedUnifiedCandidate(candidates)
		if chosen.AbilityID != 1 && chosen.AbilityID != 2 {
			t.Fatalf("unexpected candidate %d", chosen.AbilityID)
		}
	}
}

func TestUnifiedPriceModeDistinguishesRequestUnits(t *testing.T) {
	for _, unit := range []string{"request", "second", "image", "video", "megapixel"} {
		if got := unifiedPriceMode(unit); got != "request" {
			t.Fatalf("unit %q mode=%q, want request", unit, got)
		}
	}
	if got := unifiedPriceMode("input_token"); got != "token" {
		t.Fatalf("token unit mode=%q, want token", got)
	}
}
