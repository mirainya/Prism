package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

const callbackSignatureVersion = "v1"

func GenerateCallbackSecret() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate callback secret: %w", err)
	}
	return hex.EncodeToString(secret), nil
}

func SignCallback(secret string, channelID uint, taskNo string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(callbackSignaturePayload(channelID, taskNo)))
	return hex.EncodeToString(mac.Sum(nil))
}

func VerifyCallbackSignature(secret string, channelID uint, taskNo, signature string) bool {
	provided, err := hex.DecodeString(signature)
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(callbackSignaturePayload(channelID, taskNo)))
	return hmac.Equal(mac.Sum(nil), provided)
}

func BuildSignedCallbackURL(publicURL, channelType string, channelID uint, taskNo, secret string) string {
	signature := SignCallback(secret, channelID, taskNo)
	return strings.TrimRight(publicURL, "/") +
		"/internal/callback/v1/" + url.PathEscape(channelType) +
		"/" + url.PathEscape(taskNo) +
		"/" + signature
}

func callbackSignaturePayload(channelID uint, taskNo string) string {
	return fmt.Sprintf("%s\n%d\n%s", callbackSignatureVersion, channelID, taskNo)
}
