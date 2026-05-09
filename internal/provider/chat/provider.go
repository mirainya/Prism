package chat

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// ChatProvider LLM 请求适配接口
type ChatProvider interface {
	Complete(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	StreamComplete(ctx context.Context, req *ChatRequest) (*http.Response, error)
	Name() string
}

// ProviderConfig Provider 配置
type ProviderConfig struct {
	BaseURL      string
	APIKey       string
	VendorModel  string
	RequestPath  string
	Timeout      time.Duration
	ExtraHeaders map[string]string
}

// ---------- 注册表 ----------

type ProviderFactory func(ProviderConfig) ChatProvider

var providerRegistry = map[string]ProviderFactory{
	"openai":    func(c ProviderConfig) ChatProvider { return NewOpenAIProvider(c) },
	"anthropic": func(c ProviderConfig) ChatProvider { return NewAnthropicProvider(c) },
	"google":    func(c ProviderConfig) ChatProvider { return NewGoogleProvider(c) },
	"deepseek":  func(c ProviderConfig) ChatProvider { return NewOpenAIProvider(c) },
	"qwen":      func(c ProviderConfig) ChatProvider { return NewOpenAIProvider(c) },
	"moonshot":    func(c ProviderConfig) ChatProvider { return NewOpenAIProvider(c) },
	"volcengine":  func(c ProviderConfig) ChatProvider { return NewOpenAIProvider(c) },
}

// GetProvider 根据类型获取 Provider
func GetProvider(providerType string, config ProviderConfig) (ChatProvider, error) {
	factory, ok := providerRegistry[providerType]
	if !ok {
		return nil, fmt.Errorf("unknown provider type: %s", providerType)
	}

	// 设置默认超时
	if config.Timeout == 0 {
		config.Timeout = 120 * time.Second
	}

	// 设置默认请求路径
	if config.RequestPath == "" {
		config.RequestPath = "/v1/chat/completions"
	}

	return factory(config), nil
}

// RegisterProvider 注册新的 Provider
func RegisterProvider(name string, factory ProviderFactory) {
	providerRegistry[name] = factory
}
