import {
  ChatMessage,
  ContentPart,
  PlaygroundProtocol,
  PlaygroundToolCall,
} from './types';
import { extractAssistantText } from './utils';

export const PLAYGROUND_PROTOCOLS: { value: PlaygroundProtocol; label: string; endpoint: string }[] = [
  { value: 'chat', label: 'Chat', endpoint: '/v1/chat/completions' },
  { value: 'responses', label: 'Responses', endpoint: '/v1/responses' },
  { value: 'anthropic', label: 'Messages', endpoint: '/v1/messages' },
];

interface BuildPayloadOptions {
  protocol: PlaygroundProtocol;
  model: string;
  messages: ChatMessage[];
  currentMessage: ChatMessage;
  systemPrompt: string;
  hasConversation: boolean;
  conversationId?: string;
  temperature: number;
  maxTokens: number;
  topP: number;
  presencePenalty: number;
  frequencyPenalty: number;
  stop: string[] | undefined;
  stream: boolean;
  seed?: number;
  user?: string;
  responseFormat?: any;
  tools?: any;
  toolChoice?: any;
  reasoningEffort?: string;
}

export interface ParsedProtocolResponse {
  content: string;
  reasoningContent: string;
  toolCalls: PlaygroundToolCall[];
  usage: { input: number; output: number; total: number } | null;
  finishReason: string;
  responseId?: string;
  conversationId?: string;
  debug?: any;
}

export interface ProtocolStreamState extends ParsedProtocolResponse {
  error?: string;
}

export const buildProtocolPayload = (options: BuildPayloadOptions): Record<string, any> => {
  const {
    protocol, model, messages, currentMessage, systemPrompt, hasConversation, conversationId,
    temperature, maxTokens, topP, presencePenalty, frequencyPenalty, stop, stream, seed, user,
    responseFormat, tools, toolChoice, reasoningEffort,
  } = options;

  if (protocol === 'chat') {
    const incrementalMessages = hasConversation ? [currentMessage] : messages;
    const requestMessages = systemPrompt.trim() && !hasConversation
      ? [{ role: 'system', content: systemPrompt.trim() }, ...incrementalMessages.map(toChatMessage)]
      : incrementalMessages.map(toChatMessage);
    const payload: Record<string, any> = {
      model,
      messages: requestMessages,
      temperature: reasoningEffort ? undefined : temperature,
      max_tokens: maxTokens,
      stop,
      stream,
      seed,
      user,
      conversation_id: conversationId,
      response_format: responseFormat,
      tools,
      tool_choice: toolChoice,
    };
    if (topP !== 1) payload.top_p = topP;
    if (presencePenalty !== 0) payload.presence_penalty = presencePenalty;
    if (frequencyPenalty !== 0) payload.frequency_penalty = frequencyPenalty;
    if (reasoningEffort) payload.reasoning_effort = reasoningEffort;
    return payload;
  }

  if (protocol === 'responses') {
    const payload: Record<string, any> = {
      model,
      input: messages.map(toResponsesMessage),
      instructions: systemPrompt.trim() || undefined,
      temperature: reasoningEffort ? undefined : temperature,
      max_output_tokens: maxTokens,
      stream,
      user,
      tools: normalizeTools(tools, protocol),
      tool_choice: normalizeToolChoice(toolChoice, protocol),
      text: responseFormat ? { format: responseFormat } : undefined,
    };
    if (topP !== 1) payload.top_p = topP;
    if (reasoningEffort) payload.reasoning = { effort: reasoningEffort };
    return payload;
  }

  const payload: Record<string, any> = {
    model,
    messages: messages.map(toAnthropicMessage),
    system: systemPrompt.trim() || undefined,
    temperature: reasoningEffort ? undefined : temperature,
    max_tokens: maxTokens,
    stop_sequences: stop,
    stream,
    tools: normalizeTools(tools, protocol),
    tool_choice: normalizeToolChoice(toolChoice, protocol),
    metadata: user ? { user_id: user } : undefined,
  };
  if (topP !== 1) payload.top_p = topP;
  if (reasoningEffort) payload.thinking = {
    type: 'enabled',
    budget_tokens: reasoningBudget(reasoningEffort),
  };
  return payload;
};

export const parseProtocolResponse = (protocol: PlaygroundProtocol, body: any): ParsedProtocolResponse => {
  if (protocol === 'chat') {
    const message = body.choices?.[0]?.message || {};
    return {
      content: extractAssistantText(message.content),
      reasoningContent: message.reasoning_content || '',
      toolCalls: parseChatToolCalls(message.tool_calls),
      usage: normalizeUsage(protocol, body.usage),
      finishReason: body.choices?.[0]?.finish_reason || body.debug?.finishReason || '',
      responseId: body.id,
      conversationId: body.conversation_id,
      debug: body.debug,
    };
  }

  if (protocol === 'responses') {
    const result = emptyParsedResponse();
    result.responseId = body.id;
    result.finishReason = body.status || '';
    result.usage = normalizeUsage(protocol, body.usage);
    for (const item of body.output || []) {
      appendResponsesItem(result, item);
    }
    if (!result.content && typeof body.output_text === 'string') result.content = body.output_text;
    return result;
  }

  const result = emptyParsedResponse();
  result.responseId = body.id;
  result.finishReason = body.stop_reason || '';
  result.usage = normalizeUsage(protocol, body.usage);
  for (const block of body.content || []) {
    appendAnthropicBlock(result, block);
  }
  return result;
};

