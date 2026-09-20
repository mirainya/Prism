package adapter

import (
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/runtime"
)

// AsyncCodecs returns fresh stateless codec values for the worker. The
// catalog selects an exact code and contract version; unknown implementations
// are rejected before a task is created.
func AsyncCodecs() map[string]runtime.AsyncCodec {
	return map[string]runtime.AsyncCodec{
		SeedanceAdapter:     Seedance{},
		GenericVideoAdapter: GenericVideo{},
		OpenAIImagesAdapter: AsyncOpenAIImages{},
	}
}

func AsyncCodecFor(code string, version uint32) (runtime.AsyncCodec, bool) {
	codec, ok := AsyncCodecs()[fmt.Sprintf("%s@%d", strings.ToLower(strings.TrimSpace(code)), version)]
	return codec, ok
}
