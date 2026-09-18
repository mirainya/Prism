import React, { useEffect, useState } from 'react';
import { Save } from 'lucide-react';
import { Button } from '../../components/ui';
import {
  changeUnifiedCostRate,
  changeUnifiedRouteWeight,
  changeUnifiedSKUDownstreamPaths,
  changeUnifiedSKUVariant,
  fetchUnifiedCatalogRates,
  type UnifiedCatalogProduct,
  type UnifiedCatalogRate,
  type UnifiedCatalogSKU,
} from '../../services/unifiedGatewayApi';
import { ErrorNotice, errorMessage } from '../unified_gateway/Feedback';
import {
  formatRateNumber,
  formatSKUDisplayName,
  formatSKUVariantLabel,
  rateChargeEventLabel,
  rateComponentLabel,
  rateCurrencyLabel,
  ratePricingModeLabel,
  rateQuantitySourceLabel,
  rateUnitLabel,
} from './presentation';

export interface CatalogChangeContext {
  catalogId: number;
  configVersion: number;
  canEdit: boolean;
  onChanged: () => void;
  onNotice: (text: string) => void;
}

const fieldClass = 'mt-1 w-full rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] px-2 py-1.5 text-xs font-normal';
const cardClass = 'rounded-md border border-[var(--border-soft)] px-2.5 py-2 text-xs';
const editClass = 'shrink-0 text-[11px] font-semibold text-[var(--primary)]';

const useCatalogChange = (ctx: CatalogChangeContext) => {
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const run = async (key: string, call: () => Promise<unknown>, done?: () => void) => {
    if (!ctx.canEdit || busy) return;
    setBusy(key);
    setError('');
    try {
      await call();
      ctx.onNotice('已保存并生效');
      done?.();
      ctx.onChanged();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      setBusy('');
    }
  };
  return { busy, error, setError, run };
};

/** 上游成本价。成本挂在 cost_plan 上，cost_plan 挂在 offering 上，所以业务身份是
 *  product_code + plan_code + component_code 三个码，pool_code 只在同一对
 *  (product, plan) 命中多行时才需要。 */
