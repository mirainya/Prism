package canonical

import (
	"encoding/json"
	"testing"
)

func TestEventAccumulatorPreservesStructuredStreamOutput(t *testing.T) {
	acc := NewEventAccumulator()
	acc.Observe(Event{Type: EventTextDelta, Delta: "hello "})
	acc.Observe(Event{Type: EventTextDelta, Delta: "world"})
	acc.Observe(Event{Type: EventReasoningDelta, Delta: "think"})
	acc.Observe(Event{
		Type: EventOutputItemAdded, ContentIndex: 1,
		Item: &Item{Type: "message", Role: RoleAssistant, Content: []Content{
			{}, {Type: "output_image", URL: "https://example.test/image.png", MediaType: "image/png"},
		}},
	})
	acc.Observe(Event{
		Type: EventToolArgumentsDelta, ToolIndex: 0, Delta: `{"city":`,
		Item: &Item{ID: "call_weather", Type: "function_call", CallID: "call_weather", Name: "weather", Namespace: "tools"},
	})
	acc.Observe(Event{
		Type: EventToolArgumentsDelta, ToolIndex: 0, Delta: `"Tokyo"}`,
		Item: &Item{ID: "call_weather", Type: "function_call", CallID: "call_weather"},
	})
	acc.Observe(Event{
		Type: EventToolArgumentsDelta, ToolIndex: 1, Delta: `{}`,
		Item: &Item{ID: "call_time", Type: "function_call", CallID: "call_time", Name: "time"},
	})
	acc.Observe(Event{
		Type: EventCompleted, ProviderResponseID: "provider-response",
		Usage: &Usage{InputTokens: 4, OutputTokens: 5, TotalTokens: 9},
	})

	snapshot := acc.Snapshot()
	if snapshot.Status != "completed" || snapshot.ProviderResponseID != "provider-response" {
		t.Fatalf("unexpected terminal snapshot: %#v", snapshot)
	}
	if snapshot.Usage == nil || snapshot.Usage.TotalTokens != 9 {
		t.Fatalf("unexpected usage: %#v", snapshot.Usage)
	}
	if len(snapshot.Output) != 4 {
		t.Fatalf("output count = %d, want 4: %#v", len(snapshot.Output), snapshot.Output)
	}
	if snapshot.Output[0].Content[0].Text != "hello world" || snapshot.Output[0].Content[1].URL == "" {
		t.Fatalf("message output was flattened: %#v", snapshot.Output[0])
	}
	if snapshot.Output[1].Type != "reasoning" || snapshot.Output[1].Content[0].Text != "think" {
		t.Fatalf("reasoning output missing: %#v", snapshot.Output[1])
	}
	if snapshot.Output[2].Name != "weather" || snapshot.Output[2].Namespace != "tools" || !json.Valid(snapshot.Output[2].Arguments) {
		t.Fatalf("first tool output invalid: %#v", snapshot.Output[2])
	}
	if snapshot.Output[3].Name != "time" || string(snapshot.Output[3].Arguments) != `{}` {
		t.Fatalf("second tool output invalid: %#v", snapshot.Output[3])
	}
}

func TestEventAccumulatorMergesProviderProof(t *testing.T) {
	acc := NewEventAccumulator()
	acc.Observe(Event{Type: EventReasoningDelta, OutputIndex: 1, Delta: "summary"})
	acc.Observe(Event{
		Type: EventProviderProof, OutputIndex: 1,
		Item: &Item{Type: "reasoning", Proof: &ProviderProof{Provider: ProofProviderAnthropic, Value: "proof"}},
	})

	output := acc.Snapshot().Output
	if len(output) != 1 || output[0].Proof == nil || output[0].Proof.Provider != ProofProviderAnthropic || output[0].Proof.Value != "proof" {
		t.Fatalf("provider proof was not merged: %#v", output)
	}
	output[0].Proof.Value = "changed"
	if got := acc.Snapshot().Output[0].Proof.Value; got != "proof" {
		t.Fatalf("snapshot shared provider proof: %q", got)
	}
}

func TestEventAccumulatorUsesTerminalResponseAsAuthority(t *testing.T) {
	acc := NewEventAccumulator()
	acc.Observe(Event{Type: EventTextDelta, Delta: "partial"})
	acc.Observe(Event{Type: EventCompleted, Response: &Response{
		ID: "resp_1", Status: "completed",
		Output: []Item{{Type: "message", Role: RoleAssistant, Content: []Content{{Type: "output_text", Text: "final"}}}},
	}})

	snapshot := acc.Snapshot()
	if len(snapshot.Output) != 1 || snapshot.Output[0].Content[0].Text != "final" {
		t.Fatalf("terminal response did not replace deltas: %#v", snapshot.Output)
	}
	snapshot.Output[0].Content[0].Text = "mutated"
	if next := acc.Snapshot().Output[0].Content[0].Text; next != "final" {
		t.Fatalf("snapshot shared mutable output: %q", next)
	}
}

