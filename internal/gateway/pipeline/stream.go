package pipeline

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/pkg/logger"
	"go.uber.org/zap"
)

type StreamSession struct {
	UpstreamResp *http.Response

	providerID   string
	requestLogID uint
	callID       string
	attemptID    uint
	keyID        uint
	transport    model.UpstreamTransport
	stream       *engine.StreamResult
	done         chan struct{}
	cleanupOnce  sync.Once
	mu           sync.Mutex
}

func (p *Pipeline) StreamComplete(ctx context.Context, request *service.CompletionRequest) (*StreamSession, error) {
	if p == nil || p.v2 == nil {
		return nil, errors.New("Gateway V2 engine is not initialized")
	}
	return p.streamCompleteV2(ctx, request)
}

func (s *StreamSession) FinalizeStream(_ *stream.AggregationResult, deliveryErr error) string {
	providerID, finalizeErr := s.FinalizeStreamDelivery(deliveryErr, deliveryErr != nil)
	if finalizeErr != nil && logger.L != nil {
		logger.Error("finalize chat stream delivery", zap.String("call_id", s.callID), zap.Error(finalizeErr))
	}
	return providerID
}

// FinalizeStreamDelivery records the downstream delivery outcome and returns
// any persistence failure to callers that must project only terminal calls.
func (s *StreamSession) FinalizeStreamDelivery(deliveryErr error, clientDisconnected bool) (string, error) {
	var finalizeErr error
	if s != nil && s.stream != nil {
		if deliveryErr == nil {
			finalizeErr = s.stream.CompleteDelivery()
		} else {
			finalizeErr = s.stream.FailDelivery(deliveryErr, clientDisconnected)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.providerID, finalizeErr
}

func (s *StreamSession) Cleanup() {
	if s == nil {
		return
	}
	s.cleanupOnce.Do(func() {
		if s.UpstreamResp != nil && s.UpstreamResp.Body != nil {
			_ = s.UpstreamResp.Body.Close()
		}
		if s.done != nil {
			<-s.done
		}
	})
}

func (s *StreamSession) RequestLogID() uint {
	if s == nil {
		return 0
	}
	return s.requestLogID
}

func (s *StreamSession) CallID() string {
	if s == nil {
		return ""
	}
	return s.callID
}

func (s *StreamSession) AttemptID() uint {
	if s == nil {
		return 0
	}
	return s.attemptID
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

func (s *StreamSession) CanonicalResponse() canonical.Response {
	if s == nil || s.stream == nil {
		return canonical.Response{}
	}
	return s.stream.CanonicalResponse()
}
