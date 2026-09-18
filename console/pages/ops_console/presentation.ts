import type {
  UnifiedCall,
  UnifiedCatalogModelSelection,
  UnifiedCatalogRate,
  UnifiedCatalogSKU,
} from '../../services/unifiedGatewayApi';
import type { UnifiedChannel } from '../../services/unifiedChannelApi';

export type OpsConsoleTab = 'models' | 'upstream' | 'calls';

export const opsStatusLabel = (status: string) => ({
  active: '已启用',
  published: '已发布',
  draft: '草稿',
  disabled: '已停用',
  draining: '排空中',
  completed: '已完成',
  succeeded: '成功',
  failed: '失败',
  pending: '待处理',
}[status] || status || '未知');

export const opsStatusVariant = (status: string): 'success' | 'warning' | 'error' | 'info' | 'default' => {
  if (['active', 'published', 'completed', 'succeeded'].includes(status)) return 'success';
  if (['draft', 'draining', 'pending', 'preparing'].includes(status)) return 'warning';
  if (['failed', 'disabled', 'rejected'].includes(status)) return 'error';
  return 'default';
};

export const shortDigest = (digest: string | null | undefined, length = 12) => {
  if (!digest) return '-';
  return digest.length <= length ? digest : digest.slice(0, length);
};

export const formatOpsDate = (value?: string | null) => {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date);
};

const generatedCredentialCode = /^(?:legacy-)?credential-(?:gw-channel-key|key)-[a-z0-9-]+$/i;

/**
 * Imported credentials use stable machine codes so migrations can be audited.
 * Keep those codes available as a technical identifier, but use a short
 * operator-facing name in the model detail list.
 */
export const formatCredentialDisplayName = (
  credentialCode: string | null | undefined,
  poolName: string | null | undefined,
  ordinal: number,
) => {
  const code = credentialCode?.trim() || '';
  if (!generatedCredentialCode.test(code)) return code || `API Key ${ordinal + 1}`;
  const pool = poolName?.trim();
  return `${pool || '上游'} · API Key ${ordinal + 1}`;
};

interface SKUDisplayInput {
  sku_code?: string | null;
  variant_code?: string | null;
  delivery_mode?: string | null;
  service_tiers?: string[] | null;
  model_code?: string | null;
  api_name?: string | null;
  display_name?: string | null;
  operation_code?: string | null;
}

const normalizeComparableName = (value: string | null | undefined) =>
  String(value || '').trim().toLowerCase().replace(/[\s._:/-]+/g, '');

const humanizeSKUCode = (value: string | null | undefined) => String(value || '')
  .trim()
  .replace(/^legacy[-_]?/i, '')
  .replace(/[._-]+/g, ' ')
  .replace(/\s+/g, ' ');

export const formatSKUOperationLabel = (operationCode: string | null | undefined) => {
  const normalized = String(operationCode || '').trim().toLowerCase();
  return ({
    'chat.completions': '聊天补全',
    'responses.create': 'Responses',
    'messages.create': 'Anthropic 消息',
    'image.generate': '图片生成',
    'images.generate': '图片生成',
    'image.edit': '图片编辑',
    'images.edit': '图片编辑',
    'video.generate': '视频生成',
    'videos.generate': '视频生成',
    'audio.generate': '音频生成',
    'embeddings.create': '文本向量',
  }[normalized] || humanizeSKUCode(operationCode));
};

export const formatSKUDeliveryLabel = (deliveryMode: string | null | undefined) => ({
  reference: '直链',
  managed_copy: '托管副本',
}[String(deliveryMode || '').trim().toLowerCase()] || humanizeSKUCode(deliveryMode));

export const formatSKUVariantLabel = (variantCode: string | null | undefined) => {
  const code = String(variantCode || '').trim().toLowerCase();
  if (!code || code === 'default') return '';
  return ({
    official: '官方渠道',
    h_channel: 'H 渠道',
    seedance25: 'Seedance 2.5',
    fast: '快速',
  }[code] || humanizeSKUCode(variantCode));
};

export const formatSKUServiceTierLabel = (serviceTier: string | null | undefined) => ({
  standard: '标准',
  priority: '优先',
  vip: 'VIP',
  flex: '弹性',
  batch: '批量',
}[String(serviceTier || '').trim().toLowerCase()] || humanizeSKUCode(serviceTier));

export const formatSKUServiceTiers = (serviceTiers: string[] | null | undefined) => {
  const tierOrder: Record<string, number> = { standard: 0, priority: 1, vip: 2, flex: 3, batch: 4 };
  const uniqueTiers = [...new Set((serviceTiers || []).map((tier) => tier.trim()).filter(Boolean))];
  return uniqueTiers
    .sort((left, right) => (tierOrder[left.toLowerCase()] ?? 100) - (tierOrder[right.toLowerCase()] ?? 100))
    .map(formatSKUServiceTierLabel)
    .join(' / ');
};

