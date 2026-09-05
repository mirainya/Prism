import { request } from './request';

export interface UnifiedGatewayOverview {
  state: 'legacy_runtime' | 'target_empty' | 'target_configured' | string;
  runtime: {
    active_release_id: number | null;
    release_state_version: number;
    deployment_id: number;
    deployment_status: string;
  };
  target: {
    channels: number;
    models: number;
    credentials: number;
    catalog_releases: number;
    calls: number;
  };
  legacy: {
    channels: number;
    abilities: number;
    runtime_active: boolean;
  };
}

export const fetchUnifiedGatewayOverview = () =>
  request<UnifiedGatewayOverview>('/admin/unified-gateway/overview');

export interface UnifiedGatewayPage<T> {
  items: T[];
  page: number;
  page_size: number;
  total: number;
}
export interface UnifiedCatalogRelease { id: number; release_no: number; status: string; semantic_version: string; content_hash: string; semantic_digest: string; published_at?: string | null; created_at: string; }
export interface UnifiedCredential { id: number; channel_id: number; credential_pool_id?: number | null; credential_code: string; status: string; config_version: number; request_limit?: number | null; task_limit?: number | null; weight: number; current_version_id?: number | null; pool_code?: string | null; pool_name?: string | null; }
export interface UnifiedCall { id: number; public_id: string; user_id: number; token_id: number; status: string; quoted_amount: string; price_currency: string; delivery_mode: string; created_at: string; updated_at: string; }
export interface UnifiedCallAttempt { id: number; attempt_no: number; state: string; catalog_release_id: number; sku_id: number; route_id: number; offering_id: number; credential_id: number; credential_version_id: number; purpose_grant_id: number; created_at: string; updated_at: string; }
export interface UnifiedCallDetail { call: UnifiedCall & { catalog_release_id: number; model_operation_id: number; sku_id: number }; attempts: UnifiedGatewayPage<UnifiedCallAttempt> }

const pageQuery = (page: number, pageSize = 20) => `?page=${page}&page_size=${pageSize}`;
export const fetchUnifiedCatalog = (page = 1) => request<UnifiedGatewayPage<UnifiedCatalogRelease>>(`/admin/unified-gateway/catalog${pageQuery(page)}`);
export const publishUnifiedCatalog = (id: number) => request(`/admin/unified-gateway/catalog/${id}/publish`, { method: 'POST' });
export const retireUnifiedCatalog = (id: number) => request(`/admin/unified-gateway/catalog/${id}/retire`, { method: 'POST' });
export const fetchUnifiedCredentials = (page = 1) => request<UnifiedGatewayPage<UnifiedCredential>>(`/admin/unified-gateway/credentials${pageQuery(page)}`);
export const fetchUnifiedCalls = (page = 1) => request<UnifiedGatewayPage<UnifiedCall>>(`/admin/unified-gateway/calls${pageQuery(page)}`);
export const fetchUnifiedCallDetail = (id: number, page = 1) => request<UnifiedCallDetail>(`/admin/unified-gateway/calls/${id}${pageQuery(page)}`);
export const createUnifiedDeployment = (data: { generation_no: number; semantic_version: string; semantic_digest: string }) => request<{ id: number }>('/admin/unified-gateway/deployments', { method: 'POST', body: JSON.stringify(data) });
export const activateUnifiedDeployment = (id: number) => request(`/admin/unified-gateway/deployments/${id}/activate`, { method: 'POST' });
