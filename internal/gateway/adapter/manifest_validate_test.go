package adapter

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// validManifest is the smallest manifest that passes, so each test can break
// exactly one rule.
func validManifest() Manifest {
	return Manifest{
		Adapter:         "seedance",
		AdapterVersion:  1,
		Capability:      "video",
		DownstreamPaths: []string{"/v1/videos/generations"},
		BillingFuncs:    []string{"tier"},
		BillingVars: map[varLayer][]BillingVar{
			LayerCapability: {{Name: "seconds", Desc: "生成视频秒数", From: "duration"}},
		},
		Variants: []Variant{{
			Code: "official", Models: []string{"seedance-2.0"},
			ParamHints: map[string]ParamHint{"duration": {Kind: HintInt, Min: "4", Max: "30"}},
		}},
	}
}

func TestManifestValidationRejects(t *testing.T) {
	for _, test := range []struct {
		name   string
		want   string
		mutate func(*Manifest)
	}{
		{name: "no capability", want: "no capability", mutate: func(m *Manifest) { m.Capability = "" }},
		{name: "no downstream path", want: "no downstream path", mutate: func(m *Manifest) { m.DownstreamPaths = nil }},
		{name: "unimplemented func", want: "not implemented by the DSL",
			mutate: func(m *Manifest) { m.BillingFuncs = []string{"sqrt"} }},
		{name: "variable without description", want: "no description", mutate: func(m *Manifest) {
			m.BillingVars[LayerCapability][0].Desc = ""
		}},
		{name: "variable in two layers", want: "declared in both", mutate: func(m *Manifest) {
			m.BillingVars[LayerAdapter] = []BillingVar{{Name: "seconds", Desc: "重复声明", From: "duration"}}
		}},
		{name: "two domain sources", want: "from both a hint and an inline domain", mutate: func(m *Manifest) {
			m.BillingVars[LayerCapability][0].Domain = &VarDomainSpec{Kind: HintBool}
		}},
		{name: "hint typo", want: "no variant declares", mutate: func(m *Manifest) {
			m.BillingVars[LayerCapability][0].From = "duratoin"
		}},
		{name: "no variant", want: "declares no variant", mutate: func(m *Manifest) { m.Variants = nil }},
		{name: "duplicate variant", want: "duplicate variant", mutate: func(m *Manifest) {
			m.Variants = append(m.Variants, m.Variants[0])
		}},
		{name: "variant without model", want: "lists no upstream model", mutate: func(m *Manifest) {
			m.Variants[0].Models = nil
		}},
		{name: "inverted interval", want: "unusable interval", mutate: func(m *Manifest) {
			m.Variants[0].ParamHints["duration"] = ParamHint{Kind: HintInt, Min: "30", Max: "4"}
		}},
		{name: "fractional interval", want: "unusable interval", mutate: func(m *Manifest) {
			m.Variants[0].ParamHints["duration"] = ParamHint{Kind: HintInt, Min: "1.5", Max: "4"}
		}},
		{name: "enum without options", want: "enum with no options", mutate: func(m *Manifest) {
			m.Variants[0].ParamHints["resolution"] = ParamHint{Kind: HintEnum}
		}},
		{name: "default outside options", want: "which is not an option", mutate: func(m *Manifest) {
			m.Variants[0].ParamHints["resolution"] = ParamHint{Kind: HintEnum, Options: []string{"720p"}, Default: "4k"}
		}},
		{name: "conditional narrows nothing", want: "narrows undeclared parameter", mutate: func(m *Manifest) {
			m.Variants[0].ParamHints["resolution"] = ParamHint{Kind: HintConditionalEnum,
				Cases: []HintCase{{Value: "720p", Dependent: "lenght", Min: "4", Max: "8"}}}
		}},
		{name: "unbounded tier step is not last", want: "unbounded step before the last", mutate: func(m *Manifest) {
			m.Variants[0].Tiers = map[string][]billing.ExprTier{"context": {{UpTo: "", Value: "1"}, {UpTo: "10", Value: "2"}}}
		}},
		{name: "tier bounds do not increase", want: "does not increase", mutate: func(m *Manifest) {
			m.Variants[0].Tiers = map[string][]billing.ExprTier{"context": {{UpTo: "10", Value: "1"}, {UpTo: "10", Value: "2"}}}
		}},
		{name: "inverted reference limit", want: "inverted limit", mutate: func(m *Manifest) {
			m.Variants[0].RefMedia = map[string]RefMediaLimit{"video": {Max: 1, ItemSeconds: [2]int{15, 2}}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest()
			test.mutate(&manifest)
			err := validateManifest(manifest)
			if !errors.Is(err, ErrManifestInvalid) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want ErrManifestInvalid containing %q", err, test.want)
			}
			// The registry refuses to build rather than serving a bad manifest.
			if _, err := NewManifestRegistry([]Manifest{manifest}); !errors.Is(err, ErrManifestInvalid) {
				t.Fatalf("registry accepted an invalid manifest: %v", err)
			}
		})
	}
}

