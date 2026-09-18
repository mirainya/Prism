package adapter

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// manifestKey identifies one adapter coordinate.
type manifestKey struct {
	code    string
	version uint32
}

// ManifestRegistry resolves manifests and derives the ExpressionSpec the billing
// gate proves against. It is immutable after construction, so concurrent reads
// need no lock.
type ManifestRegistry struct {
	manifests map[manifestKey]Manifest
	specs     map[string]billing.ExpressionSpec
}

// NewManifestRegistry validates every manifest and precomputes one spec per
// adapter/variant. Validation happens once at startup rather than per
// publication so a malformed manifest fails the build's own tests.
func NewManifestRegistry(manifests []Manifest) (*ManifestRegistry, error) {
	registry := &ManifestRegistry{
		manifests: make(map[manifestKey]Manifest, len(manifests)),
		specs:     make(map[string]billing.ExpressionSpec),
	}
	for _, manifest := range manifests {
		if err := validateManifest(manifest); err != nil {
			return nil, err
		}
		key := manifestKey{code: manifest.Adapter, version: manifest.AdapterVersion}
		if _, exists := registry.manifests[key]; exists {
			return nil, fmt.Errorf("%w: duplicate manifest for %s@%d", ErrManifestInvalid, manifest.Adapter, manifest.AdapterVersion)
		}
		// A manifest may only describe an adapter this binary actually runs, so
		// a manifest cannot outlive the code it prices.
		if _, ok := DescriptorFor(manifest.Adapter, manifest.AdapterVersion); !ok {
			return nil, fmt.Errorf("%w: %s@%d has no descriptor", ErrManifestInvalid, manifest.Adapter, manifest.AdapterVersion)
		}
		registry.manifests[key] = manifest
		for _, variant := range manifest.Variants {
			spec, err := buildExpressionSpec(manifest, variant)
			if err != nil {
				return nil, err
			}
			registry.specs[specKey(manifest.Adapter, manifest.AdapterVersion, variant.Code)] = spec
		}
	}
	return registry, nil
}

func specKey(code string, version uint32, variant string) string {
	return code + "@" + uintString(version) + "/" + variant
}

// ExpressionSpec implements the billing gate's manifest source.
func (r *ManifestRegistry) ExpressionSpec(code string, version uint32, variant string) (billing.ExpressionSpec, error) {
	if r == nil {
		return billing.ExpressionSpec{}, ErrManifestUnknown
	}
	spec, ok := r.specs[specKey(code, version, variant)]
	if !ok {
		return billing.ExpressionSpec{}, fmt.Errorf("%w: %s", ErrManifestUnknown, specKey(code, version, variant))
	}
	return spec, nil
}

// Manifest returns the full declaration for the console and the change layer.
func (r *ManifestRegistry) Manifest(code string, version uint32) (Manifest, bool) {
	if r == nil {
		return Manifest{}, false
	}
	manifest, ok := r.manifests[manifestKey{code: code, version: version}]
	return manifest, ok
}

// Variant returns one variant of one adapter.
func (r *ManifestRegistry) Variant(code string, version uint32, variant string) (Variant, bool) {
	manifest, ok := r.Manifest(code, version)
	if !ok {
		return Variant{}, false
	}
	for _, candidate := range manifest.Variants {
		if candidate.Code == variant {
			return candidate, true
		}
	}
	return Variant{}, false
}

// DownstreamPaths returns the manifest-declared public paths for an adapter.
// The repository uses this narrow interface when validating a release-scoped
// SKU path edit; returning a copy keeps the immutable registry private.
func (r *ManifestRegistry) DownstreamPaths(code string, version uint32) ([]string, error) {
	manifest, ok := r.Manifest(code, version)
	if ok {
		return append([]string(nil), manifest.DownstreamPaths...), nil
	}
	if code == "generic" && version == 1 {
		return []string{"/v1/videos/generations"}, nil
	}
	return nil, ErrManifestUnknown
}

// ValidateDownstreamPath checks the public Prism route selected for a SKU.
// Generic video predates the manifest registry but has one fixed public entry;
// its upstream submit and poll paths remain product mapping concerns.
func ValidateDownstreamPath(code string, version uint32, path string) error {
	code = strings.ToLower(strings.TrimSpace(code))
	path = strings.TrimSpace(path)
	if allowed, err := ProcessManifests().DownstreamPaths(code, version); err == nil {
		if slices.Contains(allowed, path) {
			return nil
		}
		return fmt.Errorf("%w: path %q is not declared by %s@%d", ErrManifestInvalid, path, code, version)
	}
	return nil
}

// Manifests returns every manifest in a stable order.
func (r *ManifestRegistry) Manifests() []Manifest {
	if r == nil {
		return nil
	}
	out := make([]Manifest, 0, len(r.manifests))
	for _, manifest := range r.manifests {
		out = append(out, manifest)
	}
	slices.SortFunc(out, func(a, b Manifest) int {
		if a.Adapter != b.Adapter {
			return strings.Compare(a.Adapter, b.Adapter)
		}
		return int(a.AdapterVersion) - int(b.AdapterVersion)
	})
	return out
}
