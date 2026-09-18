package adapter

import (
	"sync"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

// processRegistry is the manifest set of this binary. Building it panics on an
// invalid manifest: the manifests are compiled-in constants, so a failure is a
// programming error of the same class as a bad regexp literal, and a gateway
// running with a half-loaded pricing surface is worse than one that will not
// start. The package's own tests build it, so the panic fires in CI.
var processRegistry = sync.OnceValue(func() *ManifestRegistry {
	registry, err := NewManifestRegistry(processManifests())
	if err != nil {
		panic("adapter: process manifest is invalid: " + err.Error())
	}
	return registry
})

// ProcessManifests returns the registry backing this binary's pricing surface.
func ProcessManifests() *ManifestRegistry { return processRegistry() }

// init hands the registry to the catalog publication gate. The repository
// package cannot import this one, so the direction is inverted through the
// interface it declares; registering here rather than in main means every entry
// point — server, migration tool, tests — proves bounds against the same
// manifests instead of failing closed by accident.
func init() {
	registry := processRegistry()
	repository.SetExpressionSpecSource(registry)
	repository.SetDownstreamPathSource(registry)
}
