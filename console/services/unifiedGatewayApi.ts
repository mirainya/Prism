import { request } from './request';

export interface UnifiedGatewayOverview {
  runtime_ready?: boolean;
  management_revision?: number;
  blockers?: string[];
  runtime: {
    active_release_id: number | null;
  };
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
export interface UnifiedCatalogChangeResult { release_id: number; config_version: number; activated: boolean; source_release_id: number; pending_reason?: string; }
export interface UnifiedCredential { id: number; channel_id: number; credential_pool_id?: number | null; credential_code: string; status: string; config_version: number; request_limit?: number | null; task_limit?: number | null; weight: number; current_version_id?: number | null; pool_code?: string | null; pool_name?: string | null; purposes?: string[]; }
export interface UnifiedCall { id: number; public_id: string; user_id: number; token_id: number; status: string; quoted_amount: string; price_currency: string; delivery_mode: string; created_at: string; updated_at: string; }
// transport_code / channel_name / credential_code 把裸 id 翻成人看得懂的东西；
// error_code + http_status 取该次尝试最后一条上游请求日志——v2 没有存上游错误正文的列。
export interface UnifiedCallAttempt { id: number; attempt_no: number; state: string; catalog_release_id: number; sku_id: number; route_id: number; offering_id: number; credential_id: number; credential_version_id: number; purpose_grant_id: number; created_at: string; updated_at: string; transport_code: string; channel_name: string; credential_code: string; error_code: string; http_status: number | null; }
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
export interface UnifiedCatalogSKU {
  id: number; sku_code: string; variant_code?: string; delivery_mode: string; max_results: number; idempotency_mode: string;
  service_tiers: string[]; model_code: string; api_name: string; display_name: string; visibility: string;
  description?: string; capability_tags?: string[]; operation_code: string; contract_version: number; http_method: string; route_template: string; downstream_paths?: string[];
  sell_rate_count: number; route_count: number;
}

export interface UnifiedCatalogRoute { sku_id: number; priority: number; weight: number }

export interface UnifiedCatalogProduct {
  id: number; product_code: string; vendor_model: string; channel_id: number; channel_name: string;
  product_transport_id: number; channel_transport_id: number; transport_code: string; base_url: string;
  protocol: string; request_method: string; request_path: string; task_scope: string; cancel_mode: string;
  source_url_policy: string; adapter_code: string; adapter_version: number; offering_id: number;
  offering_state?: UnifiedOfferingRuntimeState; offering_state_version?: number;
  credential_pool_id: number; pool_code?: string; pool_name: string; cost_plan_id: number; cost_plan_code: string;
  route_count: number; cost_rate_count: number; commercial_state: string; entitled_credential_count: number;
    capability_constraints?: unknown; constraints_schema_version?: number;
    model_type?: UnifiedCatalogModelType;
  // 只有模型条目接口会带；route-weight 改动要用它回填当前优先级与权重。
  sku_ids?: number[]; routes?: UnifiedCatalogRoute[];
}

export type UnifiedOfferingRuntimeState = 'active' | 'draining' | 'disabled';

export interface UnifiedAllowedHost {
  protocol: 'http' | 'https';
  host: string;
  port: number;
}

export interface UnifiedTransportAllowedHosts {
  release_id: number;
  config_version: number;
  transport_id: number;
  transport_code: string;
  base_url: string;
  allowed_hosts: UnifiedAllowedHost[];
}

export interface UnifiedCatalogModelEntry {
  id: number; release_id: number; model_id: number; model_code: string; display_name: string;
  description: string; capability_tags: string[]; visibility: string;
  model_type: UnifiedCatalogModelType; status: UnifiedCatalogModelStatus;
  sku_count: number; product_count: number; skus: UnifiedCatalogSKU[]; products: UnifiedCatalogProduct[];
}

export type UnifiedCatalogModelType = 'llm' | 'image' | 'video' | 'other';
export type UnifiedCatalogModelStatus = 'active' | 'inactive' | 'disabled';
export interface UnifiedCatalogModelFilters {
  q?: string;
  type?: UnifiedCatalogModelType | '';
  status?: UnifiedCatalogModelStatus | '';
}

export interface UnifiedCatalogModelSelection extends UnifiedCatalogRelease {
  model_entry: UnifiedCatalogModelEntry;
}

export interface UnifiedRateEvidence {
  id: number; source_type: string; authority_level: string; source_reference: string; observed_at: string;
  unit_code: string; unit_price: string; currency_code: string; currency_version: number; state: string;
  state_version: number; reason_code: string; reviewer_user_id: number; reviewed_at: string; created_at: string;
}

export interface UnifiedCatalogRate {
  id: number; kind: 'sell' | 'cost'; parent_id: number; parent_label: string; parent_key?: string; component_code: string;
  unit_code: string; quantity_source: string; charge_event: string; unit_price: string; unit_scale: number;
  quantity_step: string; max_quantity: string; currency_code: string; currency_version: number;
  pricing_mode?: 'flat' | 'expression' | string; pricing_expr?: string | null; max_price?: string | null;
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
export const fetchUnifiedCredentials = (page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCredential>>(`/admin/unified-gateway/credentials${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedCalls = (page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCall>>(`/admin/unified-gateway/calls${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedCallDetail = (id: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedCallDetail>(`/admin/unified-gateway/calls/${id}${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedRequestLogs = (id: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedRequestLog>>(`/admin/unified-gateway/calls/${id}/requests${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedRequestLogPayloads = (callId: number, requestId: number, signal?: AbortSignal) => request<UnifiedRequestLogPayloads>(`/admin/unified-gateway/calls/${callId}/requests/${requestId}/payloads`, { signal });

export const fetchUnifiedCatalogSKUs = (releaseId: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogSKU>>(`/admin/unified-gateway/catalog/${releaseId}/skus${pageQuery(page, pageSize)}`, { signal });
export const createUnifiedCatalogSKU = (releaseId: number, data: Record<string, unknown>) => request<{ id: number }>(`/admin/unified-gateway/catalog/${releaseId}/skus`, { method: 'POST', body: JSON.stringify(data) });
export const updateUnifiedCatalogSKU = (releaseId: number, id: number, data: Record<string, unknown>) => request<{ id: number }>(`/admin/unified-gateway/catalog/${releaseId}/skus/${id}`, { method: 'PATCH', body: JSON.stringify(data) });
export const deleteUnifiedCatalogSKU = (releaseId: number, id: number, expectedVersion: number) => request(`/admin/unified-gateway/catalog/${releaseId}/skus/${id}`, { method: 'DELETE', body: JSON.stringify({ expected_version: expectedVersion }) });
export const fetchUnifiedCatalogProducts = (releaseId: number, page = 1, pageSize = 20, signal?: AbortSignal) => request<UnifiedGatewayPage<UnifiedCatalogProduct>>(`/admin/unified-gateway/catalog/${releaseId}/products${pageQuery(page, pageSize)}`, { signal });
export const fetchUnifiedTransportAllowedHosts = (releaseId: number, transportId: number, signal?: AbortSignal) => request<UnifiedTransportAllowedHosts>(`/admin/unified-gateway/catalog/${releaseId}/transports/${transportId}/allowed-hosts`, { signal });
const isAbortSignal = (value: UnifiedCatalogModelFilters | AbortSignal | undefined): value is AbortSignal =>
  Boolean(value && 'aborted' in value && typeof value.addEventListener === 'function');

export function fetchUnifiedCatalogModelEntries(releaseId: number, page?: number, pageSize?: number, signal?: AbortSignal): Promise<UnifiedGatewayPage<UnifiedCatalogModelEntry>>;
export function fetchUnifiedCatalogModelEntries(releaseId: number, page: number, pageSize: number, filters: UnifiedCatalogModelFilters, signal?: AbortSignal): Promise<UnifiedGatewayPage<UnifiedCatalogModelEntry>>;
export function fetchUnifiedCatalogModelEntries(releaseId: number, page = 1, pageSize = 20, filtersOrSignal: UnifiedCatalogModelFilters | AbortSignal = {}, signal?: AbortSignal) {
  const filters = isAbortSignal(filtersOrSignal) ? {} : filtersOrSignal;
  const requestSignal = isAbortSignal(filtersOrSignal) ? filtersOrSignal : signal;
  const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
  const search = filters.q?.trim();
  if (search) query.set('q', search);
  if (filters.type) query.set('type', filters.type);
  if (filters.status) query.set('status', filters.status);
  return request<UnifiedGatewayPage<UnifiedCatalogModelEntry>>(`/admin/unified-gateway/catalog/${releaseId}/model-entries?${query.toString()}`, { signal: requestSignal });
}
export const createUnifiedCatalogProduct = (releaseId: number, data: Record<string, unknown>) => request<{ id: number }>(`/admin/unified-gateway/catalog/${releaseId}/products`, { method: 'POST', body: JSON.stringify(data) });
export const deleteUnifiedCatalogProduct = (releaseId: number, id: number, expectedVersion: number) => request(`/admin/unified-gateway/catalog/${releaseId}/products/${id}`, { method: 'DELETE', body: JSON.stringify({ expected_version: expectedVersion }) });
export const changeUnifiedProduct = (data: { expected_active_release_id: number; expected_config_version: number; semantic_version: string; product_code: string; vendor_model: string; capability_constraints: unknown }) => request<UnifiedCatalogChangeResult>('/admin/unified-gateway/catalog-changes/product', { method: 'POST', body: JSON.stringify(data) });
export const changeUnifiedTransportAllowedHosts = (data: { expected_active_release_id: number; expected_config_version: number; semantic_version: string; transport_code: string; allowed_hosts: UnifiedAllowedHost[] }) => request<UnifiedCatalogChangeResult>('/admin/unified-gateway/catalog-changes/transport-allowed-hosts', { method: 'POST', body: JSON.stringify(data) });
export interface UnifiedActiveProductCreate {
  expected_active_release_id: number; expected_config_version: number;
  channel_id: number; credential_pool_id: number;
  product_code: string; vendor_model: string; capability_constraints: unknown; constraints_schema_version: number;
  adapter_code: string; adapter_version: number; transport_code: string;
  base_url: string; request_method: string; request_path: string; auth_scheme: string;
  transport_timeout_ms: number; task_timeout_ms: number; task_scope: string; cancel_mode: string; source_url_policy: string;
  upstream_scope_kind: string; upstream_scope_key: string;
  allowed_hosts: { protocol: string; host: string; port: number }[];
  actions: { action_code: string; allowed_source_state: string; idempotency_mode: string; request_schema_version: number; response_schema_version: number }[];
  cost_plan_code: string; routes: { sku_id: number; priority: number; weight: number }[];
}
export const createUnifiedActiveProduct = (data: UnifiedActiveProductCreate) => request<UnifiedCatalogChangeResult>('/admin/unified-gateway/catalog-changes/product-create', { method: 'POST', body: JSON.stringify(data) });

export interface UnifiedCatalogModelOnboardRate {
  unit_code: string; unit_price: string; component_code: string;
  quantity_source: string; charge_event: string; unit_scale: number;
  quantity_step: string; max_quantity: string; pricing_mode: 'flat' | 'expression';
  pricing_expr: string; reason_code?: string;
}

export interface UnifiedCatalogModelOnboardInput {
  expected_active_release_id: number;
  expected_config_version: number;
  sku: {
    model_code: string; api_name: string; display_name: string; description: string;
    visibility: string; capability_tags: string[]; operation_code: string;
    contract_version: number; http_method: string; route_template: string;
    normalization_version: number; sku_code: string; variant_code: string;
    delivery_mode: string; max_results: number; idempotency_mode: string;
    service_tiers: string[];
  };
  product: {
    channel_id: number; credential_pool_id: number; product_code: string;
    vendor_model: string; capability_constraints: unknown; constraints_schema_version: number;
    adapter_code: string; adapter_version: number; transport_code: string;
    base_url: string; request_method: string; request_path: string; auth_scheme: string;
    transport_timeout_ms: number; task_timeout_ms: number; task_scope: string;
    cancel_mode: string; source_url_policy: string; upstream_scope_kind: string;
    upstream_scope_key: string; allowed_hosts: UnifiedAllowedHost[];
    actions: UnifiedProductTransportAction[]; cost_plan_code: string;
  };
  route: { priority: number; weight: number };
  sell_rate: UnifiedCatalogModelOnboardRate;
  cost_rate: UnifiedCatalogModelOnboardRate;
}

export interface UnifiedCatalogModelOnboardResult extends UnifiedCatalogChangeResult {
  sku_id: number; product_id: number; offering_id: number; cost_plan_id: number;
  sell_rate_id: number; cost_rate_id: number;
}

export const onboardUnifiedCatalogModel = (data: UnifiedCatalogModelOnboardInput) =>
  request<UnifiedCatalogModelOnboardResult>('/admin/unified-gateway/catalog-changes/model-onboard', { method: 'POST', body: JSON.stringify(data) });
export const changeUnifiedSellRate = (data: { expected_active_release_id: number; expected_config_version: number; semantic_version: string; sku_code: string; component_code: string; unit_price: string; pricing_mode: 'flat' | 'expression'; pricing_expr?: string; reason_code?: string }) => request<UnifiedCatalogChangeResult>('/admin/unified-gateway/catalog-changes/sell-rate', { method: 'POST', body: JSON.stringify(data) });
// 配置更新使用业务码寻址，避免把目录内部行 id 暴露为编辑契约。
export const changeUnifiedCostRate = (data: { expected_active_release_id: number; expected_config_version: number; semantic_version: string; product_code: string; plan_code: string; component_code: string; pool_code?: string; unit_price: string; pricing_mode: 'flat' | 'expression'; pricing_expr?: string; reason_code?: string }) => request<UnifiedCatalogChangeResult>('/admin/unified-gateway/catalog-changes/cost-rate', { method: 'POST', body: JSON.stringify(data) });
export const changeUnifiedRouteWeight = (data: { expected_active_release_id: number; expected_config_version: number; semantic_version: string; sku_code: string; product_code: string; pool_code: string; transport_code: string; priority: number; weight: number }) => request<UnifiedCatalogChangeResult>('/admin/unified-gateway/catalog-changes/route-weight', { method: 'POST', body: JSON.stringify(data) });
export const changeUnifiedSKUVariant = (data: { expected_active_release_id: number; expected_config_version: number; semantic_version: string; sku_code: string; variant_code: string }) => request<UnifiedCatalogChangeResult>('/admin/unified-gateway/catalog-changes/sku-variant', { method: 'POST', body: JSON.stringify(data) });
export const changeUnifiedSKUDownstreamPaths = (data: { expected_active_release_id: number; expected_config_version: number; semantic_version: string; sku_code: string; downstream_paths: string[] }) => request<UnifiedCatalogChangeResult>('/admin/unified-gateway/catalog-changes/sku-downstream-paths', { method: 'POST', body: JSON.stringify(data) });
export const recordUnifiedOfferingValidation = (offeringId: number, credentialId: number, data: { state: string; valid_until?: string; evidence: Record<string, unknown> }) => request<{ id: number }>(`/admin/unified-gateway/offerings/${offeringId}/credentials/${credentialId}/validations/all`, { method: 'POST', body: JSON.stringify(data) });
export const setUnifiedOfferingRuntimeState = (offeringId: number, data: { state: UnifiedOfferingRuntimeState; reason_code: string; expected_version: number }) => request<{ id: number }>(`/admin/unified-gateway/offerings/${offeringId}/runtime-state`, { method: 'PATCH', body: JSON.stringify(data) });
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

// 熔断状态。选路 SQL 直接把熔断中的路由过滤掉，配置再正确也会连续 503 直到退避窗口走完，
// 而在此之前控制台没有任何地方能看到这张表。
export interface UnifiedRouteState {
  id: number; model_name: string; transport: string;
  disabled_until: string | null; updated_at: string | null;
  remaining_seconds: number; active: boolean;
  reason: string; status_code: number; fail_count: number;
  credential_id: number; credential_code: string; credential_status: string;
  credential_pool_id: number | null; pool_code: string; pool_name: string;
  channel_id: number | null; channel_name: string;
}
export const fetchUnifiedRouteStates = (page = 1, pageSize = 20, options: { includeExpired?: boolean; credentialId?: number; modelName?: string } = {}, signal?: AbortSignal) =>
  request<UnifiedGatewayPage<UnifiedRouteState>>(`/admin/unified-gateway/route-states${pageQuery(page, pageSize)}${options.includeExpired ? '&include_expired=1' : ''}${options.credentialId ? `&credential_id=${options.credentialId}` : ''}${options.modelName ? `&model_name=${encodeURIComponent(options.modelName)}` : ''}`, { signal });
// 清掉熔断行就是全部的恢复动作：选路只跳过窗口未到期的行，删掉后下一个请求立刻重新可选。
export const clearUnifiedRouteState = (id: number) => request<{ cleared: number }>(`/admin/unified-gateway/route-states/${id}`, { method: 'DELETE' });
// 换完密钥后真正需要的那个按钮：熔断键是 credential id，换密钥不换 id，不清就得干等。
export const clearUnifiedCredentialRouteStates = (credentialId: number) => request<{ cleared: number }>(`/admin/unified-gateway/credentials/${credentialId}/route-states`, { method: 'DELETE' });

// Adapter 契约。目录选项接口只给了 Descriptor 的 5 个字段，能回答"这个 adapter
// 收什么参数、哪些量可以计费"的 Manifest 此前没有任何 HTTP 出口。
export interface UnifiedAdapterSummary {
  code: string; version: number; protocol: string;
  minimum_semantic_version: string; implementation_digest: string;
  has_manifest: boolean; selectable_for_product: boolean;
}
export interface UnifiedAdapterParamHint {
  kind: string; options?: string[]; min?: string; max?: string; default?: string;
  allowed?: boolean; required: boolean;
  cases?: { value: string; dependent: string; min?: string; max?: string }[];
}
export interface UnifiedAdapterBillingVar {
  name: string; desc: string; layer: 'common' | 'capability' | 'adapter'; from?: string;
  domain?: { kind: string; options?: string[]; numbers?: string[]; min?: string; max?: string };
}
export interface UnifiedAdapterVariant {
  code: string; display: string; models: string[];
  param_hints: Record<string, UnifiedAdapterParamHint>;
  ref_media?: { field: string; max: number; item_seconds_min: number; item_seconds_max: number; total_seconds: number }[];
  hard_constraints?: { field: string; must_be: string }[];
  pricing_default_mode?: string; pricing_example?: string;
  tiers?: Record<string, { up_to: string; value: string }[]>;
}
export interface UnifiedAdapterManifest {
  adapter: string; adapter_version: number; protocol: string; has_manifest: boolean;
  capability?: string; downstream_paths?: string[];
  billing_vars?: UnifiedAdapterBillingVar[]; billing_funcs?: string[];
  variants?: UnifiedAdapterVariant[];
  minimum_semantic_version?: string; implementation_digest?: string;
}
export const fetchUnifiedAdapters = (signal?: AbortSignal) => request<{ items: UnifiedAdapterSummary[]; semantic_digest: string }>('/admin/unified-gateway/adapters', { signal });
export const fetchUnifiedAdapterManifest = (code: string, version = 1, signal?: AbortSignal) => request<UnifiedAdapterManifest>(`/admin/unified-gateway/adapters/${encodeURIComponent(code)}/manifest?version=${version}`, { signal });

// Key ↔ 模型。Key 不直接指向模型，链路要走 credential → pool ← offering → route
// → sku → model；blocked_by 是这里唯一重要的字段——它说明为什么配好了却不在服务。
export interface UnifiedRelationLink {
    api_name: string; display_name: string; visibility: string;
    model_type?: UnifiedCatalogModelType;
  sku_id: number; sku_code: string; delivery_mode: string;
  route_id: number; priority: number; route_weight: number;
  offering_id: number; offering_state: string; offering_state_version?: number;
  credential_id: number; credential_code: string; credential_status: string; credential_config_version: number; credential_weight: number;
  request_limit: number | null; task_limit: number | null;
  credential_pool_id: number; pool_code: string; pool_name: string; pool_status: string;
  channel_id: number; channel_name: string; channel_status: string;
  transport_code: string; product_code: string; vendor_model: string;
  product_id?: number; product_transport_id?: number; channel_transport_id?: number;
  adapter_code?: string; adapter_version?: number; base_url?: string; protocol?: string;
  request_method?: string; request_path?: string; task_scope?: string;
  capability_constraints?: unknown;
  actions?: UnifiedProductTransportAction[];
  commercial_valid: boolean; entitlement_valid: boolean;
  secret_identity_active: boolean; execution_granted: boolean; credential_version_usable: boolean;
  breaker_seconds: number; serving: boolean; blocked_by?: string[];
}
export interface UnifiedProductTransportAction {
  action_code: 'submit' | 'recover' | 'query' | 'cancel' | 'named_action' | 'result_fetch' | string;
  allowed_source_state?: string;
  idempotency_mode?: string;
  request_schema_version?: number;
  response_schema_version?: number;
}
export interface UnifiedRelationResult {
  items: UnifiedRelationLink[]; active_release_id: number | null; total?: number; serving?: number;
}
export const fetchUnifiedCredentialModels = (credentialId: number, signal?: AbortSignal) => request<UnifiedRelationResult>(`/admin/unified-gateway/credentials/${credentialId}/models`, { signal });
export const fetchUnifiedModelCredentials = (modelName: string, signal?: AbortSignal) => request<UnifiedRelationResult>(`/admin/unified-gateway/model-credentials?model_name=${encodeURIComponent(modelName)}`, { signal });
export const fetchUnifiedChannelRelations = (channelId: number, signal?: AbortSignal) => request<UnifiedRelationResult>(`/admin/unified-gateway/channels/${channelId}/relations`, { signal });
