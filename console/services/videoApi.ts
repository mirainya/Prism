import { request } from './request';

// ========== 视频引擎管理 API ==========

export interface VideoChannel {
  id: number;
  name: string;
  adapter_type: string;
  base_url: string;
  status: string;
  priority: number;
  models: any;
  capabilities: any;
  pricing: any;
  asset_resolver: string;
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

export interface VideoTask {
  id: string;
  call_id: string;
  user_id: number;
  token_id: number;
  channel_id: number;
  key_id: number;
  model: string;
  status: string;
  progress: number;
  task_mode: string;
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
  estimated_cost: number | string;
  markup_ratio: number | string;
  final_cost: number | string;
  billing_status: string;
  result_json: any;
  error_message: string;
  poll_count: number;
  callback_url: string;
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

// ===== Channels =====

export const fetchVideoChannels = () =>
  request<VideoChannel[]>('/admin/video/channels');

export const getVideoChannel = (id: number) =>
  request<VideoChannel>(`/admin/video/channels/${id}`);

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

export const fetchVideoTasks = (params: { page?: number; page_size?: number; status?: string; model?: string }) => {
  const qs = new URLSearchParams();
  if (params.page) qs.set('page', String(params.page));
  if (params.page_size) qs.set('page_size', String(params.page_size));
  if (params.status) qs.set('status', params.status);
  if (params.model) qs.set('model', params.model);
  return request<{ total: number; items: VideoTask[] }>(`/admin/video/tasks?${qs.toString()}`);
};

export const getVideoTask = (id: string) =>
  request<VideoTask>(`/admin/video/tasks/${id}`);

// ===== Stats =====

export const fetchVideoStats = () =>
  request<VideoStats>('/admin/video/stats');
