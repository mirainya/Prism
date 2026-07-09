// Package openai 实现 OpenAI 及兼容协议(deepseek/qwen/moonshot 等)的透传适配器。
// 透传 = ChatRequest 本就是 OpenAI 格式,直接 marshal→POST→unmarshal,无翻译层。
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/httputil"
)

const defaultPath = "/v1/chat/completions"

// Adapter OpenAI 兼容协议透传适配器。无状态。
type Adapter struct{}

// New 构造适配器。
func New() adapter.UpstreamAdapter { return &Adapter{} }

func (a *Adapter) Protocol() model.Protocol { return model.ProtocolOpenAI }

func (a *Adapter) Complete(ctx context.Context, req *adapter.UpstreamRequest) (*adapter.UpstreamResponse, error) {
	req.Chat.Model = req.VendorModel
	req.Chat.Stream = false

	url := req.BaseURL + resolvePath(req.RequestPath)
	headers := buildHeaders(req)

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	respBody, err := httputil.PostJSON(ctx, url, req.Chat, headers)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}

	var chatResp chat.ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("openai unmarshal response failed: %w", err)
	}
	return &adapter.UpstreamResponse{Chat: &chatResp}, nil
}

func (a *Adapter) StreamComplete(ctx context.Context, req *adapter.UpstreamRequest) (*http.Response, error) {
	req.Chat.Model = req.VendorModel
	req.Chat.Stream = true
	if req.Chat.StreamOptions == nil {
		req.Chat.StreamOptions = &chat.StreamOptions{IncludeUsage: true}
	}

	url := req.BaseURL + resolvePath(req.RequestPath)
	headers := buildHeaders(req)

	// 流式请求不设 context timeout,生命周期由 handler 管理。Body 本就是 OpenAI SSE,不包裹。
	resp, err := httputil.PostJSONStream(ctx, url, req.Chat, headers)
	if err != nil {
		return nil, fmt.Errorf("openai stream request failed: %w", err)
	}
	return resp, nil
}

func resolvePath(p string) string {
	if p == "" {
		return defaultPath
	}
	return p
}

func buildHeaders(req *adapter.UpstreamRequest) map[string]string {
	headers := map[string]string{"Authorization": "Bearer " + req.APIKey}
	for k, v := range req.ExtraHeaders {
		headers[k] = v
	}
	return headers
}
