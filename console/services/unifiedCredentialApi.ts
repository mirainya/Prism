import { request } from './request';
import {
  clearUnifiedCredentialRouteStates,
  type UnifiedCredential,
  type UnifiedGatewayPage,
} from './unifiedGatewayApi';

export type CredentialPurpose =
  | 'execution'
  | 'catalog_discovery'
  | 'upstream_callback_verify';
export interface CredentialFields {
  request_limit: number | null;
  task_limit: number | null;
  weight: number;
}
export interface CredentialUpdateFields extends CredentialFields {
  secret?: string;
}
export type CredentialTransition = {
  status: 'draining' | 'disabled';
  label: '停止使用' | '完成停用';
};
export const managedCredentialTransition = (
  status: string,
): CredentialTransition | null => {
  if (status === 'active') return { status: 'draining', label: '停止使用' };
  if (status === 'draining') return { status: 'disabled', label: '完成停用' };
  return null;
};
export const fetchManagedCredentials = (
  page: number,
  size: number,
  poolId?: number,
  signal?: AbortSignal,
) => {
  const params = new URLSearchParams({
    page: String(page),
    page_size: String(size),
  });
  if (poolId) params.set('pool_id', String(poolId));
  return request<UnifiedGatewayPage<UnifiedCredential>>(
    `/admin/unified-gateway/credentials?${params}`,
    { signal },
  );
};
export const createManagedCredential = (
  poolId: number,
  data: CredentialFields & {
    credential_code: string;
    secret: string;
    purposes: CredentialPurpose[];
  },
) =>
  request(`/admin/unified-gateway/pools/${poolId}/credentials`, {
    method: 'POST',
    body: JSON.stringify(data),
  });
export const updateManagedCredential = async (
  item: UnifiedCredential,
  data: CredentialUpdateFields,
) => {
  const result = await request(`/admin/unified-gateway/credentials/${item.id}`, {
    method: 'PUT',
    body: JSON.stringify({ ...data, expected_version: item.config_version }),
  });
  if (data.secret) await clearUnifiedCredentialRouteStates(item.id);
  return result;
};
export const transitionManagedCredential = (item: UnifiedCredential) => {
  const transition = managedCredentialTransition(item.status);
  if (!transition) return Promise.reject(new Error('此 API Key 已停用'));
  return request(`/admin/unified-gateway/credentials/${item.id}/status`, {
    method: 'POST',
    body: JSON.stringify({
      status: transition.status,
      expected_version: item.config_version,
    }),
  });
};
