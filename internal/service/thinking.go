package service

import (
	"encoding/json"

	"github.com/mirainya/Prism/internal/provider/chat"
	"gorm.io/datatypes"
)

// 思考模式配置化
//
// 理念:思考档位是"模型数据"而非全局代码常量。不同模型档位数量/取值差异巨大
// (火山5档 / Gemini2档 / DeepSeek多档 / Claude用token预算),故由每个模型自带
// options 列表声明。每档的 body 是"直接合并进上游请求体的原始 JSON",字段名已按
// 各协议写好(reasoning.effort / reasoning_effort / thinking...),后端零翻译、零协议判断。

// ThinkingConfig 模型的思考模式配置 (存 model.thinking_config)
type ThinkingConfig struct {
	Locked  bool             `json:"locked"`  // true=禁止会话/请求覆盖,强制用 Default
	Default string           `json:"default"` // 默认选中的档位 value
	Options []ThinkingOption `json:"options"` // 支持的档位,数量任意
}

// ThinkingOption 单个思考档位
type ThinkingOption struct {
	Label string         `json:"label"`          // 展示名(如"关闭"/"深度思考")
	Value string         `json:"value"`          // 档位标识(如 off/medium/max)
	Body  map[string]any `json:"body,omitempty"` // 合并进请求体的原始 JSON,空=不注入
}

// parseThinkingConfig 解析模型的思考配置,无配置/无 options 返回 nil
func parseThinkingConfig(raw datatypes.JSON) *ThinkingConfig {
	if len(raw) == 0 {
		return nil
	}
	var cfg ThinkingConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Options) == 0 {
		return nil
	}
	return &cfg
}

// findOption 按 value 查档位,未命中返回 nil
func (c *ThinkingConfig) findOption(value string) *ThinkingOption {
	for i := range c.Options {
		if c.Options[i].Value == value {
			return &c.Options[i]
		}
	}
	return nil
}

// applyThinking 按"模型默认 + 请求覆盖(未锁时)"选定档位,把其 body 合并进 chatReq.ExtraBody。
// requestLevel 为请求级覆盖(nil=未指定)。cfg 为 nil 时不做任何注入。
func applyThinking(chatReq *chat.ChatRequest, cfg *ThinkingConfig, requestLevel *string) {
	if cfg == nil {
		return
	}

	// 定选中档位: 未锁 + 请求指定了合法档位 -> 用请求的; 否则用模型默认
	var opt *ThinkingOption
	if !cfg.Locked && requestLevel != nil {
		opt = cfg.findOption(*requestLevel)
	}
	if opt == nil {
		opt = cfg.findOption(cfg.Default)
	}
	if opt == nil || len(opt.Body) == 0 {
		return // 无有效档位或该档不注入(厂商默认)
	}

	if chatReq.ExtraBody == nil {
		chatReq.ExtraBody = map[string]any{}
	}
	for k, v := range opt.Body {
		chatReq.ExtraBody[k] = v
	}
}
