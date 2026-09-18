import React, { useEffect, useMemo, useState } from 'react';
import {
  Activity,
  ArrowLeft,
  ChevronRight,
  Coins,
  Search,
  X,
  Zap,
} from 'lucide-react';
import { PageHeader } from '../components/shell';
import { Button, Dialog } from '../components/ui';
import {
  fetchPublicPricing,
  type PublicPricingAvailability,
  type PublicPricingCurrency,
  type PublicPricingModel,
  type PublicPricingSKU,
  type PublicRateComponent,
} from '../services/pricingApi';
import { APP_VERSION } from '../version';

type PricingVariant = 'public' | 'console';
type PricingTypeFilter = 'all' | 'chat' | 'image' | 'video' | 'audio' | 'embedding' | 'other';

interface PricingProps {
  onBack?: () => void;
  variant?: PricingVariant;
}

const unitLabels: Record<string, string> = {
  request: '次', token: 'token', second: '秒', image: '张图', video: '个视频', megapixel: 'MP',
};

const operationLabels: Record<string, string> = {
  'chat.completions': 'Chat 对话',
  'responses.create': 'Responses',
  'messages.create': 'Messages',
  'image.generate': '图像生成',
  'images.generate': '图像生成',
  'image.edit': '图像编辑',
  'images.edit': '图像编辑',
  'video.generate': '视频生成',
  'videos.generate': '视频生成',
  'audio.generate': '音频生成',
  'embeddings.create': '文本向量',
};

const deliveryLabels: Record<string, string> = {
  sync: '同步返回',
  reference: '结果链接',
  managed_copy: '托管结果',
  polling: '异步轮询',
  callback: '回调通知',
};

const serviceTierLabels: Record<string, string> = {
  default: '默认',
  standard: '标准',
  priority: '优先',
  vip: 'VIP',
  flex: '弹性',
  batch: '批量',
};

const idempotencyLabels: Record<string, string> = {
  optional: '支持幂等键',
  required: '需提供幂等键',
  none: '不支持幂等键',
};

const sourceLabels: Record<string, string> = {
  one: '每次请求',
  'usage.input_tokens': '输入 token',
  'usage.output_tokens': '输出 token',
  'usage.cached_input_tokens': '缓存输入 token',
  'usage.uncached_input_tokens': '未缓存输入 token',
  'request.seconds': '请求时长',
  'result.seconds': '生成时长',
  'request.images': '请求图片数',
  'result.images': '生成图片数',
  'result.videos': '生成视频数',
  'result.megapixels': '生成像素量',
};

const eventLabels: Record<string, string> = {
  'call.succeeded': '调用成功',
  'call.failed': '调用失败',
  'call.cancelled': '调用取消',
  'provider.accepted': '上游接受',
  'delivery.ready': '结果交付',
  'delivery.failed': '交付失败',
};

const typeGroups: Array<{ key: Exclude<PricingTypeFilter, 'all'>; label: string }> = [
  { key: 'chat', label: 'LLM / 对话' },
  { key: 'image', label: '图像生成' },
  { key: 'video', label: '视频生成' },
  { key: 'audio', label: '音频' },
  { key: 'embedding', label: '嵌入' },
  { key: 'other', label: '其他' },
];

const formatUnit = (unit: string) => unitLabels[unit] || unit || '单位';
const formatSource = (source: string) => sourceLabels[source] || source;
const formatEvent = (event: string) => eventLabels[event] || event;
const formatOperation = (operation: string) => operationLabels[operation.toLocaleLowerCase()] || '通用调用';
const formatDelivery = (delivery: string) => deliveryLabels[delivery.toLocaleLowerCase()] || '标准返回';
const formatServiceTier = (tier: string) => serviceTierLabels[tier.toLocaleLowerCase()] || tier;

const normalizeModelType = (type: string): Exclude<PricingTypeFilter, 'all'> =>
  typeGroups.some(group => group.key === type) ? type as Exclude<PricingTypeFilter, 'all'> : 'other';

const normalizeDecimal = (value: string) => {
  const match = value.trim().match(/^([+-]?)(\d+)(?:\.(\d*))?$/);
  if (!match) return value.trim();
  const [, sign, integer, fraction = ''] = match;
  const normalizedInteger = integer.replace(/^0+(?=\d)/, '');
  const normalizedFraction = fraction.replace(/0+$/, '');
  return `${sign}${normalizedInteger}${normalizedFraction ? `.${normalizedFraction}` : ''}`;
};

