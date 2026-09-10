package tokenauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	LegacyDigestVersion uint16 = 0
	DigestVersion       uint16 = 1
	prefix                     = "sk-prism-v1"
	legacyPrefix               = "sk-prism-"
	selectorBytes              = 12
	secretBytes                = 32
	legacySecretBytes          = 24
)

var ErrInvalidCredential = errors.New("invalid API credential")

func Generate() (plain, selector string, digest []byte, err error) {
	selectorRaw := make([]byte, selectorBytes)
	secretRaw := make([]byte, secretBytes)
	if _, err = rand.Read(selectorRaw); err != nil {
		return "", "", nil, fmt.Errorf("generate token selector: %w", err)
	}
	if _, err = rand.Read(secretRaw); err != nil {
		return "", "", nil, fmt.Errorf("generate token secret: %w", err)
	}
	selector = base64.RawURLEncoding.EncodeToString(selectorRaw)
	secret := base64.RawURLEncoding.EncodeToString(secretRaw)
	digest = secretDigest(selector, secretRaw, DigestVersion)
	return prefix + "." + selector + "." + secret, selector, digest, nil
}

func Parse(plain string) (selector, secret string, err error) {
	parts := strings.Split(plain, ".")
	if len(parts) != 3 || parts[0] != prefix {
		return "", "", ErrInvalidCredential
	}
	selectorRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(selectorRaw) != selectorBytes || base64.RawURLEncoding.EncodeToString(selectorRaw) != parts[1] {
		return "", "", ErrInvalidCredential
	}
	secretRaw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(secretRaw) != secretBytes || base64.RawURLEncoding.EncodeToString(secretRaw) != parts[2] {
		return "", "", ErrInvalidCredential
	}
	return parts[1], parts[2], nil
}

// Resolve accepts the current credential format and the one-way-hash legacy
// format issued before v2. Legacy credentials remain verifiable after their
// plaintext database column is removed; newly issued credentials always use
// DigestVersion.
func Resolve(plain string) (selector, secret string, version uint16, err error) {
	if strings.HasPrefix(plain, prefix+".") {
		selector, secret, err = Parse(plain)
		return selector, secret, DigestVersion, err
	}
	if !strings.HasPrefix(plain, legacyPrefix) {
		return "", "", 0, ErrInvalidCredential
	}
	raw := strings.TrimPrefix(plain, legacyPrefix)
	decoded, decodeErr := hex.DecodeString(raw)
	if decodeErr != nil || len(decoded) != legacySecretBytes || hex.EncodeToString(decoded) != raw {
		return "", "", 0, ErrInvalidCredential
	}
	digest := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(digest[:]), plain, LegacyDigestVersion, nil
}

func Verify(selector, secret string, version uint16, expected []byte) bool {
	if len(expected) != sha256.Size {
		return false
	}
	if version == LegacyDigestVersion {
		digest := sha256.Sum256([]byte(secret))
		encoded := make([]byte, hex.EncodedLen(len(digest)))
		hex.Encode(encoded, digest[:])
		return subtle.ConstantTimeCompare(encoded, []byte(selector)) == 1 &&
			subtle.ConstantTimeCompare(digest[:], expected) == 1
	}
	if version != DigestVersion {
		return false
	}
	secretRaw, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(secretRaw) != secretBytes {
		return false
	}
	actual := secretDigest(selector, secretRaw, version)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func KeyHint(plain string) string {
	if len(plain) <= 4 {
		return "****"
	}
	return "****" + plain[len(plain)-4:]
}

func secretDigest(selector string, secret []byte, version uint16) []byte {
	hash := sha256.New()
	hash.Write([]byte("prism:token-secret-digest:"))
	hash.Write([]byte{byte(version >> 8), byte(version), 0})
	hash.Write([]byte(selector))
	hash.Write([]byte{0})
	hash.Write(secret)
	return hash.Sum(nil)
}
