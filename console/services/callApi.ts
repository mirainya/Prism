import { request } from './request';

export type APICallStatus = 'received' | 'in_progress' | 'completed' | 'failed' | 'cancelled';
export type APICallAttemptStatus = 'started' | 'completed' | 'failed' | 'cancelled';
export type APICallRouteKind = 'gateway_v2' | 'capability' | 'video';

export interface APICall {
  id: string;
  request_id: string;
  user_id: number;
  token_id: number;
  endpoint: string;
  operation: string;
  model: string;
  status: APICallStatus;
  is_stream: boolean;
  background: boolean;
  store: boolean;
  resource_type: string;
  resource_id: string;
  conversation_id: number;
  final_attempt_id: number;
  attempt_count: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_input_tokens: number;
  reasoning_output_tokens: number;
  usage?: unknown;
  reserved_amount: string;
  final_cost: string;
  refunded_amount: string;
  http_status: number;
  error_type: string;
  error_code: string;
  error_message: string;
  error_param?: unknown;
  error_retryable: boolean;
  started_at: string;
  first_byte_at: string | null;
  completed_at: string | null;
  duration_ms: number;
  ttft_ms: number;
  client_disconnected: boolean;
  created_at: string;
  updated_at: string;
}

export interface APICallAttempt {
  id: number;
  call_id: string;
  attempt_no: number;
  route_kind: APICallRouteKind;
  stage: string;
  ability_id: number;
  channel_id: number;
  key_id: number;
  endpoint_id: number;
  account_id: number;
  protocol: string;
  vendor_model: string;
  transport: string;
  request_path: string;
  status: APICallAttemptStatus;
  http_status: number;
  error_type: string;
  error_code: string;
  error_message: string;
  error_retryable: boolean;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_input_tokens: number;
  reasoning_output_tokens: number;
  usage?: unknown;
  duration_ms: number;
  ttft_ms: number;
  provider_response_id: string;
  started_at: string;
  first_byte_at: string | null;
  completed_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface APICallBillingLog {
  id: number;
  created_at: string;
  updated_at: string;
  idempotent_key: string;
  token_id: number;
  user_id: number;
  call_id: string;
  attempt_id: number;
  phase: string;
  pricing_snapshot?: unknown;
  amount: string;
  type: string;
  status: string;
  remark: string;
}

export interface APICallPayload {
  id: number;
  call_id: string;
  attempt_id: number;
  kind: string;
  content_type: string;
  data: string;
  encrypted: boolean;
  truncated: boolean;
  original_bytes: number;
  expires_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface CallListParams {
  page?: number;
  page_size?: number;
  snapshot_at?: string;
  request_id?: string;
  call_id?: string;
  status?: APICallStatus | '';
  endpoint?: string;
  model?: string;
  start_date?: string;
  end_date?: string;
  user_id?: number;
  token_id?: number;
  route_kind?: APICallRouteKind | '';
  channel_id?: number;
  transport?: string;
}

export interface CallListResponse {
  items: APICall[];
  total: number;
  page: number;
  page_size: number;
  snapshot_at: string;
}

export interface APICallDetail {
  call: APICall;
  attempts: APICallAttempt[];
  billing_logs: APICallBillingLog[];
  payloads: APICallPayload[];
}

export const fetchCalls = async (params: CallListParams = {}): Promise<CallListResponse> => {
  const query = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') query.set(key, String(value));
  });
  const suffix = query.toString();
  return request<CallListResponse>(suffix ? `/calls?${suffix}` : '/calls');
};

export const fetchCallDetail = async (id: string): Promise<APICallDetail> => {
  const detail = await request<APICallDetail>(`/calls/${encodeURIComponent(id)}`);
  return {
    ...detail,
    attempts: detail.attempts || [],
    billing_logs: detail.billing_logs || [],
    payloads: detail.payloads || [],
  };
};
