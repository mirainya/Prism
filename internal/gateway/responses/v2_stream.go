package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/engine"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

var ErrV2StreamMissingTerminal = errors.New("gateway v2 responses stream ended before a terminal event")

// V2StreamSummary contains the terminal state collected while forwarding a
// Gateway V2 stream. Response remains canonical so persistence and billing can
// consume it without decoding the downstream wire format again.
type V2StreamSummary struct {
	Response           *canonical.Response
	Usage              *canonical.Usage
	Error              *canonical.Error
	ProviderResponseID string
	Terminal           canonical.EventType
	EventCount         int64
}

type V2StreamPublicOptions struct {
	ResponseID         string
	Model              string
	CreatedAt          int64
	PreviousResponseID string
	Store              bool
	Background         bool
	PreserveNativeRaw  bool
}

// ConsumeV2Stream forwards canonical events as OpenAI Responses SSE frames and
// collects the terminal response state. The stream is always closed; the
// engine owns the once-only transport close and route release semantics.
func ConsumeV2Stream(ctx context.Context, writer io.Writer, stream *engine.StreamResult) (summary *V2StreamSummary, err error) {
	if stream == nil {
		return nil, errors.New("Gateway V2 Responses stream is required")
	}
	return consumeV2Stream(ctx, writer, stream, V2StreamPublicOptions{})
}

func ConsumeV2StreamWithOptions(ctx context.Context, writer io.Writer, stream *engine.StreamResult, options V2StreamPublicOptions) (summary *V2StreamSummary, err error) {
	if stream == nil {
		return nil, errors.New("Gateway V2 Responses stream is required")
	}
	return consumeV2Stream(ctx, writer, stream, options)
}

type v2EventSource interface {
	Next(context.Context) (canonical.Event, error)
	Close() error
}

func consumeV2Stream(ctx context.Context, writer io.Writer, stream v2EventSource, options ...V2StreamPublicOptions) (summary *V2StreamSummary, err error) {
	if writer == nil {
		return nil, errors.New("Responses stream writer is required")
	}
	if stream == nil {
		return nil, errors.New("Gateway V2 Responses stream is required")
	}

	aggregator := newV2StreamAggregator()
	publicStream := newV2PublicStream(optionsValue(options), len(options) > 0)
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			if err == nil || !errors.Is(err, closeErr) {
				err = errors.Join(err, fmt.Errorf("close Gateway V2 Responses stream: %w", closeErr))
			}
		}
	}()

	for {
		event, nextErr := stream.Next(ctx)
		if errors.Is(nextErr, io.EOF) || errors.Is(nextErr, engine.ErrStreamEndedWithoutTerminal) {
			return aggregator.summary(), ErrV2StreamMissingTerminal
		}
		if nextErr != nil && !hasV2Event(event) {
			return aggregator.summary(), nextErr
		}

		event = normalizeV2RawEventType(event)
		aggregator.add(event)
		for _, publicEvent := range publicStream.events(event) {
			aggregator.addPublic(publicEvent)
			frame, encodeErr := openairesponses.EncodeSSEFrame(publicEvent)
			if encodeErr != nil {
				abortV2Stream(stream, encodeErr, false)
				return aggregator.summary(), encodeErr
			}
			if writeErr := writeV2SSEFrame(writer, frame); writeErr != nil {
				abortV2Stream(stream, writeErr, true)
				return aggregator.summary(), writeErr
			}
			if flusher, ok := writer.(interface{ Flush() }); ok {
				flusher.Flush()
			}
		}
		if aggregator.terminal() {
			return aggregator.summary(), nextErr
		}
		if nextErr != nil {
			return aggregator.summary(), nextErr
		}
	}
}

func abortV2Stream(stream v2EventSource, err error, clientDisconnected bool) {
	if aborter, ok := stream.(interface {
		Abort(error, bool) error
	}); ok {
		_ = aborter.Abort(err, clientDisconnected)
	}
}

func optionsValue(options []V2StreamPublicOptions) V2StreamPublicOptions {
	if len(options) == 0 {
		return V2StreamPublicOptions{}
	}
	return options[0]
}

type v2PublicStream struct {
	options   V2StreamPublicOptions
	converted *v2ConvertedStream
}

func newV2PublicStream(options V2StreamPublicOptions, configured bool) *v2PublicStream {
	stream := &v2PublicStream{options: options}
	if configured && !options.PreserveNativeRaw {
		stream.converted = newV2ConvertedStream(options)
	}
	return stream
}

func (s *v2PublicStream) events(event canonical.Event) []canonical.Event {
	if s.converted != nil {
		return s.converted.events(event)
	}
	return []canonical.Event{publicV2Event(event, s.options)}
}

type v2ConvertedStream struct {
	options  V2StreamPublicOptions
	sequence int64
	created  bool

	outputs map[string]*v2ConvertedOutput
	byID    map[string]*v2ConvertedOutput
	used    map[int]bool
}

type v2ConvertedOutput struct {
	index       int
	item        canonical.Item
	sourceID    string
	added       bool
	done        bool
	parts       map[int]*v2ConvertedPart
	sourceParts map[int]int
}

type v2ConvertedPart struct {
	content canonical.Content
	added   bool
	done    bool
}

func newV2ConvertedStream(options V2StreamPublicOptions) *v2ConvertedStream {
	return &v2ConvertedStream{
		options: options,
		outputs: make(map[string]*v2ConvertedOutput),
		byID:    make(map[string]*v2ConvertedOutput),
		used:    make(map[int]bool),
	}
}