const shiftDecimal = (value: string, places: number) => {
  const match = value.trim().match(/^([+-]?)(\d+)(?:\.(\d*))?$/);
  if (!match || places === 0) return normalizeDecimal(value);
  const [, sign, integer, fraction = ''] = match;
  const digits = `${integer}${fraction}`;
  const decimalPosition = integer.length + places;
  const shifted = decimalPosition <= 0
    ? `0.${'0'.repeat(-decimalPosition)}${digits}`
    : decimalPosition >= digits.length
      ? `${digits}${'0'.repeat(decimalPosition - digits.length)}`
      : `${digits.slice(0, decimalPosition)}.${digits.slice(decimalPosition)}`;
  return normalizeDecimal(`${sign}${shifted}`);
};

const formatCurrency = (currency: PublicPricingCurrency) => currency.code ? `${currency.code} ` : '';

export const formatPublicText = (value: string) => value
  .replace(/\bAiCost\b\s*[:：·-]?\s*/gi, '')
  .replace(/\s{2,}/g, ' ')
  .trim();

export const formatPricingRate = (component: PublicRateComponent, currency: PublicPricingCurrency) => {
  if (component.unit_code === 'token') {
    const perMillion = shiftDecimal(component.unit_price, 6 - Math.max(0, component.unit_scale));
    return `${formatCurrency(currency)}${perMillion} / 百万 token`;
  }

  const quantity = component.unit_scale > 0 ? `10^${component.unit_scale} ` : '';
  return `${formatCurrency(currency)}${normalizeDecimal(component.unit_price)} / ${quantity}${formatUnit(component.unit_code)}`;
};

export const formatPublicModelName = (model: Pick<PublicPricingModel, 'name' | 'model_code' | 'code'>) => {
  const candidates = [model.name, model.model_code, model.code];
  for (const candidate of candidates) {
    const cleaned = String(candidate || '')
      .trim()
      .replace(/^aicost(?:[\s:_-]+|$)/i, '')
      .trim();
    if (cleaned) return cleaned;
  }
  return '未命名模型';
};

export const formatPricingSKUName = (sku: PublicPricingSKU) => {
  const labels = [formatOperation(sku.operation)];
  const tiers = [...new Set((sku.service_tiers || []).map(formatServiceTier).filter(Boolean))];
  if (tiers.length > 0) labels.push(tiers.join(' / '));
  return labels.join(' · ');
};

const formatRateLabel = (sku: PublicPricingSKU, component: PublicRateComponent) => {
  const measure = component.quantity_source === 'one'
    ? component.unit_code === 'second' ? '生成时长' : '每次生成'
    : formatSource(component.quantity_source);
  return `${formatPricingSKUName(sku)} · ${measure}`;
};

const formatIdempotency = (mode: string) => idempotencyLabels[mode.toLocaleLowerCase()] || '';

export const describeAvailability = (availability: PublicPricingAvailability) => {
  const hours = Math.round(availability.window_minutes / 60);
  const window = availability.window_minutes % 60 === 0 && hours > 0 ? `近 ${hours} 小时` : `近 ${availability.window_minutes} 分钟`;
  if (availability.source === 'local') {
    return `${window}本站实测 ${availability.samples ?? 0} 次调用`;
  }
  return `${window}上游报告，未提供样本数`;
};

const availabilityStaleMinutes = 30;

export const isAvailabilityStale = (availability: PublicPricingAvailability, now: number) => {
  const observed = Date.parse(availability.observed_at);
  return !Number.isFinite(observed) || now - observed > availabilityStaleMinutes * 60_000;
};

const availabilityTone = (rate: string) => {
  const value = Number(rate);
  if (!Number.isFinite(value)) return 'border-[var(--border-soft)] bg-[var(--surface)] text-[var(--text-secondary)]';
  if (value >= 95) return 'border-emerald-200 bg-emerald-50 text-emerald-700';
  if (value >= 80) return 'border-amber-200 bg-amber-50 text-amber-700';
  return 'border-red-200 bg-red-50 text-red-700';
};

