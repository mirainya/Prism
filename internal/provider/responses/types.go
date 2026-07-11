package responses

import "encoding/json"

type Request struct {
	Model                string                     `json:"model"`
	Input                json.RawMessage            `json:"input"`
	Conversation         json.RawMessage            `json:"conversation,omitempty"`
	Instructions         string                     `json:"instructions,omitempty"`
	Stream               bool                       `json:"stream,omitempty"`
	Store                *bool                      `json:"store,omitempty"`
	Background           bool                       `json:"background,omitempty"`
	PreviousResponseID   string                     `json:"previous_response_id,omitempty"`
	Tools                json.RawMessage            `json:"tools,omitempty"`
	ToolChoice           json.RawMessage            `json:"tool_choice,omitempty"`
	ParallelToolCalls    *bool                      `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens      int                        `json:"max_output_tokens,omitempty"`
	MaxToolCalls         *int                       `json:"max_tool_calls,omitempty"`
	Temperature          *float64                   `json:"temperature,omitempty"`
	TopP                 *float64                   `json:"top_p,omitempty"`
	TopLogprobs          *int                       `json:"top_logprobs,omitempty"`
	Reasoning            json.RawMessage            `json:"reasoning,omitempty"`
	Thinking             json.RawMessage            `json:"thinking,omitempty"`
	Caching              json.RawMessage            `json:"caching,omitempty"`
	Text                 json.RawMessage            `json:"text,omitempty"`
	Prompt               json.RawMessage            `json:"prompt,omitempty"`
	StreamOptions        json.RawMessage            `json:"stream_options,omitempty"`
	ContextManagement    json.RawMessage            `json:"context_management,omitempty"`
	Metadata             map[string]string          `json:"metadata,omitempty"`
	Include              []string                   `json:"include,omitempty"`
	Truncation           string                     `json:"truncation,omitempty"`
	ServiceTier          string                     `json:"service_tier,omitempty"`
	PromptCacheKey       string                     `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string                     `json:"prompt_cache_retention,omitempty"`
	SafetyIdentifier     string                     `json:"safety_identifier,omitempty"`
	User                 string                     `json:"user,omitempty"`
	ExpireAt             *int64                     `json:"expire_at,omitempty"`
	Session              json.RawMessage            `json:"session,omitempty"`
	ExtraFields          map[string]json.RawMessage `json:"-"`
}

type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type OutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type Usage struct {
	InputTokens         int                        `json:"input_tokens"`
	InputTokensDetails  *InputTokensDetails        `json:"input_tokens_details,omitempty"`
	OutputTokens        int                        `json:"output_tokens"`
	OutputTokensDetails *OutputTokensDetails       `json:"output_tokens_details,omitempty"`
	TotalTokens         int                        `json:"total_tokens"`
	ExtraFields         map[string]json.RawMessage `json:"-"`
}

type Error struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Param   any    `json:"param,omitempty"`
}

type Response struct {
	ID                   string                     `json:"id"`
	Object               string                     `json:"object"`
	CreatedAt            int64                      `json:"created_at"`
	Status               string                     `json:"status"`
	Background           bool                       `json:"background"`
	Conversation         json.RawMessage            `json:"conversation,omitempty"`
	Error                *Error                     `json:"error"`
	IncompleteDetails    json.RawMessage            `json:"incomplete_details"`
	Instructions         any                        `json:"instructions"`
	MaxOutputTokens      *int                       `json:"max_output_tokens"`
	MaxToolCalls         *int                       `json:"max_tool_calls,omitempty"`
	Model                string                     `json:"model"`
	Output               json.RawMessage            `json:"output"`
	ParallelToolCalls    bool                       `json:"parallel_tool_calls"`
	PreviousResponseID   *string                    `json:"previous_response_id"`
	Reasoning            json.RawMessage            `json:"reasoning,omitempty"`
	Thinking             json.RawMessage            `json:"thinking,omitempty"`
	Caching              json.RawMessage            `json:"caching,omitempty"`
	PromptCacheKey       string                     `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string                     `json:"prompt_cache_retention,omitempty"`
	SafetyIdentifier     string                     `json:"safety_identifier,omitempty"`
	ServiceTier          string                     `json:"service_tier,omitempty"`
	Store                bool                       `json:"store"`
	Temperature          *float64                   `json:"temperature"`
	Text                 json.RawMessage            `json:"text,omitempty"`
	ToolChoice           json.RawMessage            `json:"tool_choice,omitempty"`
	Tools                json.RawMessage            `json:"tools"`
	TopP                 *float64                   `json:"top_p"`
	TopLogprobs          *int                       `json:"top_logprobs,omitempty"`
	Truncation           string                     `json:"truncation"`
	Usage                *Usage                     `json:"usage"`
	User                 string                     `json:"user,omitempty"`
	Metadata             map[string]string          `json:"metadata,omitempty"`
	ExpireAt             *int64                     `json:"expire_at,omitempty"`
	ContextManagement    json.RawMessage            `json:"context_management,omitempty"`
	Session              json.RawMessage            `json:"session,omitempty"`
	ServiceStatus        json.RawMessage            `json:"service_status,omitempty"`
	ExtraFields          map[string]json.RawMessage `json:"-"`
}

type List struct {
	Object  string            `json:"object"`
	Data    []json.RawMessage `json:"data"`
	FirstID string            `json:"first_id,omitempty"`
	LastID  string            `json:"last_id,omitempty"`
	HasMore bool              `json:"has_more"`
}

type Deleted struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}
