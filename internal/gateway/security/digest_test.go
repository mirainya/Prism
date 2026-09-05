package security

import "testing"

func TestDomainDigestSeparatesNamespacesAndParts(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	a := DomainDigest(key, "task", []byte("a"), []byte("bc"))
	b := DomainDigest(key, "task", []byte("ab"), []byte("c"))
	c := DomainDigest(key, "other", []byte("a"), []byte("bc"))
	if a == b || a == c {
		t.Fatal("domain digest is ambiguous")
	}
}