export const CostRateEditor: React.FC<{
  ctx: CatalogChangeContext;
  products: UnifiedCatalogProduct[];
}> = ({ ctx, products }) => {
  const [rates, setRates] = useState<UnifiedCatalogRate[] | null>(null);
  const [loadError, setLoadError] = useState('');
  const [editing, setEditing] = useState<UnifiedCatalogRate | null>(null);
  const [price, setPrice] = useState('');
  const [mode, setMode] = useState<'flat' | 'expression'>('flat');
  const [expression, setExpression] = useState('');
  const { busy, error, setError, run } = useCatalogChange(ctx);
  const planKey = products.map((product) => product.cost_plan_id).sort((left, right) => left - right).join(',');

  useEffect(() => {
    const controller = new AbortController();
    const planIDs = new Set(products.map((product) => product.cost_plan_id).filter(Boolean));
    if (planIDs.size === 0) {
      setRates([]);
      return () => controller.abort();
    }
    setRates(null);
    setLoadError('');
    fetchUnifiedCatalogRates(ctx.catalogId, 'cost', 1, 100, controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) setRates(result.items.filter((rate) => planIDs.has(rate.parent_id)));
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) {
          setLoadError(errorMessage(reason));
          setRates([]);
        }
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx.catalogId, planKey]);

  const productOf = (rate: UnifiedCatalogRate) => products.find((product) => product.cost_plan_id === rate.parent_id);
  const beginEdit = (rate: UnifiedCatalogRate) => {
    setEditing(rate);
    setPrice(rate.unit_price);
    setMode(rate.pricing_mode === 'expression' ? 'expression' : 'flat');
    setExpression(rate.pricing_expr || '');
    setError('');
  };
  const save = () => {
    if (!editing) return;
    const product = productOf(editing);
    if (!product) {
      setError('找不到这条成本价对应的上游产品，请刷新后重试');
      return;
    }
    if (!/^\d+(\.\d+)?$/.test(price.trim()) || (mode === 'expression' && !expression.trim())) {
      setError('请填写有效的成本单价和计价表达式');
      return;
    }
    void run('rate', () => changeUnifiedCostRate({
      expected_active_release_id: ctx.catalogId,
      expected_config_version: ctx.configVersion,
      semantic_version: `ops-cost-${Date.now()}`,
      product_code: product.product_code,
      plan_code: product.cost_plan_code,
      component_code: editing.component_code,
      // 只有同一对 (product, plan) 命中多行时后端才需要它，多给无害。
      ...(product.pool_code ? { pool_code: product.pool_code } : {}),
      unit_price: price.trim(),
      pricing_mode: mode,
      pricing_expr: mode === 'expression' ? expression.trim() : '',
      reason_code: 'console_price_change',
    }), () => setEditing(null));
  };

  if (loadError) return <ErrorNotice message={loadError} />;
  if (rates === null) return <p className="text-xs text-[var(--text-secondary)]">正在读取成本价</p>;
  return <>
    {error && <div className="mb-2"><ErrorNotice message={error} /></div>}
    {rates.length === 0
      ? <p className="text-xs text-[var(--text-secondary)]">暂无已配置成本价。成本价只影响利润核算，缺失不会挡住调用。</p>
      : <ul className="space-y-2">{rates.map((rate) => {
        const product = productOf(rate);
        return <li key={rate.id} className={cardClass}>
          <div className="flex items-center justify-between gap-2">
            <strong className="truncate" title={rate.parent_label}>{product?.channel_name || product?.product_code || rate.parent_label} · {rateComponentLabel(rate.component_code)}</strong>
            {ctx.canEdit && <button type="button" className={editClass} onClick={() => beginEdit(rate)}>编辑</button>}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-[11px] text-[var(--text-secondary)]">
            <span>成本 {formatRateNumber(rate.unit_price)} {rateCurrencyLabel(rate.currency_code)} / {rateUnitLabel(rate.unit_code)}</span>
            <span>{ratePricingModeLabel(rate.pricing_mode)}</span>
          </div>
          <div className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-[10px] text-[var(--text-secondary)]">
            <span>计量：{rateQuantitySourceLabel(rate.quantity_source)}</span>
            <span>计费：{rateChargeEventLabel(rate.charge_event)}</span>
            <span>成本方案：{rate.parent_label || product?.cost_plan_code || '-'}</span>
          </div>
          {rate.pricing_mode === 'expression' && rate.pricing_expr && <code className="mt-1 block break-all text-[10px] text-[var(--text-secondary)]">计价公式：{rate.pricing_expr}</code>}
        </li>;
      })}</ul>}
    {editing && <div className="mt-3 rounded-md bg-[var(--surface-muted)] p-2.5">
      <div className="flex items-center justify-between gap-2">
        <strong className="text-[11px]">编辑 {rateComponentLabel(editing.component_code)}成本价</strong>
        <button type="button" className="text-[11px] text-[var(--text-secondary)]" onClick={() => setEditing(null)} disabled={Boolean(busy)}>取消</button>
      </div>
      <div className="mt-2 grid gap-2 sm:grid-cols-2">
        <label className="text-[11px] font-semibold">单位成本<input inputMode="decimal" value={price} onChange={(event) => setPrice(event.target.value)} className={fieldClass} /></label>
        <label className="text-[11px] font-semibold">计价方式<select value={mode} onChange={(event) => setMode(event.target.value as 'flat' | 'expression')} className={fieldClass}><option value="flat">固定单价</option><option value="expression">按公式计价</option></select></label>
      </div>
      {mode === 'expression' && <textarea value={expression} onChange={(event) => setExpression(event.target.value)} spellCheck={false} className="mt-2 min-h-20 w-full resize-y rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] p-2 font-mono text-[11px] leading-4" placeholder="例如 input_tokens * 0.000001" />}
      <p className="mt-1 text-[10px] leading-4 text-[var(--text-secondary)]">只用于成本核算，不改变用户售价。</p>
      <div className="mt-2 flex justify-end"><Button type="button" size="sm" onClick={save} loading={busy === 'rate'}><Save size={13} />保存成本价</Button></div>
    </div>}
  </>;
};

/** 变体 / 下游路径 / 路由权重。前两个是 SKU 内容，第三个是 (offering, sku) 这条 route
 *  上的属性——同一个上游产品服务两个 SKU 时权重可以不同，所以按 SKU 展开。 */