func (s *v2ConvertedStream) events(event canonical.Event) []canonical.Event {
	// 转换流是一个小型状态机：为粒度较粗的 Provider 事件补齐 Responses 要求的
	// created -> added -> delta -> done -> terminal 顺序，并保持 output/content 索引稳定。
	if event.Type == canonical.EventRaw {
		// Foreign provider frames are not part of the public Responses protocol.
		return nil
	}

	events := make([]canonical.Event, 0, 8)
	if event.Type == canonical.EventError {
		return s.publish([]canonical.Event{event})
	}
	if event.Type == canonical.EventResponseCreated {
		if !s.created {
			events = append(events, s.responseCreated(event.Response, event.ProviderResponseID))
			s.created = true
		}
		return s.publish(events)
	}
	if !s.created {
		events = append(events, s.responseCreated(event.Response, event.ProviderResponseID))
		s.created = true
	}

	switch event.Type {
	case canonical.EventOutputItemAdded:
		output := s.outputFor(event, itemType(event.Item, "message"))
		events = append(events, s.ensureFunctionCallProofCarrier(output)...)
		events = append(events, s.ensureOutputAdded(output)...)
		if event.Item != nil {
			for index, content := range event.Item.Content {
				contentIndex := s.contentIndex(output, index)
				events = append(events, s.ensureContentAdded(output, contentIndex, content)...)
			}
		}
	case canonical.EventContentPartAdded:
		output := s.outputFor(event, itemType(event.Item, "message"))
		events = append(events, s.ensureOutputAdded(output)...)
		content := eventContent(event, event.ContentIndex)
		contentIndex := s.contentIndex(output, event.ContentIndex)
		events = append(events, s.ensureContentAdded(output, contentIndex, content)...)
	case canonical.EventTextDelta:
		output := s.outputFor(event, "message")
		events = append(events, s.ensureOutputAdded(output)...)
		content := eventContent(event, event.ContentIndex)
		if content.Type == "" {
			content.Type = "output_text"
		}
		contentIndex := s.contentIndex(output, event.ContentIndex)
		events = append(events, s.ensureContentAdded(output, contentIndex, content)...)
		part := output.parts[contentIndex]
		part.content.Text += event.Delta
		delta := event
		delta.OutputIndex = output.index
		delta.ContentIndex = contentIndex
		delta.Item = eventItemReference(output.item)
		events = append(events, delta)
	case canonical.EventReasoningDelta:
		output := s.outputFor(event, "reasoning")
		events = append(events, s.ensureOutputAdded(output)...)
		content := eventContent(event, event.ContentIndex)
		if content.Type == "" || content.Type == "output_text" {
			content.Type = "reasoning_text"
		}
		content.Extra = nil
		contentIndex := s.contentIndex(output, event.ContentIndex)
		events = append(events, s.ensureContentAdded(output, contentIndex, content)...)
		part := output.parts[contentIndex]
		part.content.Text += event.Delta
		delta := event
		delta.OutputIndex = output.index
		delta.ContentIndex = contentIndex
		delta.Item = eventItemReference(output.item)
		events = append(events, delta)
	case canonical.EventProviderProof:
		output := s.outputFor(event, itemType(event.Item, "reasoning"))
		if event.Item != nil {
			s.mergeOutputIdentity(output, *event.Item)
		}
		events = append(events, s.ensureFunctionCallProofCarrier(output)...)
		events = append(events, s.ensureOutputAdded(output)...)
		if output.item.Proof != nil && output.item.Proof.Subject == canonical.ProofSubjectGooglePart {
			events = append(events, s.finishOutput(output)...)
		}
	case canonical.EventToolArgumentsDelta:
		output := s.outputFor(event, "function_call")
		events = append(events, s.ensureFunctionCallProofCarrier(output)...)
		events = append(events, s.ensureOutputAdded(output)...)
		if event.Delta != "" {
			output.item.Arguments = append(output.item.Arguments, event.Delta...)
		} else if event.Item != nil && len(output.item.Arguments) == 0 {
			output.item.Arguments = append(json.RawMessage(nil), event.Item.Arguments...)
		}
		delta := event
		delta.OutputIndex = output.index
		delta.Item = eventItemReference(output.item)
		events = append(events, delta)
	case canonical.EventTextDone:
		output := s.outputFor(event, "message")
		events = append(events, s.ensureOutputAdded(output)...)
		content := eventContent(event, event.ContentIndex)
		contentIndex := s.contentIndex(output, event.ContentIndex)
		events = append(events, s.ensureContentAdded(output, contentIndex, content)...)
		part := output.parts[contentIndex]
		if event.Delta != "" {
			part.content.Text = event.Delta
		}
		events = append(events, s.finishTextPart(output, contentIndex, false)...)
	case canonical.EventContentPartDone:
		output := s.outputFor(event, itemType(event.Item, "message"))
		events = append(events, s.ensureOutputAdded(output)...)
		content := eventContent(event, event.ContentIndex)
		contentIndex := s.contentIndex(output, event.ContentIndex)
		events = append(events, s.ensureContentAdded(output, contentIndex, content)...)
		events = append(events, s.finishTextPart(output, contentIndex, true)...)
	case canonical.EventOutputItemDone:
		output := s.outputFor(event, itemType(event.Item, "message"))
		events = append(events, s.ensureFunctionCallProofCarrier(output)...)
		events = append(events, s.ensureOutputAdded(output)...)
		if event.Item != nil {
			if len(output.item.Arguments) == 0 && len(event.Item.Arguments) > 0 {
				output.item.Arguments = append(json.RawMessage(nil), event.Item.Arguments...)
			}
			for index, content := range event.Item.Content {
				contentIndex := s.contentIndex(output, index)
				events = append(events, s.ensureContentAdded(output, contentIndex, content)...)
			}
		}
		events = append(events, s.finishOutput(output)...)
	case canonical.EventCompleted, canonical.EventIncomplete, canonical.EventFailed:
		events = append(events, s.finishAllOutputs()...)
		terminal := event
		terminal.Item = nil
		response := s.terminalResponse(event)
		terminal.Response = &response
		events = append(events, terminal)
	default:
		events = append(events, event)
	}
	return s.publish(events)
}

