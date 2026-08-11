package bootstrap

import (
	"testing"

	"github.com/mirainya/Prism/internal/video"
)

func TestRegisterAdapters(t *testing.T) {
	registry := video.NewRegistry()
	registerAdapters(registry)
	channel := &video.VideoChannel{}
	key := &video.VideoChannelKey{}
	for _, adapterType := range []string{"seedance", "generic"} {
		if registry.Get(adapterType, channel, key) == nil {
			t.Fatalf("adapter %q was not registered", adapterType)
		}
	}
}
