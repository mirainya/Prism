package canonical

import "encoding/json"

// Clone returns a request whose mutable fields do not share storage with the source.
func (r Request) Clone() Request {
	clone := r
	if r.Items != nil {
		clone.Items = make([]Item, len(r.Items))
		for i, item := range r.Items {
			clone.Items[i] = item
			clone.Items[i].Arguments = cloneRaw(item.Arguments)
			clone.Items[i].Output = cloneRaw(item.Output)
			clone.Items[i].Extra = cloneRawMap(item.Extra)
			if item.Content != nil {
				clone.Items[i].Content = make([]Content, len(item.Content))
				for j, content := range item.Content {
					clone.Items[i].Content[j] = content
					clone.Items[i].Content[j].Extra = cloneRawMap(content.Extra)
				}
			}
		}
	}
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

func clonePointer[T any](source *T) *T {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}
