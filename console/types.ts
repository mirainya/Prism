
export enum UserRole {
  ADMIN = 'admin',
  USER = 'user',
}

export interface User {
  id: string;
  username: string;
  role: UserRole;
  balance: number;
  avatar?: string;
  createdAt: string;
}

export interface ApiToken {
  id: string;
  name: string;
  key: string;
  balance: number;
  totalUsed: number;
  status: 'active' | 'expired';
  xfsStorage: {
    configured: boolean;
    keyHint: string;
  };
}

export interface TaskLog {
  id: string;
  task_no: string;
  call_id?: string;
  capability: string;
  capability_name: string;
  channel: string;
  status: string;
  progress: number;
  cost: string | number;
  refunded: boolean;
  error?: string;
  created_at: string;
  completed_at?: string;
}

export interface TaskDetail extends TaskLog {
  gateway_call_id?: number;
  raw_params?: unknown;
  result?: unknown;
  request_payload_expired?: boolean;
  result_payload_expired?: boolean;
  vendor_task_id?: string;
  started_at?: string;
  callback_status?: string;
  callback_attempts?: number;
}

export interface DashboardStats {
  today: {
    total_requests: number;
    total_cost: number;
    success_count: number;
    failed_count: number;
    error_rate: number;
    request_trend: number;
    cost_trend: number;
  };
  weekly_trend: Array<{
    date: string;
    requests: number;
    cost: number;
    errors: number;
  }>;
  capability_dist: Array<{
    capability: string;
    count: number;
  }>;
}

// 兼容旧的 LogEntry 接口
export interface LogEntry {
  id: string;
  traceId: string;
  model: string;
  prompt: string;
  response: string;
  status: number;
  latency: number;
  cost: number;
  timestamp: string;
  userId: string;
}

// 渠道请求日志
export interface ChannelRequestLog {
  id: number;
  task_id: number;
  task_no: string;
  conversation_id?: number;
  channel_id: number;
  account_id: number;
  capability_code: string;
  request_type: 'submit' | 'poll' | 'callback' | 'chat';
  is_stream?: boolean;
  model_code?: string;
  vendor_model?: string;
  request_path?: string;
  finish_reason?: string;
  response_preview?: string;
  usage_prompt_tokens?: number;
  usage_completion_tokens?: number;
  usage_total_tokens?: number;
  method: string;
  url: string;
  request_headers: string;
  request_body: string;
  status_code: number;
  response_body: string;
  duration_ms: number;
  error_message: string;
  request_at: string;
  created_at: string;
  channel_name?: string;
  channel_type?: string;
  capability_name?: string;
}

// ========== Chat 模型相关 ==========

// 思考模式配置
export interface ThinkingOption {
  label: string;
  value: string;
  body?: Record<string, any>; // 合并进上游请求体的原始 JSON,空=不注入
}
export interface ThinkingConfig {
  locked?: boolean;
  default?: string;
  options: ThinkingOption[];
}

export interface PlaygroundModelInfo {
  id: string;
  owned_by: string;
  max_tokens?: number;
  group?: string; // 分组名(手动组名/源渠道/未分组),与对话模型页同频
  supports_stream?: boolean;
  default_stream?: boolean;
  supports_tools?: boolean;
  supports_response_format?: boolean;
  supports_multimodal?: boolean;
	supported_operations: string[];
	supported_endpoints: string[];
  thinking?: {
    default: string;
    locked: boolean;
    options: { label: string; value: string }[];
  } | null;
}

export interface Conversation {
  id: number;
  userId?: number;
  tokenId?: number;
  title: string;
  model: string;
  systemPrompt: string;
  lastCallId?: string;
  lastRequestLogId?: number;
  lastStatus?: string;
  totalTokens: number;
  messageCount: number;
  totalCost: string | number;
  status: number;
  createdAt: string;
  updatedAt: string;
}

export interface ChatMessage {
  id: number;
  conversationId: number;
  callId?: string;
  callStatus?: string;
  requestLogId?: number;
  role: 'system' | 'user' | 'assistant';
  content: string;
  attachments?: string;
  reasoningContent?: string;
  finishReason?: string;
  inputTokens: number;
  outputTokens: number;
  model: string;
  channelId?: number;
  accountId?: number;
  latencyMs: number;
  cost: string | number;
  createdAt: string;
}

export interface ConversationCanonicalItem {
  id: string;
  direction: 'input' | 'output';
  ordinal: number;
  canonical: Record<string, any>;
}

export interface ConversationTurnRecord {
  id: string;
  conversationId: number;
  sequence: string;
  callId: string;
  requestLogId?: number;
  model: string;
  providerResponseId?: string;
  status: 'completed' | 'failed' | 'aborted';
  contextMode: 'legacy' | 'new' | 'explicit' | 'inferred' | 'snapshot';
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  cost: string | number;
  latencyMs: number;
  finishReason?: string;
  errorType?: string;
  errorCode?: string;
  errorMessage?: string;
  createdAt: string;
  items: ConversationCanonicalItem[];
}

export interface PlaygroundConversation extends Conversation {}

export interface PlaygroundMessage extends ChatMessage {}

export interface PlaygroundDebugDetail {
  conversationId?: number;
  requestLogId?: number;
  status?: string;
  modelCode?: string;
  vendorModel?: string;
  channelId?: number;
  channelName?: string;
  channelType?: string;
  accountId?: number;
  requestPath?: string;
  isStream?: boolean;
  latencyMs?: number;
  statusCode?: number;
  errorMessage?: string;
  finishReason?: string;
  contextMode?: string;
  providerResponseId?: string; // 上游有状态对话ID(火山 response_id)
  responsePreview?: string;
  requestHeaders?: Record<string, any>;
  requestBody?: Record<string, any>;
  responseBody?: any;
  usage?: {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
  };
}

// Provider 类型
export const CHAT_PROVIDERS = [
  {value: 'openai', label: 'OpenAI'},
  {value: 'anthropic', label: 'Anthropic (Claude)'},
  {value: 'google', label: 'Google (Gemini)'},
  {value: 'volcengine', label: '火山引擎 (豆包)'},
];

// 计价模式
export const PRICE_MODES = [
  {value: 'token', label: '按 Token 计费'},
  {value: 'request', label: '按次计费'},
];
