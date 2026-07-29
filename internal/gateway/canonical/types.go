// Package canonical defines the protocol-neutral contract used by Gateway V2.
package canonical

import "encoding/json"

// Endpoint identifies a downstream wire protocol.
type Endpoint string

const (
	EndpointOpenAIChat      Endpoint = "openai_chat"
	EndpointOpenAIResponses Endpoint = "openai_responses"
	EndpointAnthropic       Endpoint = "anthropic_messages"
)

// Feature is a semantic model capability. Wire protocols and upstream paths are
// represented by codecs and transports, not by features.
type Feature string

const (
	FeatureStream           Feature = "stream"
	FeatureVision           Feature = "vision"
	FeatureFiles            Feature = "files"
	FeatureAudio            Feature = "audio"
	FeatureVideo            Feature = "video"
	FeatureTools            Feature = "tools"
	FeatureStructuredOutput Feature = "structured_output"
	FeatureReasoning        Feature = "reasoning"
	FeatureBackground       Feature = "background"
	FeatureWebSearch        Feature = "web_search"
	FeatureFileSearch       Feature = "file_search"
	FeatureCodeInterpreter  Feature = "code_interpreter"
	FeatureComputerUse      Feature = "computer_use"
	FeatureImageGeneration  Feature = "image_generation"
)

// FeatureSet is the exact semantic feature set required or provided by a route.
type FeatureSet map[Feature]bool

func NewFeatureSet(features ...Feature) FeatureSet {
	set := make(FeatureSet, len(features))
	for _, feature := range features {
		set.Add(feature)
	}
	return set
}

func (s FeatureSet) Add(feature Feature) {
	if s != nil {
		s[feature] = true
	}
}

func (s FeatureSet) Has(feature Feature) bool { return s != nil && s[feature] }

func (s FeatureSet) Contains(required FeatureSet) bool {
	for feature, needed := range required {
		if needed && !s.Has(feature) {
			return false
		}
	}
	return true
}

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Content represents one protocol-neutral multimodal content block.
type Content struct {
	Type       string                     `json:"type"`
	Text       string                     `json:"text,omitempty"`
	URL        string                     `json:"url,omitempty"`
	Data       string                     `json:"data,omitempty"`
	FileID     string                     `json:"file_id,omitempty"`
	Filename   string                     `json:"filename,omitempty"`
	MediaType  string                     `json:"media_type,omitempty"`
	Format     string                     `json:"format,omitempty"`
	Detail     string                     `json:"detail,omitempty"`
	Transcript string                     `json:"transcript,omitempty"`
	Extra      map[string]json.RawMessage `json:"extra,omitempty"`
}

// Item represents a message, tool call/result, reasoning item, or another
// Responses-style input/output item.
type Item struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	Role      Role   `json:"role,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	// ProviderCallIDOmitted records that the signed provider part did not
	// contain an ID even though Prism assigned a public call_id.
	ProviderCallIDOmitted bool                       `json:"provider_call_id_omitted,omitempty"`
	Content               []Content                  `json:"content,omitempty"`
	Arguments             json.RawMessage            `json:"arguments,omitempty"`
	Output                json.RawMessage            `json:"output,omitempty"`
	Status                string                     `json:"status,omitempty"`
	Proof                 *ProviderProof             `json:"proof,omitempty"`
	Extra                 map[string]json.RawMessage `json:"extra,omitempty"`
}

type Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Namespace   string          `json:"namespace,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
	Options     json.RawMessage `json:"options,omitempty"`
}

type ToolChoice struct {
	Mode string          `json:"mode,omitempty"`
	Type string          `json:"type,omitempty"`
	Name string          `json:"name,omitempty"`
	Raw  json.RawMessage `json:"raw,omitempty"`
}

type ResponseFormat struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
}