func (s *v2ConvertedStream) responseCreated(source *canonical.Response, providerID string) canonical.Event {
	response := canonical.Response{ID: providerID, ProviderResponseID: providerID, Model: s.options.Model, CreatedAt: s.options.CreatedAt, Status: "in_progress", Output: []canonical.Item{}}
	if source != nil {
		response = cloneV2Response(*source)
		response.Status = "in_progress"
		response.Output = []canonical.Item{}
		response.Usage = nil
		response.Error = nil
		if response.ProviderResponseID == "" {
			response.ProviderResponseID = providerID
		}
	}
	if response.ID == "" {
		response.ID = providerID
	}
	if response.Model == "" {
		response.Model = s.options.Model
	}
	if response.CreatedAt == 0 {
		response.CreatedAt = s.options.CreatedAt
	}
	return canonical.Event{Type: canonical.EventResponseCreated, Response: &response, ProviderResponseID: providerID}
}

func (s *v2ConvertedStream) outputFor(event canonical.Event, fallbackType string) *v2ConvertedOutput {
	// 优先用 Provider ID 合并事件；缺少 ID 时退化到 choice/output/tool 的结构位置。
	if event.Item != nil && event.Item.ID != "" {
		if output := s.byID[event.Item.ID]; output != nil {
			s.mergeOutputIdentity(output, *event.Item)
			return output
		}
	}
	identityKey, sourceID := convertedOutputIdentity(event, fallbackType)
	if identityKey != "" {
		if output := s.outputs[identityKey]; output != nil {
			if event.Item != nil {
				s.mergeOutputIdentity(output, *event.Item)
			}
			return output
		}
	}
	structuralKey := convertedOutputKey(event, fallbackType)
	if output := s.outputs[structuralKey]; output != nil && (sourceID == "" || output.sourceID == "" || output.sourceID == sourceID) {
		if event.Item != nil {
			s.mergeOutputIdentity(output, *event.Item)
		}
		if output.sourceID == "" {
			output.sourceID = sourceID
		}
		if identityKey != "" {
			s.outputs[identityKey] = output
		}
		return output
	}

	index := s.nextOutputIndex(event.OutputIndex)
	item := canonical.Item{Type: fallbackType, Status: "in_progress"}
	if event.Item != nil {
		s.mergeItemIdentity(&item, *event.Item)
	}
	if item.Type == "" {
		item.Type = fallbackType
	}
	if item.Role == "" && (item.Type == "message" || item.Type == "reasoning") {
		item.Role = canonical.RoleAssistant
	}
	if item.ID == "" {
		item.ID = convertedItemID(item.Type)
	}
	if item.Type == "function_call" && item.CallID == "" {
		item.CallID = "call_" + compactID()
	}
	output := &v2ConvertedOutput{
		index: index, item: item, sourceID: sourceID,
		parts: make(map[int]*v2ConvertedPart), sourceParts: make(map[int]int),
	}
	if s.outputs[structuralKey] == nil {
		s.outputs[structuralKey] = output
	}
	if identityKey != "" {
		s.outputs[identityKey] = output
	}
	s.byID[item.ID] = output
	if event.Item != nil && event.Item.ID != "" {
		s.byID[event.Item.ID] = output
	}
	return output
}

func (s *v2ConvertedStream) contentIndex(output *v2ConvertedOutput, sourceIndex int) int {
	if sourceIndex < 0 {
		sourceIndex = 0
	}
	if index, ok := output.sourceParts[sourceIndex]; ok {
		return index
	}
	index := len(output.sourceParts)
	output.sourceParts[sourceIndex] = index
	return index
}

func (s *v2ConvertedStream) nextOutputIndex(preferred int) int {
	if preferred >= 0 && !s.used[preferred] {
		s.used[preferred] = true
		return preferred
	}
	for index := 0; ; index++ {
		if !s.used[index] {
			s.used[index] = true
			return index
		}
	}
}

func (s *v2ConvertedStream) mergeOutputIdentity(output *v2ConvertedOutput, source canonical.Item) {
	oldID := output.item.ID
	s.mergeItemIdentity(&output.item, source)
	if oldID != "" {
		output.item.ID = oldID
	}
	if source.ID != "" {
		s.byID[source.ID] = output
	}
}

