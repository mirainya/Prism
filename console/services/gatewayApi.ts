import { request } from './request';

// ========== 网关 v2 路由表(gw_*)管理 API ==========
// 后端: /api/admin/gw/*。gw_abilities 是「某 key 能跑某 model」的唯一路由记录;
// gw_model_meta 是元数据面(显示名/思考档),永不参与路由。

export interface GwChannel {
  id: number;
  name: string;
  protocol: string; // openai/anthropic/volcengine/google
  base_url: string;
  extra_headers: Record<string, any> | null;
  config: Record<string, any> | null;
  status: number;
  sort: number;
  created_at?: string;
  updated_at?: string;
}

export interface GwChannelKey {
  id: number;
  channel_id: number;
  name: string;
  api_key: string;
  weight: number;
  status: number;
  max_conc: number;
  current_conc: number;
}

export interface GwAbility {
  id: number;
  model_name: string;
  channel_id: number;
  key_id: number;
  vendor_model: string;
  priority: number;
  price_mode: string;
  input_price: string;
  output_price: string;
  status: number;
  channel_name?: string;
  protocol?: string;
  key_name?: string;
}

export interface GwModelMeta {
  model_name: string;
  display_name: string;
  thinking_config: any;
  max_tokens: number;
  features: any;
  group_name: string;
  status: number;
  sort: number;
}

// ---------- 渠道 ----------
export const fetchGwChannels = async (): Promise<GwChannel[]> =>
  (await request<GwChannel[]>('/admin/gw/channels')) || [];

export const createGwChannel = async (data: Partial<GwChannel>): Promise<GwChannel> =>
  request<GwChannel>('/admin/gw/channels', { method: 'POST', body: JSON.stringify(data) });

export const updateGwChannel = async (id: number, data: Record<string, any>): Promise<void> => {
  await request(`/admin/gw/channels/${id}`, { method: 'PUT', body: JSON.stringify(data) });
};

export const deleteGwChannel = async (id: number): Promise<void> => {
  await request(`/admin/gw/channels/${id}`, { method: 'DELETE' });
};

export const reorderGwChannels = async (ids: number[]): Promise<void> => {
  await request('/admin/gw/channels/reorder', { method: 'POST', body: JSON.stringify({ ids }) });
};

// ---------- 渠道 key ----------
export const fetchGwKeys = async (channelId: number): Promise<GwChannelKey[]> =>
  (await request<GwChannelKey[]>(`/admin/gw/channels/${channelId}/keys`)) || [];

export const createGwKey = async (data: Partial<GwChannelKey>): Promise<GwChannelKey> =>
  request<GwChannelKey>('/admin/gw/keys', { method: 'POST', body: JSON.stringify(data) });

export const updateGwKey = async (id: number, data: Record<string, any>): Promise<void> => {
  await request(`/admin/gw/keys/${id}`, { method: 'PUT', body: JSON.stringify(data) });
};

export const deleteGwKey = async (id: number): Promise<void> => {
  await request(`/admin/gw/keys/${id}`, { method: 'DELETE' });
};

// ---------- 能力(路由索引) ----------
export const fetchGwAbilities = async (params?: {
  model?: string;
  channel_id?: number;
  key_id?: number;
}): Promise<GwAbility[]> => {
  const q = new URLSearchParams();
  if (params?.model) q.set('model', params.model);
  if (params?.channel_id) q.set('channel_id', String(params.channel_id));
  if (params?.key_id) q.set('key_id', String(params.key_id));
  const qs = q.toString();
  return (await request<GwAbility[]>(`/admin/gw/abilities${qs ? '?' + qs : ''}`)) || [];
};

export const updateGwAbility = async (id: number, data: Record<string, any>): Promise<void> => {
  await request(`/admin/gw/abilities/${id}`, { method: 'PUT', body: JSON.stringify(data) });
};

export const deleteGwAbility = async (id: number): Promise<void> => {
  await request(`/admin/gw/abilities/${id}`, { method: 'DELETE' });
};

// ---------- 对话模型(可路由模型 + 元数据 + 可用性) ----------
export interface GwModel {
  model_name: string;
  display_name: string;
  thinking_config: any;
  max_tokens: number;
  features: any;
  group_name: string;     // 手动分组名(空=按源渠道分组)
  source_channel: string; // 最高优先级 ability 所属渠道名(分组兜底)
  meta_status: number | null;
  sort: number;
  key_total: number;
  key_available: number;
}

export const fetchGwModels = async (): Promise<GwModel[]> =>
  (await request<GwModel[]>('/admin/gw/models')) || [];

// 按传入 model_name 顺序调整对话模型排序(后端写 gw_model_meta.sort 升序)
export const reorderGwModels = async (names: string[]): Promise<void> => {
  await request('/admin/gw/models/reorder', { method: 'POST', body: JSON.stringify({ names }) });
};

// ---------- 元数据 ----------
export const fetchGwModelMeta = async (): Promise<GwModelMeta[]> =>
  (await request<GwModelMeta[]>('/admin/gw/model-meta')) || [];

export const upsertGwModelMeta = async (
  modelName: string,
  data: Partial<GwModelMeta>
): Promise<GwModelMeta> =>
  request<GwModelMeta>(`/admin/gw/model-meta/${encodeURIComponent(modelName)}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });

export const deleteGwModelMeta = async (modelName: string): Promise<void> => {
  await request(`/admin/gw/model-meta/${encodeURIComponent(modelName)}`, { method: 'DELETE' });
};

export const deleteGwModel = async (modelName: string): Promise<void> => {
  await request(`/admin/gw/models/${encodeURIComponent(modelName)}`, { method: 'DELETE' });
};

// ---------- 拉取 / 导入(以 key 为单位,写 gw_abilities) ----------
export interface GwUpstreamModel {
  id: string;
  imported: boolean;
}

export const discoverGwKeyModels = async (keyId: number): Promise<GwUpstreamModel[]> =>
  (await request<GwUpstreamModel[]>(`/admin/gw/keys/${keyId}/discover`)) || [];

export interface GwImportItem {
  model_name: string;
  vendor_model?: string;
  display_name?: string;
}

export interface GwImportResult {
  abilities_added: number;
  meta_added: number;
}

export const importGwKeyModels = async (
  keyId: number,
  models: GwImportItem[]
): Promise<GwImportResult> =>
  request<GwImportResult>(`/admin/gw/keys/${keyId}/import`, {
    method: 'POST',
    body: JSON.stringify({ key_id: keyId, models }),
  });
