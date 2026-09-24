import type {
  UnifiedAdapterManifest,
  UnifiedCatalogModelOnboardInput,
  UnifiedCatalogModelOnboardRate,
  UnifiedCatalogModelType,
} from '../../services/unifiedGatewayApi';

const identityPattern = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/;
const publicModelNamePattern = /^[A-Za-z0-9][A-Za-z0-9._:/%\-图]*$/u;
const pricePattern = /^(?:0|[1-9]\d{0,17})(?:\.\d{1,18})?$/;

const downstreamOperations: Record<string, { operationCode: string; maxResults: number }> = {
  '/v1/chat/completions': { operationCode: 'chat.completions', maxResults: 1 },
  '/v1/responses': { operationCode: 'responses.create', maxResults: 1 },
  '/v1/messages': { operationCode: 'messages.create', maxResults: 1 },
  '/v1/images/generations': { operationCode: 'images.generate', maxResults: 10 },
  '/v1/images/edits': { operationCode: 'images.edit', maxResults: 10 },
  '/v1/videos/generations': { operationCode: 'videos.generate', maxResults: 1 },
};

export interface OnboardingVariantChoice {
  code: string;
  label: string;
}

export interface OnboardingManifestChoices {
  downstreamPaths: string[];
  variants: OnboardingVariantChoice[];
}

export const modelTypeForAdapter = (adapterCode: string, capability = ''): UnifiedCatalogModelType => {
  if (capability === 'video' || adapterCode === 'generic' || adapterCode === 'seedance') return 'video';
  if (capability === 'image' || adapterCode === 'openai_images') return 'image';
  return 'llm';
};

export const onboardingManifestChoices = (
  adapterCode: string,
  manifest: UnifiedAdapterManifest | null,
): OnboardingManifestChoices => {
  const declaredPaths = manifest?.has_manifest
    ? (manifest.downstream_paths || []).filter((path) => downstreamOperations[path])
    : [];
  const downstreamPaths = declaredPaths.length > 0
    ? declaredPaths
    : adapterCode === 'generic' ? ['/v1/videos/generations'] : [];
  const declaredVariants = manifest?.has_manifest
    ? (manifest.variants || []).filter((variant) => identityPattern.test(variant.code))
    : [];
  const variants = declaredVariants.length > 0
    ? declaredVariants.map((variant) => ({ code: variant.code, label: variant.display || variant.code }))
    : [{ code: 'default', label: '默认' }];
  return { downstreamPaths, variants };
};

export interface NewModelOnboardingValues {
  releaseId: number;
  configVersion: number;
  channelId: number;
  channelCode: string;
  poolId: number;
  poolCode: string;
  adapterCode: string;
  adapterVersion: number;
  adapterCapability: string;
  apiName: string;
  displayName: string;
  vendorModel: string;
  baseURL: string;
  requestPath: string;
  downstreamPath: string;
  variantCode: string;
  capabilityConstraints: Record<string, unknown>;
  billingUnit: '' | 'request' | 'second';
  billingMaxQuantity: string;
  sellPrice: string;
  costPrice: string;
  uniqueSuffix: string;
}

const isPositivePrice = (value: string) => pricePattern.test(value) && Number(value) > 0;

