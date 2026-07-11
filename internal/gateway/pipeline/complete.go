package pipeline

import (
	"context"
	"errors"

	"github.com/mirainya/Prism/internal/service"
)

func (p *Pipeline) Complete(ctx context.Context, request *service.CompletionRequest) (*service.CompletionResponse, error) {
	if p == nil || p.v2 == nil {
		return nil, errors.New("Gateway V2 engine is not initialized")
	}
	return p.completeV2(ctx, request)
}