func TestEventAccumulatorKeepsToolIdentityAcrossSparseDeltas(t *testing.T) {
	acc := NewEventAccumulator()
	acc.Observe(Event{
		Type: EventOutputItemAdded, ToolIndex: 1,
		Item: &Item{ID: "call_weather", Type: "function_call", CallID: "call_weather", Name: "weather"},
	})
	acc.Observe(Event{
		Type: EventToolArgumentsDelta, ToolIndex: 1, Delta: `{"city":"Tokyo"}`,
		Item: &Item{Type: "function_call", Arguments: json.RawMessage(`{"city":"Tokyo"}`), ProviderCallIDOmitted: true},
	})

	output := acc.Snapshot().Output
	if len(output) != 1 {
		t.Fatalf("tool output count = %d, want 1: %#v", len(output), output)
	}
	if output[0].ID != "call_weather" || output[0].Name != "weather" || string(output[0].Arguments) != `{"city":"Tokyo"}` || !output[0].ProviderCallIDOmitted {
		t.Fatalf("sparse tool deltas were not merged: %#v", output[0])
	}
}

func TestEventAccumulatorReplacesAnthropicToolPlaceholdersWithSingleDelta(t *testing.T) {
	acc := NewEventAccumulator()
	tool := Item{ID: "tool_1", Type: "function_call", CallID: "tool_1", Name: "weather", Arguments: json.RawMessage(`{}`)}
	acc.Observe(Event{Type: EventOutputItemAdded, ToolIndex: 0, Item: &tool})
	acc.Observe(Event{Type: EventToolArgumentsDelta, ToolIndex: 0, Item: &tool, Delta: `{"city":"Tokyo"}`})
	tool.Status = "completed"
	acc.Observe(Event{Type: EventOutputItemDone, ToolIndex: 0, Item: &tool})

	output := acc.Snapshot().Output
	if len(output) != 1 || string(output[0].Arguments) != `{"city":"Tokyo"}` {
		t.Fatalf("Anthropic tool arguments = %#v", output)
	}
}

func TestEventAccumulatorDoesNotDuplicateCompleteStartArguments(t *testing.T) {
	acc := NewEventAccumulator()
	tool := Item{
		ID: "tool_1", Type: "function_call", CallID: "tool_1", Name: "weather",
		Arguments: json.RawMessage(`{"city":"Tokyo"}`),
	}
	acc.Observe(Event{Type: EventOutputItemAdded, ToolIndex: 0, Item: &tool})
	acc.Observe(Event{Type: EventToolArgumentsDelta, ToolIndex: 0, Item: &tool, Delta: `{"city":"Tokyo"}`})

	output := acc.Snapshot().Output
	if len(output) != 1 || string(output[0].Arguments) != `{"city":"Tokyo"}` {
		t.Fatalf("duplicated start arguments = %#v", output)
	}
}

func TestEventAccumulatorJoinsAnthropicToolArgumentDeltas(t *testing.T) {
	acc := NewEventAccumulator()
	tool := Item{ID: "tool_1", Type: "function_call", CallID: "tool_1", Name: "weather", Arguments: json.RawMessage(`{}`)}
	acc.Observe(Event{Type: EventOutputItemAdded, ToolIndex: 0, Item: &tool})
	acc.Observe(Event{Type: EventToolArgumentsDelta, ToolIndex: 0, Item: &tool, Delta: `{"city":`})
	acc.Observe(Event{Type: EventToolArgumentsDelta, ToolIndex: 0, Item: &tool, Delta: `"Tokyo"}`})
	acc.Observe(Event{Type: EventOutputItemDone, ToolIndex: 0, Item: &tool})

	output := acc.Snapshot().Output
	if len(output) != 1 || string(output[0].Arguments) != `{"city":"Tokyo"}` {
		t.Fatalf("joined Anthropic tool arguments = %#v", output)
	}
}

func TestEventAccumulatorPreservesTruncatedAnthropicToolArguments(t *testing.T) {
	acc := NewEventAccumulator()
	tool := Item{ID: "tool_1", Type: "function_call", CallID: "tool_1", Name: "weather", Arguments: json.RawMessage(`{}`)}
	acc.Observe(Event{Type: EventOutputItemAdded, ToolIndex: 0, Item: &tool})
	acc.Observe(Event{Type: EventToolArgumentsDelta, ToolIndex: 0, Item: &tool, Delta: `{"city":`})

	output := acc.Snapshot().Output
	if len(output) != 1 || string(output[0].Arguments) != `{"city":` {
		t.Fatalf("truncated Anthropic tool arguments = %#v", output)
	}
}

func TestEventAccumulatorUsesCompleteToolArgumentsFromDoneEvent(t *testing.T) {
	acc := NewEventAccumulator()
	tool := Item{ID: "tool_1", Type: "function_call", CallID: "tool_1", Name: "weather", Arguments: json.RawMessage(`{}`)}
	acc.Observe(Event{Type: EventOutputItemAdded, ToolIndex: 0, Item: &tool})
	acc.Observe(Event{Type: EventToolArgumentsDelta, ToolIndex: 0, Item: &tool, Delta: `{"city":"Tok`})
	tool.Arguments = json.RawMessage(`{"city":"Kyoto"}`)
	tool.Status = "completed"
	acc.Observe(Event{Type: EventOutputItemDone, ToolIndex: 0, Item: &tool})

	output := acc.Snapshot().Output
	if len(output) != 1 || string(output[0].Arguments) != `{"city":"Kyoto"}` {
		t.Fatalf("completed Anthropic tool arguments = %#v", output)
	}
}

