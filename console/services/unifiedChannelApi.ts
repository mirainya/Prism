import { request } from './request';
import type { UnifiedGatewayPage } from './unifiedGatewayApi';

export interface UnifiedChannel {
  id: number;
  channel_code: string;
  display_name: string;
  status: 'active' | 'disabled';
  pool_count: number;
  credential_count: number;
  created_at: string;
}
export interface UnifiedPool {
  id: number;
  channel_id: number;
  pool_code: string;
  display_name: string;
  status: 'active' | 'draining' | 'disabled';
  config_version: number;
  request_limit: number | null;
  task_limit: number | null;
  credential_count: number;
}
export interface PoolFields {
  display_name: string;
  request_limit: number | null;
  task_limit: number | null;
}
export const fetchUnifiedChannels = (
  page: number,
  size: number,
  search: string,
  status: string,
  signal?: AbortSignal
) => {
  const params = new URLSearchParams({
    page: String(page),
    page_size: String(size),
    q: search,
    status,
  });
  return request<UnifiedGatewayPage<UnifiedChannel>>(
    `/admin/unified-gateway/channels?${params}`,
    { signal }
  );
};
export const createUnifiedChannel = (data: {
  channel_code: string;
  display_name: string;
}) =>
  request('/admin/unified-gateway/channels', {
    method: 'POST',
    body: JSON.stringify(data),
  });
export const updateUnifiedChannel = (
  channel: UnifiedChannel,
  data: { display_name: string; status: string }
) =>
  request(`/admin/unified-gateway/channels/${channel.id}`, {
    method: 'PUT',
    body: JSON.stringify({
      ...data,
      expected_display_name: channel.display_name,
      expected_status: channel.status,
    }),
  });
export const fetchUnifiedPools = (
  id: number,
  page: number,
  size: number,
  signal?: AbortSignal
) =>
  request<UnifiedGatewayPage<UnifiedPool>>(
    `/admin/unified-gateway/channels/${id}/pools?page=${page}&page_size=${size}`,
    { signal }
  );
export const createUnifiedPool = (
  channelId: number,
  data: PoolFields & { pool_code: string }
) =>
  request(`/admin/unified-gateway/channels/${channelId}/pools`, {
    method: 'POST',
    body: JSON.stringify(data),
  });
export const updateUnifiedPool = (pool: UnifiedPool, data: PoolFields) =>
  request(`/admin/unified-gateway/pools/${pool.id}`, {
    method: 'PUT',
    body: JSON.stringify({ ...data, expected_version: pool.config_version }),
  });
export const transitionUnifiedPool = (pool: UnifiedPool) =>
  request(`/admin/unified-gateway/pools/${pool.id}/status`, {
    method: 'POST',
    body: JSON.stringify({
      status: pool.status === 'active' ? 'draining' : 'disabled',
      expected_version: pool.config_version,
    }),
  });
