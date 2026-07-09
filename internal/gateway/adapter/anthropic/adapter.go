// Package anthropic 实现 Anthropic Messages API 适配器。
// 请求: ChatRequest → Anthropic body(chat.ConvertRequestToAnthropic)。
// 响应: chat.ParseAnthropicResponse。流式: io.Pipe goroutine 翻译 Anthropic SSE → OpenAI SSE。
package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/pkg/httputil"
)

const (
	defaultPath      = "/v1/messages"
	anthropicVersion = "2023-06-01"
)

// Adapter Anthropic 协议适配器。无状态。
type Adapter struct{}

// New 构造适配器。
func New() adapter.UpstreamAdapter { return &Adapter{} }

func (a *Adapter) Protocol() model.Protocol { return model.ProtocolAnthropic }

func (a *Adapter) Complete(ctx context.Context, req *adapter.UpstreamRequest) (*adapter.UpstreamResponse, error) {
	req.Chat.Model = req.VendorModel
	req.Chat.Stream = false

	body := toRequestBody(req.Chat)
	url := req.BaseURL + resolvePath(req.RequestPath)
	headers := buildHeaders(req)

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	respBody, err := httputil.PostJSON(ctx, url, body, headers)
	if err != nil {
		return nil, fmt.Errorf("anthropic request failed: %w", err)
	}

	chatResp, err := chat.ParseAnthropicResponse(respBody, req.VendorModel)
	if err != nil {
		return nil, fmt.Errorf("anthropic parse response failed: %w", err)
	}
	return &adapter.UpstreamResponse{Chat: chatResp}, nil
}

func (a *Adapter) StreamComplete(ctx context.Context, req *adapter.UpstreamRequest) (*http.Response, error) {
	req.Chat.Model = req.VendorModel
	req.Chat.Stream = true

	body := toRequestBody(req.Chat)
	url := req.BaseURL + resolvePath(req.RequestPath)
	headers := buildHeaders(req)

	resp, err := httputil.PostJSONStream(ctx, url, body, headers)
	if err != nil {
		return nil, fmt.Errorf("anthropic stream request failed: %w", err)
	}
	// Body 翻译成 OpenAI SSE
	resp.Body = newStreamAdapter(resp.Body, req.VendorModel)
	return resp, nil
}

// toRequestBody ChatRequest → Anthropic Messages API 请求体。
func toRequestBody(chatReq *chat.ChatRequest) map[string]any {
	body := chat.ConvertRequestToAnthropic(chatReq)
	body["model"] = chatReq.Model
	if chatReq.Stream {
		body["stream"] = true
	}
	for k, v := range chatReq.ExtraBody {
		body[k] = v
	}
	return body
}

func resolvePath(p string) string {
	// Anthropic 固定 /v1/messages(端点路径可能配成 openai 路径,统一纠正)
	if p == "" || p == "/v1/chat/completions" {
		return defaultPath
	}
	return p
}

func buildHeaders(req *adapter.UpstreamRequest) map[string]string {
	headers := map[string]string{
		"x-api-key":         req.APIKey,
		"anthropic-version": anthropicVersion,
	}
	for k, v := range req.ExtraHeaders {
		headers[k] = v
	}
	return headers
}
