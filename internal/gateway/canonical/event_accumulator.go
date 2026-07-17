package canonical

import (
	"encoding/json"
	"fmt"
)

// EventAccumulator rebuilds protocol-neutral output items before downstream
// protocol encoders flatten a stream into wire-specific deltas.
type EventAccumulator struct {
	response           Response
	authoritative      bool
	outputs            map[string]*Item
	order              []string
	toolArgumentDeltas map[string]bool
}

func NewEventAccumulator() *EventAccumulator {
	return &EventAccumulator{
		outputs:            make(map[string]*Item),
		toolArgumentDeltas: make(map[string]bool),
	}
}

func (a *EventAccumulator) Observe(event Event) {
	// Provider 事件可能缺少 added/done 阶段，ensure 会按稳定身份创建或复用目标 Item。
	if a == nil {
		return
	}
	if event.Response != nil {
		a.mergeResponse(*event.Response)
	}
	if event.Usage != nil {
		a.response.Usage = cloneUsage(event.Usage)
	}
	if event.ProviderResponseID != "" {
		a.response.ProviderResponseID = event.ProviderResponseID
	}
	if event.Error != nil {
		a.response.Error = cloneError(event.Error)
	}

	switch event.Type {
	case EventOutputItemAdded, EventOutputItemDone, EventContentPartAdded, EventContentPartDone, EventTextDone,
		EventReasoningPartAdded, EventReasoningTextDone, EventReasoningPartDone:
		if event.Item != nil {
			item := CloneItems([]Item{*event.Item})[0]
			if item.Type == "function_call" && a.toolArgumentDeltas[accumulatorItemKey(event, item)] && isEmptyJSONObject(item.Arguments) {
				item.Arguments = nil
			}
			itemEvent := event
			itemEvent.Item = &item
			target := a.ensure(itemEvent, item.Type)
			mergeEventItem(target, item, event.ContentIndex)
		}
	case EventTextDelta:
		target := a.ensure(event, "message")
		appendContentDelta(target, event.ContentIndex, "output_text", event.Delta)
	case EventReasoningDelta:
		target := a.ensure(event, "reasoning")
		appendContentDelta(target, event.ContentIndex, "reasoning_text", event.Delta)
	case EventProviderProof:
		if event.Item != nil && event.Item.Proof != nil {
			item := CloneItems([]Item{*event.Item})[0]
			itemEvent := event
			itemEvent.Item = &item
			target := a.ensure(itemEvent, "reasoning")
			mergeEventItem(target, item, event.ContentIndex)
		}
	case EventToolArgumentsDelta:
		itemEvent := event
		item := Item{Type: "function_call"}
		if event.Item != nil {
			item = CloneItems([]Item{*event.Item})[0]
			// Some transports expose both the complete arguments and the same
			// value as a delta. Metadata still needs merging, but the arguments
			// must only be appended once.
			item.Arguments = nil
			itemEvent.Item = &item
		}
		key := accumulatorItemKey(event, item)
		target := a.ensure(itemEvent, "function_call")
		if !a.toolArgumentDeltas[key] {
			target.Arguments = nil
		}
		a.toolArgumentDeltas[key] = true
		target.Arguments = append(target.Arguments, event.Delta...)
	}

	switch event.Type {
	case EventCompleted:
		a.response.Status = "completed"
	case EventIncomplete:
		a.response.Status = "incomplete"
	case EventFailed, EventError:
		a.response.Status = "failed"
	}
}

func (a *EventAccumulator) Snapshot() Response {
	// 返回深拷贝，避免下游编码器修改累计状态后影响计费、日志或会话投影。
	if a == nil {
		return Response{}
	}
	result := cloneResponse(a.response)
	if !a.authoritative || len(result.Output) == 0 {
		result.Output = make([]Item, 0, len(a.order))
		for _, key := range a.order {
			if item := a.outputs[key]; item != nil {
				result.Output = append(result.Output, CloneItems([]Item{*item})[0])
			}
		}
	}
	return result
}

func (a *EventAccumulator) mergeResponse(response Response) {
	if response.ID != "" {
		a.response.ID = response.ID
	}
	if response.ProviderResponseID != "" {
		a.response.ProviderResponseID = response.ProviderResponseID
	}
	if response.Model != "" {
		a.response.Model = response.Model
	}
	if response.Status != "" {
		a.response.Status = response.Status
	}
	if response.CreatedAt != 0 {
		a.response.CreatedAt = response.CreatedAt
	}
	if response.FinishReason != "" {
		a.response.FinishReason = response.FinishReason
	}
	if response.Usage != nil {
		a.response.Usage = cloneUsage(response.Usage)
	}
	if response.Error != nil {
		a.response.Error = cloneError(response.Error)
	}
	if response.Metadata != nil {
		a.response.Metadata = cloneStringMap(response.Metadata)
	}
	if response.ProviderExtensions != nil {
		a.response.ProviderExtensions = cloneRawMap(response.ProviderExtensions)
	}
	if len(response.IncompleteDetails) > 0 {
		a.response.IncompleteDetails = cloneRaw(response.IncompleteDetails)
	}
	if len(response.Output) > 0 {
		a.response.Output = CloneItems(response.Output)
		a.authoritative = true
	}
}

