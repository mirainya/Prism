package responses

import "encoding/json"

var requestJSONFields = fieldSet(
	"model", "input", "conversation", "instructions", "stream", "store", "background", "previous_response_id",
	"tools", "tool_choice", "parallel_tool_calls", "max_output_tokens", "max_tool_calls", "temperature", "top_p",
	"top_logprobs", "reasoning", "thinking", "caching", "text", "prompt", "stream_options", "context_management",
	"metadata", "include", "truncation", "service_tier", "prompt_cache_key", "prompt_cache_retention",
	"safety_identifier", "user", "expire_at", "session",
)

var usageJSONFields = fieldSet(
	"input_tokens", "input_tokens_details", "output_tokens", "output_tokens_details", "total_tokens",
)

var responseJSONFields = fieldSet(
	"id", "object", "created_at", "status", "background", "conversation", "error", "incomplete_details", "instructions",
	"max_output_tokens", "max_tool_calls", "model", "output", "parallel_tool_calls", "previous_response_id", "reasoning",
	"thinking", "caching", "prompt_cache_key", "prompt_cache_retention", "safety_identifier", "service_tier", "store",
	"temperature", "text", "tool_choice", "tools", "top_p", "top_logprobs", "truncation", "usage", "user", "metadata",
	"expire_at", "context_management", "session", "service_status",
)

func (r *Request) UnmarshalJSON(data []byte) error {
	type requestAlias Request
	var decoded requestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	extras, err := collectExtraFields(data, requestJSONFields)
	if err != nil {
		return err
	}
	decoded.ExtraFields = extras
	*r = Request(decoded)
	return nil
}

func (r Request) MarshalJSON() ([]byte, error) {
	type requestAlias Request
	return marshalWithExtraFields(requestAlias(r), r.ExtraFields)
}

func (u *Usage) UnmarshalJSON(data []byte) error {
	type usageAlias Usage
	var decoded usageAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	extras, err := collectExtraFields(data, usageJSONFields)
	if err != nil {
		return err
	}
	decoded.ExtraFields = extras
	*u = Usage(decoded)
	return nil
}

func (u Usage) MarshalJSON() ([]byte, error) {
	type usageAlias Usage
	return marshalWithExtraFields(usageAlias(u), u.ExtraFields)
}

func (r *Response) UnmarshalJSON(data []byte) error {
	type responseAlias Response
	var decoded responseAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	extras, err := collectExtraFields(data, responseJSONFields)
	if err != nil {
		return err
	}
	decoded.ExtraFields = extras
	*r = Response(decoded)
	return nil
}

func (r Response) MarshalJSON() ([]byte, error) {
	type responseAlias Response
	return marshalWithExtraFields(responseAlias(r), r.ExtraFields)
}

func fieldSet(names ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}
	return result
}

func collectExtraFields(data []byte, known map[string]struct{}) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	for name := range known {
		delete(fields, name)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return fields, nil
}

func marshalWithExtraFields(value any, extras map[string]json.RawMessage) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(extras) == 0 {
		return encoded, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	for name, raw := range extras {
		if _, exists := fields[name]; exists || !json.Valid(raw) {
			continue
		}
		fields[name] = raw
	}
	return json.Marshal(fields)
}
