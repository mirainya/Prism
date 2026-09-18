package repository

import (
	"errors"
	"strings"
	"testing"
)

func validChangeGuard() catalogChangeGuard {
	return catalogChangeGuard{
		ExpectedActiveReleaseID: 7,
		SemanticVersion:         "1.0.1",
		SemanticDigest:          strings.Repeat("a", 64),
	}
}

func TestRouteWeightChangeValidation(t *testing.T) {
	base := RouteWeightChange{
		catalogChangeGuard: validChangeGuard(), SKUCode: "video-standard",
		ProductCode: "provider-video", PoolCode: "primary", TransportCode: "video-v1",
		Priority: 100, Weight: 100,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid route change rejected: %v", err)
	}
	for name, mutate := range map[string]func(*RouteWeightChange){
		"missing sku":       func(v *RouteWeightChange) { v.SKUCode = "" },
		"missing product":   func(v *RouteWeightChange) { v.ProductCode = "" },
		"missing pool":      func(v *RouteWeightChange) { v.PoolCode = "" },
		"missing transport": func(v *RouteWeightChange) { v.TransportCode = "" },
		"zero weight":       func(v *RouteWeightChange) { v.Weight = 0 },
		"too much weight":   func(v *RouteWeightChange) { v.Weight = 1000001 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if err := candidate.Validate(); err != ErrInvalidInput {
				t.Fatalf("err=%v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestSKUVariantChangeValidation(t *testing.T) {
	base := SKUVariantChange{catalogChangeGuard: validChangeGuard(), SKUCode: "video-standard", VariantCode: "official"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid variant change rejected: %v", err)
	}
	for _, candidate := range []SKUVariantChange{
		{catalogChangeGuard: validChangeGuard(), SKUCode: "", VariantCode: "official"},
		{catalogChangeGuard: validChangeGuard(), SKUCode: "video-standard", VariantCode: ""},
		{catalogChangeGuard: validChangeGuard(), SKUCode: "video standard", VariantCode: "official"},
	} {
		if err := candidate.Validate(); err != ErrInvalidInput {
			t.Fatalf("candidate %#v err=%v, want ErrInvalidInput", candidate, err)
		}
	}
}

func TestSKUDownstreamPathsChangeValidation(t *testing.T) {
	base := SKUDownstreamPathsChange{catalogChangeGuard: validChangeGuard(), SKUCode: "video-standard", DownstreamPaths: []string{"/v1/videos/generations", "/v1/responses"}}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid downstream path change rejected: %v", err)
	}
	for name, paths := range map[string][]string{
		"empty":        nil,
		"relative":     {"v1/videos"},
		"query":        {"/v1/videos?x=1"},
		"duplicate":    {"/v1/videos", "/v1/videos"},
		"control char": {"/v1/videos\n"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.DownstreamPaths = paths
			if err := candidate.Validate(); err != ErrInvalidInput {
				t.Fatalf("paths=%q err=%v, want ErrInvalidInput", paths, err)
			}
		})
	}
}

func TestProductChangeNormalizesAndRejectsForbiddenPricingFields(t *testing.T) {
	in := ProductChange{catalogChangeGuard: validChangeGuard(), ProductCode: " Provider-Video ", VendorModel: " model-v2 ", CapabilityConstraints: []byte(`{"duration":{"max":12}}`)}
	if err := in.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("valid product change rejected: %v", err)
	}
	if in.ProductCode != "provider-video" || in.VendorModel != "model-v2" {
		t.Fatalf("normalization failed: %#v", in)
	}
	unicodeModel := ProductChange{catalogChangeGuard: validChangeGuard(), ProductCode: "provider-video", VendorModel: " seedance2.5-10图 ", CapabilityConstraints: []byte(`{}`)}
	if err := unicodeModel.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := unicodeModel.Validate(); err != nil || unicodeModel.VendorModel != "seedance2.5-10图" {
		t.Fatalf("unicode vendor model rejected: value=%q err=%v", unicodeModel.VendorModel, err)
	}
	for _, vendorModel := range []string{"model\x00name", "model\rname", "model\nname", "model\tname", strings.Repeat("a", 256)} {
		candidate := ProductChange{catalogChangeGuard: validChangeGuard(), ProductCode: "provider-video", VendorModel: vendorModel, CapabilityConstraints: []byte(`{}`)}
		if err := candidate.Normalize(); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("vendor_model=%q err=%v, want ErrInvalidInput", vendorModel, err)
		}
	}
	for _, raw := range []string{`{"fixed_price":1}`, `{"api_key":"secret"}`, `[]`} {
		candidate := ProductChange{catalogChangeGuard: validChangeGuard(), ProductCode: "provider-video", VendorModel: "model-v2", CapabilityConstraints: []byte(raw)}
		if err := candidate.Normalize(); err != ErrInvalidInput {
			t.Fatalf("constraints=%s err=%v, want ErrInvalidInput", raw, err)
		}
	}
}

func TestCatalogRollbackInputValidation(t *testing.T) {
	base := CatalogRollbackInput{ExpectedActiveReleaseID: 7, SemanticVersion: "1.0.1", SemanticDigest: strings.Repeat("b", 64)}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid rollback rejected: %v", err)
	}

	// Rollback is still a release-scoped B-class change.  It must carry the
	// optimistic active-release guard and a valid label for the new fork; neither
	// field can be inferred from the historical path parameter without risking a
	// stale or ambiguous write.
	for name, mutate := range map[string]func(*CatalogRollbackInput){
		"missing active guard":      func(v *CatalogRollbackInput) { v.ExpectedActiveReleaseID = 0 },
		"missing semantic version":  func(v *CatalogRollbackInput) { v.SemanticVersion = "" },
		"invalid semantic version":  func(v *CatalogRollbackInput) { v.SemanticVersion = "1.0/rollback" },
		"semantic version too long": func(v *CatalogRollbackInput) { v.SemanticVersion = strings.Repeat("v", 65) },
		"missing semantic digest":   func(v *CatalogRollbackInput) { v.SemanticDigest = "" },
		"short semantic digest":     func(v *CatalogRollbackInput) { v.SemanticDigest = strings.Repeat("b", 63) },
		"non-hex semantic digest":   func(v *CatalogRollbackInput) { v.SemanticDigest = strings.Repeat("g", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err=%v, want ErrInvalidInput", err)
			}
		})
	}

	// The HTTP handler trims the label before validation, and the repository
	// repeats that normalization defensively for direct callers.
	normalized := base
	normalized.SemanticVersion = "  1.0.1  "
	normalized.Normalize()
	if normalized.SemanticVersion != "1.0.1" {
		t.Fatalf("rollback semantic version was not normalized: %q", normalized.SemanticVersion)
	}
	if err := normalized.Validate(); err != nil {
		t.Fatalf("normalized rollback rejected: %v", err)
	}
}
