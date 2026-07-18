
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
  sort: number;
  accountsCount: number;
  createdAt: string;
  updatedAt: string;
}

// 账号级 per-model 熔断状态
export interface AccountCircuitState {
  modelCode: string;
  disabledUntil: string;
  reason: string;
  statusCode: number;
  failCount: number;
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
  supportedModels: string[];
  circuitStates: AccountCircuitState[];
  createdAt: string;
  updatedAt: string;
}

export type CapabilityStandardParamType = 'string' | 'number' | 'array' | 'enum';

export interface CapabilityStandardParamSchema {
  type: CapabilityStandardParamType;
  name: string;
  description?: string;
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
  sort: number;
  createdAt: string;
  updatedAt: string;
}

// 渠道能力配置
export interface ChannelCapability {
  id: string;
  channelId: string;
  accountId: string;
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
  paramSchema?: Record<string, any> | null;
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
  interactionMode: string;
  paramSchema?: CapabilityStandardParams | null;
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
  raw_params?: Record<string, any>;
  vendor_response?: Record<string, any>;
  result?: Record<string, any>;
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
  contextMode?: string;        // 上下文策略: stateful(B/有状态) | full_history(A/全量历史)
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

export interface PlaygroundTaskListParams {
  page?: number;
  page_size?: number;
  snapshot_at?: string;
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
  snapshot_at?: string;
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
  {value: 'volcengine', label: '火山引擎 (豆包)'},
];

// 计价模式
export const PRICE_MODES = [
  {value: 'token', label: '按 Token 计费'},
  {value: 'request', label: '按次计费'},
];