func TestRegistryRejectsDuplicateAndUndeployedManifests(t *testing.T) {
	manifest := validManifest()
	if _, err := NewManifestRegistry([]Manifest{manifest, manifest}); !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("err=%v, want ErrManifestInvalid", err)
	}
	// A manifest cannot price code this binary does not run.
	manifest.Adapter = "not_deployed"
	if _, err := NewManifestRegistry([]Manifest{manifest}); err == nil || !strings.Contains(err.Error(), "no descriptor") {
		t.Fatalf("err=%v, want no descriptor", err)
	}
}

func TestValidateDownstreamPathRejectsWrongGenericVideoEntry(t *testing.T) {
	paths, err := ProcessManifests().DownstreamPaths("generic", 1)
	if err != nil || !slices.Equal(paths, []string{"/v1/videos/generations"}) {
		t.Fatalf("generic paths=%v err=%v", paths, err)
	}
	if err := ValidateDownstreamPath("generic", 1, "/v1/videos/generations"); err != nil {
		t.Fatalf("fixed generic video path rejected: %v", err)
	}
	if err := ValidateDownstreamPath("generic", 1, "/v1/video/generations"); !errors.Is(err, ErrManifestInvalid) {
		t.Fatalf("err=%v, want ErrManifestInvalid", err)
	}
}

// TestVariableWithoutHintOnThisVariantStaysUnbounded records the deliberate
// asymmetry: a parameter only some variants accept leaves the variable
// referenceable but unprovable, so publication fails instead of reserving
// nothing.
func TestVariableWithoutHintOnThisVariantStaysUnbounded(t *testing.T) {
	registry := ProcessManifests()
	spec, err := registry.ExpressionSpec("seedance", 1, "official")
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Declared["task_mode"] {
		t.Fatal("task_mode must stay declared on every seedance variant")
	}
	if _, ok := spec.Domains["task_mode"]; ok {
		t.Fatal("official has no task_mode hint, so the variable must have no domain")
	}
	expression, err := billing.ParseExpression("in(task_mode,'references') ? 2 : 1", spec.Declared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expression.ProveUpperBound(spec); !errors.Is(err, billing.ErrBoundNotEnumerable) {
		t.Fatalf("err=%v, want ErrBoundNotEnumerable", err)
	}
	// The variant that does declare the hint proves fine.
	spec, err = registry.ExpressionSpec("seedance", 1, "seedance25")
	if err != nil {
		t.Fatal(err)
	}
	expression, err = billing.ParseExpression("in(task_mode,'references') ? 2 : 1", spec.Declared)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := expression.ProveUpperBound(spec)
	if err != nil {
		t.Fatal(err)
	}
	if proof.MaxPrice.String() != "2" {
		t.Fatalf("bound=%s, want 2", proof.MaxPrice.String())
	}
}
