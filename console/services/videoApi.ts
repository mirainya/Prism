import { request } from './request';

// ========== 视频引擎管理 API ==========

export interface VideoModelMapping {
  model_name: string;
  vendor_model: string;
}

export interface DiscoveredVideoModel {
  vendor_model: string;
  public_models: string[];
}

export interface VideoChannel {
  id: number;
  name: string;
  adapter_type: string;
  adapter_profile?: string;
  base_url: string;
  status: string;
  priority: number;
  request_timeout_seconds?: number;
  models: VideoModelMapping[] | string[] | string;
  capabilities: any;
  supports_first_frame?: boolean;
  supports_last_frame?: boolean;
  supports_audio?: boolean;
  supports_web_search?: boolean;
  cancel_mode?: string;
  pricing: any;
  pricing_mode?: string;
  fixed_price?: number | string;
  markup_ratio?: number | string;
  asset_resolver: string;
  result_storage_enabled?: boolean;
  extra_config: any;
  created_at?: string;
  updated_at?: string;
}

export interface VideoChannelKey {
  id: number;
  channel_id: number;
  label: string;
  masked_key: string;
  weight: number;
  max_concurrency: number;
  current_concurrency: number;
  status: string;
  total_calls: number;
}

export interface VideoCallPayload {
  id: number;
  call_id: string;
  attempt_id: number;
  kind: string;
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
  user_id: number;
  token_id: number;
  channel_id: number;
  key_id: number;
  model: string;
  vendor_model: string;
  status: string;
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

// ===== Channels =====

export const fetchVideoChannels = () =>
  request<VideoChannel[]>('/admin/video/channels');

export const getVideoChannel = (id: number) =>
  request<VideoChannel>(`/admin/video/channels/${id}`);

export const discoverVideoChannelModels = (id: number) =>
  request<{ models: DiscoveredVideoModel[] }>(`/admin/video/channels/${id}/models/discover`);

export const createVideoChannel = (data: Partial<VideoChannel>) =>
  request<VideoChannel>('/admin/video/channels', { method: 'POST', body: JSON.stringify(data) });

export const updateVideoChannel = (id: number, data: Partial<VideoChannel>) =>
  request<VideoChannel>(`/admin/video/channels/${id}`, { method: 'PUT', body: JSON.stringify(data) });

export const deleteVideoChannel = (id: number) =>
  request<null>(`/admin/video/channels/${id}`, { method: 'DELETE' });

// ===== Keys =====

export const fetchVideoKeys = (channelId: number) =>
  request<VideoChannelKey[]>(`/admin/video/channels/${channelId}/keys`);

export const createVideoKey = (channelId: number, data: { api_key: string; label?: string; weight?: number; max_concurrency?: number; status?: string }) =>
  request<VideoChannelKey>(`/admin/video/channels/${channelId}/keys`, { method: 'POST', body: JSON.stringify(data) });

export const updateVideoKey = (id: number, data: Partial<{ label: string; weight: number; max_concurrency: number; status: string }>) =>
  request<VideoChannelKey>(`/admin/video/keys/${id}`, { method: 'PUT', body: JSON.stringify(data) });

export const deleteVideoKey = (id: number) =>
  request<null>(`/admin/video/keys/${id}`, { method: 'DELETE' });

// ===== Tasks =====

export const fetchVideoTasks = (params: VideoTaskListParams) => {
  const qs = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') qs.set(key, String(value));
  });
  return request<{ total: number; items: VideoTask[]; snapshot_at?: string }>(`/admin/video/tasks?${qs.toString()}`);
};

export const getVideoTask = (id: string) =>
  request<VideoTask>(`/admin/video/tasks/${id}`);

// ===== Stats =====

export const fetchVideoStats = () =>
  request<VideoStats>('/admin/video/stats');
