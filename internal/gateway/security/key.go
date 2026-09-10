package security

import (
	"encoding/base64"
	"strings"
)

// DecodeBase64Key accepts standard Base64 with or without padding and
// requires exactly one AES-256/HMAC-SHA-256 key.
func DecodeBase64Key(encoded string) ([]byte, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, ErrInvalidKey
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(decoded) != KeySize {
		clear(decoded)
		return nil, ErrInvalidKey
	}
	return decoded, nil
}