export const filterPricingModels = (
  models: PublicPricingModel[],
  query: string,
  typeFilter: PricingTypeFilter = 'all',
) => {
  const normalizedQuery = query.trim().toLocaleLowerCase();
  return models.filter(model => {
    if (typeFilter !== 'all' && normalizeModelType(model.type) !== typeFilter) return false;
    if (!normalizedQuery) return true;
    const fields = [
      formatPublicModelName(model),
      formatPublicText(model.description || ''),
      ...model.skus.flatMap(sku => [
        formatPricingSKUName(sku),
        formatDelivery(sku.delivery_mode),
        ...sku.routes.flatMap(route => [route.method, route.path]),
      ]),
    ];
    return fields.some(value => value?.toLocaleLowerCase().includes(normalizedQuery));
  });
};

export const groupPricingModels = (models: PublicPricingModel[]) =>
  typeGroups
    .map(group => ({ ...group, models: models.filter(model => normalizeModelType(model.type) === group.key) }))
    .filter(group => group.models.length > 0);

const modelRateRows = (model: PublicPricingModel) => model.skus.flatMap(sku =>
  sku.components.map(component => ({ sku, component })),
);

const Pricing: React.FC<PricingProps> = ({ onBack, variant = 'public' }) => {
  const [models, setModels] = useState<PublicPricingModel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [reloadKey, setReloadKey] = useState(0);
  const [search, setSearch] = useState('');
  const [typeFilter, setTypeFilter] = useState<PricingTypeFilter>('all');
  const [selectedModel, setSelectedModel] = useState<PublicPricingModel | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    void fetchPublicPricing(controller.signal)
      .then(items => {
        if (!controller.signal.aborted) setModels(items);
      })
      .catch(reason => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '价格加载失败');
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [reloadKey]);

  const presentTypes = useMemo(() => new Set(models.map(model => normalizeModelType(model.type))), [models]);
  const typeOptions = useMemo(() => [
    { key: 'all' as PricingTypeFilter, label: `全部 ${models.length}` },
    ...typeGroups
      .filter(group => presentTypes.has(group.key))
      .map(group => ({
        key: group.key as PricingTypeFilter,
        label: `${group.label} ${models.filter(model => normalizeModelType(model.type) === group.key).length}`,
      })),
  ], [models, presentTypes]);
  const filteredModels = useMemo(
    () => filterPricingModels(models, search, typeFilter),
    [models, search, typeFilter],
  );
  const groupedModels = useMemo(() => groupPricingModels(filteredModels), [filteredModels]);

  const content = (
    <PricingCatalog
      models={models}
      loading={loading}
      error={error}
      search={search}
      typeFilter={typeFilter}
      typeOptions={typeOptions}
      groups={groupedModels}
      onSearch={setSearch}
      onTypeFilter={setTypeFilter}
      onSelectModel={setSelectedModel}
      onRetry={() => setReloadKey(key => key + 1)}
    />
  );

  if (variant === 'console') {
    return (
      <div className="space-y-4">
        <PageHeader icon={Coins} title="模型价格" meta="浏览模型售价与近期成功率" />
        {content}
        <PricingDetailDrawer model={selectedModel} onClose={() => setSelectedModel(null)} />
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gradient-to-b from-[var(--surface)] to-[var(--surface-card)]">
      <header className="fixed inset-x-0 top-0 z-50 border-b border-[var(--border-soft)] bg-[var(--surface-card)]/90 backdrop-blur-md">
        <div className="mx-auto flex max-w-7xl items-center justify-between px-4 py-3 sm:px-6">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-[var(--primary)] text-xl font-bold text-white shadow-lg">P</div>
            <div><div className="font-bold text-[var(--text-primary)]">棱镜</div><div className="text-xs text-[var(--text-secondary)]">模型价格</div></div>
          </div>
          {onBack && <button onClick={onBack} className="flex items-center gap-2 px-3 py-2 text-sm text-[var(--text-secondary)] transition-colors hover:text-[var(--text-primary)]"><ArrowLeft className="h-4 w-4" />返回首页</button>}
        </div>
      </header>

      <main className="px-4 pb-16 pt-24 sm:px-6">
        <div className="mx-auto max-w-7xl">
          <div className="mb-7">
            <h1 className="text-2xl font-bold text-[var(--text-primary)] sm:text-3xl">模型价格</h1>
            <p className="mt-2 text-sm text-[var(--text-secondary)]">按模型类型浏览售价、可用规格与近期成功率。</p>
          </div>
          {content}
        </div>
      </main>

      <footer className="border-t border-[var(--border-soft)] px-6 py-6"><div className="mx-auto flex max-w-7xl items-center justify-between text-sm text-[var(--text-secondary)]"><span>棱镜 Prism</span><span>v{APP_VERSION}</span></div></footer>
      <PricingDetailDrawer model={selectedModel} onClose={() => setSelectedModel(null)} />
    </div>
  );
};