func (s *v2ConvertedStream) mergeItemIdentity(target *canonical.Item, source canonical.Item) {
	if target.ID == "" {
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
	if source.Proof != nil {
		proof := *source.Proof
		target.Proof = &proof
	}
	if source.Extra != nil {
		target.Extra = cloneV2RawMap(source.Extra)
	}
}

func (s *v2ConvertedStream) ensureOutputAdded(output *v2ConvertedOutput) []canonical.Event {
	// ensure* 方法均为幂等操作，允许不同 Provider 从任意粒度的事件开始发送。
	if output.added {
		return nil
	}
	output.added = true
	if output.item.Type == "reasoning" {
		if output.item.Extra == nil {
			output.item.Extra = map[string]json.RawMessage{}
		}
		if _, ok := output.item.Extra["summary"]; !ok {
			output.item.Extra["summary"] = json.RawMessage("[]")
		}
	}
	item := cloneV2Item(output.item)
	item.Status = "in_progress"
	item.Content = nil
	item.Arguments = nil
	if item.Extra == nil {
		item.Extra = map[string]json.RawMessage{}
	}
	if item.Type == "message" {
		item.Extra["content"] = json.RawMessage("[]")
	}
	if item.Type == "function_call" {
		item.Extra["arguments"] = json.RawMessage(`""`)
	}
	return []canonical.Event{{Type: canonical.EventOutputItemAdded, OutputIndex: output.index, Item: &item}}
}

func (s *v2ConvertedStream) ensureFunctionCallProofCarrier(output *v2ConvertedOutput) []canonical.Event {
	carrier, ok := openairesponses.FunctionCallProofCarrier(output.item)
	if !ok {
		return nil
	}
	output.item.Proof = nil
	carrierOutput := s.byID[carrier.ID]
	if carrierOutput == nil {
		carrierOutput = &v2ConvertedOutput{
			index: s.nextOutputIndex(-1), item: carrier,
			parts: make(map[int]*v2ConvertedPart), sourceParts: make(map[int]int),
		}
		if !output.added && carrierOutput.index > output.index {
			carrierOutput.index, output.index = output.index, carrierOutput.index
		}
		s.outputs["proof:"+carrier.ID] = carrierOutput
		s.byID[carrier.ID] = carrierOutput
	}
	events := s.ensureOutputAdded(carrierOutput)
	events = append(events, s.finishOutput(carrierOutput)...)
	return events
}

func (s *v2ConvertedStream) ensureContentAdded(output *v2ConvertedOutput, index int, content canonical.Content) []canonical.Event {
	part := output.parts[index]
	if part == nil {
		if output.item.Type == "reasoning" {
			content.Extra = nil
		}
		if content.Type == "" {
			content.Type = "output_text"
		}
		if content.Extra == nil {
			content.Extra = map[string]json.RawMessage{}
		}
		if content.Type == "output_text" {
			content.Extra["annotations"] = json.RawMessage("[]")
			if content.Text == "" {
				content.Extra["text"] = json.RawMessage(`""`)
			}
		}
		part = &v2ConvertedPart{content: content}
		output.parts[index] = part
	}
	if part.added {
		return nil
	}
	part.added = true
	item := eventItemReference(output.item)
	item.Content = []canonical.Content{cloneV2Content(part.content)}
	eventType := canonical.EventContentPartAdded
	if output.item.Type == "reasoning" {
		eventType = canonical.EventReasoningPartAdded
	}
	return []canonical.Event{{Type: eventType, OutputIndex: output.index, ContentIndex: index, Item: item}}
}

func (s *v2ConvertedStream) finishTextPart(output *v2ConvertedOutput, index int, includeContentDone bool) []canonical.Event {
	part := output.parts[index]
	if part == nil || part.done {
		return nil
	}
	events := make([]canonical.Event, 0, 2)
	item := eventItemReference(output.item)
	item.Content = []canonical.Content{cloneV2Content(part.content)}
	if output.item.Type == "reasoning" {
		events = append(events,
			canonical.Event{Type: canonical.EventReasoningTextDone, OutputIndex: output.index, ContentIndex: index, Item: eventItemReference(output.item), Delta: part.content.Text},
			canonical.Event{Type: canonical.EventReasoningPartDone, OutputIndex: output.index, ContentIndex: index, Item: item},
		)
		part.done = true
		return events
	}
	if part.content.Type == "output_text" {
		events = append(events, canonical.Event{Type: canonical.EventTextDone, OutputIndex: output.index, ContentIndex: index, Item: eventItemReference(output.item), Delta: part.content.Text})
	}
	if includeContentDone || part.content.Type != "" {
		events = append(events, canonical.Event{Type: canonical.EventContentPartDone, OutputIndex: output.index, ContentIndex: index, Item: item})
	}
	part.done = true
	return events
}

func (s *v2ConvertedStream) finishOutput(output *v2ConvertedOutput) []canonical.Event {
	if output.done {
		return nil
	}
	events := make([]canonical.Event, 0, len(output.parts)*2+2)
	indexes := sortedPartIndexes(output.parts)
	for _, index := range indexes {
		events = append(events, s.finishTextPart(output, index, true)...)
	}
	if output.item.Type == "function_call" {
		events = append(events, toolArgumentsDoneEvent(output))
	}
	output.item.Content = make([]canonical.Content, 0, len(indexes))
	for _, index := range indexes {
		output.item.Content = append(output.item.Content, cloneV2Content(output.parts[index].content))
	}
	output.item.Status = "completed"
	item := cloneV2Item(output.item)
	events = append(events, canonical.Event{Type: canonical.EventOutputItemDone, OutputIndex: output.index, Item: &item})
	output.done = true
	return events
}

func (s *v2ConvertedStream) finishAllOutputs() []canonical.Event {
	outputs := s.sortedOutputs()
	events := make([]canonical.Event, 0, len(outputs)*3)
	for _, output := range outputs {
		events = append(events, s.finishOutput(output)...)
	}
	return events
}

func (s *v2ConvertedStream) terminalResponse(event canonical.Event) canonical.Response {
	response := canonical.Response{ID: event.ProviderResponseID, ProviderResponseID: event.ProviderResponseID, Model: s.options.Model, CreatedAt: s.options.CreatedAt}
	if event.Response != nil {
		response = cloneV2Response(*event.Response)
	}
	response.Output = make([]canonical.Item, 0, len(s.outputs))
	for _, output := range s.sortedOutputs() {
		response.Output = append(response.Output, cloneV2Item(output.item))
	}
	if response.ID == "" {
		response.ID = event.ProviderResponseID
	}
	if response.ProviderResponseID == "" {
		response.ProviderResponseID = event.ProviderResponseID
	}
	if response.Model == "" {
		response.Model = s.options.Model
	}
	if response.CreatedAt == 0 {
		response.CreatedAt = s.options.CreatedAt
	}
	if event.Usage != nil {
		response.Usage = cloneV2Usage(event.Usage)
	}
	if event.Error != nil {
		response.Error = cloneV2Error(event.Error)
	}
	switch event.Type {
	case canonical.EventIncomplete:
		response.Status = "incomplete"
	case canonical.EventFailed:
		response.Status = "failed"
	default:
		response.Status = "completed"
	}
	return response
}

func (s *v2ConvertedStream) sortedOutputs() []*v2ConvertedOutput {
	result := make([]*v2ConvertedOutput, 0, len(s.outputs))
	seen := make(map[*v2ConvertedOutput]bool, len(s.outputs))
	for _, output := range s.outputs {
		if !seen[output] {
			seen[output] = true
			result = append(result, output)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].index < result[j].index })
	return result
}

