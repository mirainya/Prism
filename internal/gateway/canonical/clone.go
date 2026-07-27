package canonical

import "encoding/json"

// Clone returns a request whose mutable fields do not share storage with the source.
func (r Request) Clone() Request {
	clone := r
	clone.Items = CloneItems(r.Items)
	if r.Tools != nil {
		clone.Tools = make([]Tool, len(r.Tools))
		for i, tool := range r.Tools {
			clone.Tools[i] = tool
			clone.Tools[i].InputSchema = cloneRaw(tool.InputSchema)
			clone.Tools[i].Strict = clonePointer(tool.Strict)
			clone.Tools[i].Options = cloneRaw(tool.Options)
		}
	}
	if r.ToolChoice != nil {
		value := *r.ToolChoice
		value.Raw = cloneRaw(r.ToolChoice.Raw)
		clone.ToolChoice = &value
	}
	clone.ParallelToolCalls = clonePointer(r.ParallelToolCalls)
	if r.ResponseFormat != nil {
		value := *r.ResponseFormat
		value.Schema = cloneRaw(r.ResponseFormat.Schema)
		value.Strict = clonePointer(r.ResponseFormat.Strict)
		clone.ResponseFormat = &value
	}
	if r.Reasoning != nil {
		value := *r.Reasoning
		value.Raw = cloneRaw(r.Reasoning.Raw)
		clone.Reasoning = &value
	}
	clone.Store = clonePointer(r.Store)
	clone.MaxOutputTokens = clonePointer(r.MaxOutputTokens)
	clone.Temperature = clonePointer(r.Temperature)
	clone.TopP = clonePointer(r.TopP)
	clone.Stop = append([]string(nil), r.Stop...)
	clone.Metadata = cloneStringMap(r.Metadata)
	clone.Include = append([]string(nil), r.Include...)
	clone.Modalities = append([]string(nil), r.Modalities...)
	clone.TransportHints = append([]string(nil), r.TransportHints...)
	clone.ClientExtensions = cloneRawMap(r.ClientExtensions)
	if r.ProviderOptions.Volcengine != nil {
		options := *r.ProviderOptions.Volcengine
		options.Thinking = cloneRaw(options.Thinking)
		options.Caching = cloneRaw(options.Caching)
		options.Session = cloneRaw(options.Session)
		options.ContextManagement = cloneRaw(options.ContextManagement)
		options.ExpireAt = clonePointer(options.ExpireAt)
		options.Unknown = cloneRawMap(options.Unknown)
		clone.ProviderOptions.Volcengine = &options
	}
	return clone
}

// CloneItems returns a deep copy suitable for retaining request or stream
// snapshots after protocol processing continues.
func CloneItems(items []Item) []Item {
	if items == nil {
		return nil
	}
	clone := make([]Item, len(items))
	for i, item := range items {
		clone[i] = item
		clone[i].Arguments = cloneRaw(item.Arguments)
		clone[i].Output = cloneRaw(item.Output)
		clone[i].Proof = clonePointer(item.Proof)
		clone[i].Extra = cloneRawMap(item.Extra)
		if item.Content != nil {
			clone[i].Content = make([]Content, len(item.Content))
			for j, content := range item.Content {
				clone[i].Content[j] = content
				clone[i].Content[j].Extra = cloneRawMap(content.Extra)
			}
		}
	}
	return clone
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	if source == nil {
		return nil
	}
	clone := make(map[string]json.RawMessage, len(source))
	for key, raw := range source {
		clone[key] = cloneRaw(raw)
	}
	return clone
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// cloneAny 深拷贝解码自上游 JSON 的任意值，避免快照与源共享 map/slice 存储。
func cloneAny(source any) any {
	switch value := source.(type) {
	case nil:
		return nil
	case map[string]any:
		clone := make(map[string]any, len(value))
		for key, element := range value {
			clone[key] = cloneAny(element)
		}
		return clone
	case []any:
		clone := make([]any, len(value))
		for index, element := range value {
			clone[index] = cloneAny(element)
		}
		return clone
	case json.RawMessage:
		return cloneRaw(value)
	case string, bool, float64, float32, int, int32, int64, uint, uint32, uint64:
		return source
	default:
		// 非 JSON 基础类型走一次 JSON 往返，失败则原样保留。
		raw, err := json.Marshal(source)
		if err != nil {
			return source
		}
		var decoded any
		if json.Unmarshal(raw, &decoded) != nil {
			return source
		}
		return decoded
	}
}

func clonePointer[T any](source *T) *T {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