export const formatSKUDisplayName = (sku: SKUDisplayInput) => {
  const operation = formatSKUOperationLabel(sku.operation_code);
  const displayName = String(sku.display_name || '').trim();
  const normalizedDisplay = normalizeComparableName(displayName);
  const duplicatesIdentity = [sku.model_code, sku.api_name, sku.sku_code]
    .some((value) => normalizedDisplay && normalizedDisplay === normalizeComparableName(value));
  const readableDisplay = displayName && !/^legacy[-_]/i.test(displayName) && !duplicatesIdentity
    ? displayName
    : '';

  if (readableDisplay && operation && normalizeComparableName(readableDisplay) !== normalizeComparableName(operation)) {
    return `${readableDisplay} · ${operation}`;
  }
  return readableDisplay || operation || '未命名规格';
};

/** Backward-compatible alias for callers that describe a SKU as a service spec. */
export const formatServiceSpecName = (sku: SKUDisplayInput) => {
  const base = formatSKUDisplayName(sku);
  const variant = formatSKUVariantLabel(sku.variant_code);
  return variant && !base.includes(variant) ? `${base} · ${variant}` : base;
};

export const formatSKUServiceSummary = (sku: SKUDisplayInput) => {
  const labels = [
    formatSKUServiceTiers(sku.service_tiers),
    formatSKUDeliveryLabel(sku.delivery_mode),
  ].filter(Boolean);
  const variant = formatSKUVariantLabel(sku.variant_code);
  if (variant) labels.push(`变体：${variant}`);
  return labels.join(' · ') || '-';
};

export type OpsSelectedEntity =
  | { kind: 'model'; item: UnifiedCatalogModelSelection }
  | { kind: 'upstream'; item: UnifiedChannel }
  | { kind: 'call'; item: UnifiedCall };

export const selectedEntityTitle = (entity: OpsSelectedEntity | null) => {
  if (!entity) return '选择一条记录';
  if (entity.kind === 'model') return entity.item.model_entry.display_name || entity.item.model_entry.model_code;
  if (entity.kind === 'upstream') return entity.item.display_name || entity.item.channel_code;
  return entity.item.public_id;
};

// 费率文案。原本只有运维台抽屉一个调用者，B 档改动表单接进来后成了两处共用，
// 所以从 OpsConsole.tsx 提到这里——两边必须对同一个 component_code 说同一句话。
export const humanizeOpsCode = (value: string | null | undefined) => {
  const text = String(value || '').trim();
  if (!text) return '-';
  return text
    .replace(/^legacy[-_]?/i, '')
    .replace(/[._-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
};

export const rateComponentLabel = (value: string | null | undefined) => ({
  input: '输入 Token',
  output: '输出 Token',
  request: '请求',
  image: '图片生成',
  video: '视频生成',
  audio: '音频生成',
  duration: '生成时长',
}[String(value || '').toLowerCase()] || humanizeOpsCode(value));

export const rateUnitLabel = (value: string | null | undefined) => ({
  token: 'Token',
  request: '请求',
  image: '图片',
  video: '视频',
  audio: '音频',
  second: '秒',
  minute: '分钟',
}[String(value || '').toLowerCase()] || humanizeOpsCode(value));

export const rateCurrencyLabel = (value: string | null | undefined) => ({
  credit: '积分',
  cny: '人民币',
  usd: '美元',
}[String(value || '').toLowerCase()] || humanizeOpsCode(value));

export const rateQuantitySourceLabel = (value: string | null | undefined) => {
  const text = String(value || '').trim();
  const direct: Record<string, string> = {
    one: '每次请求 1 单位',
    'usage.input_tokens': '输入 Token 数量',
    'usage.output_tokens': '输出 Token 数量',
    'usage.total_tokens': '总 Token 数量',
    'result.images': '生成图片数量',
    'result.videos': '生成视频数量',
    'result.audio': '生成音频数量',
  };
  if (direct[text.toLowerCase()]) return direct[text.toLowerCase()];
  if (text.toLowerCase().startsWith('usage.')) return `${humanizeOpsCode(text.slice(6))} 用量`;
  if (text.toLowerCase().startsWith('result.')) return `生成${humanizeOpsCode(text.slice(7))}数量`;
  return humanizeOpsCode(text);
};

export const rateChargeEventLabel = (value: string | null | undefined) => ({
  'call.started': '调用开始时',
  'call.succeeded': '调用成功时',
  'call.failed': '调用失败时',
  'request.completed': '请求完成时',
}[String(value || '').toLowerCase()] || humanizeOpsCode(value));

export const ratePricingModeLabel = (value: string | null | undefined) => ({
  flat: '固定单价',
  expression: '按公式计价',
}[String(value || '').toLowerCase()] || humanizeOpsCode(value));

export const formatRateNumber = (value: string | number | null | undefined) => {
  const text = String(value ?? '').trim();
  if (!text) return '-';
  if (!/^-?\d+(\.\d+)?$/.test(text)) return text;
  const [integer, fraction = ''] = text.split('.');
  const normalizedInteger = integer.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
  const normalizedFraction = fraction.replace(/0+$/, '');
  return normalizedFraction ? `${normalizedInteger}.${normalizedFraction}` : normalizedInteger;
};

export const rateParentLabel = (rate: UnifiedCatalogRate, skus: UnifiedCatalogSKU[]) => {
  const sku = skus.find((item) => item.id === rate.parent_id);
  return sku?.display_name?.trim() || sku?.model_code?.trim() || sku?.api_name?.trim() || humanizeOpsCode(rate.parent_label);
};