export const createProtocolStreamState = (): ProtocolStreamState => emptyParsedResponse();

export const consumeProtocolStreamEvent = (
  protocol: PlaygroundProtocol,
  eventName: string,
  body: any,
  state: ProtocolStreamState,
) => {
  if (body?.error) {
    state.error = typeof body.error === 'string' ? body.error : body.error.message;
  }
  if (protocol === 'chat') {
    const delta = body.choices?.[0]?.delta || {};
    if (typeof delta.content === 'string') state.content += delta.content;
    if (typeof delta.reasoning_content === 'string') state.reasoningContent += delta.reasoning_content;
    appendChatToolCallDeltas(state.toolCalls, delta.tool_calls);
    if (body.choices?.[0]?.finish_reason) state.finishReason = body.choices[0].finish_reason;
    if (body.usage) state.usage = normalizeUsage(protocol, body.usage);
    return;
  }

  if (protocol === 'responses') {
    if (body.response?.id) state.responseId = body.response.id;
    if (body.response?.usage) state.usage = normalizeUsage(protocol, body.response.usage);
    if (eventName === 'response.output_text.delta') state.content += body.delta || '';
    if (eventName === 'response.output_text.done' && !state.content) state.content = body.text || '';
    if (eventName === 'response.reasoning_summary_text.delta' || eventName === 'response.reasoning_text.delta') {
      state.reasoningContent += body.delta || '';
    }
    if ((eventName === 'response.reasoning_summary_text.done' || eventName === 'response.reasoning_text.done') && !state.reasoningContent) {
      state.reasoningContent = body.text || '';
    }
    if ((eventName === 'response.output_item.added' || eventName === 'response.output_item.done') && body.item?.type === 'function_call') {
      appendResponsesItem(state, body.item, body.output_index);
    }
    if (eventName === 'response.function_call_arguments.delta') {
      appendToolCallDelta(state.toolCalls, body.item_id, body.name, body.delta || '', body.output_index);
    }
    if (eventName === 'response.completed' || eventName === 'response.incomplete' || eventName === 'response.failed') {
      state.finishReason = body.response?.status || eventName.slice('response.'.length);
      if (body.response?.usage) state.usage = normalizeUsage(protocol, body.response.usage);
      if (body.response?.error) state.error = body.response.error.message || String(body.response.error);
    }
    return;
  }

  if (eventName === 'message_start') {
    state.responseId = body.message?.id;
    if (body.message?.usage) state.usage = normalizeUsage(protocol, body.message.usage);
  }
  if (eventName === 'content_block_start') appendAnthropicBlock(state, body.content_block, body.index);
  if (eventName === 'content_block_delta') {
    const delta = body.delta || {};
    if (delta.type === 'text_delta') state.content += delta.text || '';
    if (delta.type === 'thinking_delta') state.reasoningContent += delta.thinking || '';
    if (delta.type === 'input_json_delta') appendToolCallDelta(state.toolCalls, undefined, undefined, delta.partial_json || '', body.index);
  }
  if (eventName === 'message_delta') {
    if (body.delta?.stop_reason) state.finishReason = body.delta.stop_reason;
    if (body.usage) state.usage = mergeAnthropicUsage(state.usage, body.usage);
  }
};

const emptyParsedResponse = (): ParsedProtocolResponse => ({
  content: '', reasoningContent: '', toolCalls: [], usage: null, finishReason: '',
});

const toChatMessage = (message: ChatMessage) => ({ role: message.role, content: message.content });

const toResponsesMessage = (message: ChatMessage) => ({
  type: 'message',
  role: message.role,
  content: typeof message.content === 'string'
    ? message.content
    : message.content.map(part => toResponsesContent(part)),
});

const toResponsesContent = (part: ContentPart): Record<string, any> => {
  if (part.type === 'image_url') return { type: 'input_image', image_url: part.image_url?.url, detail: part.image_url?.detail };
  if (part.type === 'file_url') return { type: 'input_file', file_url: part.file_url?.url, content_type: part.file_url?.content_type };
  return { type: 'input_text', text: part.text || '' };
};

const toAnthropicMessage = (message: ChatMessage) => ({
  role: message.role,
  content: typeof message.content === 'string'
    ? message.content
    : message.content.map(part => toAnthropicContent(part)),
});

const toAnthropicContent = (part: ContentPart): Record<string, any> => {
  if (part.type === 'image_url') return { type: 'image', source: { type: 'url', url: part.image_url?.url } };
  if (part.type === 'file_url') return { type: 'document', source: { type: 'url', url: part.file_url?.url } };
  return { type: 'text', text: part.text || '' };
};

