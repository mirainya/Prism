package service

import (
	"testing"

	"github.com/mirainya/Prism/internal/provider/chat"
	"gorm.io/datatypes"
)

// 构造一个通用思考配置(火山3档 + 厂商默认档)
func sampleVolcConfig(locked bool, def string) *ThinkingConfig {
	return &ThinkingConfig{
		Locked:  locked,
		Default: def,
		Options: []ThinkingOption{
			{Label: "厂商默认", Value: "default", Body: nil},
			{Label: "关闭", Value: "off", Body: map[string]any{"reasoning": map[string]any{"effort": "minimal"}}},
			{Label: "中", Value: "medium", Body: map[string]any{"reasoning": map[string]any{"effort": "medium"}}},
			{Label: "最高", Value: "max", Body: map[string]any{"reasoning": map[string]any{"effort": "max"}}},
		},
	}
}

func strPtr(s string) *string { return &s }

func TestApplyThinking_NilConfig(t *testing.T) {
	req := &chat.ChatRequest{}
	applyThinking(req, nil, strPtr("high"))
	if req.ExtraBody != nil {
		t.Fatalf("nil config 不应注入,得到 %v", req.ExtraBody)
	}
}

func TestApplyThinking_ModelDefault(t *testing.T) {
	req := &chat.ChatRequest{}
	applyThinking(req, sampleVolcConfig(false, "medium"), nil)
	r, ok := req.ExtraBody["reasoning"].(map[string]any)
	if !ok || r["effort"] != "medium" {
		t.Fatalf("应使用模型默认 medium,得到 %v", req.ExtraBody)
	}
}

func TestApplyThinking_RequestOverride(t *testing.T) {
	req := &chat.ChatRequest{}
	applyThinking(req, sampleVolcConfig(false, "medium"), strPtr("max"))
	r := req.ExtraBody["reasoning"].(map[string]any)
	if r["effort"] != "max" {
		t.Fatalf("未锁定应被请求级 max 覆盖,得到 %v", req.ExtraBody)
	}
}

func TestApplyThinking_Locked(t *testing.T) {
	req := &chat.ChatRequest{}
	applyThinking(req, sampleVolcConfig(true, "medium"), strPtr("max"))
	r := req.ExtraBody["reasoning"].(map[string]any)
	if r["effort"] != "medium" {
		t.Fatalf("锁定时应忽略请求级,仍用 medium,得到 %v", req.ExtraBody)
	}
}

func TestApplyThinking_DefaultOptionNoInject(t *testing.T) {
	req := &chat.ChatRequest{}
	applyThinking(req, sampleVolcConfig(false, "default"), nil)
	if req.ExtraBody != nil {
		t.Fatalf("厂商默认档(body空)不应注入,得到 %v", req.ExtraBody)
	}
}

func TestApplyThinking_UnknownRequestFallback(t *testing.T) {
	req := &chat.ChatRequest{}
	applyThinking(req, sampleVolcConfig(false, "medium"), strPtr("nonexist"))
	r := req.ExtraBody["reasoning"].(map[string]any)
	if r["effort"] != "medium" {
		t.Fatalf("请求传非法档位应回退默认 medium,得到 %v", req.ExtraBody)
	}
}

// 验证各协议原生写法都能原样合并(后端零翻译)
func TestApplyThinking_OpenAIBody(t *testing.T) {
	cfg := &ThinkingConfig{
		Default: "high",
		Options: []ThinkingOption{
			{Value: "high", Body: map[string]any{"reasoning_effort": "high"}},
		},
	}
	req := &chat.ChatRequest{}
	applyThinking(req, cfg, nil)
	if req.ExtraBody["reasoning_effort"] != "high" {
		t.Fatalf("openai reasoning_effort 应原样注入,得到 %v", req.ExtraBody)
	}
}

func TestApplyThinking_AnthropicBody(t *testing.T) {
	cfg := &ThinkingConfig{
		Default: "on",
		Options: []ThinkingOption{
			{Value: "on", Body: map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 16384}}},
		},
	}
	req := &chat.ChatRequest{}
	applyThinking(req, cfg, nil)
	th, ok := req.ExtraBody["thinking"].(map[string]any)
	if !ok || th["budget_tokens"] != 16384 {
		t.Fatalf("anthropic thinking 应原样注入,得到 %v", req.ExtraBody)
	}
}

// 已有 extra_body 时思考字段应叠加而非覆盖
func TestApplyThinking_MergeIntoExisting(t *testing.T) {
	req := &chat.ChatRequest{ExtraBody: map[string]any{"foo": "bar"}}
	applyThinking(req, sampleVolcConfig(false, "medium"), nil)
	if req.ExtraBody["foo"] != "bar" {
		t.Fatalf("原有 extra_body 字段丢失: %v", req.ExtraBody)
	}
	if req.ExtraBody["reasoning"] == nil {
		t.Fatalf("思考字段未叠加: %v", req.ExtraBody)
	}
}

func TestParseThinkingConfig(t *testing.T) {
	if parseThinkingConfig(nil) != nil {
		t.Fatal("空 JSON 应返回 nil")
	}
	if parseThinkingConfig(datatypes.JSON([]byte(`{"options":[]}`))) != nil {
		t.Fatal("无 options 应返回 nil")
	}
	cfg := parseThinkingConfig(datatypes.JSON([]byte(`{"default":"a","options":[{"value":"a","body":{"x":1}}]}`)))
	if cfg == nil || cfg.Default != "a" || len(cfg.Options) != 1 {
		t.Fatalf("合法配置解析失败: %v", cfg)
	}
}

