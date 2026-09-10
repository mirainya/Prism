package migrate

import "testing"

func TestLegacyV1TransportCodeMatchesManagedRepair(t *testing.T) {
	tests := []struct {
		name                         string
		code, protocol               string
		sourceChannel, targetChannel int64
		want                         bool
	}{
		{name: "original V1", code: "legacy-transport-7", protocol: "openai", sourceChannel: 7, targetChannel: 19, want: true},
		{name: "OpenAI repair", code: "openai_chat-19", protocol: "openai", sourceChannel: 7, targetChannel: 19, want: true},
		{name: "Anthropic repair", code: "anthropic_messages-19", protocol: "anthropic", sourceChannel: 7, targetChannel: 19, want: true},
		{name: "Google repair", code: "google_generate_content-19", protocol: "google", sourceChannel: 7, targetChannel: 19, want: true},
		{name: "Volcengine repair", code: "volcengine_responses_v3-19", protocol: "volcengine", sourceChannel: 7, targetChannel: 19, want: true},
		{name: "default repair", code: "openai_chat-19", protocol: "custom", sourceChannel: 7, targetChannel: 19, want: true},
		{name: "wrong target", code: "openai_chat-7", protocol: "openai", sourceChannel: 7, targetChannel: 19, want: false},
		{name: "manual edit", code: "edited-19", protocol: "openai", sourceChannel: 7, targetChannel: 19, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := legacyV1TransportCodeMatches(test.code, test.protocol, test.sourceChannel, test.targetChannel); got != test.want {
				t.Fatalf("legacyV1TransportCodeMatches()=%t, want %t", got, test.want)
			}
		})
	}
}
