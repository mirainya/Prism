package bootstrap

import (
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/generic"
	"github.com/mirainya/Prism/internal/video/seedance"
)

// New creates the process-wide video engine with all built-in protocols.
func New() *video.Engine {
	engine := video.NewEngine()
	if engine == nil {
		return nil
	}
	registerAdapters(engine.Registry())
	return engine
}

func registerAdapters(registry *video.Registry) {
	registry.Register(video.AdapterTypeSeedance, seedance.NewAdapter)
	registry.Register(video.AdapterTypeGeneric, generic.NewAdapter)
}
