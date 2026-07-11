import { PlaygroundCapability } from '../../types';

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

export interface TaskResult {
  taskNo: string;
  status: string;
  progress: number;
  result: any;
  error: string;
  cost: number;
  capability?: string;
  capabilityName?: string;
  capabilityType?: PlaygroundCapability['type'];
  channel?: string;
  refunded?: boolean;
  params?: Record<string, any>;
  rawParams?: Record<string, any>;
  mappedParams?: Record<string, any>;
  vendorResponse?: any;
  vendorTaskId?: string;
  createdAt?: string;
  startedAt?: string;
  completedAt?: string;
}

export type MediaItem = {
  type: 'image' | 'video';
  url: string;
  label: string;
};

export type MediaContext = {
  capabilityType?: PlaygroundCapability['type'];
};