type Reasoning struct {
	Effort  string          `json:"effort,omitempty"`
	Summary string          `json:"summary,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// VolcengineOptions contains documented Ark Responses v3 extensions. Unknown
// fields are kept separately and may only be used by the native v3 transport.
type VolcengineOptions struct {
	Thinking          json.RawMessage            `json:"thinking,omitempty"`
	Caching           json.RawMessage            `json:"caching,omitempty"`
	Session           json.RawMessage            `json:"session,omitempty"`
	ContextManagement json.RawMessage            `json:"context_management,omitempty"`
	ExpireAt          *int64                     `json:"expire_at,omitempty"`
	Unknown           map[string]json.RawMessage `json:"unknown,omitempty"`
}

type ProviderOptions struct {
	Volcengine *VolcengineOptions `json:"volcengine,omitempty"`
}

// Request is the only request type accepted by the Gateway V2 engine.
type Request struct {
	Endpoint Endpoint `json:"endpoint"`
	Model    string   `json:"model"`
	Items    []Item   `json:"items"`

	Instructions      string          `json:"instructions,omitempty"`
	Tools             []Tool          `json:"tools,omitempty"`
	ToolChoice        *ToolChoice     `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	ResponseFormat    *ResponseFormat `json:"response_format,omitempty"`
	Reasoning         *Reasoning      `json:"reasoning,omitempty"`

	Stream             bool              `json:"stream,omitempty"`
	Store              *bool             `json:"store,omitempty"`
	ServiceTier        string            `json:"service_tier,omitempty"`
	Background         bool              `json:"background,omitempty"`
	PreviousResponseID string            `json:"previous_response_id,omitempty"`
	MaxOutputTokens    *int              `json:"max_output_tokens,omitempty"`
	Temperature        *float64          `json:"temperature,omitempty"`
	TopP               *float64          `json:"top_p,omitempty"`
	Stop               []string          `json:"stop,omitempty"`
	User               string            `json:"user,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Include            []string          `json:"include,omitempty"`
	Modalities         []string          `json:"modalities,omitempty"`
	TransportHints     []string          `json:"transport_hints,omitempty"`

	ProviderOptions  ProviderOptions            `json:"provider_options,omitempty"`
	ClientExtensions map[string]json.RawMessage `json:"client_extensions,omitempty"`
}

func (r *Request) RequiredFeatures() FeatureSet {
	required := FeatureSet{}
	if r == nil {
		return required
	}
	if r.Stream {
		required.Add(FeatureStream)
	}
	if r.Background {
		required.Add(FeatureBackground)
	}
	if len(r.Tools) > 0 || r.ToolChoice != nil {
		required.Add(FeatureTools)
	}
	if r.ResponseFormat != nil {
		required.Add(FeatureStructuredOutput)
	}
	if r.Reasoning != nil {
		required.Add(FeatureReasoning)
	}
	for _, modality := range r.Modalities {
		if modality == "audio" {
			required.Add(FeatureAudio)
		}
	}
	for _, item := range r.Items {
		if item.Type == "reasoning" && item.Proof == nil {
			required.Add(FeatureReasoning)
		}
		for _, content := range item.Content {
			switch content.Type {
			case "input_image", "output_image", "image", "image_url":
				required.Add(FeatureVision)
			case "input_file", "output_file", "file", "document":
				required.Add(FeatureFiles)
			case "input_audio", "output_audio", "audio":
				required.Add(FeatureAudio)
			case "input_video", "output_video", "video":
				required.Add(FeatureVideo)
			}
		}
	}
	for _, tool := range r.Tools {
		switch tool.Type {
		case "web_search", "web_search_preview":
			required.Add(FeatureWebSearch)
		case "file_search":
			required.Add(FeatureFileSearch)
		case "code_interpreter":
			required.Add(FeatureCodeInterpreter)
		case "computer", "computer_use_preview":
			required.Add(FeatureComputerUse)
		case "image_generation":
			required.Add(FeatureImageGeneration)
		}
	}
	return required
}

type Usage struct {
	InputTokens           int                        `json:"input_tokens"`
	OutputTokens          int                        `json:"output_tokens"`
	TotalTokens           int                        `json:"total_tokens"`
	CachedInputTokens     int                        `json:"cached_input_tokens,omitempty"`
	ReasoningOutputTokens int                        `json:"reasoning_output_tokens,omitempty"`
	Extra                 map[string]json.RawMessage `json:"extra,omitempty"`
}

type Error struct {
	Status    int             `json:"status,omitempty"`
	Type      string          `json:"type,omitempty"`
	Code      string          `json:"code,omitempty"`
	Message   string          `json:"message"`
	Param     any             `json:"param,omitempty"`
	Retryable bool            `json:"retryable,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

type Response struct {
	ID                 string                     `json:"id"`
	ProviderResponseID string                     `json:"provider_response_id,omitempty"`
	Model              string                     `json:"model"`
	Status             string                     `json:"status"`
	CreatedAt          int64                      `json:"created_at"`
	Output             []Item                     `json:"output"`
	Usage              *Usage                     `json:"usage,omitempty"`
	Error              *Error                     `json:"error,omitempty"`
	FinishReason       string                     `json:"finish_reason,omitempty"`
	IncompleteDetails  json.RawMessage            `json:"incomplete_details,omitempty"`
	Metadata           map[string]string          `json:"metadata,omitempty"`
	ProviderExtensions map[string]json.RawMessage `json:"provider_extensions,omitempty"`
}