func (s *v2ConvertedStream) publish(events []canonical.Event) []canonical.Event {
	result := make([]canonical.Event, 0, len(events))
	for _, event := range events {
		event.SequenceNumber = s.sequence
		if event.Type == canonical.EventRaw && len(event.Raw) > 0 {
			var fields map[string]json.RawMessage
			if json.Unmarshal(event.Raw, &fields) == nil {
				fields["sequence_number"] = mustV2JSON(s.sequence)
				event.Raw = mustV2JSON(fields)
			}
		}
		s.sequence++
		result = append(result, publicV2Event(event, s.options))
	}
	return result
}

func convertedOutputKey(event canonical.Event, fallbackType string) string {
	if fallbackType == "function_call" {
		return fmt.Sprintf("tool:%d:%d", event.ChoiceIndex, event.ToolIndex)
	}
	if fallbackType == "reasoning" {
		return fmt.Sprintf("reasoning:%d:%d", event.ChoiceIndex, event.OutputIndex)
	}
	return fmt.Sprintf("%s:%d:%d", fallbackType, event.ChoiceIndex, event.OutputIndex)
}

func convertedOutputIdentity(event canonical.Event, fallbackType string) (string, string) {
	if event.Item == nil {
		return "", ""
	}
	identity := event.Item.ID
	if identity == "" && fallbackType == "function_call" {
		identity = event.Item.CallID
	}
	if identity == "" {
		return "", ""
	}
	return fmt.Sprintf("%s:%d:id:%s", fallbackType, event.ChoiceIndex, identity), identity
}

func convertedItemID(itemType string) string {
	prefix := "item_"
	switch itemType {
	case "message":
		prefix = "msg_"
	case "function_call":
		prefix = "fc_"
	case "reasoning":
		prefix = "rs_"
	}
	return prefix + compactID()
}

func itemType(item *canonical.Item, fallback string) string {
	if item != nil && item.Type != "" {
		return item.Type
	}
	return fallback
}

func eventContent(event canonical.Event, index int) canonical.Content {
	if event.Item == nil || len(event.Item.Content) == 0 {
		return canonical.Content{Type: "output_text", Extra: map[string]json.RawMessage{"annotations": json.RawMessage("[]")}}
	}
	if index >= 0 && index < len(event.Item.Content) {
		return cloneV2Content(event.Item.Content[index])
	}
	return cloneV2Content(event.Item.Content[0])
}

func eventItemReference(item canonical.Item) *canonical.Item {
	return &canonical.Item{ID: item.ID, Type: item.Type, Role: item.Role, Name: item.Name, Namespace: item.Namespace, CallID: item.CallID}
}

