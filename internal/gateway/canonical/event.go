package canonical

import "encoding/json"

type EventType string

const (
	EventResponseCreated    EventType = "response.created"
	EventResponseQueued     EventType = "response.queued"
	EventResponseInProgress EventType = "response.in_progress"
	EventOutputItemAdded    EventType = "response.output_item.added"
	EventOutputItemDone     EventType = "response.output_item.done"
	EventContentPartAdded   EventType = "response.content_part.added"
	EventContentPartDone    EventType = "response.content_part.done"
	EventTextDelta          EventType = "response.output_text.delta"
	EventTextDone           EventType = "response.output_text.done"
	EventReasoningDelta     EventType = "response.reasoning.delta"
	EventToolArgumentsDelta EventType = "response.function_call_arguments.delta"
	EventUsage              EventType = "response.usage"
	EventCompleted          EventType = "response.completed"
	EventIncomplete         EventType = "response.incomplete"
	EventFailed             EventType = "response.failed"
	EventError              EventType = "error"
	EventRaw                EventType = "raw"

	// EventOutputTextDelta is retained as the explicit encoder-facing name.
	EventOutputTextDelta EventType = EventTextDelta
)

// Event retains all indexes needed by Chat, Responses, and Anthropic streaming
// encoders. Raw is reserved for native extension events.
type Event struct {
	Type               EventType       `json:"type"`
	SequenceNumber     int64           `json:"sequence_number,omitempty"`
	ChoiceIndex        int             `json:"choice_index,omitempty"`
	OutputIndex        int             `json:"output_index,omitempty"`
	ContentIndex       int             `json:"content_index,omitempty"`
	ToolIndex          int             `json:"tool_index,omitempty"`
	Item               *Item           `json:"item,omitempty"`
	Delta              string          `json:"delta,omitempty"`
	Usage              *Usage          `json:"usage,omitempty"`
	Response           *Response       `json:"response,omitempty"`
	Error              *Error          `json:"error,omitempty"`
	ProviderResponseID string          `json:"provider_response_id,omitempty"`
	RawType            string          `json:"raw_type,omitempty"`
	Raw                json.RawMessage `json:"raw,omitempty"`
}