const normalizeTools = (tools: any, protocol: PlaygroundProtocol) => {
  if (!Array.isArray(tools) || protocol === 'chat') return tools;
  return tools.map(tool => {
    if (tool?.type !== 'function' || !tool.function) return tool;
    const definition = tool.function;
    if (protocol === 'responses') {
      return { type: 'function', name: definition.name, description: definition.description, parameters: definition.parameters, strict: definition.strict };
    }
    return { name: definition.name, description: definition.description, input_schema: definition.parameters, strict: definition.strict };
  });
};

const normalizeToolChoice = (choice: any, protocol: PlaygroundProtocol) => {
  if (choice === undefined || choice === null || protocol === 'chat') return choice;
  if (protocol === 'responses') {
    if (choice?.type === 'function' && choice.function?.name) return { type: 'function', name: choice.function.name };
    return choice;
  }
  if (typeof choice === 'string') {
    if (choice === 'required') return { type: 'any' };
    if (choice === 'none') return undefined;
    return { type: choice };
  }
  if (choice?.type === 'function' && choice.function?.name) return { type: 'tool', name: choice.function.name };
  return choice;
};

const reasoningBudget = (effort: string) => {
  if (effort === 'high') return 8192;
  if (effort === 'medium') return 4096;
  return 1024;
};

const normalizeUsage = (protocol: PlaygroundProtocol, usage: any) => {
  if (!usage) return null;
  if (protocol === 'chat') {
    const input = usage.prompt_tokens || 0;
    const output = usage.completion_tokens || 0;
    return { input, output, total: usage.total_tokens || input + output };
  }
  const input = usage.input_tokens || 0;
  const output = usage.output_tokens || 0;
  return { input, output, total: usage.total_tokens || input + output };
};

const mergeAnthropicUsage = (current: ParsedProtocolResponse['usage'], usage: any) => {
  const input = usage.input_tokens ?? current?.input ?? 0;
  const output = usage.output_tokens ?? current?.output ?? 0;
  return { input, output, total: input + output };
};

const appendResponsesItem = (result: ParsedProtocolResponse, item: any, outputIndex?: number) => {
  if (!item) return;
  if (item.type === 'message') {
    for (const part of item.content || []) {
      if (part.type === 'output_text' || part.type === 'text' || part.type === 'refusal') {
        result.content += part.text || part.refusal || '';
      }
    }
  }
  if (item.type === 'reasoning') {
    for (const part of item.summary || item.content || []) result.reasoningContent += part.text || '';
  }
  if (item.type === 'function_call') {
    upsertToolCall(result.toolCalls, item.call_id || item.id, item.name, item.arguments || '', outputIndex ?? item.output_index);
  }
};

const appendAnthropicBlock = (result: ParsedProtocolResponse, block: any, index?: number) => {
  if (!block) return;
  if (block.type === 'text') result.content += block.text || '';
  if (block.type === 'thinking' || block.type === 'redacted_thinking') result.reasoningContent += block.thinking || '';
  if (block.type === 'tool_use') {
    const input = block.input && typeof block.input === 'object' && Object.keys(block.input).length === 0 ? '' : stringifyArguments(block.input);
    upsertToolCall(result.toolCalls, block.id, block.name, input, index);
  }
};

const parseChatToolCalls = (calls: any): PlaygroundToolCall[] => !Array.isArray(calls) ? [] : calls.map(call => ({
  id: call.id,
  name: call.function?.name || call.name || '',
  arguments: stringifyArguments(call.function?.arguments ?? call.arguments),
}));

const appendChatToolCallDeltas = (calls: PlaygroundToolCall[], deltas: any) => {
  if (!Array.isArray(deltas)) return;
  for (const delta of deltas) {
    appendToolCallDelta(calls, delta.id, delta.function?.name, delta.function?.arguments || '', delta.index);
  }
};

const appendToolCallDelta = (calls: PlaygroundToolCall[], id: string | undefined, name: string | undefined, delta: string, index?: number) => {
  const target = findToolCall(calls, id, index);
  if (target) {
    if (name) target.name = name;
    target.arguments += delta;
    return;
  }
  calls.push({ id, index, name: name || `tool_${index ?? calls.length}`, arguments: delta });
};

const upsertToolCall = (calls: PlaygroundToolCall[], id: string | undefined, name: string | undefined, args: any, index?: number) => {
  const target = findToolCall(calls, id, index);
  const argumentsText = stringifyArguments(args);
  if (target) {
    target.id = id || target.id;
    target.index = index ?? target.index;
    target.name = name || target.name;
    target.arguments = argumentsText || target.arguments;
    return;
  }
  calls.push({ id, index, name: name || `tool_${index ?? calls.length}`, arguments: argumentsText });
};

const findToolCall = (calls: PlaygroundToolCall[], id?: string, index?: number) => {
  if (id) {
    const byId = calls.find(call => call.id === id);
    if (byId) return byId;
  }
  return index === undefined ? undefined : calls.find(call => call.index === index);
};

const stringifyArguments = (value: any) => {
  if (typeof value === 'string') return value;
  if (value === undefined || value === null) return '';
  try { return JSON.stringify(value); } catch { return String(value); }
};
