package canonical

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeResponsesEncryptedContentUsesStablePrefixes(t *testing.T) {
	openAI := &ProviderProof{Provider: ProofProviderOpenAI, Value: "anthropic#opaque-native"}
	if got := EncodeResponsesEncryptedContent(openAI); got != openAI.Value {
		t.Fatalf("OpenAI proof changed: %q", got)
	}
	for _, proof := range []ProviderProof{
		{Provider: ProofProviderAnthropic, Value: "claude-proof#opaque"},
		{Provider: ProofProviderGoogle, Value: "gemini-proof"},
		{Provider: ProofProviderVolcengine, Value: "ark-proof"},
	} {
		encoded := EncodeResponsesEncryptedContent(&proof)
		decoded, ok := ParseTaggedProviderProof(encoded)
		if !ok || decoded.Provider != proof.Provider || decoded.Value != proof.Value || !strings.HasPrefix(encoded, responsesProviderProofPrefix) {
			t.Fatalf("proof round trip: encoded=%q decoded=%#v ok=%v", encoded, decoded, ok)
		}
	}
	googleText := &ProviderProof{Provider: ProofProviderGoogle, Value: "text-proof", Subject: ProofSubjectGooglePart, TargetID: "msg_1"}
	encodedText := EncodeResponsesEncryptedContent(googleText)
	decodedText, ok := ParseTaggedProviderProof(encodedText)
	if !ok || decodedText.Provider != googleText.Provider || decodedText.Value != googleText.Value || decodedText.Subject != googleText.Subject || decodedText.TargetID != googleText.TargetID {
		t.Fatalf("Google text proof round trip: encoded=%q decoded=%#v ok=%v", encodedText, decodedText, ok)
	}
	redacted := &ProviderProof{Provider: ProofProviderAnthropic, Value: "redacted-data", Subject: ProofSubjectAnthropicRedacted}
	encodedRedacted := EncodeResponsesEncryptedContent(redacted)
	decodedRedacted, ok := ParseTaggedProviderProof(encodedRedacted)
	if !ok || decodedRedacted.Provider != redacted.Provider || decodedRedacted.Value != redacted.Value || decodedRedacted.Subject != redacted.Subject || decodedRedacted.TargetID != "" {
		t.Fatalf("Anthropic redacted proof round trip: encoded=%q decoded=%#v ok=%v", encodedRedacted, decodedRedacted, ok)
	}
	if got := EncodeResponsesEncryptedContent(nil); got != "" {
		t.Fatalf("nil proof encoded as %q", got)
	}
}

func TestParseTaggedProviderProofRequiresKnownPrefix(t *testing.T) {
	encoded := EncodeResponsesEncryptedContent(&ProviderProof{Provider: ProofProviderAnthropic, Value: "proof"})
	proof, ok := ParseTaggedProviderProof(encoded)
	if !ok || proof.Provider != ProofProviderAnthropic || proof.Value != "proof" {
		t.Fatalf("tagged proof = %#v, %v", proof, ok)
	}
	for _, value := range []string{"opaque", "future#opaque", "anthropic#proof", "google#proof", responsesProviderProofPrefix + "invalid"} {
		if proof, ok := ParseTaggedProviderProof(value); ok || proof != nil {
			t.Fatalf("ParseTaggedProviderProof(%q) = %#v, %v", value, proof, ok)
		}
	}
	for _, invalid := range []*ProviderProof{
		{Provider: ProofProviderGoogle, Value: "proof", Subject: ProofSubjectGooglePart},
		{Provider: ProofProviderAnthropic, Value: "proof", Subject: ProofSubjectGooglePart, TargetID: "msg_1"},
	} {
		if encoded := EncodeResponsesEncryptedContent(invalid); encoded != "" {
			t.Fatalf("invalid proof encoded as %q: %#v", encoded, invalid)
		}
	}
}

func TestNativeProviderProofValueRequiresSameProvider(t *testing.T) {
	proof := &ProviderProof{Provider: ProofProviderAnthropic, Value: "native"}
	if value, ok := NativeProviderProofValue(proof, ProofProviderAnthropic); !ok || value != "native" {
		t.Fatalf("matching provider value = %q, %v", value, ok)
	}
	if value, ok := NativeProviderProofValue(proof, ProofProviderOpenAI); ok || value != "" {
		t.Fatalf("foreign provider value = %q, %v", value, ok)
	}
}

func TestCloneItemsClonesProviderProof(t *testing.T) {
	items := []Item{{Type: "reasoning", Proof: &ProviderProof{Provider: ProofProviderGoogle, Value: "proof"}}}
	clone := CloneItems(items)
	clone[0].Proof.Value = "changed"
	if items[0].Proof.Value != "proof" {
		t.Fatalf("CloneItems shared provider proof: %#v", items[0].Proof)
	}
}

func TestProviderProofJSONRoundTrip(t *testing.T) {
	item := Item{Type: "reasoning", Proof: &ProviderProof{Provider: ProofProviderVolcengine, Value: "opaque"}}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Item
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Proof == nil || decoded.Proof.Provider != ProofProviderVolcengine || decoded.Proof.Value != "opaque" {
		t.Fatalf("provider proof round trip = %#v", decoded.Proof)
	}
}

func TestRequestClonePreservesNamespaces(t *testing.T) {
	request := Request{
		Items: []Item{{Type: "function_call", Name: "run", Namespace: "shell"}},
		Tools: []Tool{{Type: "function", Name: "run", Namespace: "shell"}},
	}
	clone := request.Clone()
	if clone.Items[0].Namespace != "shell" || clone.Tools[0].Namespace != "shell" {
		t.Fatalf("request clone lost namespaces: %#v", clone)
	}
}
