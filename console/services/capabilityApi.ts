import { Capability, ChannelCapability, CapabilityWithChannels, EndpointAccountBinding, EndpointOriginSnapshot } from '../types';
import { request } from './request';

const mapEndpointAccountBindings = (bindings: any[] | undefined): EndpointAccountBinding[] =>
  (bindings || []).map(binding => ({
    id: String(binding.id),
    endpointId: String(binding.endpoint_id),
    accountId: String(binding.account_id),
    status: binding.status ?? 1,
    priority: binding.priority ?? 0,
    weight: binding.weight ?? 10,
    accountName: binding.account?.name || '',
    accountStatus: binding.account?.status ?? 0,
  }));

const mapEndpointOriginSnapshot = (snapshot: any): EndpointOriginSnapshot => ({
  channelId: snapshot?.channel_id || undefined,
  channelName: snapshot?.channel_name || undefined,
  channelType: snapshot?.channel_type || undefined,
  accountId: snapshot?.account_id || undefined,
  accountName: snapshot?.account_name || undefined,
  vendorModel: snapshot?.vendor_model || undefined,
  adapter: snapshot?.adapter || undefined,
  sourceEndpointId: snapshot?.source_endpoint_id || undefined,
  inferred: snapshot?.inferred === true,
});

export const fetchCapabilities = async (): Promise<Capability[]> => {
  const data = await request<any[]>('/admin/capabilities');
  return data.map(c => ({
    code: c.code,
    name: c.name,
    type: c.type || 'image',
    description: c.description || '',
    aliases: Array.isArray(c.aliases) ? c.aliases : [],
    standardParams: c.param_schema || c.standard_params || {},
    standardResponse: c.standard_response || {},
    status: c.status,
    sort: c.sort ?? 0,
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
    aliases: Array.isArray(c.aliases) ? c.aliases : [],
    standardParams: c.param_schema || c.standard_params || {},
    standardResponse: c.standard_response || {},
    status: c.status,
    sort: c.sort ?? 0,
    createdAt: c.created_at,
    updatedAt: c.updated_at,
  };
};

export const createCapability = async (data: {
  code: string;
  name: string;
  type?: string;
  description?: string;
  aliases?: string[];
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
    aliases: Array.isArray(c.aliases) ? c.aliases : [],
    standardParams: c.param_schema || c.standard_params || {},
    standardResponse: c.standard_response || {},
    status: c.status,
    sort: c.sort ?? 0,
    createdAt: c.created_at,
    updatedAt: c.updated_at,
  };
};

export const updateCapability = async (code: string, data: {
  code?: string;
  name?: string;
  type?: string;
  description?: string;
  aliases?: string[];
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

// 按传入 code 顺序调整能力排序(后端写 sort DESC)
export const reorderCapabilities = async (codes: string[]): Promise<void> => {
  await request('/admin/capabilities/reorder', { method: 'POST', body: JSON.stringify({ codes }) });
};

// Endpoint 管理。函数名暂时保留，避免影响已有页面调用。
export const fetchChannelCapabilities = async (channelId?: string, capabilityCode?: string): Promise<ChannelCapability[]> => {
  const params = new URLSearchParams();
  if (channelId) params.append('channel_id', channelId);
  if (capabilityCode) params.append('model_code', capabilityCode);
  const url = params.toString() ? `/admin/endpoints?${params}` : '/admin/endpoints';

  const data = await request<any[]>(url);
  return data.map(cc => ({
    id: String(cc.id),
    channelId: String(cc.channel_id),
    accountId: String(cc.account_id || 0),
    capabilityCode: cc.model_code || cc.capability_code,
    routeOperation: cc.route_operation || '',
    supportedOperations: cc.supported_operations || (cc.route_operation ? [cc.route_operation] : []),
    model: cc.vendor_model || '',
    name: cc.name || (cc.model && cc.model.name) || '',
    modelType: cc.model?.type || '',
    price: Number(cc.input_price ?? cc.price ?? 0),
    priceUnit: cc.price_mode || cc.price_unit || 'request',
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
    originType: cc.origin_type || 'legacy_unknown',
    originAccountId: String(cc.origin_account_id || 0),
    originSnapshot: mapEndpointOriginSnapshot(cc.origin_snapshot),
    discoveredAt: cc.discovered_at || undefined,
    accountBindings: mapEndpointAccountBindings(cc.account_bindings),
    status: cc.status,
    createdAt: cc.created_at,
    updatedAt: cc.updated_at,
    channel: cc.channel,
    capability: cc.capability,
  }));
};

export const getChannelCapability = async (id: string): Promise<ChannelCapability> => {
  const cc = await request<any>(`/admin/endpoints/${id}`);
  return {
    id: String(cc.id),
    channelId: String(cc.channel_id),
    accountId: String(cc.account_id || 0),
    capabilityCode: cc.model_code || cc.capability_code,
    routeOperation: cc.route_operation || '',
    supportedOperations: cc.supported_operations || (cc.route_operation ? [cc.route_operation] : []),
    model: cc.vendor_model || '',
    name: cc.name || (cc.model && cc.model.name) || '',
    modelType: cc.model?.type || '',
    price: Number(cc.input_price ?? cc.price ?? 0),
    priceUnit: cc.price_mode || cc.price_unit || 'request',
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
    originType: cc.origin_type || 'legacy_unknown',
    originAccountId: String(cc.origin_account_id || 0),
    originSnapshot: mapEndpointOriginSnapshot(cc.origin_snapshot),
    discoveredAt: cc.discovered_at || undefined,
    accountBindings: mapEndpointAccountBindings(cc.account_bindings),
    status: cc.status,
    createdAt: cc.created_at,
    updatedAt: cc.updated_at,
    channel: cc.channel,
    capability: cc.capability,
  };
};

export interface CreateChannelCapabilityInput {
  channel_id: number;
  account_id?: number;
  account_bindings?: Array<{
    account_id: number;
    status: number;
    priority: number;
    weight: number;
  }>;
  model_code: string;
  route_operation?: string;
  supported_operations?: string[];
  protocol?: string;
  vendor_model?: string;
  interaction_mode?: string;
  supports_stream?: boolean;
  default_stream?: boolean;
  price_mode?: string;
  input_price?: number;
  output_price?: number;
  request_path?: string;
  request_method?: string;
  content_type?: string;
  auth_location?: string;
  auth_key?: string;
  auth_value_prefix?: string;
  poll_path?: string;
  poll_method?: string;
  poll_interval?: number;
  poll_max_attempts?: number;
  poll_param_mapping?: Record<string, any>;
  poll_response_mapping?: Record<string, any>;
  param_schema?: Record<string, any> | null;
  param_mapping?: Record<string, any>;
  response_mapping?: Record<string, any>;
  callback_mapping?: Record<string, any>;
  extra_headers?: Record<string, any>;
  extra_config?: Record<string, any>;
  timeout?: number;
  priority?: number;
  status?: number;
}

export const createChannelCapability = async (data: CreateChannelCapabilityInput): Promise<ChannelCapability> => {
  const cc = await request<any>('/admin/endpoints', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  return {
    id: String(cc.id),
    channelId: String(cc.channel_id),
    accountId: String(cc.account_id || 0),
    capabilityCode: cc.model_code || cc.capability_code,
    routeOperation: cc.route_operation || '',
    supportedOperations: cc.supported_operations || (cc.route_operation ? [cc.route_operation] : []),
    model: cc.vendor_model || '',
    name: cc.name || (cc.model && cc.model.name) || '',
    modelType: cc.model?.type || '',
    price: Number(cc.input_price ?? cc.price ?? 0),
    priceUnit: cc.price_mode || cc.price_unit || 'request',
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
    originType: cc.origin_type || 'manual',
    originAccountId: String(cc.origin_account_id || 0),
    originSnapshot: mapEndpointOriginSnapshot(cc.origin_snapshot),
    discoveredAt: cc.discovered_at || undefined,
    accountBindings: mapEndpointAccountBindings(cc.account_bindings),
    status: cc.status,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  };
};

export const updateChannelCapability = async (id: string, data: Record<string, any>): Promise<void> => {
  await request(`/admin/endpoints/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
};

export const deleteChannelCapability = async (id: string): Promise<void> => {
  await request(`/admin/endpoints/${id}`, { method: 'DELETE' });
};

export interface EndpointDiscoveredModel {
  id: string;
  object?: string;
  owned_by?: string;
  model_code: string;
  imported: boolean;
}

export interface EndpointModelDiscoveryResult {
  endpoint_id: number;
  adapter: string;
  revision_id: number;
  models: EndpointDiscoveredModel[];
  checked_at: string;
}

export interface EndpointModelImportItem {
  id: string;
  model_code?: string;
  name?: string;
  operations?: string[];
}

export interface EndpointModelImportResult {
  models_created: number;
  endpoints_created: number;
  bindings_added: number;
}

export const discoverEndpointModels = async (endpointId: string): Promise<EndpointModelDiscoveryResult> =>
  request<EndpointModelDiscoveryResult>(`/admin/endpoints/${endpointId}/discover`);

export const importEndpointModels = async (
  endpointId: string,
  models: EndpointModelImportItem[],
): Promise<EndpointModelImportResult> =>
  request<EndpointModelImportResult>(`/admin/endpoints/${endpointId}/import`, {
    method: 'POST',
    body: JSON.stringify({ models }),
  });

// 用户级 API - 获取能力及可用渠道列表
export const fetchCapabilityChannels = async (): Promise<CapabilityWithChannels[]> => {
    const data = await request<any[]>('/capability-channels');
    return data.map(c => ({
        id: c.id || c.code,
        code: c.code,
        name: c.name,
        type: c.type,
        description: c.description,
        standardParams: c.param_schema || c.standard_params || {},
        operations: (c.operations || []).map((operation: any) => ({
            id: operation.id,
            path: operation.path || '',
            supportsStream: Boolean(operation.supports_stream),
            paramSchema: operation.param_schema || null,
        })),
        channels: (c.channels || []).map((ch: any) => ({
            channelId: ch.channel_id,
            channelType: ch.channel_type,
            channelName: ch.channel_name,
            model: ch.model,
            routeOperation: ch.route_operation || '',
            price: ch.price,
            interactionMode: ch.interaction_mode || '',
            paramSchema: ch.param_schema || null,
        })),
    }));
};

// 用户级 API - 获取所有能力的渠道列表(chat 已迁网关,不再合并老 chat-model-channels)
export const fetchAllCapabilityChannels = async (): Promise<CapabilityWithChannels[]> => {
    return fetchCapabilityChannels();
};
