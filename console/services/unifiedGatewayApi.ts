import { request } from './request';

export interface UnifiedGatewayOverview {
  state: 'legacy_runtime' | 'target_empty' | 'target_configured' | string;
  ready_for_cutover: boolean;
  runtime_ready?: boolean;
  management_revision?: number;
  blockers?: string[];
  runtime: {
    active_release_id: number | null;
    release_state_version: number;
    deployment_id: number;
    deployment_status: string;
    latest_generation_no?: number;
  };
  process?: UnifiedDeploymentIdentity;
  target: {
    channels: number;
    models: number;
    credentials: number;
    catalog_releases: number;
    calls: number;
    offerings?: number;
    routes?: number;
    sell_rates?: number;
    cost_rates?: number;
    currencies?: number;
  };
  legacy: {
    channels: number;
    abilities: number;
    runtime_active?: boolean;
    data_present?: boolean;
  };
}

export const fetchUnifiedGatewayOverview = (signal?: AbortSignal) =>
  request<UnifiedGatewayOverview>('/admin/unified-gateway/overview', { signal });

export interface UnifiedGatewayPage<T> {
  items: T[];
  page: number;
  page_size: number;
  total: number;
}
export interface UnifiedCatalogRelease { id: number; release_no: number; status: string; config_version: number; semantic_version: string; content_hash: string; semantic_digest: string; published_at?: string | null; created_at: string; updated_at: string; }
export interface UnifiedCredential { id: number; channel_id: number; credential_pool_id?: number | null; credential_code: string; status: string; config_version: number; request_limit?: number | null; task_limit?: number | null; weight: number; current_version_id?: number | null; pool_code?: string | null; pool_name?: string | null; purposes?: string[]; }
export interface UnifiedCall { id: number; public_id: string; user_id: number; token_id: number; status: string; quoted_amount: string; price_currency: string; delivery_mode: string; created_at: string; updated_at: string; }
export interface UnifiedCallAttempt { id: number; attempt_no: number; state: string; catalog_release_id: number; sku_id: number; route_id: number; offering_id: number; credential_id: number; credential_version_id: number; purpose_grant_id: number; created_at: string; updated_at: string; }
export interface UnifiedCallDetail { call: UnifiedCall & { catalog_release_id: number; model_operation_id: number; sku_id: number }; attempts: UnifiedGatewayPage<UnifiedCallAttempt>; request_logs_available?: boolean }
export interface UnifiedRequestLog {
  id: number; attempt_no: number; request_seq: number; action: string; status: string;
  http_status: number | null; duration_ms: number | null; error_code: string;
  request_complete: boolean; response_complete: boolean;
  request_payload_available: boolean; response_payload_available: boolean;
  created_at: string; completed_at: string | null; async_execution_id: number | null; async_state: string;
}
export interface UnifiedRequestLogPayloads {
  request_body: string | null; response_body: string | null;
  request_complete: boolean; response_complete: boolean;
}
export interface UnifiedDeployment { id: number; generation_no: number; status: string; semantic_version: string; semantic_digest: string; member_count: number; current_member_id: number; member_frozen_at: string | null; created_at: string; }
export interface UnifiedDeploymentIdentity { instance_id: string; role: string; adapter_digest: string; semantic_digest: string; }
export interface UnifiedDeploymentProof extends UnifiedDeploymentIdentity { generation_id: number; member_id: number; release_id: number; expires_at: string; }

export interface UnifiedCatalogSKU {
  id: number; sku_code: string; delivery_mode: string; max_results: number; idempotency_mode: string;
  service_tiers: string[]; model_code: string; api_name: string; display_name: string; visibility: string;
  operation_code: string; contract_version: number; http_method: string; route_template: string;
  sell_rate_count: number; route_count: number;
}

export interface UnifiedCatalogProduct {
  id: number; product_code: string; vendor_model: string; channel_id: number; channel_name: string;
  product_transport_id: number; channel_transport_id: number; transport_code: string; base_url: string;
  protocol: string; request_method: string; request_path: string; task_scope: string; cancel_mode: string;
  source_url_policy: string; adapter_code: string; adapter_version: number; offering_id: number;
  credential_pool_id: number; pool_name: string; cost_plan_id: number; cost_plan_code: string;
  route_count: number; cost_rate_count: number; commercial_state: string; entitled_credential_count: number;
}

