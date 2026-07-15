import { request } from './request';

export interface ObservabilityListParams {
  page?: number;
  page_size?: number;
  snapshot_id?: number;
  user_id?: number;
  token_id?: number;
  start_date?: string;
  end_date?: string;
}

export interface APIAccessLog {
  id: number;
  request_id: string;
  call_id: string;
  user_id: number;
  token_id: number;
  actor_type: string;
  method: string;
  path: string;
  route: string;
  query: string;
  status_code: number;
  duration_ms: number;
  ip: string;
  user_agent: string;
  error_code: string;
  created_at: string;
}

export interface AuditEvent {
  id: number;
  request_id: string;
  actor_type: string;
  actor_user_id: number;
  actor_token_id: number;
  action: string;
  resource_type: string;
  resource_id: string;
  outcome: string;
  http_status: number;
  ip: string;
  metadata?: unknown;
  created_at: string;
}

export interface BalanceEntry {
  id: number;
  entry_key: string;
  source_key: string;
  account_type: string;
  account_id: number;
  user_id: number;
  token_id: number;
  direction: string;
  category: string;
  amount: string;
  balance_before: string;
  balance_after: string;
  call_id: string;
  attempt_id: number;
  actor_user_id: number;
  metadata?: unknown;
  created_at: string;
}

export interface ObservabilityListResponse<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
  snapshot_id: number;
}

export interface APIAccessLogListParams extends ObservabilityListParams {
  request_id?: string;
  call_id?: string;
  method?: string;
  path?: string;
  status_code?: number;
  error_code?: string;
}

export interface AuditEventListParams extends ObservabilityListParams {
  request_id?: string;
  action?: string;
  resource_type?: string;
  outcome?: string;
}

export interface BalanceEntryListParams extends ObservabilityListParams {
  account_type?: string;
  direction?: string;
  category?: string;
  call_id?: string;
}

const fetchObservabilityPage = <T>(
  path: string,
  params: object,
): Promise<ObservabilityListResponse<T>> => {
  const query = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && value !== '') {
      query.set(key, String(value));
    }
  });
  const suffix = query.toString();
  return request<ObservabilityListResponse<T>>(`${path}${suffix ? `?${suffix}` : ''}`);
};

export const fetchAPIAccessLogs = (params: APIAccessLogListParams = {}) =>
  fetchObservabilityPage<APIAccessLog>('/observability/access-logs', params);

export const fetchAuditEvents = (params: AuditEventListParams = {}) =>
  fetchObservabilityPage<AuditEvent>('/observability/audit-events', params);

export const fetchBalanceEntries = (params: BalanceEntryListParams = {}) =>
  fetchObservabilityPage<BalanceEntry>('/observability/balance-entries', params);
