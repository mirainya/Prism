package adapter

import (
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/runtime"
)

// VideoAsyncCodecs returns fresh stateless codec values for the worker. The
// catalog selects an exact code and contract version; unknown implementations
// are rejected before a task is created.
func VideoAsyncCodecs() map[string]runtime.AsyncCodec {
	return map[string]runtime.AsyncCodec{
		SeedanceAdapter:     Seedance{},
		GenericVideoAdapter: GenericVideo{},
	}
}

func VideoAsyncCodecFor(code string, version uint32) (runtime.AsyncCodec, bool) {
	codec, ok := VideoAsyncCodecs()[fmt.Sprintf("%s@%d", strings.ToLower(strings.TrimSpace(code)), version)]
	return codec, ok
}
