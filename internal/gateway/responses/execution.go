package responses

import (
	"context"
	"errors"

	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

func (p *Pipeline) Create(ctx context.Context, userID, tokenID uint, request *protocol.Request, idempotencyKey string, requestIDs ...string) (*Result, error) {
	if p == nil || p.v2 == nil || p.engine == nil {
		return nil, errors.New("Gateway V2 engine is not initialized")
	}
	requestID := ""
	if len(requestIDs) > 0 {
		requestID = requestIDs[0]
	}
	return p.createV2(ctx, userID, tokenID, request, idempotencyKey, requestID)
}

func (p *Pipeline) ExecuteBackground(ctx context.Context, responseID string, finalAttempt bool, attempts ...int) error {
	if p == nil || p.v2 == nil || p.engine == nil {
		return errors.New("Gateway V2 engine is not initialized")
	}
	attempt := 0
	if len(attempts) > 0 {
		attempt = attempts[0]
	}
	return p.executeBackgroundV2(ctx, responseID, finalAttempt, attempt)
}
