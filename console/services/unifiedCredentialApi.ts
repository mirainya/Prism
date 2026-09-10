import { request } from './request';
import type {
  UnifiedCredential,
  UnifiedGatewayPage,
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
export const updateManagedCredential = (
  item: UnifiedCredential,
  data: CredentialFields,
) =>
  request(`/admin/unified-gateway/credentials/${item.id}`, {
    method: 'PUT',
    body: JSON.stringify({ ...data, expected_version: item.config_version }),
  });
export const transitionManagedCredential = (item: UnifiedCredential) =>
  request(`/admin/unified-gateway/credentials/${item.id}/status`, {
    method: 'POST',
    body: JSON.stringify({
      status: item.status === 'active' ? 'draining' : 'disabled',
      expected_version: item.config_version,
    }),
  });