func TestEventAccumulatorPlacesSingleContentAtEventIndex(t *testing.T) {
	acc := NewEventAccumulator()
	acc.Observe(Event{Type: EventTextDelta, ContentIndex: 0, Delta: "answer"})
	acc.Observe(Event{
		Type: EventOutputItemAdded, ContentIndex: 2,
		Item: &Item{Type: "message", Role: RoleAssistant, Content: []Content{{Type: "output_image", URL: "https://example.test/image.png"}}},
	})

	// 稀疏索引留下的空占位块不进入快照，索引 1 的空洞被丢弃。
	output := acc.Snapshot().Output
	if len(output) != 1 || len(output[0].Content) != 2 {
		t.Fatalf("indexed content was flattened: %#v", output)
	}
	if output[0].Content[0].Text != "answer" || output[0].Content[1].URL != "https://example.test/image.png" {
		t.Fatalf("indexed content mismatch: %#v", output[0].Content)
	}
}

func TestEventAccumulatorKeepsAnthropicToolIdentityWhenDoneDropsToolIndex(t *testing.T) {
	acc := NewEventAccumulator()
	tool := Item{ID: "tool_1", Type: "function_call", CallID: "tool_1", Name: "weather", Arguments: json.RawMessage(`{}`)}
	acc.Observe(Event{Type: EventOutputItemAdded, OutputIndex: 1, ToolIndex: 1, Item: &tool})
	acc.Observe(Event{Type: EventToolArgumentsDelta, OutputIndex: 1, ToolIndex: 1, Item: &tool, Delta: `{"city":"Tokyo"}`})
	done := tool
	done.Status = "completed"
	done.Arguments = json.RawMessage(`{"city":"Tokyo"}`)
	// content_block_stop 只带 index，ToolIndex/OutputIndex 与 start 阶段不一致。
	acc.Observe(Event{Type: EventOutputItemDone, OutputIndex: 1, ToolIndex: 0, Item: &done})

	output := acc.Snapshot().Output
	if len(output) != 1 {
		t.Fatalf("tool output count = %d, want 1: %#v", len(output), output)
	}
	if output[0].CallID != "tool_1" || string(output[0].Arguments) != `{"city":"Tokyo"}` || output[0].Status != "completed" {
		t.Fatalf("tool identity was split: %#v", output[0])
	}
}

func TestEventAccumulatorAppendsStreamedAudioFragments(t *testing.T) {
	acc := NewEventAccumulator()
	for _, fragment := range []Content{
		{Type: "output_audio", Data: "AAA", Transcript: "he"},
		{Type: "output_audio", Data: "BBB", Transcript: "llo"},
		{Type: "output_audio", Data: "CCC", Transcript: " world"},
	} {
		part := fragment
		acc.Observe(Event{
			Type: EventOutputItemAdded, ContentIndex: 2,
			Item: &Item{Type: "message", Role: RoleAssistant, Content: []Content{part}},
		})
	}

	output := acc.Snapshot().Output
	if len(output) != 1 {
		t.Fatalf("audio output count = %d, want 1: %#v", len(output), output)
	}
	audio := output[0].Content[len(output[0].Content)-1]
	if audio.Type != "output_audio" || audio.Data != "AAABBBCCC" || audio.Transcript != "hello world" {
		t.Fatalf("audio fragments were not appended: %#v", audio)
	}
}

func TestEventAccumulatorDeepCopiesErrorParam(t *testing.T) {
	acc := NewEventAccumulator()
	param := map[string]any{"field": "model", "path": []any{"body", "model"}}
	acc.Observe(Event{Type: EventError, Error: &Error{Message: "bad request", Param: param}})

	param["field"] = "mutated"
	param["path"].([]any)[0] = "mutated"
	snapshot := acc.Snapshot()
	if snapshot.Error == nil {
		t.Fatal("error was dropped")
	}
	held, ok := snapshot.Error.Param.(map[string]any)
	if !ok {
		t.Fatalf("param type = %T", snapshot.Error.Param)
	}
	if held["field"] != "model" || held["path"].([]any)[0] != "body" {
		t.Fatalf("param shared storage with source: %#v", held)
	}
}

func TestEventAccumulatorOmitsRoleOnToolFallbackItems(t *testing.T) {
	acc := NewEventAccumulator()
	acc.Observe(Event{Type: EventToolArgumentsDelta, ToolIndex: 0, Delta: `{"city":"Tokyo"}`})

	output := acc.Snapshot().Output
	if len(output) != 1 || output[0].Type != "function_call" {
		t.Fatalf("fallback tool item = %#v", output)
	}
	if output[0].Role != "" {
		t.Fatalf("function_call carried role %q", output[0].Role)
	}
}
