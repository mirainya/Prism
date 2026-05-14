package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/pkg/httputil"
)

// OpenAIProvider OpenAI 及兼容 API
type OpenAIProvider struct {
	config ProviderConfig
}

func NewOpenAIProvider(config ProviderConfig) *OpenAIProvider {
	return &OpenAIProvider{config: config}
}

func (p *OpenAIProvider) Name() string {
	return "openai"
}

func (p *OpenAIProvider) Complete(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// 替换模型名
	req.Model = p.config.VendorModel

	// 构建 URL
	url := p.config.BaseURL + p.config.RequestPath

	// 构建请求头
	headers := map[string]string{
		"Authorization": "Bearer " + p.config.APIKey,
	}
	for k, v := range p.config.ExtraHeaders {
		headers[k] = v
	}

	// 设置超时上下文
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	// 发送请求
	resp, err := httputil.PostJSON(ctx, url, req, headers)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// 解析响应
	var chatResp ChatResponse
	if err := json.Unmarshal(resp, &chatResp); err != nil {
		return nil, fmt.Errorf("unmarshal response failed: %w", err)
	}

	return &chatResp, nil
}

func (p *OpenAIProvider) StreamComplete(ctx context.Context, req *ChatRequest) (*http.Response, error) {
	req.Model = p.config.VendorModel
	req.Stream = true

	url := p.config.BaseURL + p.config.RequestPath

	headers := map[string]string{
		"Authorization": "Bearer " + p.config.APIKey,
	}
	for k, v := range p.config.ExtraHeaders {
		headers[k] = v
	}

	// 流式请求不设置 context timeout，生命周期由 handler 管理
	resp, err := httputil.PostJSONStream(ctx, url, req, headers)
	if err != nil {
		return nil, fmt.Errorf("stream request failed: %w", err)
	}

	return resp, nil
}

func (p *OpenAIProvider) ListModels(ctx context.Context) ([]domain.ModelInfo, error) {
	url := p.config.BaseURL + "/v1/models"

	headers := map[string]string{
		"Authorization": "Bearer " + p.config.APIKey,
	}

	resp, err := httputil.Get(ctx, url, headers)
	if err != nil {
		return nil, fmt.Errorf("list models failed: %w", err)
	}

	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("unmarshal models failed: %w", err)
	}

	models := make([]domain.ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, domain.ModelInfo{
			ID:       m.ID,
			Name:     m.ID,
			Provider: p.Name(),
			Type:     "chat",
			RawMeta:  map[string]any{"owned_by": m.OwnedBy},
		})
	}
	return models, nil
}
