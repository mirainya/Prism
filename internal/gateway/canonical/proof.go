package canonical

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const responsesProviderProofPrefix = "prism-provider-proof-v1#"

type responsesProviderProofEnvelope struct {
	Version  int           `json:"v"`
	Provider ProofProvider `json:"provider"`
	Value    string        `json:"value"`
	Subject  ProofSubject  `json:"subject,omitempty"`
	TargetID string        `json:"target_id,omitempty"`
}

// ProofProvider identifies the provider that issued an opaque continuation proof.
type ProofProvider string

const (
	ProofProviderOpenAI     ProofProvider = "openai"
	ProofProviderAnthropic  ProofProvider = "anthropic"
	ProofProviderGoogle     ProofProvider = "google"
	ProofProviderVolcengine ProofProvider = "volcengine"
)

// ProofSubject identifies the provider-native object covered by a proof when
// the canonical item itself is only a transport carrier.
type ProofSubject string

const (
	ProofSubjectGooglePart        ProofSubject = "google_part"
	ProofSubjectAnthropicRedacted ProofSubject = "anthropic_redacted"
)

// ProviderProof carries opaque provider-native continuation state without interpreting it.
type ProviderProof struct {
	Provider ProofProvider `json:"provider"`
	Value    string        `json:"value"`
	Subject  ProofSubject  `json:"subject,omitempty"`
	TargetID string        `json:"target_id,omitempty"`
}

// ParseTaggedProviderProof accepts only Prism's versioned proof envelope.
// Other encrypted_content values remain protocol-native data.
func ParseTaggedProviderProof(value string) (*ProviderProof, bool) {
	if !strings.HasPrefix(value, responsesProviderProofPrefix) {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, responsesProviderProofPrefix))
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return nil, false
	}
	for _, field := range []string{"v", "provider", "value"} {
		if _, ok := fields[field]; !ok {
			return nil, false
		}
	}
	var envelope responsesProviderProofEnvelope
	if json.Unmarshal(payload, &envelope) != nil || envelope.Value == "" || !foreignProofProvider(envelope.Provider) {
		return nil, false
	}
	switch envelope.Version {
	case 1:
		if len(fields) != 3 || envelope.Subject != "" || envelope.TargetID != "" {
			return nil, false
		}
	case 2:
		validGooglePart := len(fields) == 5 && envelope.Provider == ProofProviderGoogle && envelope.Subject == ProofSubjectGooglePart && strings.TrimSpace(envelope.TargetID) != ""
		validAnthropicRedacted := len(fields) == 4 && envelope.Provider == ProofProviderAnthropic && envelope.Subject == ProofSubjectAnthropicRedacted && envelope.TargetID == ""
		if !validGooglePart && !validAnthropicRedacted {
			return nil, false
		}
	default:
		return nil, false
	}
	return &ProviderProof{Provider: envelope.Provider, Value: envelope.Value, Subject: envelope.Subject, TargetID: envelope.TargetID}, true
}

// EncodeResponsesEncryptedContent renders a proof for encrypted_content.
// OpenAI values remain unchanged; foreign proofs use a versioned Prism envelope.
func EncodeResponsesEncryptedContent(proof *ProviderProof) string {
	if proof == nil || proof.Value == "" {
		return ""
	}
	switch proof.Provider {
	case ProofProviderOpenAI:
		if proof.Subject != "" || proof.TargetID != "" {
			return ""
		}
		return proof.Value
	case ProofProviderAnthropic, ProofProviderGoogle, ProofProviderVolcengine:
		envelope := responsesProviderProofEnvelope{Version: 1, Provider: proof.Provider, Value: proof.Value}
		if proof.Subject != "" || proof.TargetID != "" {
			validGooglePart := proof.Provider == ProofProviderGoogle && proof.Subject == ProofSubjectGooglePart && strings.TrimSpace(proof.TargetID) != ""
			validAnthropicRedacted := proof.Provider == ProofProviderAnthropic && proof.Subject == ProofSubjectAnthropicRedacted && proof.TargetID == ""
			if !validGooglePart && !validAnthropicRedacted {
				return ""
			}
			envelope.Version, envelope.Subject, envelope.TargetID = 2, proof.Subject, proof.TargetID
		}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			return ""
		}
		return responsesProviderProofPrefix + base64.RawURLEncoding.EncodeToString(encoded)
	default:
		return ""
	}
}

// NativeProviderProofValue returns a proof only to its issuing provider.
func NativeProviderProofValue(proof *ProviderProof, provider ProofProvider) (string, bool) {
	if proof == nil || proof.Value == "" || proof.Provider != provider {
		return "", false
	}
	return proof.Value, true
}

func foreignProofProvider(provider ProofProvider) bool {
	return provider == ProofProviderAnthropic || provider == ProofProviderGoogle || provider == ProofProviderVolcengine
}
