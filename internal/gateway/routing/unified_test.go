package routing

import (
	"testing"

	"github.com/mirainya/Prism/internal/model"
)

func TestUnifiedTransportUsesDeclaredProtocol(t *testing.T) {
	tests := map[string]model.UpstreamTransport{
		"openai":           model.UpstreamTransportOpenAIChat,
		"anthropic":        model.UpstreamTransportAnthropic,
		"google":           model.UpstreamTransportGoogle,
		"volcengine":       model.UpstreamTransportVolcengineV3,
		"openai_responses": model.UpstreamTransportOpenAIResponses,
		"openai_images":    model.UpstreamTransportOpenAIImages,
		"seedance":         model.UpstreamTransportVideoGeneration,
		"video_generation": model.UpstreamTransportVideoGeneration,
		"unsupported":      "",
	}
	for protocol, want := range tests {
		if got := unifiedTransport(protocol); got != want {
			t.Fatalf("protocol %q mapped to %q, want %q", protocol, got, want)
		}
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
	marked := []TransportAttempt{{KeyID: unifiedKeyMarker | 7, Transport: model.UpstreamTransportAnthropic}}
	if !excludedUnifiedAttempt(marked, 7, model.UpstreamTransportAnthropic) {
		t.Fatal("an engine attempt's marked key must exclude the underlying credential")
	}
}

func TestDeterministicUnifiedCandidateIsStable(t *testing.T) {
	candidates := []unifiedCandidate{
		{RouteID: 1, OfferingID: 10, CredentialID: 100, RouteWeight: 1, CredentialWeight: 1},
		{RouteID: 2, OfferingID: 20, CredentialID: 200, RouteWeight: 2, CredentialWeight: 3},
	}
	first, err := deterministicUnifiedCandidate("018f0d55-76de-7a4b-b3ad-01a41a6cdb20", candidates)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		selected, selectErr := deterministicUnifiedCandidate("018f0d55-76de-7a4b-b3ad-01a41a6cdb20", candidates)
		if selectErr != nil || selected.RouteID != first.RouteID {
			t.Fatalf("selection changed: route=%d err=%v", selected.RouteID, selectErr)
		}
	}
}

func TestWeightedRendezvousGoldenVector(t *testing.T) {
	score, err := weightedRendezvousScore("018f0d55-76de-7a4b-b3ad-01a41a6cdb20", 7, 11, 13, 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "5844dd470047100b85a03287953b66370e82b02bd6975c51a5f9a0b8979a717"
	if score.Text(16) != expected {
		t.Fatalf("score = %s, want %s", score.Text(16), expected)
	}
}
