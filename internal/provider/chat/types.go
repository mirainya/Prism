package chat

import "encoding/json"

// ---------- 多模态内容类型 ----------

// ContentPart 多模态内容块（文本/图片/音频/文件）
type ContentPart struct {
	Type     string    `json:"type"`                // "text", "image_url", "input_audio", "file_url"
	Text     string    `json:"text,omitempty"`      // type=text 时
	ImageURL *ImageURL `json:"image_url,omitempty"` // type=image_url 时
	FileURL  *FileURL  `json:"file_url,omitempty"`  // type=file_url 时
}

// ImageURL 图片 URL 或 base64
type ImageURL struct {
	URL    string `json:"url"`              // URL 或 "data:image/png;base64,..."
	Detail string `json:"detail,omitempty"` // "auto", "low", "high"
}

// FileURL 文件 URL（PDF/文档等）
type FileURL struct {
	URL         string `json:"url"`                    // 文件 URL
	ContentType string `json:"content_type,omitempty"` // MIME 类型
}

// ---------- Function Calling / Tool Use ----------

// ToolDefinition 工具定义
type ToolDefinition struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef 函数定义
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON Schema
}

// ToolCall 模型发起的工具调用
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall 函数调用详情
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ---------- Response Format ----------

// ResponseFormat 响应格式控制
type ResponseFormat struct {
	Type       string          `json:"type"`                  // "text", "json_object", "json_schema"
	JSONSchema json.RawMessage `json:"json_schema,omitempty"` // type=json_schema 时
}

// ---------- 请求/响应结构 ----------

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatRequest 统一请求格式
type ChatRequest struct {
	Model            string           `json:"model"`
	Messages         []ChatMessage    `json:"messages"`
	Temperature      *float64         `json:"temperature,omitempty"`
	MaxTokens        int              `json:"max_tokens,omitempty"`
	TopP             *float64         `json:"top_p,omitempty"`
	FrequencyPenalty *float64         `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64         `json:"presence_penalty,omitempty"`
	Stop             []string         `json:"stop,omitempty"`
	Stream           bool             `json:"stream,omitempty"`
	StreamOptions    *StreamOptions   `json:"stream_options,omitempty"`
	Tools            []ToolDefinition `json:"tools,omitempty"`
	ToolChoice       any              `json:"tool_choice,omitempty"` // "auto","none","required" 或 {"type":"function","function":{"name":"..."}}
	ResponseFormat   *ResponseFormat  `json:"response_format,omitempty"`
	Seed             *int             `json:"seed,omitempty"`
	User             string           `json:"user,omitempty"`

	// ExtraBody 额外请求体参数，序列化时合并到顶层 JSON
	// 用于支持各厂商特有字段（如 reasoning、max_output_tokens 等）
	ExtraBody map[string]any `json:"-"`
}

// MarshalJSON 自定义序列化：将 ExtraBody 中的字段合并到请求体顶层
func (r ChatRequest) MarshalJSON() ([]byte, error) {
	// 用别名避免递归调用
	type Alias ChatRequest
	base, err := json.Marshal(Alias(r))
	if err != nil {
		return nil, err
	}

	if len(r.ExtraBody) == 0 {
		return base, nil
	}

	// 将 base 反序列化为 map，合并 ExtraBody
	var merged map[string]any
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range r.ExtraBody {
		merged[k] = v
	}

	return json.Marshal(merged)
}

// ChatMessage 消息（Content 支持 string 或 []ContentPart）
type ChatMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`                     // string 或 []ContentPart
	ReasoningContent string     `json:"reasoning_content,omitempty"` // 模型思考过程（Gemini thinking / Claude extended thinking）
	Name             string     `json:"name,omitempty"`              // 可选的发送者名称
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`        // role=assistant 时，模型发起的工具调用
	ToolCallID       string     `json:"tool_call_id,omitempty"`      // role=tool 时，对应的 tool_call id
}

// ContentText 便捷方法：提取纯文本内容
func (m *ChatMessage) ContentText() string {
	if m.Content == nil {
		return ""
	}
	switch v := m.Content.(type) {
	case string:
		return v
	case []any:
		for _, part := range v {
			if pm, ok := part.(map[string]any); ok {
				if pm["type"] == "text" {
					if text, ok := pm["text"].(string); ok {
						return text
					}
				}
			}
		}
	}
	return ""
}

// ChatResponse 统一响应格式
type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   *ChatUsage   `json:"usage,omitempty"`
}

// ChatChoice 选项
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage Token 使用统计
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