export const RoutingEditor: React.FC<{
  ctx: CatalogChangeContext;
  skus: UnifiedCatalogSKU[];
  products: UnifiedCatalogProduct[];
}> = ({ ctx, skus, products }) => {
  const [editing, setEditing] = useState<{ kind: 'variant' | 'paths'; skuId: number } | { kind: 'route'; skuId: number; productId: number } | null>(null);
  const [variant, setVariant] = useState('');
  const [paths, setPaths] = useState('');
  const [priority, setPriority] = useState('0');
  const [weight, setWeight] = useState('1');
  const { busy, error, setError, run } = useCatalogChange(ctx);

  const close = () => setEditing(null);
  const beginVariant = (sku: UnifiedCatalogSKU) => {
    setEditing({ kind: 'variant', skuId: sku.id });
    setVariant(sku.variant_code || 'default');
    setError('');
  };
  const beginPaths = (sku: UnifiedCatalogSKU) => {
    setEditing({ kind: 'paths', skuId: sku.id });
    setPaths((sku.downstream_paths?.length ? sku.downstream_paths : [sku.route_template].filter(Boolean)).join('\n'));
    setError('');
  };
  const beginRoute = (sku: UnifiedCatalogSKU, product: UnifiedCatalogProduct) => {
    const route = (product.routes || []).find((item) => item.sku_id === sku.id);
    setEditing({ kind: 'route', skuId: sku.id, productId: product.id });
    setPriority(String(route?.priority ?? 0));
    setWeight(String(route?.weight ?? 1));
    setError('');
  };

  const saveVariant = (sku: UnifiedCatalogSKU) => {
    const code = variant.trim().toLowerCase();
    if (!code) {
      setError('变体标识不能为空，没有变体时填 default');
      return;
    }
    void run('variant', () => changeUnifiedSKUVariant({
      expected_active_release_id: ctx.catalogId,
      expected_config_version: ctx.configVersion,
      semantic_version: `ops-variant-${Date.now()}`,
      sku_code: sku.sku_code,
      variant_code: code,
    }), close);
  };
  const savePaths = (sku: UnifiedCatalogSKU) => {
    const list = paths.split('\n').map((line) => line.trim()).filter(Boolean);
    if (list.length === 0 || list.some((path) => !path.startsWith('/'))) {
      setError('每行一个下游路径，且必须以 / 开头');
      return;
    }
    if (new Set(list).size !== list.length) {
      setError('下游路径不能重复');
      return;
    }
    void run('paths', () => changeUnifiedSKUDownstreamPaths({
      expected_active_release_id: ctx.catalogId,
      expected_config_version: ctx.configVersion,
      semantic_version: `ops-paths-${Date.now()}`,
      sku_code: sku.sku_code,
      downstream_paths: list,
    }), close);
  };
  const saveRoute = (sku: UnifiedCatalogSKU, product: UnifiedCatalogProduct) => {
    const priorityValue = Number(priority.trim());
    const weightValue = Number(weight.trim());
    if (!Number.isInteger(priorityValue) || priorityValue < 0 || priorityValue > 1000000000) {
      setError('优先级是 0 到 1000000000 的整数，数值越小越先被选');
      return;
    }
    if (!Number.isInteger(weightValue) || weightValue < 1 || weightValue > 1000000) {
      setError('权重是 1 到 1000000 的整数');
      return;
    }
    if (!product.pool_code) {
      setError('这条线路缺少密钥池标识，请刷新后重试');
      return;
    }
    void run('route', () => changeUnifiedRouteWeight({
      expected_active_release_id: ctx.catalogId,
      expected_config_version: ctx.configVersion,
      semantic_version: `ops-route-${Date.now()}`,
      sku_code: sku.sku_code,
      product_code: product.product_code,
      pool_code: product.pool_code as string,
      transport_code: product.transport_code,
      priority: priorityValue,
      weight: weightValue,
    }), close);
  };

  if (skus.length === 0) return <p className="text-xs text-[var(--text-secondary)]">该模型还没有服务规格，无法配置变体、下游入口或线路权重。</p>;
  return <>
    {error && <div className="mb-2"><ErrorNotice message={error} /></div>}
    <ul className="space-y-2">{skus.map((sku) => {
      const skuPaths = sku.downstream_paths?.length ? sku.downstream_paths : [sku.route_template].filter(Boolean);
      const skuProducts = products.filter((product) => (product.routes || []).some((route) => route.sku_id === sku.id));
      const variantLabel = formatSKUVariantLabel(sku.variant_code) || (sku.variant_code || 'default');
      return <li key={sku.id} className={cardClass}>
        <div className="flex items-center justify-between gap-2">
          <strong className="truncate" title={sku.sku_code}>{formatSKUDisplayName(sku)}</strong>
          <code className="shrink-0 text-[10px] text-[var(--text-secondary)]">{sku.sku_code}</code>
        </div>

        <div className="mt-2 flex items-center justify-between gap-2 text-[11px]">
          <span className="min-w-0 truncate text-[var(--text-secondary)]">变体：<span className="text-[var(--text-primary)]">{variantLabel}</span></span>
          {ctx.canEdit && <button type="button" className={editClass} onClick={() => beginVariant(sku)}>编辑</button>}
        </div>
        {editing?.kind === 'variant' && editing.skuId === sku.id && <div className="mt-2 rounded-md bg-[var(--surface-muted)] p-2.5">
          <label className="block text-[11px] font-semibold">变体标识<input value={variant} onChange={(event) => setVariant(event.target.value)} spellCheck={false} className={`${fieldClass} font-mono`} placeholder="default" /></label>
          <p className="mt-1 text-[10px] leading-4 text-[var(--text-secondary)]">变体需与当前模型适配器支持的值一致。</p>
          <div className="mt-2 flex justify-end gap-2">
            <Button type="button" size="sm" variant="ghost" onClick={close} disabled={Boolean(busy)}>取消</Button>
            <Button type="button" size="sm" onClick={() => saveVariant(sku)} loading={busy === 'variant'}><Save size={13} />保存变体</Button>
          </div>
        </div>}

        <div className="mt-2 flex items-start justify-between gap-2 text-[11px]">
          <span className="min-w-0 text-[var(--text-secondary)]">下游入口：<span className="break-all text-[var(--text-primary)]">{skuPaths.join('、') || '-'}</span></span>
          {ctx.canEdit && <button type="button" className={editClass} onClick={() => beginPaths(sku)}>编辑</button>}
        </div>
        {editing?.kind === 'paths' && editing.skuId === sku.id && <div className="mt-2 rounded-md bg-[var(--surface-muted)] p-2.5">
          <label className="block text-[11px] font-semibold">下游路径，每行一个<textarea value={paths} onChange={(event) => setPaths(event.target.value)} spellCheck={false} className="mt-1 min-h-20 w-full resize-y rounded-md border border-[var(--border-soft)] bg-[var(--surface-card-solid)] p-2 font-mono text-[11px] leading-4" placeholder="/v1/chat/completions" /></label>
          <p className="mt-1 text-[10px] leading-4 text-[var(--text-secondary)]">这是客户端能用哪些下游地址调到这个 SKU，写错会直接让现有调用 404。</p>
          <div className="mt-2 flex justify-end gap-2">
            <Button type="button" size="sm" variant="ghost" onClick={close} disabled={Boolean(busy)}>取消</Button>
            <Button type="button" size="sm" onClick={() => savePaths(sku)} loading={busy === 'paths'}><Save size={13} />保存入口</Button>
          </div>
        </div>}

        <div className="mt-2 border-t border-[var(--border-soft)] pt-2">
          <div className="text-[10px] font-semibold text-[var(--text-secondary)]">线路优先级 / 权重</div>
          {skuProducts.length === 0
            ? <p className="mt-1 text-[11px] text-[var(--text-secondary)]">这个规格还没有任何线路指向上游产品。</p>
            : <ul className="mt-1 space-y-1">{skuProducts.map((product) => {
              const route = (product.routes || []).find((item) => item.sku_id === sku.id);
              const open = editing?.kind === 'route' && editing.skuId === sku.id && editing.productId === product.id;
              return <li key={product.id}>
                <div className="flex items-center justify-between gap-2 text-[11px]">
                  <span className="min-w-0 truncate" title={`${product.product_code} · ${product.transport_code}`}>{product.channel_name || product.product_code} · {product.transport_code}</span>
                  <span className="shrink-0 text-[var(--text-secondary)]">优先级 {route?.priority ?? '-'} · 权重 {route?.weight ?? '-'}</span>
                  {ctx.canEdit && <button type="button" className={editClass} onClick={() => beginRoute(sku, product)}>编辑</button>}
                </div>
                {open && <div className="mt-2 rounded-md bg-[var(--surface-muted)] p-2.5">
                  <div className="grid gap-2 sm:grid-cols-2">
                    <label className="text-[11px] font-semibold">优先级<input inputMode="numeric" value={priority} onChange={(event) => setPriority(event.target.value)} className={fieldClass} /></label>
                    <label className="text-[11px] font-semibold">权重<input inputMode="numeric" value={weight} onChange={(event) => setWeight(event.target.value)} className={fieldClass} /></label>
                  </div>
                  <p className="mt-1 text-[10px] leading-4 text-[var(--text-secondary)]">优先级小的先被选中；同优先级的线路之间按权重分流量。</p>
                  <div className="mt-2 flex justify-end gap-2">
                    <Button type="button" size="sm" variant="ghost" onClick={close} disabled={Boolean(busy)}>取消</Button>
                    <Button type="button" size="sm" onClick={() => saveRoute(sku, product)} loading={busy === 'route'}><Save size={13} />保存线路</Button>
                  </div>
                </div>}
              </li>;
            })}</ul>}
        </div>
      </li>;
    })}</ul>
  </>;
};
