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

func TestOpenAIImagesManifestExposesOperationsAndBillingFacts(t *testing.T) {
	registry := ProcessManifests()
	manifest, ok := registry.Manifest("openai_images", 1)
	if !ok {
		t.Fatal("openai_images@1 manifest is missing")
	}
	if manifest.Capability != "image" {
		t.Fatalf("capability=%q, want image", manifest.Capability)
	}
	wantPaths := []string{"/v1/images/generations", "/v1/images/edits"}
	if !slices.Equal(manifest.DownstreamPaths, wantPaths) {
		t.Fatalf("paths=%v, want %v", manifest.DownstreamPaths, wantPaths)
	}

	spec, err := registry.ExpressionSpec("openai_images", 1, DefaultVariant)
	if err != nil {
		t.Fatal(err)
	}
	observation := OpenAIImagesObservation{Outputs: make([]OpenAIImageOutput, 1)}
	runtimeFacts, err := imageBillingFacts(observation)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Declared) != len(runtimeFacts.Declared) {
		t.Fatalf("manifest variables=%v, runtime variables=%v", spec.Declared, runtimeFacts.Declared)
	}
	for name := range runtimeFacts.Declared {
		if !spec.Declared[name] {
			t.Fatalf("runtime billing variable %q is absent from the manifest", name)
		}
	}
	for _, name := range []string{"count", "input_tokens", "output_tokens"} {
		if !spec.Declared[name] {
			t.Fatalf("image billing variable %q is not declared", name)
		}
	}

	expression, err := billing.ParseExpression("count + input_tokens + output_tokens", spec.Declared)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := expression.ProveUpperBound(spec)
	if err != nil {
		t.Fatal(err)
	}
	if proof.MaxPrice.String() != "4000010" {
		t.Fatalf("bound=%s, want 4000010 (witness %v)", proof.MaxPrice.String(), proof.Witness)
	}
}

func TestOpenAIImagesManifestIncludesSub2DiscoveredModels(t *testing.T) {
	manifest, ok := ProcessManifests().Manifest("openai_images", 1)
	if !ok || len(manifest.Variants) != 1 {
		t.Fatalf("openai_images manifest variants=%d, want 1", len(manifest.Variants))
	}
	want := []string{
		"gpt-image-2",
		"gpt-image-2-adobe",
		"gpt-image-2-auto",
		"gpt-image-2-c",
		"gpt-image-2-high",
		"gpt-image-2.5",
		"gpt-image-2.5-flare",
		"gpt-image-2.5-sunburst",
	}
	for _, model := range want {
		if !slices.Contains(manifest.Variants[0].Models, model) {
			t.Errorf("Sub2 model %q is absent from the image manifest", model)
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
