package security

import (
	"bytes"
	"errors"
	"testing"
)

func TestSealOpenBindsAADAndKEKVersion(t *testing.T) {
	kek := bytes.Repeat([]byte{0x42}, KeySize)
	aad := []byte("blob=17;purpose=credential;owner=channel:3")
	want := []byte("secret payload")
	envelope, err := Seal(want, aad, kek, 7)
	if err != nil {
		t.Fatal(err)
	}
	got, err := envelope.Open(aad, kek)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("open got %q, %v", got, err)
	}
	if _, err := envelope.Open([]byte("different owner"), kek); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected AAD authentication failure, got %v", err)
	}
	wrongKey := bytes.Repeat([]byte{0x43}, KeySize)
	if _, err := envelope.Open(aad, wrongKey); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("expected KEK authentication failure, got %v", err)
	}
}

func TestSealRejectsInvalidKey(t *testing.T) {
	if _, err := Seal(nil, nil, []byte("short"), 1); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("expected invalid key, got %v", err)
	}
}

func TestHMACSHA256IsDeterministic(t *testing.T) {
	first := HMACSHA256([]byte("k"), []byte("v"))
	second := HMACSHA256([]byte("k"), []byte("v"))
	if first != second {
		t.Fatal("HMAC changed between identical inputs")
	}
}

func TestCanonicalAADIsUnambiguous(t *testing.T) {
	first, err := CanonicalAAD(1, "credential", 1, []byte("channel:2"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalAAD(1, "credential", 1, []byte("channel:2"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("canonical AAD is not deterministic")
	}
	if _, err := CanonicalAAD(0, "credential", 1, []byte("owner")); !errors.Is(err, ErrInvalidAAD) {
		t.Fatalf("expected invalid AAD, got %v", err)
	}
}

func TestKeyVersionRotation(t *testing.T) {
	for _, transition := range [][2]KeyVersionState{
		{KeyPreparing, KeyReadable},
		{KeyReadable, KeyCurrent},
		{KeyCurrent, KeyReadable},
		{KeyCurrent, KeySecurityRevoked},
		{KeyRetired, KeySecurityRevoked},
	} {
		if err := TransitionKeyVersion(transition[0], transition[1]); err != nil {
			t.Fatalf("%s -> %s: %v", transition[0], transition[1], err)
		}
	}
	if err := TransitionKeyVersion(KeyRetired, KeyCurrent); err == nil {
		t.Fatal("retired key version was reactivated")
	}
}