interface PricingCatalogProps {
  models: PublicPricingModel[];
  loading: boolean;
  error: string;
  search: string;
  typeFilter: PricingTypeFilter;
  typeOptions: Array<{ key: PricingTypeFilter; label: string }>;
  groups: ReturnType<typeof groupPricingModels>;
  onSearch: (value: string) => void;
  onTypeFilter: (value: PricingTypeFilter) => void;
  onSelectModel: (model: PublicPricingModel) => void;
  onRetry: () => void;
}

const PricingCatalog: React.FC<PricingCatalogProps> = ({
  models,
  loading,
  error,
  search,
  typeFilter,
  typeOptions,
  groups,
  onSearch,
  onTypeFilter,
  onSelectModel,
  onRetry,
}) => (
  <div className="space-y-5">
    <div className="glass-surface flex flex-col gap-3 rounded-lg border border-[var(--border-soft)] p-3 shadow-[var(--shadow-soft)] lg:flex-row lg:items-center">
      <label className="relative min-w-0 flex-1">
        <span className="sr-only">搜索模型</span>
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-tertiary)]" />
        <input
          type="search"
          value={search}
          onChange={event => onSearch(event.target.value)}
          placeholder="搜索模型名称或介绍"
          className="h-10 w-full rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card-solid)] pl-9 pr-9 text-sm text-[var(--text-primary)] outline-none transition focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--focus-ring)]"
        />
        {search && <button type="button" title="清空搜索" aria-label="清空搜索" onClick={() => onSearch('')} className="absolute right-2 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-md text-[var(--text-tertiary)] hover:bg-[var(--surface-muted)] hover:text-[var(--text-primary)]"><X size={14} /></button>}
      </label>
      <div className="flex max-w-full gap-1 overflow-x-auto" role="radiogroup" aria-label="模型类型">
        {typeOptions.map(option => <button
          key={option.key}
          type="button"
          role="radio"
          aria-checked={typeFilter === option.key}
          onClick={() => onTypeFilter(option.key)}
          className={`h-9 shrink-0 rounded-md px-3 text-xs font-semibold transition ${typeFilter === option.key ? 'bg-[var(--primary)] text-white shadow-sm' : 'bg-[var(--surface-muted)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}
        >{option.label}</button>)}
      </div>
    </div>

    {loading ? <PricingSkeleton /> : error ? (
      <div role="alert" className="rounded-lg border border-red-200 bg-red-50 px-5 py-8 text-center text-sm text-red-700">
        <p>{error}</p>
        <Button type="button" size="sm" variant="secondary" className="mt-3" onClick={onRetry}>重新加载</Button>
      </div>
    ) : models.length === 0 ? (
      <div className="py-20 text-center text-sm text-[var(--text-secondary)]">暂无可用模型价格</div>
    ) : groups.length === 0 ? (
      <div className="py-20 text-center">
        <p className="text-sm font-semibold text-[var(--text-primary)]">没有匹配的模型</p>
        <button type="button" onClick={() => onSearch('')} className="mt-2 text-sm text-[var(--primary)] hover:underline">清空搜索</button>
      </div>
    ) : groups.map(group => (
      <section key={group.key} aria-labelledby={`pricing-group-${group.key}`}>
        <div className="mb-3 flex items-center gap-2">
          <h2 id={`pricing-group-${group.key}`} className="text-base font-bold text-[var(--text-primary)]">{group.label}</h2>
          <span className="text-xs text-[var(--text-tertiary)]">{group.models.length} 个模型</span>
        </div>
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {group.models.map(model => <PricingModelCard key={model.code} model={model} onOpen={() => onSelectModel(model)} />)}
        </div>
      </section>
    ))}
  </div>
);

const PricingModelCard: React.FC<{ model: PublicPricingModel; onOpen: () => void }> = ({ model, onOpen }) => {
  const rates = modelRateRows(model);
  const availability = model.skus.find(sku => sku.availability)?.availability;
  const modelName = formatPublicModelName(model);
  const description = formatPublicText(model.description || '');
  return (
    <article className="flex h-full min-h-64 flex-col rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-4 shadow-[var(--shadow-soft)] transition hover:-translate-y-0.5 hover:border-[var(--primary-light)] hover:shadow-[var(--shadow-floating)]">
      <div className="flex items-start gap-3">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-[var(--primary-lighter)] text-[var(--primary)]"><Zap size={19} /></span>
        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-2">
            <h3 className="min-w-0 break-words text-sm font-bold leading-5 text-[var(--text-primary)]">{modelName}</h3>
            <span className="shrink-0 rounded-md bg-[var(--surface-muted)] px-2 py-1 text-[10px] font-semibold text-[var(--text-secondary)]">{typeGroups.find(group => group.key === normalizeModelType(model.type))?.label}</span>
          </div>
        </div>
      </div>

      <p className="mt-3 line-clamp-2 min-h-10 text-xs leading-5 text-[var(--text-secondary)]">{description || '暂无模型介绍'}</p>

      <div className="mt-3 flex min-h-7 items-center justify-between gap-2 border-y border-[var(--border-soft)] py-2">
        {availability ? <AvailabilityBadge availability={availability} compact /> : <span className="text-xs text-[var(--text-tertiary)]">暂无成功率</span>}
        <span className="shrink-0 text-xs text-[var(--text-secondary)]">{model.skus.length} 种规格</span>
      </div>

      <div className="mt-3 flex-1">
        <div className="mb-1.5 text-[11px] font-semibold text-[var(--text-secondary)]">售价</div>
        {rates.length === 0 ? <span className="text-xs text-[var(--text-tertiary)]">暂无售价</span> : (
          <div className="space-y-1.5">
            {rates.slice(0, 2).map(({ sku, component }) => <div key={`${sku.id}-${component.id}`} className="grid gap-1 text-xs sm:grid-cols-[minmax(0,1fr)_auto] sm:items-baseline sm:gap-3">
              <span className="min-w-0 truncate text-[var(--text-secondary)]" title={formatRateLabel(sku, component)}>{formatRateLabel(sku, component)}</span>
              <strong className="break-words font-semibold text-[var(--primary)] sm:text-right">{formatPricingRate(component, sku.currency)}</strong>
            </div>)}
            {rates.length > 2 && <div className="text-[11px] text-[var(--text-tertiary)]">另有 {rates.length - 2} 项费率</div>}
          </div>
        )}
      </div>

      <button type="button" onClick={onOpen} className="mt-4 flex h-9 w-full items-center justify-center gap-1.5 rounded-md bg-[var(--surface-muted)] text-xs font-semibold text-[var(--text-primary)] transition hover:bg-[var(--primary-lighter)] hover:text-[var(--primary)]">
        查看规格与价格<ChevronRight size={14} />
      </button>
    </article>
  );
};

const PricingDetailDrawer: React.FC<{ model: PublicPricingModel | null; onClose: () => void }> = ({ model, onClose }) => (
  <Dialog
    open={Boolean(model)}
    onClose={onClose}
    motion="right"
    ariaLabel={model ? `${formatPublicModelName(model)} 价格详情` : '价格详情'}
    containerClassName="items-stretch justify-end p-0"
    panelClassName="h-full w-full max-w-3xl overflow-y-auto border-l border-[var(--border-soft)] bg-[var(--surface-card-solid)] shadow-2xl"
  >
    {model && <>
      <header className="sticky top-0 z-10 flex items-start justify-between gap-4 border-b border-[var(--border-soft)] bg-[var(--surface-card-solid)]/95 px-5 py-4 backdrop-blur-md sm:px-6">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2"><h2 className="text-lg font-bold text-[var(--text-primary)]">{formatPublicModelName(model)}</h2><span className="rounded-md bg-[var(--primary-lighter)] px-2 py-1 text-xs font-semibold text-[var(--primary)]">{typeGroups.find(group => group.key === normalizeModelType(model.type))?.label}</span></div>
        </div>
        <button type="button" title="关闭" aria-label="关闭" onClick={onClose} className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-muted)]"><X size={18} /></button>
      </header>
      <div className="px-5 py-4 sm:px-6">
        {formatPublicText(model.description || '') && <p className="mb-5 text-sm leading-6 text-[var(--text-secondary)]">{formatPublicText(model.description)}</p>}
        <div className="divide-y divide-[var(--border-soft)] border-y border-[var(--border-soft)]">
          {model.skus.map(sku => <SKUDetail key={sku.id} sku={sku} />)}
        </div>
      </div>
    </>}
  </Dialog>
);

const SKUDetail: React.FC<{ sku: PublicPricingSKU }> = ({ sku }) => (
  <section className="py-5 first:pt-0 last:pb-0">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div>
        <div className="flex flex-wrap items-center gap-2">
          <span className="rounded-md bg-[var(--primary-lighter)] px-2 py-1 text-sm font-semibold text-[var(--primary)]">{formatPricingSKUName(sku)}</span>
          <span className="rounded-md border border-[var(--border-soft)] px-2 py-0.5 text-xs text-[var(--text-secondary)]">{formatDelivery(sku.delivery_mode)}</span>
        </div>
        <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-[var(--text-tertiary)]">
          {sku.max_results > 0 && <span>结果上限：{sku.max_results}</span>}
          {formatIdempotency(sku.idempotency_mode) && <span>{formatIdempotency(sku.idempotency_mode)}</span>}
        </div>
      </div>
      {sku.availability ? <AvailabilityBadge availability={sku.availability} /> : <span className="text-xs text-[var(--text-tertiary)]">暂无成功率</span>}
    </div>

    <div className="mt-4">
      <h3 className="text-xs font-bold text-[var(--text-secondary)]">售价</h3>
      <div className="mt-2 divide-y divide-[var(--border-soft)] border-y border-[var(--border-soft)]">
        {(sku.components || []).map(component => <div key={component.id} className="grid gap-2 py-3 text-xs sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
          <div className="min-w-0">
            <div className="font-semibold text-[var(--text-primary)]">{formatSource(component.quantity_source)} · {formatEvent(component.charge_event)}</div>
            <div className="mt-1 text-[var(--text-tertiary)]">价格单位：{component.unit_code === 'token' ? '百万 token' : formatUnit(component.unit_code)}
              {component.max_quantity && ` · 单次上限 ${normalizeDecimal(component.max_quantity)} ${formatUnit(component.unit_code)}`}
            </div>
          </div>
          <strong className="font-semibold text-[var(--primary)]">{formatPricingRate(component, sku.currency)}</strong>
        </div>)}
        {sku.components.length === 0 && <div className="py-3 text-xs text-[var(--text-tertiary)]">暂无售价</div>}
      </div>
    </div>

    <div className="mt-4">
      <h3 className="text-xs font-bold text-[var(--text-secondary)]">调用入口</h3>
      <div className="mt-2 flex flex-wrap gap-2">
        {(sku.routes || []).map(route => <span key={`${route.method}:${route.path}`} className="inline-flex items-center gap-2 rounded-md bg-[var(--surface-muted)] px-2.5 py-1.5 text-xs"><b className="text-[var(--primary)]">{route.method}</b><code className="text-[var(--text-primary)]">{route.path}</code></span>)}
      </div>
    </div>
  </section>
);

const AvailabilityBadge: React.FC<{ availability: PublicPricingAvailability; compact?: boolean }> = ({ availability, compact = false }) => {
  const stale = isAvailabilityStale(availability, Date.now());
  return (
    <span
      title={`${describeAvailability(availability)}，观测于 ${new Date(availability.observed_at).toLocaleString()}`}
      className={`inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs font-medium ${stale ? 'border-[var(--border-soft)] bg-[var(--surface)] text-[var(--text-secondary)]' : availabilityTone(availability.success_rate)}`}
    >
      <Activity className="h-3.5 w-3.5" />
      成功率 {availability.success_rate}%
      {!compact && <span className="font-normal opacity-70">{stale ? '· 数据已过期' : availability.source === 'local' ? `· 实测 ${availability.samples ?? 0} 次` : '· 上游报告'}</span>}
    </span>
  );
};

const PricingSkeleton = () => (
  <div className="space-y-5" aria-label="正在加载模型价格">
    {[0, 1].map(group => <section key={group}>
      <div className="mb-3 h-5 w-28 animate-pulse rounded bg-[var(--surface-muted)]" />
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {[0, 1, 2].map(card => <div key={card} className="h-64 animate-pulse rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)]" />)}
      </div>
    </section>)}
  </div>
);

export default Pricing;
