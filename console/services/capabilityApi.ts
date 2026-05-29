import { Capability, ChannelCapability, CapabilityWithChannels } from '../types';
import { request } from './request';

export interface CapabilityPrice {
    code: string;
    name: string;
    type: string;
    channels: { channelName: string; price: number; priceUnit: string }[];
}

export const fetchCapabilityPrices = async (): Promise<CapabilityPrice[]> => {
    const data = await request<any[]>('/capability-prices');
    return data || [];
};

export const fetchCapabilities = async (): Promise<Capability[]> => {
  const data = await request<any[]>('/admin/capabilities');
  return data.map(c => ({
    code: c.code,
    name: c.name,
      type: c.type || 'image',
    description: c.description || '',
    standardParams: c.param_schema || c.standard_params || {},
    standardResponse: c.standard_response || {},
    status: c.status,
    createdAt: c.created_at,
    updatedAt: c.updated_at,
  }));
};

export const getCapability = async (code: string): Promise<Capability> => {
  const c = await request<any>(`/admin/capabilities/${code}`);
  return {
    code: c.code,
    name: c.name,
      type: c.type || 'image',
    description: c.description || '',
    standardParams: c.param_schema || c.standard_params || {},
    standardResponse: c.standard_response || {},
    status: c.status,
    createdAt: c.created_at,
    updatedAt: c.updated_at,
  };
};

