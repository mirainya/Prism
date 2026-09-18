package adapter

import (
	"errors"
	"slices"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// TestProcessManifestsBuild is the reason the panic in manifest_process.go is
// acceptable: the compiled-in manifest set is validated by the package's tests.
func TestProcessManifestsBuild(t *testing.T) {
	registry := ProcessManifests()
	for _, manifest := range registry.Manifests() {
		if _, ok := DescriptorFor(manifest.Adapter, manifest.AdapterVersion); !ok {
			t.Fatalf("%s@%d has no descriptor", manifest.Adapter, manifest.AdapterVersion)
		}
		for _, variant := range manifest.Variants {
			if _, err := registry.ExpressionSpec(manifest.Adapter, manifest.AdapterVersion, variant.Code); err != nil {
				t.Fatalf("%s/%s: %v", manifest.Adapter, variant.Code, err)
			}
		}
	}
	if _, err := registry.ExpressionSpec("seedance", 1, "nope"); !errors.Is(err, ErrManifestUnknown) {
		t.Fatalf("err=%v, want ErrManifestUnknown", err)
	}
}

func TestChatManifestsExposeEveryPublicConversationProtocol(t *testing.T) {
	registry := ProcessManifests()
	want := []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"}
	for _, code := range []string{"openai_chat", "openai_responses", "anthropic_messages", "google_generate_content"} {
		paths, err := registry.DownstreamPaths(code, 1)
		if err != nil {
			t.Fatalf("%s paths: %v", code, err)
		}
		if !slices.Equal(paths, want) {
			t.Fatalf("%s paths=%v, want %v", code, paths, want)
		}
	}
}

// TestSeedanceVariantsBoundTheirOwnExample proves the spec's own pricing_hints
// examples publish, and that each variant's duration hint is what bounds them.
func TestSeedanceVariantsBoundTheirOwnExample(t *testing.T) {
	registry := ProcessManifests()
	for _, test := range []struct {
		variant string
		expr    string
		want    string
	}{
		// 30s at the 1.5 priority tier and the 1.2 reference multiplier.
		{variant: "seedance25", expr: "seconds * 0.20 * (priority==4 ? 1.5 : 1) * (has_video_ref==1 ? 1.2 : 1)", want: "10.8"},
		{variant: "official", expr: "seconds * 0.30", want: "3"},
		// The conditional enum widens duration to 4~15 for 720p, so the bound is
		// taken over the union rather than the narrower 1080p window.
		{variant: "h_channel", expr: "seconds * 0.10", want: "1.5"},
	} {
		t.Run(test.variant, func(t *testing.T) {
			spec, err := registry.ExpressionSpec("seedance", 1, test.variant)
			if err != nil {
				t.Fatal(err)
			}
			expression, err := billing.ParseExpression(test.expr, spec.Declared)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := expression.ProveUpperBound(spec)
			if err != nil {
				t.Fatal(err)
			}
			if proof.MaxPrice.String() != test.want {
				t.Fatalf("bound=%s, want %s (witness %v)", proof.MaxPrice.String(), test.want, proof.Witness)
			}
		})
	}
}

// TestUndeclaredVariableIsRejectedBeforeArithmetic is the whitelist's whole
// point: a price may not reference a fact the manifest never promised.
func TestUndeclaredVariableIsRejectedBeforeArithmetic(t *testing.T) {
	spec, err := ProcessManifests().ExpressionSpec("openai_chat", 1, DefaultVariant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := billing.ParseExpression("seconds * 2", spec.Declared); err == nil {
		t.Fatal("a video variable was accepted by a chat manifest")
	}
	if _, err := billing.ParseExpression("input_tokens * 0.000003 + output_tokens * 0.000015", spec.Declared); err != nil {
		t.Fatalf("declared chat variables were rejected: %v", err)
	}
}

// TestSpecDigestSeparatesCoordinates keeps the parse cache honest: two variants
// with different domains must not share a cache entry.
func TestSpecDigestSeparatesCoordinates(t *testing.T) {
	registry := ProcessManifests()
	seen := make(map[string]string)
	for _, manifest := range registry.Manifests() {
		for _, variant := range manifest.Variants {
			spec, err := registry.ExpressionSpec(manifest.Adapter, manifest.AdapterVersion, variant.Code)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Digest == "" {
				t.Fatalf("%s/%s has no digest", manifest.Adapter, variant.Code)
			}
			key := manifest.Adapter + "/" + variant.Code
			if previous, exists := seen[spec.Digest]; exists {
				t.Fatalf("%s and %s share digest %s", previous, key, spec.Digest)
			}
			seen[spec.Digest] = key
		}
	}
	// The digest is stable across calls, otherwise the cache would never hit.
	first, err := registry.ExpressionSpec("seedance", 1, "seedance25")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := registry.ExpressionSpec("seedance", 1, "seedance25")
	if first.Digest != second.Digest {
		t.Fatal("digest is not stable")
	}
}
