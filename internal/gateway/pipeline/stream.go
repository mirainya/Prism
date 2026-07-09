package pipeline

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/log"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/stream"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
)

// StreamSession 流式会话:handler 拿到后自行 ProxyStream,结束再调 FinalizeStream。
type StreamSession struct {
	UpstreamResp *http.Response
	route        *routing.RouteResult
	reqLog       *model.ChannelRequestLog
	req          *service.CompletionRequest
	reqID        string
	start        time.Time
	pipeline     *Pipeline
}

// StreamComplete 流式补全:重试直到拿到上游流(或耗尽)。返回 session 供 handler 转发。
func (p *Pipeline) StreamComplete(ctx context.Context, req *service.CompletionRequest) (*StreamSession, error) {
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

		ad, ok := adapter.Get(route.Protocol)
		if !ok {
			p.router.Release(route.KeyID)
			lastErr = routing.ErrNoRoute
			excludeChannels = append(excludeChannels, route.ChannelID)
			continue
		}

		chatReq := p.buildChatRequest(req, route)
		up := p.buildUpstreamRequest(req, route, chatReq)
		reqLog := log.Create(0, route, chatReq, up.ExtraHeaders, true)
		start := time.Now()

		resp, err := ad.StreamComplete(ctx, up)
		if err != nil {
			latency := time.Since(start).Milliseconds()
			log.Finalize(reqLog, nil, nil, latency, err)
			p.circuit.MarkUnavailable(route.KeyID, route.ModelName, err)
			p.router.Release(route.KeyID)
			lastErr = err
			excludeKeys = append(excludeKeys, route.KeyID)
			continue
		}

		return &StreamSession{
			UpstreamResp: resp,
			route:        route,
			reqLog:       reqLog,
			req:          req,
			reqID:        reqID,
			start:        start,
			pipeline:     p,
		}, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, routing.ErrNoRoute
}

// FinalizeStream 流式结束后:日志 + 结算 + 释放并发。返回 ProviderResponseID(火山 B 模式回写用)。
func (s *StreamSession) FinalizeStream(agg *stream.AggregationResult, streamErr error) string {
	latency := time.Since(s.start).Milliseconds()
	log.Finalize(s.reqLog, nil, agg, latency, streamErr)
	if agg != nil && agg.Usage != nil {
		settleUsage(s.pipeline.billing, s.req.TokenID, s.req.UserID, s.route, agg.Usage, s.reqID)
	}
	s.pipeline.router.Release(s.route.KeyID)
	if agg != nil {
		return agg.ProviderResponseID
	}
	return ""
}

// Cleanup 关闭上游响应体(handler defer 调用)。
func (s *StreamSession) Cleanup() {
	if s.UpstreamResp != nil && s.UpstreamResp.Body != nil {
		s.UpstreamResp.Body.Close()
	}
}

// RequestLogID 本次请求的 channel_request_logs 主键(playground 下发 prism-debug 用)。
func (s *StreamSession) RequestLogID() uint {
	if s.reqLog == nil {
		return 0
	}
	return s.reqLog.ID
}
