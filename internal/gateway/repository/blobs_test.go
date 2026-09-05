package repository

import (
	"encoding/hex"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestOpenBlobAuthenticatesContent(t *testing.T) {
	kek := []byte("01234567890123456789012345678901")
	hmacKey := []byte("hmac-key")
	owner := []byte("credential:1")
	aad, err := security.CanonicalAAD(7, "credential", 1, owner)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := security.Seal([]byte("secret"), aad, kek, 3)
	if err != nil {
		t.Fatal(err)
	}
	h := security.HMACSHA256(hmacKey, []byte("secret"))
	em := BlobEnvelope{KeyringID: 1, KEKVersion: 3, Purpose: "credential", SchemaVersion: 1, Nonce: sealed.Nonce, Ciphertext: sealed.Ciphertext, WrapNonce: sealed.WrapNonce, WrappedDEK: sealed.WrappedDEK, ContentHMAC: hex.EncodeToString(h[:])}
	plain, err := OpenBlob(em, 7, owner, kek, hmacKey)
	if err != nil || string(plain) != "secret" {
		t.Fatalf("open=%q err=%v", plain, err)
	}
	em.ContentHMAC = hex.EncodeToString(make([]byte, 32))
	if _, err := OpenBlob(em, 7, owner, kek, hmacKey); err == nil {
		t.Fatal("tampered content accepted")
	}
}
