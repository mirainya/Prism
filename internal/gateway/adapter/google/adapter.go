// Package google 实现 Gemini API 适配器。
// 复用现有 chat.GoogleProvider 的 convertRequest/convertResponse(自包含,不重复实现)。
// 流式当前是 stub(Gemini 流式未接入),返回明确的 not-implemented 错误。
package google

import (
	"context"
	"net/http"
	"time"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
)

// Adapter Gemini 协议适配器。无状态。
type Adapter struct{}

// New 构造适配器。
func New() adapter.UpstreamAdapter { return &Adapter{} }

func (a *Adapter) Protocol() model.Protocol { return model.ProtocolGoogle }

func (a *Adapter) Complete(ctx context.Context, req *adapter.UpstreamRequest) (*adapter.UpstreamResponse, error) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	p := chat.NewGoogleProvider(chat.ProviderConfig{
		BaseURL:      req.BaseURL,
		APIKey:       req.APIKey,
		VendorModel:  req.VendorModel,
		Timeout:      timeout,
		ExtraHeaders: req.ExtraHeaders,
	})
	req.Chat.Model = req.VendorModel
	chatResp, err := p.Complete(ctx, req.Chat)
	if err != nil {
		return nil, err
	}
	return &adapter.UpstreamResponse{Chat: chatResp}, nil
}

func (a *Adapter) StreamComplete(ctx context.Context, req *adapter.UpstreamRequest) (*http.Response, error) {
	// Gemini 流式未接入。返回明确错误,pipeline 视为 400 类终态(不重试)。
	p := chat.NewGoogleProvider(chat.ProviderConfig{
		BaseURL:     req.BaseURL,
		APIKey:      req.APIKey,
		VendorModel: req.VendorModel,
	})
	return p.StreamComplete(ctx, req.Chat)
}
