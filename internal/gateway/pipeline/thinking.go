package pipeline

import (
	"encoding/json"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"gorm.io/datatypes"
)

// thinkingConfig 模型思考配置(存 gw_model_meta.thinking_config)。移植自 service.ThinkingConfig。
type thinkingConfig struct {
	Locked  bool             `json:"locked"`
	Default string           `json:"default"`
	Options []thinkingOption `json:"options"`
}

type thinkingOption struct {
	Label string         `json:"label"`
	Value string         `json:"value"`
	Body  map[string]any `json:"body,omitempty"`
}

func parseThinkingConfig(raw datatypes.JSON) *thinkingConfig {
	if len(raw) == 0 {
		return nil
	}
	var cfg thinkingConfig
	if err := json.Unmarshal(raw, &cfg); err != nil || len(cfg.Options) == 0 {
		return nil
	}
	return &cfg
}

func (c *thinkingConfig) findOption(value string) *thinkingOption {
	for i := range c.Options {
		if c.Options[i].Value == value {
			return &c.Options[i]
		}
	}
	return nil
}

// applyThinking 按「模型默认 + 请求覆盖(未锁时)」选定档位,把其 body 合并进 ExtraBody。
func applyThinking(chatReq *chat.ChatRequest, cfg *thinkingConfig, requestLevel *string) {
	if cfg == nil {
		return
	}
	var opt *thinkingOption
	if !cfg.Locked && requestLevel != nil {
		opt = cfg.findOption(*requestLevel)
	}
	if opt == nil {
		opt = cfg.findOption(cfg.Default)
	}
	if opt == nil || len(opt.Body) == 0 {
		return
	}
	if chatReq.ExtraBody == nil {
		chatReq.ExtraBody = map[string]any{}
	}
	for k, v := range opt.Body {
		chatReq.ExtraBody[k] = v
	}
}

// loadModelMeta 读 gw_model_meta(元数据面);缺失返回 nil,不影响路由。
func loadModelMeta(modelName string) *model.GwModelMeta {
	var meta model.GwModelMeta
	if err := model.DB().Where("model_name = ?", modelName).First(&meta).Error; err != nil {
		return nil
	}
	return &meta
}
