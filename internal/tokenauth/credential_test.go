package tokenauth

import (
	"crypto/sha256"
	"testing"
)

func TestCredentialRoundTripAndDomainBinding(t *testing.T) {
	plain, selector, digest, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	parsedSelector, secret, err := Parse(plain)
	if err != nil {
		t.Fatal(err)
	}
	if parsedSelector != selector || !Verify(selector, secret, DigestVersion, digest) {
		t.Fatal("generated credential did not verify")
	}
	if Verify(selector+"x", secret, DigestVersion, digest) {
		t.Fatal("digest was not bound to the selector")
	}
	if Verify(selector, secret, DigestVersion+1, digest) {
		t.Fatal("unsupported digest version verified")
	}
}

func TestLegacyCredentialRoundTrip(t *testing.T) {
	plain := "sk-prism-0123456789abcdef0123456789abcdef0123456789abcdef"
	selector, secret, version, err := Resolve(plain)
	if err != nil || secret != plain || version != LegacyDigestVersion {
		t.Fatalf("resolve legacy credential: selector=%q version=%d error=%v", selector, version, err)
	}
	digest := sha256.Sum256([]byte(plain))
	if !Verify(selector, secret, version, digest[:]) {
		t.Fatal("legacy credential did not verify")
	}
	if Verify(selector, secret+"0", version, digest[:]) {
		t.Fatal("modified legacy credential verified")
	}
}

func TestResolveCurrentCredential(t *testing.T) {
	plain, selector, digest, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	resolved, secret, version, err := Resolve(plain)
	if err != nil || resolved != selector || version != DigestVersion || !Verify(resolved, secret, version, digest) {
		t.Fatalf("current credential resolution failed: selector=%q version=%d error=%v", resolved, version, err)
	}
}

func TestParseRejectsLegacyAndMalformedCredentials(t *testing.T) {
	for _, value := range []string{"", "sk-prism-legacy", "sk-prism-v1.bad.bad", "sk-prism-v2.a.b"} {
		if _, _, err := Parse(value); err == nil {
			t.Fatalf("credential %q was accepted", value)
		}
	}
}

func TestResolveRejectsMalformedLegacyCredentials(t *testing.T) {
	for _, value := range []string{
		"sk-prism-legacy",
		"sk-prism-0123456789ABCDEF0123456789abcdef0123456789abcdef",
		"sk-prism-0123456789abcdef0123456789abcdef0123456789abcdeg",
	} {
		if _, _, _, err := Resolve(value); err == nil {
			t.Fatalf("credential %q was accepted", value)
		}
	}
}
