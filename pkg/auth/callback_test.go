package auth

import (
	"strings"
	"testing"
)

func TestCallbackSignatureIsStableAndRejectsTampering(t *testing.T) {
	const (
		secret    = "channel-secret"
		channelID = uint(42)
		taskNo    = "task_123"
	)

	signature := SignCallback(secret, channelID, taskNo)
	if signature != SignCallback(secret, channelID, taskNo) {
		t.Fatal("SignCallback returned different signatures for the same input")
	}
	if !VerifyCallbackSignature(secret, channelID, taskNo, signature) {
		t.Fatal("VerifyCallbackSignature rejected a valid signature")
	}

	tests := []struct {
		name      string
		secret    string
		channelID uint
		taskNo    string
		signature string
	}{
		{
			name:      "task changed",
			secret:    secret,
			channelID: channelID,
			taskNo:    "task_456",
			signature: signature,
		},
		{
			name:      "channel changed",
			secret:    secret,
			channelID: channelID + 1,
			taskNo:    taskNo,
			signature: signature,
		},
		{
			name:      "secret changed",
			secret:    "different-secret",
			channelID: channelID,
			taskNo:    taskNo,
			signature: signature,
		},
		{
			name:      "signature changed",
			secret:    secret,
			channelID: channelID,
			taskNo:    taskNo,
			signature: flipFirstHexCharacter(signature),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if VerifyCallbackSignature(tt.secret, tt.channelID, tt.taskNo, tt.signature) {
				t.Fatal("VerifyCallbackSignature accepted tampered callback data")
			}
		})
	}
}

func TestBuildSignedCallbackURL(t *testing.T) {
	const (
		publicURL   = "https://prism.example///"
		channelType = "doubao"
		channelID   = uint(7)
		taskNo      = "task_123"
		secret      = "channel-secret"
	)

	signature := SignCallback(secret, channelID, taskNo)
	want := "https://prism.example/internal/callback/v1/doubao/task_123/" + signature
	got := BuildSignedCallbackURL(publicURL, channelType, channelID, taskNo, secret)
	if got != want {
		t.Fatalf("BuildSignedCallbackURL() = %q, want %q", got, want)
	}
}

func flipFirstHexCharacter(signature string) string {
	if strings.HasPrefix(signature, "0") {
		return "1" + signature[1:]
	}
	return "0" + signature[1:]
}
