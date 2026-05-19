
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

export interface Channel {
  id: string;
  type: string;
  name: string;
  baseUrl: string;
  config: Record<string, any>;
  status: number;
  accountsCount: number;
  createdAt: string;
  updatedAt: string;
}

export interface ChannelAccount {
  id: string;
  channelId: string;
  name: string;
  apiKey: string;
  maskedKey: string;
  config: Record<string, any>;
  weight: number;
  maxTasks: number;
  status: number;
  currentTasks: number;
  createdAt: string;
  updatedAt: string;
}

export type CapabilityStandardParamType = 'string' | 'number' | 'array' | 'enum';

export interface CapabilityStandardParamSchema {
  type: CapabilityStandardParamType;
  name: string;
  required?: boolean;
  options?: string[];
  enumValues?: string[];
  default?: string | number;
}

export type CapabilityStandardParams = Record<string, CapabilityStandardParamSchema>;

// 能力定义
export interface Capability {
  code: string;
  name: string;
    type: 'image' | 'video' | 'chat' | 'other';
  description: string;
  standardParams: CapabilityStandardParams;
  standardResponse: Record<string, any>;
  status: number;
  createdAt: string;
  updatedAt: string;
}

// 渠道能力配置
export interface ChannelCapability {
  id: string;
  channelId: string;
  capabilityCode: string;
  model: string;
  name: string;
  modelType?: string;
  price: number;
  priceUnit: string;
  resultMode: 'sync' | 'poll' | 'callback';
  requestPath: string;
  requestMethod: string;
  contentType: string;
    // 认证配置
    authLocation: 'header' | 'body' | 'query';
    authKey: string;
    authValuePrefix: string;
    // 轮询配置
  pollPath: string;
    pollMethod: string;
  pollInterval: number;
  pollMaxAttempts: number;
    pollParamMapping: Record<string, any>;
    pollResponseMapping: Record<string, any>;
    // 映射配置
  paramMapping: Record<string, any>;
  responseMapping: Record<string, any>;
  callbackMapping: Record<string, any>;
  extraConfig: Record<string, any>;
  status: number;
  createdAt: string;
  updatedAt: string;
  channel?: Channel;
  capability?: Capability;
}

export interface ApiToken {
  id: string;
  name: string;
  key: string;
    balance: number;
    totalUsed: number;
  status: 'active' | 'expired';
  channelPriorities?: ChannelPriorityItem[];
}

// 渠道优先级配置项
export interface ChannelPriorityItem {
  capabilityCode: string;
  channelId: number;
  priority: number;
}

// 能力及其可用渠道
export interface CapabilityWithChannels {
  code: string;
  name: string;
  type: string;
  description: string;
  standardParams?: CapabilityStandardParams;
  channels: ChannelOption[];
}

export interface PlaygroundCapability extends CapabilityWithChannels {
  standardParams: CapabilityStandardParams;
}

// 渠道选项
export interface ChannelOption {
  channelId: number;
  channelType: string;
  channelName: string;
  model: string;
  price: number;
}

export interface TaskLog {
  id: string;
  task_no: string;
  capability: string;
  capability_name: string;
  channel: string;
  status: string;
  progress: number;
  cost: number;
    refunded: boolean;
  error?: string;
  created_at: string;
  completed_at?: string;
}

export interface TaskDetail extends TaskLog {
  raw_params?: Record<string, any>;
  vendor_response?: Record<string, any>;
  result?: Record<string, any>;
  vendor_task_id?: string;
  started_at?: string;
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

export interface ChatModel {
  id: number;
  code: string;
  name: string;
  provider: string;
  description: string;
  features?: string[];
  maxTokens?: number;
  status: number;
  createdAt: string;
  updatedAt: string;
}

export interface ChatModelChannel {
  id: number;
  modelCode: string;
  channelId: number;
  vendorModel: string;
  priority: number;
  priceMode: 'token' | 'request';
  inputPrice: number;
  outputPrice: number;
  requestPath: string;
  timeout: number;
  supportsStream?: boolean;
  defaultStream?: boolean;
  extraHeaders: Record<string, string>;
  extraConfig: Record<string, any>;
  status: number;
  createdAt: string;
  updatedAt: string;
  chatModel?: ChatModel;
  channel?: Channel;
}

export interface PlaygroundModelInfo {
  id: string;
  owned_by: string;
  supports_stream?: boolean;
  default_stream?: boolean;
  supports_tools?: boolean;
  supports_response_format?: boolean;
  supports_multimodal?: boolean;
}

export interface Conversation {
  id: number;
  userId: number;
  tokenId: number;
  title: string;
  model: string;
  systemPrompt: string;
  lastRequestLogId?: number;
  lastStatus?: string;
  totalTokens: number;
  messageCount: number;
  totalCost: number;
  status: number;
  createdAt: string;
  updatedAt: string;
}

export interface ChatMessage {
  id: number;
  conversationId: number;
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
  cost: number;
  createdAt: string;
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

export interface PlaygroundTaskListParams {
  page?: number;
  page_size?: number;
  status?: string;
  capability?: string;
  keyword?: string;
}

export interface PlaygroundTaskListItem {
  id: string;
  taskNo: string;
  capability: string;
  capabilityName: string;
  channel: string;
  status: string;
  progress: number;
  cost: number;
  refunded: boolean;
  error?: string;
  createdAt: string;
  completedAt?: string;
}

export interface PlaygroundTaskListResponse {
  items: PlaygroundTaskListItem[];
  total: number;
  page: number;
  page_size: number;
}

export interface PlaygroundTaskDetail {
  taskId: string;
  taskNo: string;
  status: string;
  progress: number;
  result: any;
  error: string;
  cost: number;
  rawParams?: Record<string, any>;
  mappedParams?: Record<string, any>;
  vendorResponse?: any;
  vendorTaskId?: string;
  createdAt?: string;
  startedAt?: string;
  completedAt?: string;
}

// Provider 类型
export const CHAT_PROVIDERS = [
  {value: 'openai', label: 'OpenAI'},
  {value: 'anthropic', label: 'Anthropic (Claude)'},
  {value: 'google', label: 'Google (Gemini)'},
];

// 计价模式
export const PRICE_MODES = [
  {value: 'token', label: '按 Token 计费'},
  {value: 'request', label: '按次计费'},
];