func (a *EventAccumulator) ensure(event Event, fallbackType string) *Item {
	item := Item{Type: fallbackType, Role: RoleAssistant}
	if event.Item != nil {
		item = CloneItems([]Item{*event.Item})[0]
		if event.Type == EventToolArgumentsDelta {
			item.Arguments = nil
		}
		if item.Type == "" {
			item.Type = fallbackType
		}
		if item.Role == "" && (item.Type == "message" || item.Type == "reasoning") {
			item.Role = RoleAssistant
		}
	}
	key := accumulatorItemKey(event, item)
	if current := a.outputs[key]; current != nil {
		mergeItem(current, item)
		return current
	}
	copy := item
	if copy.Extra == nil {
		copy.Extra = make(map[string]json.RawMessage)
	}
	if event.ChoiceIndex != 0 {
		copy.Extra["prism.choice_index"] = json.RawMessage(fmt.Sprintf("%d", event.ChoiceIndex))
	}
	a.outputs[key] = &copy
	a.order = append(a.order, key)
	return &copy
}

func accumulatorItemKey(event Event, item Item) string {
	// 首选 Provider ID；缺失时用 choice/output/tool 位置构造流内稳定键。
	switch item.Type {
	case "function_call":
		return fmt.Sprintf("tool:%d:%d:%d", event.ChoiceIndex, event.OutputIndex, event.ToolIndex)
	case "reasoning":
		return fmt.Sprintf("reasoning:%d:%d", event.ChoiceIndex, event.OutputIndex)
	case "message":
		return fmt.Sprintf("message:%d:%d", event.ChoiceIndex, event.OutputIndex)
	}
	if item.ID != "" {
		return "id:" + item.ID
	}
	if item.CallID != "" {
		return "call:" + item.CallID
	}
	return fmt.Sprintf("item:%s:%d:%d:%d", item.Type, event.ChoiceIndex, event.OutputIndex, event.ContentIndex)
}

func mergeEventItem(target *Item, source Item, contentIndex int) {
	if contentIndex > 0 && len(source.Content) == 1 && (source.Type == "message" || source.Type == "reasoning") {
		part := source.Content[0]
		source.Content = nil
		mergeItem(target, source)
		for len(target.Content) <= contentIndex {
			target.Content = append(target.Content, Content{})
		}
		mergeContent(&target.Content[contentIndex], part)
		return
	}
	mergeItem(target, source)
}

func appendContentDelta(item *Item, index int, contentType, delta string) {
	if item == nil || delta == "" {
		return
	}
	if index < 0 {
		index = 0
	}
	for len(item.Content) <= index {
		item.Content = append(item.Content, Content{})
	}
	part := &item.Content[index]
	if part.Type == "" {
		part.Type = contentType
	}
	part.Text += delta
}

func mergeItem(target *Item, source Item) {
	if target == nil {
		return
	}
	if source.ID != "" {
		target.ID = source.ID
	}
	if source.Type != "" {
		target.Type = source.Type
	}
	if source.Role != "" {
		target.Role = source.Role
	}
	if source.Name != "" {
		target.Name = source.Name
	}
	if source.Namespace != "" {
		target.Namespace = source.Namespace
	}
	if source.CallID != "" {
		target.CallID = source.CallID
	}
	if source.ProviderCallIDOmitted {
		target.ProviderCallIDOmitted = true
	}
	if len(source.Arguments) > 0 && (len(target.Arguments) == 0 || json.Valid(source.Arguments)) {
		target.Arguments = cloneRaw(source.Arguments)
	}
	if len(source.Output) > 0 {
		target.Output = cloneRaw(source.Output)
	}
	if source.Status != "" {
		target.Status = source.Status
	}
	if source.Proof != nil {
		target.Proof = clonePointer(source.Proof)
	}
	if source.Extra != nil {
		if target.Extra == nil {
			target.Extra = make(map[string]json.RawMessage, len(source.Extra))
		}
		for key, value := range source.Extra {
			target.Extra[key] = cloneRaw(value)
		}
	}
	for index, sourcePart := range source.Content {
		for len(target.Content) <= index {
			target.Content = append(target.Content, Content{})
		}
		mergeContent(&target.Content[index], sourcePart)
	}
}

func isEmptyJSONObject(raw json.RawMessage) bool {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return false
	}
	object, ok := value.(map[string]any)
	return ok && len(object) == 0
}

func mergeContent(target *Content, source Content) {
	if source.Type != "" {
		target.Type = source.Type
	}
	if source.Text != "" && len(source.Text) >= len(target.Text) {
		target.Text = source.Text
	}
	if source.URL != "" {
		target.URL = source.URL
	}
	if source.Data != "" {
		target.Data = source.Data
	}
	if source.FileID != "" {
		target.FileID = source.FileID
	}
	if source.Filename != "" {
		target.Filename = source.Filename
	}
	if source.MediaType != "" {
		target.MediaType = source.MediaType
	}
	if source.Format != "" {
		target.Format = source.Format
	}
	if source.Detail != "" {
		target.Detail = source.Detail
	}
	if source.Transcript != "" {
		target.Transcript = source.Transcript
	}
	if source.Extra != nil {
		if target.Extra == nil {
			target.Extra = make(map[string]json.RawMessage, len(source.Extra))
		}
		for key, value := range source.Extra {
			target.Extra[key] = cloneRaw(value)
		}
	}
}

func cloneResponse(source Response) Response {
	clone := source
	clone.Output = CloneItems(source.Output)
	clone.Usage = cloneUsage(source.Usage)
	clone.Error = cloneError(source.Error)
	clone.IncompleteDetails = cloneRaw(source.IncompleteDetails)
	clone.Metadata = cloneStringMap(source.Metadata)
	clone.ProviderExtensions = cloneRawMap(source.ProviderExtensions)
	return clone
}

func cloneUsage(source *Usage) *Usage {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Extra = cloneRawMap(source.Extra)
	return &clone
}

func cloneError(source *Error) *Error {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Raw = cloneRaw(source.Raw)
	return &clone
}
