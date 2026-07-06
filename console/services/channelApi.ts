import { Channel, ChannelAccount, AccountCircuitState } from '../types';
import { request } from './request';

export const fetchChannels = async (): Promise<Channel[]> => {
  const data = await request<any[]>('/admin/channels');
  return data.map(ch => ({
    id: String(ch.id),
    type: ch.type,
    name: ch.name,
    baseUrl: ch.base_url,
    config: ch.config || {},
    status: ch.status,
    sort: ch.sort || 0,
    accountsCount: ch.accounts_count || 0,
    createdAt: ch.created_at,
    updatedAt: ch.updated_at,
  }));
};

export const reorderChannels = async (ids: number[]): Promise<void> => {
  await request('/admin/channels/reorder', {
    method: 'POST',
    body: JSON.stringify({ ids }),
  });
};

export const getChannel = async (id: string): Promise<Channel> => {
  const ch = await request<any>(`/admin/channels/${id}`);
  return {
    id: String(ch.id),
    type: ch.type,
    name: ch.name,
    baseUrl: ch.base_url,
    config: ch.config || {},
    status: ch.status,
    sort: ch.sort || 0,
    accountsCount: ch.accounts_count || 0,
    createdAt: ch.created_at,
    updatedAt: ch.updated_at,
  };
};

export const createChannel = async (data: {
  type: string;
  name: string;
  base_url: string;
  config?: Record<string, any>;
}): Promise<Channel> => {
  const ch = await request<any>('/admin/channels', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  return {
    id: String(ch.id),
    type: ch.type,
    name: ch.name,
    baseUrl: ch.base_url,
    config: {},
    status: ch.status,
    sort: ch.sort || 0,
    accountsCount: 0,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  };
};

export const updateChannel = async (id: string, data: {
  name?: string;
  base_url?: string;
  config?: Record<string, any>;
  status?: number;
}): Promise<void> => {
  await request(`/admin/channels/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
};

export const deleteChannel = async (id: string): Promise<void> => {
  await request(`/admin/channels/${id}`, { method: 'DELETE' });
};

// 渠道账号管理
const mapCircuitStates = (raw: any[]): AccountCircuitState[] =>
  (raw || []).map(s => ({
    modelCode: s.model_code,
    disabledUntil: s.disabled_until,
    reason: s.reason || '',
    statusCode: s.status_code || 0,
    failCount: s.fail_count || 0,
  }));

const mapAccount = (acc: any): ChannelAccount => ({
  id: String(acc.id),
  channelId: String(acc.channel_id),
  name: acc.name,
  apiKey: acc.api_key,
  maskedKey: acc.masked_key || '',
  config: acc.config || {},
  weight: acc.weight,
  maxTasks: acc.max_tasks || 0,
  status: acc.status,
  currentTasks: acc.current_tasks || 0,
  supportedModels: acc.supported_models || [],
  circuitStates: mapCircuitStates(acc.circuit_states),
  createdAt: acc.created_at,
  updatedAt: acc.updated_at,
});

export const fetchChannelAccounts = async (channelId?: string): Promise<ChannelAccount[]> => {
  const url = channelId ? `/admin/channel-accounts?channel_id=${channelId}` : '/admin/channel-accounts';
  const data = await request<any[]>(url);
  return data.map(mapAccount);
};

export const getChannelAccount = async (id: string): Promise<ChannelAccount> => {
  const acc = await request<any>(`/admin/channel-accounts/${id}`);
  return mapAccount(acc);
};

export const fetchAccountCircuitStates = async (id: string): Promise<AccountCircuitState[]> => {
  const data = await request<any[]>(`/admin/channel-accounts/${id}/circuit-states`);
  return mapCircuitStates(data);
};

export const clearCircuitState = async (id: string, modelCode: string): Promise<void> => {
  await request(`/admin/channel-accounts/${id}/circuit-states/${encodeURIComponent(modelCode)}`, { method: 'DELETE' });
};

export const createChannelAccount = async (data: {
  channel_id: number;
  name: string;
  api_key: string;
  config?: Record<string, any>;
  weight?: number;
  max_tasks?: number;
  supported_models?: string[];
}): Promise<ChannelAccount> => {
  const acc = await request<any>('/admin/channel-accounts', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  return {
    id: String(acc.id),
    channelId: String(acc.channel_id),
    name: acc.name,
    apiKey: '',
    maskedKey: '',
    config: {},
    weight: acc.weight,
    maxTasks: acc.max_tasks || 0,
    status: acc.status,
    currentTasks: 0,
    supportedModels: data.supported_models || [],
    circuitStates: [],
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  };
};

export const updateChannelAccount = async (id: string, data: {
  name?: string;
  api_key?: string;
  config?: Record<string, any>;
  weight?: number;
  max_tasks?: number;
  status?: number;
  supported_models?: string[];
}): Promise<void> => {
  await request(`/admin/channel-accounts/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
};

export const deleteChannelAccount = async (id: string): Promise<void> => {
  await request(`/admin/channel-accounts/${id}`, { method: 'DELETE' });
};
