export interface ContentPart {
  type: 'text' | 'image_url' | 'file_url';
  text?: string;
  image_url?: { url: string; detail?: string };
  file_url?: { url: string; content_type?: string };
}

export interface Attachment {
  id: string;
  file: File;
  preview?: string;
  uploading: boolean;
  uploaded: boolean;
  url?: string;
  thUrl?: string;
  contentType: string;
  error?: string;
}

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant';
  content: string | ContentPart[];
  reasoningContent?: string;
  toolCalls?: PlaygroundToolCall[];
  requestLogId?: number;
  finishReason?: string;
  status?: 'streaming' | 'completed' | 'failed' | 'aborted';
}

export interface PlaygroundToolCall {
  id?: string;
  index?: number;
  name: string;
  arguments: string;
}

export type PlaygroundProtocol = 'chat' | 'responses' | 'anthropic';

export interface ChatState {
  messages: ChatMessage[];
  isStreaming: boolean;
  usage: { input: number; output: number; total?: number; cost?: number } | null;
  latencyMs: number | null;
  statusText: string;
}
