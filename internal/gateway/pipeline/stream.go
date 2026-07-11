package pipeline

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
)

type StreamSession struct {
	UpstreamResp *http.Response

	providerID   string
	requestLogID uint
	keyID        uint
	transport    model.UpstreamTransport
	mu           sync.Mutex
}

func (p *Pipeline) StreamComplete(ctx context.Context, request *service.CompletionRequest) (*StreamSession, error) {
	if p == nil || p.v2 == nil {
		return nil, errors.New("Gateway V2 engine is not initialized")
	}
	return p.streamCompleteV2(ctx, request)
}

func (s *StreamSession) FinalizeStream(_ *stream.AggregationResult, _ error) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerID
}

func (s *StreamSession) Cleanup() {
	if s != nil && s.UpstreamResp != nil && s.UpstreamResp.Body != nil {
		_ = s.UpstreamResp.Body.Close()
	}
}

func (s *StreamSession) RequestLogID() uint {
	if s == nil {
		return 0
	}
	return s.requestLogID
}

func (s *StreamSession) ProviderKeyID() uint {
	if s == nil {
		return 0
	}
	return s.keyID
}

func (s *StreamSession) UpstreamTransport() model.UpstreamTransport {
	if s == nil {
		return ""
	}
	return s.transport
}