export const validateNewModelOnboarding = (values: NewModelOnboardingValues): string => {
  const apiName = values.apiName.trim();
  const displayName = values.displayName.trim();
  const vendorModel = values.vendorModel.trim();
  const requestPath = values.requestPath.trim();
  if (!values.releaseId || !values.configVersion || !values.channelId || !values.poolId || !values.poolCode) return '请选择有效的 Key 池';
  if (!publicModelNamePattern.test(apiName) || new TextEncoder().encode(apiName).length > 128) return '公开调用名须以字母或数字开头，最多 128 字节，仅支持字母、数字、图或 . _ : / - %';
  if (!displayName || [...displayName].length > 128 || /[\x00\r\n\t]/.test(displayName)) return '显示名须为 1-128 个字符';
  if (!vendorModel || vendorModel.length > 255 || /[\x00\r\n\t]/.test(vendorModel)) return '请填写有效的上游模型';
  try {
    const endpoint = new URL(values.baseURL.trim());
    if (!['http:', 'https:'].includes(endpoint.protocol) || !endpoint.hostname || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) throw new Error();
  } catch {
    return '上游地址必须是无查询参数的 HTTP 或 HTTPS 地址';
  }
  if (requestPath.length > 255 || !requestPath.startsWith('/') || requestPath.startsWith('//') || /[#\x00\r\n\s]/.test(requestPath)) return '请填写有效的上游请求路径';
  if (!downstreamOperations[values.downstreamPath]) return '请选择 Adapter 声明的下游入口';
  if (values.variantCode.length > 64 || !identityPattern.test(values.variantCode)) return '请选择有效的模型规格';
  if (values.billingUnit !== 'request' && values.billingUnit !== 'second') return '请选择计费单位';
  if (values.billingUnit === 'second') {
    if (modelTypeForAdapter(values.adapterCode.trim(), values.adapterCapability) !== 'video') return '按秒计费仅支持视频模型';
    const maxQuantity = values.billingMaxQuantity.trim();
    if (!/^[1-9]\d*$/.test(maxQuantity) || Number(maxQuantity) > 86400) return '按秒计费上限须为 1-86400 的整数';
  }
  if (!isPositivePrice(values.sellPrice.trim())) return '售价须为大于 0 的十进制数';
  if (!isPositivePrice(values.costPrice.trim())) return '成本须为大于 0 的十进制数';
  if (!values.capabilityConstraints || Array.isArray(values.capabilityConstraints)) return '参数与能力配置必须是 JSON 对象';
  return '';
};

const identityPart = (value: string) => value.toLowerCase()
  .replace(/[^a-z0-9]+/g, '-')
  .replace(/^-+|-+$/g, '') || 'model';

const generatedCode = (parts: string[], suffix: string) => {
  const tail = `-${identityPart(suffix)}`;
  const stem = parts.map(identityPart).join('-');
  return `${stem.slice(0, Math.max(1, 128 - tail.length)).replace(/-+$/g, '')}${tail}`;
};

const billingRate = (
  unitPrice: string,
  billingUnit: NewModelOnboardingValues['billingUnit'],
  billingMaxQuantity: string,
): UnifiedCatalogModelOnboardRate => ({
  unit_code: billingUnit,
  unit_price: unitPrice.trim(),
  component_code: billingUnit === 'second' ? 'duration' : 'request',
  quantity_source: billingUnit === 'second' ? 'request.seconds' : 'one',
  charge_event: 'call.succeeded',
  unit_scale: 0,
  quantity_step: '0',
  max_quantity: billingUnit === 'second' ? billingMaxQuantity.trim() : '1',
  pricing_mode: 'flat',
  pricing_expr: '',
  reason_code: 'model_onboard',
});

export const buildModelOnboardInput = (values: NewModelOnboardingValues): UnifiedCatalogModelOnboardInput => {
  const validationError = validateNewModelOnboarding(values);
  if (validationError) throw new Error(validationError);
  const apiName = values.apiName.trim();
  const adapterCode = values.adapterCode.trim();
  const operation = downstreamOperations[values.downstreamPath];
  const modelType = modelTypeForAdapter(adapterCode, values.adapterCapability);
  const asyncImageConfig = values.capabilityConstraints.async_image;
  const taskBased = modelType === 'video' || (
    modelType === 'image'
    && adapterCode === 'openai_images'
    && Boolean(asyncImageConfig)
    && typeof asyncImageConfig === 'object'
    && !Array.isArray(asyncImageConfig)
  );
  const capabilityTag = modelType === 'image' ? 'image_generation' : modelType;
  const productCode = generatedCode([values.channelCode, apiName, 'product'], values.uniqueSuffix);
  const transportCode = generatedCode([adapterCode, apiName, 'transport'], values.uniqueSuffix);
  const payload: UnifiedCatalogModelOnboardInput = {
    expected_active_release_id: values.releaseId,
    expected_config_version: values.configVersion,
    sku: {
      model_code: apiName,
      api_name: apiName,
      display_name: values.displayName.trim(),
      description: '',
      visibility: 'visible',
      capability_tags: [capabilityTag],
      operation_code: operation.operationCode,
      contract_version: 1,
      http_method: 'POST',
      route_template: values.downstreamPath,
      normalization_version: 1,
      sku_code: generatedCode([apiName, values.variantCode, operation.operationCode], values.uniqueSuffix),
      variant_code: values.variantCode,
      delivery_mode: 'reference',
      max_results: operation.maxResults,
      idempotency_mode: 'optional',
      service_tiers: ['standard'],
    },
    product: {
      channel_id: values.channelId,
      credential_pool_id: values.poolId,
      product_code: productCode,
      vendor_model: values.vendorModel.trim(),
      capability_constraints: values.capabilityConstraints,
      constraints_schema_version: 1,
      adapter_code: adapterCode,
      adapter_version: values.adapterVersion,
      transport_code: transportCode,
      base_url: values.baseURL.trim().replace(/\/$/, ''),
      request_method: 'POST',
      request_path: values.requestPath.trim(),
      auth_scheme: 'bearer',
      transport_timeout_ms: taskBased ? 600000 : 30000,
      task_timeout_ms: taskBased ? 600000 : 30000,
      task_scope: taskBased ? 'task' : 'request',
      cancel_mode: 'none',
      source_url_policy: 'fixed',
      upstream_scope_kind: 'credential_pool',
      upstream_scope_key: values.poolCode,
      allowed_hosts: [],
      actions: taskBased ? [
        { action_code: 'submit', allowed_source_state: 'allocated', idempotency_mode: 'none', request_schema_version: 1, response_schema_version: 1 },
        { action_code: 'query', allowed_source_state: 'nonterminal', idempotency_mode: 'none', request_schema_version: 1, response_schema_version: 1 },
      ] : [],
      cost_plan_code: generatedCode([apiName, 'cost'], values.uniqueSuffix),
    },
    route: { priority: 100, weight: 100 },
    sell_rate: billingRate(values.sellPrice, values.billingUnit, values.billingMaxQuantity),
    cost_rate: billingRate(values.costPrice, values.billingUnit, values.billingMaxQuantity),
  };
  return payload;
};
