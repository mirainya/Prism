package repository

import "testing"

func TestValidCryptoReadinessOperation(t *testing.T) {
	for _, value := range []string{"mac", "wrap", "unwrap", "encrypt", "decrypt"} {
		if !validCryptoReadinessOperation(value) {
			t.Fatalf("operation %q should be accepted", value)
		}
	}
	for _, value := range []string{"", "sign", "MAC", "encrypt_decrypt"} {
		if validCryptoReadinessOperation(value) {
			t.Fatalf("operation %q should be rejected", value)
		}
	}
}