func sortedPartIndexes(parts map[int]*v2ConvertedPart) []int {
	indexes := make([]int, 0, len(parts))
	for index := range parts {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

func cloneV2Content(source canonical.Content) canonical.Content {
	result := source
	result.Extra = cloneV2RawMap(source.Extra)
	return result
}

func toolArgumentsDoneEvent(output *v2ConvertedOutput) canonical.Event {
	arguments := string(output.item.Arguments)
	if arguments == "" {
		arguments = "{}"
		output.item.Arguments = json.RawMessage(arguments)
	}
	raw := mustV2JSON(map[string]any{
		"type": "response.function_call_arguments.done", "item_id": output.item.ID,
		"output_index": output.index, "arguments": arguments,
	})
	return canonical.Event{Type: canonical.EventRaw, RawType: "response.function_call_arguments.done", Raw: raw}
}

func publicV2Event(event canonical.Event, options V2StreamPublicOptions) canonical.Event {
	if options.PreserveNativeRaw && len(event.Raw) > 0 {
		if rewritten, err := rewriteNativeV2Event(event.Raw, options); err == nil {
			event.Raw = rewritten
			if event.RawType == "" {
				event.RawType = string(event.Type)
			}
			event.Type = canonical.EventRaw
			return event
		}
	}
	if event.Type != canonical.EventRaw {
		event.Raw = nil
		event.RawType = ""
	}
	if event.Response == nil {
		return event
	}
	response := cloneV2Response(*event.Response)
	if options.ResponseID != "" {
		response.ID = options.ResponseID
	}
	if options.Model != "" {
		response.Model = options.Model
	}
	if response.ProviderExtensions == nil {
		response.ProviderExtensions = map[string]json.RawMessage{}
	}
	response.ProviderExtensions["store"] = json.RawMessage(fmt.Sprintf("%t", options.Store))
	response.ProviderExtensions["background"] = json.RawMessage(fmt.Sprintf("%t", options.Background))
	if options.PreviousResponseID == "" {
		response.ProviderExtensions["previous_response_id"] = json.RawMessage("null")
	} else {
		encoded, _ := json.Marshal(options.PreviousResponseID)
		response.ProviderExtensions["previous_response_id"] = encoded
	}
	event.Response = &response
	return event
}

func rewriteNativeV2Event(raw json.RawMessage, options V2StreamPublicOptions) (json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	if options.ResponseID != "" {
		if _, exists := root["response_id"]; exists {
			root["response_id"] = mustV2JSON(options.ResponseID)
		}
	}
	if responseRaw := root["response"]; len(responseRaw) > 0 && string(responseRaw) != "null" {
		var response map[string]json.RawMessage
		if json.Unmarshal(responseRaw, &response) == nil && response != nil {
			if options.ResponseID != "" {
				response["id"] = mustV2JSON(options.ResponseID)
			}
			if options.Model != "" {
				response["model"] = mustV2JSON(options.Model)
			}
			if options.CreatedAt != 0 {
				response["created_at"] = mustV2JSON(options.CreatedAt)
			}
			response["store"] = mustV2JSON(options.Store)
			response["background"] = mustV2JSON(options.Background)
			if options.PreviousResponseID == "" {
				response["previous_response_id"] = json.RawMessage("null")
			} else {
				response["previous_response_id"] = mustV2JSON(options.PreviousResponseID)
			}
			root["response"] = mustV2JSON(response)
		}
	}
	return json.Marshal(root)
}

func mustV2JSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func hasV2Event(event canonical.Event) bool {
	return event.Type != "" || event.RawType != "" || len(event.Raw) > 0 || event.Response != nil || event.Item != nil || event.Error != nil
}

type v2StreamAggregator struct {
	response           *canonical.Response
	usage              *canonical.Usage
	streamError        *canonical.Error
	providerResponseID string
	terminalType       canonical.EventType
	eventCount         int64
	transcript         *canonical.EventAccumulator
	output             map[int]canonical.Item
	maxOutputIndex     int
}

func newV2StreamAggregator() *v2StreamAggregator {
	return &v2StreamAggregator{
		transcript:     canonical.NewEventAccumulator(),
		output:         make(map[int]canonical.Item),
		maxOutputIndex: -1,
	}
}

func (a *v2StreamAggregator) add(event canonical.Event) {
	// 同时保留 canonical transcript 与原生 raw 帧，终态持久化无需重新解析已写出的 SSE。
	a.eventCount++
	if event.ProviderResponseID != "" {
		a.providerResponseID = event.ProviderResponseID
	}
	if event.Response != nil {
		a.mergeResponse(*event.Response)
	}
	if event.Usage != nil {
		a.usage = cloneV2Usage(event.Usage)
	}
	if event.Error != nil {
		a.streamError = cloneV2Error(event.Error)
	}
	if event.Item != nil {
		switch event.Type {
		case canonical.EventOutputItemAdded:
			a.setOutput(event.OutputIndex, *event.Item)
		case canonical.EventOutputItemDone:
			a.setOutputDone(event.OutputIndex, *event.Item)
		case canonical.EventContentPartAdded:
			a.addContentPart(event)
		}
	}
	if event.Delta != "" && isV2DeltaEvent(event.Type) {
		a.addDelta(event)
	}

	a.addRaw(event.Raw)
	eventType := v2EventType(event)
	switch eventType {
	case canonical.EventCompleted:
		a.terminalType = canonical.EventCompleted
		a.ensureResponseStatus("completed")
	case canonical.EventIncomplete:
		a.terminalType = canonical.EventIncomplete
		a.ensureResponseStatus("incomplete")
	case canonical.EventFailed:
		a.terminalType = canonical.EventFailed
		a.ensureResponseStatus("failed")
	case canonical.EventError:
		a.terminalType = canonical.EventError
		if a.streamError == nil {
			a.streamError = &canonical.Error{Code: "upstream_error", Message: "upstream Responses stream failed"}
		}
		a.ensureResponseStatus("failed")
	}
}

func (a *v2StreamAggregator) addPublic(event canonical.Event) {
	if a == nil || a.transcript == nil {
		return
	}
	a.transcript.Observe(event)
}

func (a *v2StreamAggregator) addRaw(raw json.RawMessage) {
	if len(raw) == 0 || !json.Valid(raw) {
		return
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	if responseRaw := root["response"]; len(responseRaw) > 0 {
		if response, err := canonicalV2Response(responseRaw); err == nil {
			a.mergeResponse(response)
		}
	}
	if usageRaw := root["usage"]; len(usageRaw) > 0 {
		var usage protocol.Usage
		if json.Unmarshal(usageRaw, &usage) == nil {
			a.usage = canonicalV2Usage(&usage)
		}
	}
	if itemRaw := root["item"]; len(itemRaw) > 0 {
		if item, err := canonicalV2Item(itemRaw); err == nil {
			var index int
			_ = json.Unmarshal(root["output_index"], &index)
			a.setOutput(index, item)
		}
	}
	if rawError := rawV2Error(root); rawError != nil {
		a.streamError = rawError
	}
}

func (a *v2StreamAggregator) mergeResponse(source canonical.Response) {
	providerID := source.ProviderResponseID
	if providerID == "" {
		providerID = source.ID
	}
	if providerID != "" {
		a.providerResponseID = providerID
	}
	if a.response == nil {
		clone := cloneV2Response(source)
		a.response = &clone
	} else {
		mergeV2Response(a.response, source)
	}
	if source.Usage != nil {
		a.usage = cloneV2Usage(source.Usage)
	}
	if source.Error != nil {
		a.streamError = cloneV2Error(source.Error)
	}
	for index, item := range source.Output {
		a.setOutput(index, item)
	}
}

func (a *v2StreamAggregator) setOutput(index int, item canonical.Item) {
	if index < 0 {
		return
	}
	a.output[index] = cloneV2Item(item)
	if index > a.maxOutputIndex {
		a.maxOutputIndex = index
	}
}

func (a *v2StreamAggregator) setOutputDone(index int, item canonical.Item) {
	// 某些 done 事件只携带身份字段，已累计的 arguments/content 不应被空值覆盖。
	current, exists := a.output[index]
	if exists && len(current.Arguments) > 0 && emptyV2Arguments(item.Arguments) && !emptyV2Arguments(current.Arguments) {
		item.Arguments = append(json.RawMessage(nil), current.Arguments...)
	}
	if exists && len(current.Content) > 0 && len(item.Content) == 0 {
		item.Content = cloneV2Item(current).Content
	}
	a.setOutput(index, item)
}

func (a *v2StreamAggregator) addContentPart(event canonical.Event) {
	if event.Item == nil {
		return
	}
	item := a.output[event.OutputIndex]
	source := cloneV2Item(*event.Item)
	source.Content = nil
	mergeV2Item(&item, source)
	if item.Type == "" {
		item.Type = "message"
		item.Role = canonical.RoleAssistant
	}
	content := eventContent(event, event.ContentIndex)
	for len(item.Content) <= event.ContentIndex {
		item.Content = append(item.Content, canonical.Content{Type: "output_text"})
	}
	item.Content[event.ContentIndex] = content
	a.setOutput(event.OutputIndex, item)
}

func (a *v2StreamAggregator) addDelta(event canonical.Event) {
	index := event.OutputIndex
	item := a.output[index]
	if event.Item != nil {
		source := cloneV2Item(*event.Item)
		source.Content = nil
		source.Arguments = nil
		mergeV2Item(&item, source)
	}
	switch event.Type {
	case canonical.EventToolArgumentsDelta:
		if item.Type == "" {
			item.Type = "function_call"
		}
		if emptyV2Arguments(item.Arguments) {
			item.Arguments = nil
		}
		item.Arguments = append(item.Arguments, event.Delta...)
	default:
		if item.Type == "" {
			item.Type = "message"
			item.Role = canonical.RoleAssistant
		}
		contentIndex := event.ContentIndex
		for len(item.Content) <= contentIndex {
			item.Content = append(item.Content, canonical.Content{Type: "output_text"})
		}
		item.Content[contentIndex].Text += event.Delta
	}
	a.setOutput(index, item)
}

func isV2DeltaEvent(eventType canonical.EventType) bool {
	switch eventType {
	case canonical.EventTextDelta, canonical.EventReasoningDelta, canonical.EventToolArgumentsDelta:
		return true
	default:
		return false
	}
}

func emptyV2Arguments(arguments json.RawMessage) bool {
	trimmed := bytes.TrimSpace(arguments)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("{}"))
}

func (a *v2StreamAggregator) ensureResponseStatus(status string) {
	if a.response == nil {
		a.response = &canonical.Response{}
	}
	a.response.Status = status
	if a.usage != nil {
		a.response.Usage = cloneV2Usage(a.usage)
	}
	if a.streamError != nil {
		a.response.Error = cloneV2Error(a.streamError)
	}
}

func (a *v2StreamAggregator) terminal() bool { return a.terminalType != "" }

func (a *v2StreamAggregator) summary() *V2StreamSummary {
	result := &V2StreamSummary{
		Usage:              cloneV2Usage(a.usage),
		Error:              cloneV2Error(a.streamError),
		ProviderResponseID: a.providerResponseID,
		Terminal:           a.terminalType,
		EventCount:         a.eventCount,
	}
	var response *canonical.Response
	if a.response != nil {
		clone := cloneV2Response(*a.response)
		response = &clone
	}
	transcriptOutput := false
	if a.transcript != nil {
		snapshot := a.transcript.Snapshot()
		transcriptOutput = len(snapshot.Output) > 0
		if response == nil && (snapshot.Status != "" || transcriptOutput || snapshot.ID != "" || snapshot.ProviderResponseID != "") {
			clone := cloneV2Response(snapshot)
			response = &clone
		} else if response != nil {
			mergeV2Response(response, snapshot)
		}
	}
	if response != nil {
		if response.ProviderResponseID == "" {
			response.ProviderResponseID = a.providerResponseID
		}
		if !transcriptOutput && a.maxOutputIndex >= 0 {
			response.Output = make([]canonical.Item, a.maxOutputIndex+1)
			for index := 0; index <= a.maxOutputIndex; index++ {
				response.Output[index] = cloneV2Item(a.output[index])
			}
		}
		if result.Usage != nil {
			response.Usage = cloneV2Usage(result.Usage)
		}
		if result.Error != nil {
			response.Error = cloneV2Error(result.Error)
		}
		result.Response = response
	}
	return result
}

func normalizeV2RawEventType(event canonical.Event) canonical.Event {
	if len(event.Raw) == 0 || event.RawType != "" {
		return event
	}
	var root struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(event.Raw, &root) == nil {
		event.RawType = root.Type
	}
	return event
}

func v2EventType(event canonical.Event) canonical.EventType {
	if isV2TerminalType(event.Type) {
		return event.Type
	}
	if candidate := canonical.EventType(event.RawType); isV2TerminalType(candidate) {
		return candidate
	}
	if len(event.Raw) > 0 {
		var root struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(event.Raw, &root) == nil {
			candidate := canonical.EventType(root.Type)
			if isV2TerminalType(candidate) {
				return candidate
			}
		}
	}
	return event.Type
}

func isV2TerminalType(eventType canonical.EventType) bool {
	switch eventType {
	case canonical.EventCompleted, canonical.EventIncomplete, canonical.EventFailed, canonical.EventError:
		return true
	default:
		return false
	}
}

func writeV2SSEFrame(writer io.Writer, frame []byte) error {
	for len(frame) > 0 {
		written, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		frame = frame[written:]
	}
	return nil
}

func canonicalV2Response(raw json.RawMessage) (canonical.Response, error) {
	var source protocol.Response
	if err := json.Unmarshal(raw, &source); err != nil {
		return canonical.Response{}, err
	}
	result := canonical.Response{
		ID: source.ID, ProviderResponseID: source.ID, Model: source.Model,
		Status: source.Status, CreatedAt: source.CreatedAt,
		IncompleteDetails: append(json.RawMessage(nil), source.IncompleteDetails...),
		Metadata:          cloneV2Strings(source.Metadata), ProviderExtensions: cloneV2RawMap(source.ExtraFields),
		Usage: canonicalV2Usage(source.Usage), Error: canonicalV2ProtocolError(source.Error),
	}
	if len(source.Output) > 0 && string(source.Output) != "null" {
		request := protocol.Request{Model: source.Model, Input: source.Output}
		decoded, err := openairesponses.DecodeRequest(request)
		if err != nil {
			return canonical.Response{}, err
		}
		result.Output = decoded.Items
	}
	return result, nil
}

func canonicalV2Item(raw json.RawMessage) (canonical.Item, error) {
	input, err := json.Marshal([]json.RawMessage{raw})
	if err != nil {
		return canonical.Item{}, err
	}
	decoded, err := openairesponses.DecodeRequest(protocol.Request{Model: "stream", Input: input})
	if err != nil {
		return canonical.Item{}, err
	}
	if len(decoded.Items) != 1 {
		return canonical.Item{}, errors.New("Responses stream item is missing")
	}
	return decoded.Items[0], nil
}

func rawV2Error(root map[string]json.RawMessage) *canonical.Error {
	var typeName string
	_ = json.Unmarshal(root["type"], &typeName)
	if typeName != "error" {
		return nil
	}
	source := root
	if nested := root["error"]; len(nested) > 0 && string(nested) != "null" {
		var value map[string]json.RawMessage
		if json.Unmarshal(nested, &value) == nil {
			source = value
		}
	}
	result := &canonical.Error{}
	_ = json.Unmarshal(source["code"], &result.Code)
	_ = json.Unmarshal(source["message"], &result.Message)
	_ = json.Unmarshal(source["type"], &result.Type)
	if param := source["param"]; len(param) > 0 {
		_ = json.Unmarshal(param, &result.Param)
	}
	if result.Message == "" {
		result.Message = "upstream Responses stream failed"
	}
	return result
}

func canonicalV2Usage(source *protocol.Usage) *canonical.Usage {
	if source == nil {
		return nil
	}
	result := &canonical.Usage{
		InputTokens: source.InputTokens, OutputTokens: source.OutputTokens,
		TotalTokens: source.TotalTokens, Extra: cloneV2RawMap(source.ExtraFields),
	}
	if source.InputTokensDetails != nil {
		result.CachedInputTokens = source.InputTokensDetails.CachedTokens
	}
	if source.OutputTokensDetails != nil {
		result.ReasoningOutputTokens = source.OutputTokensDetails.ReasoningTokens
	}
	return result
}

func canonicalV2ProtocolError(source *protocol.Error) *canonical.Error {
	if source == nil {
		return nil
	}
	return &canonical.Error{Code: source.Code, Message: source.Message, Type: source.Type, Param: source.Param}
}

func cloneV2Response(source canonical.Response) canonical.Response {
	result := source
	result.Output = make([]canonical.Item, len(source.Output))
	for index, item := range source.Output {
		result.Output[index] = cloneV2Item(item)
	}
	result.Usage = cloneV2Usage(source.Usage)
	result.Error = cloneV2Error(source.Error)
	result.IncompleteDetails = append(json.RawMessage(nil), source.IncompleteDetails...)
	result.Metadata = cloneV2Strings(source.Metadata)
	result.ProviderExtensions = cloneV2RawMap(source.ProviderExtensions)
	return result
}

func mergeV2Response(target *canonical.Response, source canonical.Response) {
	if source.ID != "" {
		target.ID = source.ID
	}
	if source.ProviderResponseID != "" {
		target.ProviderResponseID = source.ProviderResponseID
	}
	if source.Model != "" {
		target.Model = source.Model
	}
	if source.Status != "" {
		target.Status = source.Status
	}
	if source.CreatedAt != 0 {
		target.CreatedAt = source.CreatedAt
	}
	if len(source.Output) > 0 {
		target.Output = cloneV2Response(source).Output
	}
	if source.Usage != nil {
		target.Usage = cloneV2Usage(source.Usage)
	}
	if source.Error != nil {
		target.Error = cloneV2Error(source.Error)
	}
	if len(source.IncompleteDetails) > 0 {
		target.IncompleteDetails = append(json.RawMessage(nil), source.IncompleteDetails...)
	}
	if source.Metadata != nil {
		target.Metadata = cloneV2Strings(source.Metadata)
	}
	if source.ProviderExtensions != nil {
		target.ProviderExtensions = cloneV2RawMap(source.ProviderExtensions)
	}
}

func mergeV2Item(target *canonical.Item, source canonical.Item) {
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
	if source.Status != "" {
		target.Status = source.Status
	}
	if source.Proof != nil {
		proof := *source.Proof
		target.Proof = &proof
	}
	if len(source.Content) > 0 {
		target.Content = cloneV2Item(source).Content
	}
	if len(source.Arguments) > 0 {
		target.Arguments = append(json.RawMessage(nil), source.Arguments...)
	}
	if len(source.Output) > 0 {
		target.Output = append(json.RawMessage(nil), source.Output...)
	}
	if source.Extra != nil {
		target.Extra = cloneV2RawMap(source.Extra)
	}
}

func cloneV2Item(source canonical.Item) canonical.Item {
	result := source
	if source.Proof != nil {
		proof := *source.Proof
		result.Proof = &proof
	}
	result.Content = append([]canonical.Content(nil), source.Content...)
	for index := range result.Content {
		result.Content[index].Extra = cloneV2RawMap(source.Content[index].Extra)
	}
	result.Arguments = append(json.RawMessage(nil), source.Arguments...)
	result.Output = append(json.RawMessage(nil), source.Output...)
	result.Extra = cloneV2RawMap(source.Extra)
	return result
}

func cloneV2Usage(source *canonical.Usage) *canonical.Usage {
	if source == nil {
		return nil
	}
	result := *source
	result.Extra = cloneV2RawMap(source.Extra)
	return &result
}

func cloneV2Error(source *canonical.Error) *canonical.Error {
	if source == nil {
		return nil
	}
	result := *source
	result.Raw = append(json.RawMessage(nil), source.Raw...)
	return &result
}

func cloneV2RawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func cloneV2Strings(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
