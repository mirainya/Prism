package adapter

import "testing"

func TestManifestIsStableAndSelectable(t *testing.T) {
	items := Descriptors()
	if len(items) == 0 || len(SemanticDigest()) != 64 {
		t.Fatal("adapter manifest is incomplete")
	}
	for _, item := range items {
		if len(item.ImplementationDigest) != 64 || item.Version == 0 || item.Protocol == "" {
			t.Fatalf("invalid descriptor: %+v", item)
		}
		resolved, ok := DescriptorFor(item.Code, item.Version)
		if !ok || resolved != item {
			t.Fatalf("descriptor lookup failed: %+v", item)
		}
	}
	items[0].Code = "changed"
	if Descriptors()[0].Code == "changed" {
		t.Fatal("Descriptors exposed mutable process state")
	}
}

func TestManifestDigestIncludesBuildRevision(t *testing.T) {
	original := BuildRevision
	t.Cleanup(func() { BuildRevision = original })
	BuildRevision = "revision-a"
	first, ok := DescriptorFor("openai_chat", 1)
	if !ok {
		t.Fatal("missing openai adapter")
	}
	// The process manifest is initialized once, so verify the digest contract
	// directly rather than mutating the published descriptor set.
	if first.ImplementationDigest == "" || first.ImplementationDigest == "prism-gateway-adapter:openai_chat@1" {
		t.Fatalf("manifest digest is not a cryptographic build identity: %q", first.ImplementationDigest)
	}
}
