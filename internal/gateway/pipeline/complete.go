package pipeline

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	vadapter "github.com/mirainya/Prism/internal/gateway/adapter/volcengine"
	"github.com/mirainya/Prism/internal/gateway/log"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/service"
)

// Complete 非流式补全(带重试/熔断/火山自愈)。
func (p *Pipeline) Complete(ctx context.Context, req *service.CompletionRequest) (*service.CompletionResponse, error) {
	var excludeChannels, excludeKeys []uint
	var lastErr error
	reqID := uuid.NewString()

	for attempt := 0; attempt < maxRetries; attempt++ {
		route, err := p.router.Select(req.Model, excludeChannels, excludeKeys)
		if err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}

		resp, done := p.tryComplete(ctx, req, route, reqID, &excludeChannels, &excludeKeys, &lastErr)
		p.router.Release(route.KeyID)
		if done {
			return resp, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, routing.ErrNoRoute
}

// tryComplete 单次尝试。done=true 表示成功返回;false 表示需继续重试。
func (p *Pipeline) tryComplete(
	ctx context.Context, req *service.CompletionRequest, route *routing.RouteResult, reqID string,
	excludeChannels, excludeKeys *[]uint, lastErr *error,
) (*service.CompletionResponse, bool) {
	ad, ok := adapter.Get(route.Protocol)
	if !ok {
		*lastErr = routing.ErrNoRoute
		*excludeChannels = append(*excludeChannels, route.ChannelID)
		return nil, false
	}

	chatReq := p.buildChatRequest(req, route)
	up := p.buildUpstreamRequest(req, route, chatReq)

	reqLog := log.Create(0, route, chatReq, up.ExtraHeaders, false)
	start := time.Now()
	upResp, err := ad.Complete(ctx, up)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		// 火山 B 模式 previous_response_id 失效 → 清 B 字段同 key 重试(自愈)
		if vadapter.IsPreviousResponseNotFound(err) && req.PreviousResponseID != "" {
			req.PreviousResponseID = ""
			req.NewMessages = nil
			log.Finalize(reqLog, nil, nil, latency, err)
			// 同 key 重试:不加 exclude,直接再来一轮(交给外层 for)
			*lastErr = err
			return nil, false
		}
		log.Finalize(reqLog, nil, nil, latency, err)
		p.circuit.MarkUnavailable(route.KeyID, route.ModelName, err)
		*lastErr = err
		*excludeKeys = append(*excludeKeys, route.KeyID)
		return nil, false
	}

	// 结算 + 日志
	log.Finalize(reqLog, upResp.Chat, nil, latency, nil)
	if upResp.Chat != nil && upResp.Chat.Usage != nil {
		settleUsage(p.billing, req.TokenID, req.UserID, route, upResp.Chat.Usage, reqID)
	}

	resp := &service.CompletionResponse{
		ID:                 upResp.Chat.ID,
		Object:             upResp.Chat.Object,
		Created:            upResp.Chat.Created,
		Model:              req.Model,
		Choices:            upResp.Chat.Choices,
		Usage:              upResp.Chat.Usage,
		ProviderResponseID: upResp.ProviderResponseID,
	}
	if reqLog != nil {
		resp.RequestLogID = reqLog.ID
	}
	return resp, true
}
