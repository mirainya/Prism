package pipeline

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider/chat"
	"gorm.io/datatypes"
)

const ThinkingLevelHeader = "X-Prism-Thinking-Level"

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

// ApplyModelThinkingLevel resolves a model option after route selection and
// normalizes its upstream body for the selected transport.
func ApplyModelThinkingLevel(request canonical.Request, modelName string, transportID transport.ID, level string) (canonical.Request, error) {
	level = strings.TrimSpace(level)
	if level == "" {
		return request, nil
	}
	meta := loadModelMeta(modelName)
	if meta == nil {
		return canonical.Request{}, domain.ErrBadRequest("thinking level requires a model thinking configuration")
	}
	cfg := parseThinkingConfig(meta.ThinkingConfig)
	if cfg == nil {
		return canonical.Request{}, domain.ErrBadRequest("thinking level is not configured for this model")
	}
	selected := level
	if cfg.Locked {
		selected = cfg.Default
	}
	option := cfg.findOption(selected)
	if option == nil {
		return canonical.Request{}, domain.ErrBadRequest(fmt.Sprintf("thinking level %q is not configured for this model", selected))
	}
	if len(option.Body) == 0 {
		return request, nil
	}

	request = request.Clone()
	request.ClientExtensions = cloneClientExtensions(request.ClientExtensions)
	extras := make(map[string]json.RawMessage)
	if raw := request.ClientExtensions["openai_chat.request_extras"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &extras); err != nil {
			return canonical.Request{}, err
		}
	}
	for key, value := range option.Body {
		raw, err := json.Marshal(value)
		if err != nil {
			return canonical.Request{}, err
		}
		extras[key] = raw
	}
	encoded, err := json.Marshal(extras)
	if err != nil {
		return canonical.Request{}, err
	}
	request.ClientExtensions["openai_chat.request_extras"] = encoded
	if err := adaptChatExtensions(&request, transportID); err != nil {
		return canonical.Request{}, err
	}
	return request, nil
}

// loadModelMeta 读 gw_model_meta(元数据面);缺失返回 nil,不影响路由。
func loadModelMeta(modelName string) *model.GwModelMeta {
	var meta model.GwModelMeta
	if err := model.DB().Where("model_name = ?", modelName).First(&meta).Error; err != nil {
		return nil
	}
	return &meta
}