export interface UnifiedRateEvidence {
  id: number; source_type: string; authority_level: string; source_reference: string; observed_at: string;
  unit_code: string; unit_price: string; currency_code: string; currency_version: number; state: string;
  state_version: number; reason_code: string; reviewer_user_id: number; reviewed_at: string; created_at: string;
}

export interface UnifiedCatalogRate {
  id: number; kind: 'sell' | 'cost'; parent_id: number; parent_label: string; component_code: string;
  unit_code: string; quantity_source: string; charge_event: string; unit_price: string; unit_scale: number;
  quantity_step: string; max_quantity: string; currency_code: string; currency_version: number;
  review_event_id: number; evidence_id: number; evidence_state: string;
}

export interface UnifiedCurrency {
  id: number; currency_code: string; definition_version: number; fraction_digits: number; rounding_mode: string;
  max_amount: string; status: string; is_settlement: boolean; created_at: string;
}

export interface UnifiedCatalogOption {
  id: number; code: string; name: string; pool_id?: number; pool_code?: string; pool_name?: string;
}
export interface UnifiedAdapterOption { code: string; version: number; protocol: string; minimum_semantic_version: string; implementation_digest: string; }
export interface UnifiedCatalogOptions { channels: UnifiedCatalogOption[]; skus: UnifiedCatalogOption[]; adapters: UnifiedAdapterOption[]; }

export interface UnifiedCatalogSource {
  id: number; channel_id: number; channel_code: string; channel_name: string;
  source_code: string; contract_code: 'aicost_models_v1' | 'aicost_pricing_v1';
  base_url: string; external_group: string; request_timeout_ms: number;
  credential_id: number; credential_code: string; status: 'active' | 'draining' | 'disabled';
  state_version: number; nonterminal_run_count: number; last_run_id: number | null;
  last_run_state: string | null; last_run_reason: string | null;
  created_at: string | null; updated_at: string | null;
}
export interface UnifiedCatalogSourceOptions {
  channels: { id: number; code: string; name: string }[];
  credentials: { id: number; channel_id: number; code: string }[];
  contracts: { code: UnifiedCatalogSource['contract_code']; name: string }[];
}
export interface UnifiedCatalogDiscovery {
  id: number; release_source_id: number; source_id: number; source_code: string;
  contract_code: string; external_group: string; state: string; state_version: number;
  attempt_count: number; last_reason_code: string;
  snapshot_id: number | null; item_count: number | null; observed_at: string | null;
  response_hmac: string | null; review_state: string | null; review_version: number | null;
  created_at: string | null; updated_at: string | null;
}
export interface UnifiedCatalogDiscoveryItem {
  id: number; ordinal: number; model_code: string; description: string; tags: string;
  vendor_id: number | null; provider_quota_type: number | null; model_price: string | null;
  model_ratio: string | null; completion_ratio: string | null; owner_by: string;
  pricing_version: string; selected_group_enabled: boolean; groups: string[]; endpoint_types: string[];
}
export interface UnifiedCatalogPriceCandidate {
  id: number; snapshot_id: number; model_code: string; model_price: string;
  provider_quota_type: number | null; observed_at: string | null; source_code: string;
  external_group: string; state: 'pending' | 'confirmed' | 'dismissed'; unit_code: string | null;
  currency_code: string | null; currency_version: number | null; rate_evidence_id: number | null;
  reviewed_at: string | null;
}

