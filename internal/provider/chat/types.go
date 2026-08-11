package chat

import (
	"encoding/json"
	"strings"
)

const MaxSupportedChoices = 16

type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	FileURL  *FileURL  `json:"file_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type FileURL struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type,omitempty"`
}

type ToolDefinition struct {
	Type     string          `json:"type"`
	Function FunctionDef     `json:"function"`
	Raw      json.RawMessage `json:"-"` // 完整原始 JSON，供非 function 类型工具透传
}

func (t *ToolDefinition) UnmarshalJSON(data []byte) error {
	type Alias ToolDefinition
	if err := json.Unmarshal(data, (*Alias)(t)); err != nil {
		return err
	}
	t.Raw = append(json.RawMessage(nil), data...)
	return nil
}

type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ResponseFormat struct {
	Type       string          `json:"type"`
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
}

type StreamOptions struct {
	IncludeUsage       bool  `json:"include_usage"`
	IncludeObfuscation *bool `json:"include_obfuscation,omitempty"`
}

type AudioConfig struct {
	Format string `json:"format"`
	Voice  any    `json:"voice"`
}

type Prediction struct {
	Type    string `json:"type"`
	Content any    `json:"content"`
}

type ChatRequest struct {
	Model               string            `json:"model"`
	Messages            []ChatMessage     `json:"messages"`
	Temperature         *float64          `json:"temperature,omitempty"`
	MaxTokens           int               `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int              `json:"max_completion_tokens,omitempty"`
	TopP                *float64          `json:"top_p,omitempty"`
	FrequencyPenalty    *float64          `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64          `json:"presence_penalty,omitempty"`
	Stop                []string          `json:"stop,omitempty"`
	Stream              bool              `json:"stream,omitempty"`
	StreamOptions       *StreamOptions    `json:"stream_options,omitempty"`
	N                   *int              `json:"n,omitempty"`
	Logprobs            *bool             `json:"logprobs,omitempty"`
	TopLogprobs         *int              `json:"top_logprobs,omitempty"`
	Tools               []ToolDefinition  `json:"tools,omitempty"`
	ToolChoice          any               `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool             `json:"parallel_tool_calls,omitempty"`
	ResponseFormat      *ResponseFormat   `json:"response_format,omitempty"`
	Seed                *int              `json:"seed,omitempty"`
	User                string            `json:"user,omitempty"`
	Modalities          []string          `json:"modalities,omitempty"`
	Audio               *AudioConfig      `json:"audio,omitempty"`
	Prediction          *Prediction       `json:"prediction,omitempty"`
	Store               *bool             `json:"store,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
	ServiceTier         *string           `json:"service_tier,omitempty"`
	ExtraBody           map[string]any    `json:"-"`
}

func (r ChatRequest) MarshalJSON() ([]byte, error) {
	type Alias ChatRequest
	base, err := json.Marshal(Alias(r))
	if err != nil || len(r.ExtraBody) == 0 {
		return base, err
	}
	var merged map[string]any
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for key, value := range r.ExtraBody {
		merged[key] = value
	}
	return json.Marshal(merged)
}

type ChatMessage struct {
	Role             string          `json:"role"`
	Content          any             `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Name             string          `json:"name,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	Refusal          *string         `json:"refusal,omitempty"`
	Annotations      json.RawMessage `json:"annotations,omitempty"`
	Audio            json.RawMessage `json:"audio,omitempty"`
}

func (m *ChatMessage) ContentText() string {
	if m.Content == nil {
		return ""
	}
	switch value := m.Content.(type) {
	case string:
		return value
	case []any:
		var texts []string
		for _, rawPart := range value {
			part, ok := rawPart.(map[string]any)
			if !ok || part["type"] != "text" {
				continue
			}
			if text, ok := part["text"].(string); ok {
				texts = append(texts, text)
			}
		}
		return strings.Join(texts, "\n")
	default:
		return ""
	}
}

func (m *ChatMessage) ContentAttachments() string {
	parts, ok := m.Content.([]any)
	if !ok {
		return ""
	}
	var attachments []any
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if ok && part["type"] != "text" {
			attachments = append(attachments, part)
		}
	}
	if len(attachments) == 0 {
		return ""
	}
	data, err := json.Marshal(attachments)
	if err != nil {
		return ""
	}
	return string(data)
}

type ChatResponse struct {
	ID                string       `json:"id"`
	Object            string       `json:"object"`
	Created           int64        `json:"created"`
	Model             string       `json:"model"`
	Choices           []ChatChoice `json:"choices"`
	Usage             *ChatUsage   `json:"usage,omitempty"`
	SystemFingerprint string       `json:"system_fingerprint,omitempty"`
	ServiceTier       string       `json:"service_tier,omitempty"`
}

type ChatChoice struct {
	Index        int             `json:"index"`
	Message      ChatMessage     `json:"message"`
	FinishReason string          `json:"finish_reason"`
	Logprobs     json.RawMessage `json:"logprobs,omitempty"`
}

type ChatUsage struct {
	PromptTokens            int             `json:"prompt_tokens"`
	CompletionTokens        int             `json:"completion_tokens"`
	TotalTokens             int             `json:"total_tokens"`
	PromptTokensDetails     json.RawMessage `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails json.RawMessage `json:"completion_tokens_details,omitempty"`
}