export const createCapability = async (data: {
  code: string;
  name: string;
    type?: string;
  description?: string;
  standard_params?: Record<string, any>;
  standard_response?: Record<string, any>;
}): Promise<Capability> => {
  const c = await request<any>('/admin/capabilities', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  return {
    code: c.code,
    name: c.name,
      type: c.type || 'image',
    description: c.description || '',
    standardParams: c.param_schema || c.standard_params || {},
    standardResponse: c.standard_response || {},
    status: c.status,
    createdAt: c.created_at,
    updatedAt: c.updated_at,
  };
};

export const updateCapability = async (code: string, data: {
  code?: string;
  name?: string;
    type?: string;
  description?: string;
  standard_params?: Record<string, any>;
  standard_response?: Record<string, any>;
  status?: number;
}): Promise<void> => {
  await request(`/admin/capabilities/${code}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
};

export const deleteCapability = async (code: string): Promise<void> => {
  await request(`/admin/capabilities/${code}`, { method: 'DELETE' });
};

// 渠道能力配置管理
export const fetchChannelCapabilities = async (channelId?: string, capabilityCode?: string): Promise<ChannelCapability[]> => {
  const params = new URLSearchParams();
  if (channelId) params.append('channel_id', channelId);
  if (capabilityCode) params.append('model_code', capabilityCode);
  const url = params.toString() ? `/admin/channel-capabilities?${params}` : '/admin/channel-capabilities';

  const data = await request<any[]>(url);
  return data.map(cc => ({
    id: String(cc.id),
    channelId: String(cc.channel_id),
    capabilityCode: cc.model_code || cc.capability_code,
    model: cc.vendor_model || '',
    name: cc.name || (cc.model && cc.model.name) || '',
    modelType: cc.model?.type || '',
    price: cc.price || 0,
    priceUnit: cc.price_unit || 'request',
    resultMode: cc.interaction_mode || cc.result_mode || 'sync',
    requestPath: cc.request_path || '',
    requestMethod: cc.request_method || 'POST',
    contentType: cc.content_type || 'application/json',
    pollPath: cc.poll_path || '',
    pollMethod: cc.poll_method || 'GET',
    pollInterval: cc.poll_interval || 5,
    pollMaxAttempts: cc.poll_max_attempts || 60,
    pollParamMapping: cc.poll_param_mapping || {},
    pollResponseMapping: cc.poll_response_mapping || {},
    authLocation: cc.auth_location || 'header',
    authKey: cc.auth_key || 'Authorization',
    authValuePrefix: cc.auth_value_prefix ?? '',
    paramMapping: cc.param_mapping || {},
    paramSchema: cc.param_schema || null,
    responseMapping: cc.response_mapping || {},
    callbackMapping: cc.callback_mapping || {},
    extraConfig: cc.extra_config || {},
    status: cc.status,
    createdAt: cc.created_at,
    updatedAt: cc.updated_at,
    channel: cc.channel,
    capability: cc.capability,
  }));
};

export const getChannelCapability = async (id: string): Promise<ChannelCapability> => {
  const cc = await request<any>(`/admin/channel-capabilities/${id}`);
  return {
    id: String(cc.id),
    channelId: String(cc.channel_id),
    capabilityCode: cc.model_code || cc.capability_code,
    model: cc.vendor_model || '',
    name: cc.name || (cc.model && cc.model.name) || '',
    modelType: cc.model?.type || '',
    price: cc.price || 0,
    priceUnit: cc.price_unit || 'request',
    resultMode: cc.interaction_mode || cc.result_mode || 'sync',
    requestPath: cc.request_path || '',
    requestMethod: cc.request_method || 'POST',
    contentType: cc.content_type || 'application/json',
    pollPath: cc.poll_path || '',
    pollMethod: cc.poll_method || 'GET',
    pollInterval: cc.poll_interval || 5,
    pollMaxAttempts: cc.poll_max_attempts || 60,
    pollParamMapping: cc.poll_param_mapping || {},
    pollResponseMapping: cc.poll_response_mapping || {},
    authLocation: cc.auth_location || 'header',
    authKey: cc.auth_key || 'Authorization',
    authValuePrefix: cc.auth_value_prefix ?? '',
    paramMapping: cc.param_mapping || {},
    paramSchema: cc.param_schema || null,
    responseMapping: cc.response_mapping || {},
    callbackMapping: cc.callback_mapping || {},
    extraConfig: cc.extra_config || {},
    status: cc.status,
    createdAt: cc.created_at,
    updatedAt: cc.updated_at,
    channel: cc.channel,
    capability: cc.capability,
  };
};

export const createChannelCapability = async (data: {
  channel_id: number;
  model_code: string;
  model?: string;
  name?: string;
  price?: number;
  price_unit?: string;
  result_mode?: string;
  request_path?: string;
  request_method?: string;
  content_type?: string;
  poll_path?: string;
  poll_interval?: number;
  poll_max_attempts?: number;
  param_mapping?: Record<string, any>;
  response_mapping?: Record<string, any>;
  callback_mapping?: Record<string, any>;
  extra_config?: Record<string, any>;
}): Promise<ChannelCapability> => {
  const cc = await request<any>('/admin/channel-capabilities', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  return {
    id: String(cc.id),
    channelId: String(cc.channel_id),
    capabilityCode: cc.model_code || cc.capability_code,
    model: cc.vendor_model || '',
    name: cc.name || (cc.model && cc.model.name) || '',
    modelType: cc.model?.type || '',
    price: cc.price || 0,
    priceUnit: cc.price_unit || 'request',
    resultMode: cc.interaction_mode || cc.result_mode || 'sync',
    requestPath: cc.request_path || '',
    requestMethod: cc.request_method || 'POST',
    contentType: cc.content_type || 'application/json',
    pollPath: cc.poll_path || '',
    pollMethod: cc.poll_method || 'GET',
    pollInterval: cc.poll_interval || 5,
    pollMaxAttempts: cc.poll_max_attempts || 60,
    pollParamMapping: cc.poll_param_mapping || {},
    pollResponseMapping: cc.poll_response_mapping || {},
    authLocation: cc.auth_location || 'header',
    authKey: cc.auth_key || 'Authorization',
    authValuePrefix: cc.auth_value_prefix ?? '',
    paramMapping: cc.param_mapping || {},
    paramSchema: cc.param_schema || null,
    responseMapping: cc.response_mapping || {},
    callbackMapping: cc.callback_mapping || {},
    extraConfig: cc.extra_config || {},
    status: cc.status,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  };
};

export const updateChannelCapability = async (id: string, data: Record<string, any>): Promise<void> => {
  await request(`/admin/channel-capabilities/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
};

export const deleteChannelCapability = async (id: string): Promise<void> => {
  await request(`/admin/channel-capabilities/${id}`, { method: 'DELETE' });
};

// 用户级 API - 获取能力及可用渠道列表
export const fetchCapabilityChannels = async (): Promise<CapabilityWithChannels[]> => {
    const data = await request<any[]>('/capability-channels');
    return data.map(c => ({
        code: c.code,
        name: c.name,
        type: c.type,
        description: c.description,
        channels: (c.channels || []).map((ch: any) => ({
            channelId: ch.channel_id,
            channelType: ch.channel_type,
            channelName: ch.channel_name,
            model: ch.model,
            price: ch.price,
        })),
    }));
};

// 用户级 API - 获取 Chat 模型及可用渠道列表
export const fetchChatModelChannelsForToken = async (): Promise<CapabilityWithChannels[]> => {
    const data = await request<any[]>('/chat-model-channels');
    return data.map(c => ({
        code: c.code,
        name: c.name,
        type: c.type,
        description: c.description,
        channels: (c.channels || []).map((ch: any) => ({
            channelId: ch.channel_id,
      channelType: ch.channel_type,
            channelName: ch.channel_name,
            model: ch.model,
            price: ch.price,
    })),
  }));
};

// 用户级 API - 获取所有能力和 Chat 模型的渠道列表（合并）
export const fetchAllCapabilityChannels = async (): Promise<CapabilityWithChannels[]> => {
    const [capabilities, chatModels] = await Promise.all([
        fetchCapabilityChannels(),
        fetchChatModelChannelsForToken(),
    ]);
    return [...capabilities, ...chatModels];
};
