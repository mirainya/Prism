import { request } from './request';

export interface VideoCallPayload {
  id: number;
  call_id: string;
  attempt_id: number;
  /** The unified payload table only stores one request and one result per call. */
  kind: 'request' | 'result';
  content_type: string;
  data: string;
  encrypted: boolean;
  truncated: boolean;
  original_bytes: number;
  expires_at?: string | null;
  created_at: string;
}

export interface VideoTask {
  id: string;
  call_id: string;
  gateway_call_id?: number;
  user_id: number;
  token_id: number;
  channel_id: number;
  key_id: number;
  model: string;
  vendor_model: string;
  status: string;
  execution_state?: string;
  can_resolve?: boolean;
  progress: number;
  task_mode: string;
  service_tier: string;
  prompt: string;
  resolution: string;
  ratio: string;
  duration: number;
  generate_audio: boolean;
  content_json: any;
  params_json: any;
  adapter_type: string;
  provider_task_id: string;
  provider_response?: any;
  provider_metadata?: any;
  route_plan?: any;
  estimated_cost: number | string;
  markup_ratio: number | string;
  final_cost: number | string;
  billing_status: string;
  result_json: any;
  error_message: string;
  poll_count: number;
  callback_url: string;
  call_payloads?: VideoCallPayload[];
  request_payload_expired?: boolean;
  result_payload_expired?: boolean;
  created_at: string;
  submitted_at?: string;
  completed_at?: string;
}

export interface VideoStats {
  channels: number;
  keys: number;
  total_tasks: number;
  active_tasks: number;
}

export interface VideoTaskListParams {
  page?: number;
  page_size?: number;
  keyword?: string;
  status?: string;
  model?: string;
  task_mode?: string;
  service_tier?: string;
  channel_id?: number;
  user_id?: number;
  token_id?: number;
  start_date?: string;
  end_date?: string;
  snapshot_at?: string;
}

export interface VideoTaskPage {
  items: VideoTask[];
  total: number;
  page: number;
  page_size: number;
  snapshot_at?: string;
}

type VideoTaskPageResponse = Partial<VideoTaskPage> & {
  items?: VideoTask[];
  total?: number | string;
  page?: number | string;
  page_size?: number | string;
};

const positiveInteger = (value: unknown, fallback: number) => {
  const parsed = typeof value === 'number' ? value : Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : fallback;
};

// ===== Tasks =====

export const fetchVideoTasks = async (params: VideoTaskListParams = {}): Promise<VideoTaskPage> => {
  const page = positiveInteger(params.page, 1);
  const pageSize = Math.min(100, positiveInteger(params.page_size, 20));
  const qs = new URLSearchParams();
  Object.entries({ ...params, page, page_size: pageSize }).forEach(([key, value]) => {
    if (value !== undefined && value !== '') qs.set(key, String(value));
  });
  const response = await request<VideoTaskPageResponse>(`/admin/video/tasks?${qs.toString()}`);
  const total = positiveInteger(response?.total, 0);
  return {
    items: Array.isArray(response?.items) ? response.items : [],
    total,
    // Older unified handlers omit these two fields. Keep the requested values
    // so the UI can calculate page bounds without guessing from row counts.
    page: positiveInteger(response?.page, page),
    page_size: positiveInteger(response?.page_size, pageSize),
    snapshot_at: typeof response?.snapshot_at === 'string' ? response.snapshot_at : undefined,
  };
};

export const getVideoTask = (id: string) =>
  request<VideoTask>(`/admin/video/tasks/${id}`);

export const resolveVideoTask = (id: string) =>
  request<{ id: string; status: string; resolution: string }>(`/admin/video/tasks/${id}/resolve`, {
    method: 'POST',
    body: JSON.stringify({ resolution: 'terminated_unknown' }),
  });

// ===== Stats =====

export const fetchVideoStats = () =>
  request<VideoStats>('/admin/video/stats');