const pageQuery = (page: number, pageSize = 20) => `?page=${page}&page_size=${pageSize}`;
export const fetchUnifiedCatalog = (page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogRelease>>(`/admin/unified-gateway/catalog${pageQuery(page, pageSize)}`, { signal });
export const createUnifiedCatalog = (data: { semantic_version: string }) => request<{ id: number }>('/admin/unified-gateway/catalog', { method: 'POST', body: JSON.stringify(data) });
export const fetchUnifiedCatalogRelease = (id: number, signal?: AbortSignal) => request<UnifiedCatalogRelease>(`/admin/unified-gateway/catalog/${id}`, { signal });
export const publishUnifiedCatalog = (id: number) => request(`/admin/unified-gateway/catalog/${id}/publish`, { method: 'POST' });
export const retireUnifiedCatalog = (id: number) => request(`/admin/unified-gateway/catalog/${id}/retire`, { method: 'POST' });
export const activateUnifiedCatalog = (id: number, generationId: number, expectedStateVersion: number) => request(`/admin/unified-gateway/catalog/${id}/activate`, { method: 'POST', body: JSON.stringify({ generation_id: generationId, expected_state_version: expectedStateVersion }) });
export const fetchUnifiedCredentials = (page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCredential>>(`/admin/unified-gateway/credentials${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedCalls = (page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCall>>(`/admin/unified-gateway/calls${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedCallDetail = (id: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedCallDetail>(`/admin/unified-gateway/calls/${id}${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedRequestLogs = (id: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedRequestLog>>(`/admin/unified-gateway/calls/${id}/requests${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedRequestLogPayloads = (callId: number, requestId: number, signal?: AbortSignal) => request<UnifiedRequestLogPayloads>(`/admin/unified-gateway/calls/${callId}/requests/${requestId}/payloads`, { signal });
export const fetchUnifiedDeployments = (page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedDeployment>>(`/admin/unified-gateway/deployments${pageQuery(page, pageSize)}`, { signal });
export const createUnifiedDeployment = (data: { generation_no: number; semantic_version: string }) => request<{ id: number }>('/admin/unified-gateway/deployments', { method: 'POST', body: JSON.stringify(data) });
export const activateUnifiedDeployment = (id: number) => request(`/admin/unified-gateway/deployments/${id}/activate`, { method: 'POST' });
export const fetchUnifiedDeploymentIdentity = (signal?: AbortSignal) => request<UnifiedDeploymentIdentity>('/admin/unified-gateway/deployments/runtime-identity', { signal });
export const proveCurrentUnifiedDeployment = (id: number, releaseId?: number | null) => request<UnifiedDeploymentProof>(`/admin/unified-gateway/deployments/${id}/prove-current`, { method: 'POST', body: JSON.stringify(releaseId ? { release_id: releaseId } : {}) });

export const fetchUnifiedCatalogSKUs = (releaseId: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogSKU>>(`/admin/unified-gateway/catalog/${releaseId}/skus${pageQuery(page, pageSize)}`, { signal });
export const createUnifiedCatalogSKU = (releaseId: number, data: Record<string, unknown>) => request<{ id: number }>(`/admin/unified-gateway/catalog/${releaseId}/skus`, { method: 'POST', body: JSON.stringify(data) });
export const deleteUnifiedCatalogSKU = (releaseId: number, id: number, expectedVersion: number) => request(`/admin/unified-gateway/catalog/${releaseId}/skus/${id}`, { method: 'DELETE', body: JSON.stringify({ expected_version: expectedVersion }) });
export const fetchUnifiedCatalogProducts = (releaseId: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogProduct>>(`/admin/unified-gateway/catalog/${releaseId}/products${pageQuery(page, pageSize)}`, { signal });
export const createUnifiedCatalogProduct = (releaseId: number, data: Record<string, unknown>) => request<{ id: number }>(`/admin/unified-gateway/catalog/${releaseId}/products`, { method: 'POST', body: JSON.stringify(data) });
export const deleteUnifiedCatalogProduct = (releaseId: number, id: number, expectedVersion: number) => request(`/admin/unified-gateway/catalog/${releaseId}/products/${id}`, { method: 'DELETE', body: JSON.stringify({ expected_version: expectedVersion }) });
export const recordUnifiedOfferingValidation = (offeringId: number, credentialId: number, data: { state: string; valid_until?: string; evidence: Record<string, unknown> }) => request<{ id: number }>(`/admin/unified-gateway/offerings/${offeringId}/credentials/${credentialId}/validations/all`, { method: 'POST', body: JSON.stringify(data) });
export const fetchUnifiedCatalogRates = (releaseId: number, kind: 'sell' | 'cost', page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogRate>>(`/admin/unified-gateway/catalog/${releaseId}/rates${pageQuery(page, pageSize)}&kind=${kind}`, { signal });
export const createUnifiedCatalogRate = (releaseId: number, kind: 'sell' | 'cost', data: Record<string, unknown>) => request<{ id: number }>(`/admin/unified-gateway/catalog/${releaseId}/rates/${kind}`, { method: 'POST', body: JSON.stringify(data) });
export const deleteUnifiedCatalogRate = (releaseId: number, kind: 'sell' | 'cost', id: number, expectedVersion: number) => request(`/admin/unified-gateway/catalog/${releaseId}/rates/${kind}/${id}`, { method: 'DELETE', body: JSON.stringify({ expected_version: expectedVersion }) });
export const fetchUnifiedCatalogOptions = (releaseId: number, signal?: AbortSignal) => request<UnifiedCatalogOptions>(`/admin/unified-gateway/catalog/${releaseId}/options`, { signal });

export const fetchUnifiedCatalogSourceOptions = (signal?: AbortSignal) => request<UnifiedCatalogSourceOptions>('/admin/unified-gateway/catalog-sources/options', { signal });
export const fetchUnifiedCatalogSources = (page = 1, pageSize = 20, status = '', signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogSource>>(`/admin/unified-gateway/catalog-sources${pageQuery(page, pageSize)}&status=${encodeURIComponent(status)}`, { signal });
export const createUnifiedCatalogSource = (data: { channel_id: number; credential_id: number; source_code: string; contract_code: UnifiedCatalogSource['contract_code']; base_url: string; external_group: string; request_timeout_ms: number }) => request<{ id: number }>('/admin/unified-gateway/catalog-sources', { method: 'POST', body: JSON.stringify(data) });
export const transitionUnifiedCatalogSource = (source: UnifiedCatalogSource) => request(`/admin/unified-gateway/catalog-sources/${source.id}/status`, { method: 'POST', body: JSON.stringify({ status: source.status === 'active' ? 'draining' : 'disabled', expected_version: source.state_version, reason_code: source.status === 'active' ? 'admin_drain' : 'admin_disable' }) });
export const scheduleUnifiedCatalogDiscovery = (releaseId: number, sourceId: number) => request<{ id: number }>(`/admin/unified-gateway/catalog/${releaseId}/catalog-sources/${sourceId}/discover`, { method: 'POST', body: '{}' });
export const fetchUnifiedCatalogDiscoveries = (releaseId: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogDiscovery>>(`/admin/unified-gateway/catalog/${releaseId}/discoveries${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedCatalogDiscoveryItems = (snapshotId: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogDiscoveryItem>>(`/admin/unified-gateway/catalog-discoveries/${snapshotId}/items${pageQuery(page, pageSize)}`, { signal });
export const reviewUnifiedCatalogDiscovery = (snapshotId: number, data: { decision: 'accepted' | 'rejected'; reason_code: string; expected_version: number }) => request(`/admin/unified-gateway/catalog-discoveries/${snapshotId}/review`, { method: 'POST', body: JSON.stringify(data) });
export const fetchUnifiedCatalogPriceCandidates = (releaseId: number, page = 1, pageSize = 20, state = '', signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogPriceCandidate>>(`/admin/unified-gateway/catalog/${releaseId}/price-candidates${pageQuery(page, pageSize)}&state=${encodeURIComponent(state)}`, { signal });
export const reviewUnifiedCatalogPriceCandidate = (candidateId: number, data: { decision: 'confirmed' | 'dismissed'; unit_code?: string; currency_code?: string; currency_version?: number; reason_code: string }) => request(`/admin/unified-gateway/catalog-price-candidates/${candidateId}/review`, { method: 'POST', body: JSON.stringify(data) });

export const fetchUnifiedRateEvidence = (page = 1, pageSize = 20, state = '', signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedRateEvidence>>(`/admin/unified-gateway/rate-evidence${pageQuery(page, pageSize)}&state=${encodeURIComponent(state)}`, { signal });
export const createUnifiedRateEvidence = (data: Record<string, unknown>) => request<{ id: number }>('/admin/unified-gateway/rate-evidence', { method: 'POST', body: JSON.stringify(data) });
export const reviewUnifiedRateEvidence = (id: number, data: { decision: 'accepted' | 'rejected' | 'superseded'; reason_code: string; expected_version: number }) => request(`/admin/unified-gateway/rate-evidence/${id}/review`, { method: 'POST', body: JSON.stringify(data) });
export const fetchUnifiedCurrencies = (page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCurrency>>(`/admin/unified-gateway/currencies${pageQuery(page, pageSize)}`, { signal });
export const createUnifiedCurrency = (data: Record<string, unknown>) => request<{ id: number }>('/admin/unified-gateway/currencies', { method: 'POST', body: JSON.stringify(data) });
export const activateUnifiedCurrency = (code: string, version: number) => request(`/admin/unified-gateway/currencies/${encodeURIComponent(code)}/activate`, { method: 'POST', body: JSON.stringify({ definition_version: version }) });
