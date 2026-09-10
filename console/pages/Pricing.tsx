import React, { useEffect, useState } from 'react';
import { ArrowLeft, ChevronDown, ChevronUp, Coins, Zap } from 'lucide-react';
import {
  fetchPublicPricing,
  type PublicPricingCurrency,
  type PublicPricingModel,
  type PublicPricingSKU,
  type PublicRateComponent,
} from '../services/pricingApi';
import { APP_VERSION } from '../version';

interface PricingProps {
  onBack: () => void;
}

const unitLabels: Record<string, string> = {
  request: '次', token: 'token', second: '秒', image: '张图', video: '个视频', megapixel: 'MP',
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

const typeLabels: Record<string, string> = {
  chat: '对话', image: '图像', video: '视频', audio: '音频', embedding: '嵌入', other: '其他',
};

const formatUnit = (unit: string) => unitLabels[unit] || unit || '单位';
const formatSource = (source: string) => sourceLabels[source] || source;
const formatEvent = (event: string) => eventLabels[event] || event;

export const formatPricingRate = (component: PublicRateComponent, currency: PublicPricingCurrency) => {
  const quantity = component.unit_scale > 0 ? `10^${component.unit_scale} ` : '';
  return `${currency.code} ${component.unit_price} / ${quantity}${formatUnit(component.unit_code)}`;
};

const Pricing: React.FC<PricingProps> = ({ onBack }) => {
  const [models, setModels] = useState<PublicPricingModel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [expandedModels, setExpandedModels] = useState<Set<string>>(new Set());

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    void fetchPublicPricing(controller.signal)
      .then(items => {
        if (controller.signal.aborted) return;
        setModels(items);
        setExpandedModels(new Set(items.map(item => item.code)));
      })
      .catch(reason => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '价格加载失败');
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, []);

  const toggleModel = (code: string) => {
    setExpandedModels(previous => {
      const next = new Set(previous);
      if (next.has(code)) next.delete(code);
      else next.add(code);
      return next;
    });
  };

  if (loading) {
    return <div className="flex min-h-screen items-center justify-center bg-[var(--surface)]"><div className="h-8 w-8 animate-spin rounded-full border-2 border-[var(--primary-light)] border-b-[var(--primary)]" /></div>;
  }

  return (
    <div className="min-h-screen bg-gradient-to-b from-[var(--surface)] to-[var(--surface-card)]">
      <header className="fixed inset-x-0 top-0 z-50 border-b border-[var(--border-soft)] bg-[var(--surface-card)]/80 backdrop-blur-md">
        <div className="mx-auto flex max-w-7xl items-center justify-between px-6 py-4">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-[var(--primary)] text-xl font-bold text-white shadow-lg">P</div>
            <span className="text-xl font-bold text-[var(--text-primary)]">棱镜</span>
            <span className="hidden text-sm text-[var(--text-secondary)] sm:inline">价格列表</span>
          </div>
          <button onClick={onBack} className="flex items-center gap-2 px-4 py-2 text-[var(--text-secondary)] transition-colors hover:text-[var(--text-primary)]"><ArrowLeft className="h-4 w-4" />返回首页</button>
        </div>
      </header>

      <main className="px-6 pb-20 pt-24">
        <div className="mx-auto max-w-5xl">
          <div className="mb-12 text-center">
            <div className="mb-6 inline-flex items-center gap-2 rounded-full bg-[var(--primary-lighter)] px-4 py-2 text-sm font-medium text-[var(--primary)]"><Coins className="h-4 w-4" />透明定价</div>
            <h1 className="mb-4 text-3xl font-bold text-[var(--text-primary)] sm:text-4xl">模型与 SKU 价格</h1>
            <p className="mx-auto max-w-2xl text-[var(--text-secondary)]">价格属于可执行 SKU，按费率组件和实际计量项计算；不同操作可以拥有不同服务档位。</p>
          </div>

          {error ? <div role="alert" className="rounded-xl border border-red-200 bg-red-50 px-5 py-4 text-sm text-red-700">{error}</div> : (
            <div className="space-y-4">
              {models.length === 0 ? <div className="py-20 text-center text-[var(--text-secondary)]">暂无可用价格</div> : models.map(model => {
                const skus = model.skus || [];
                const expanded = expandedModels.has(model.code);
                return <section key={model.code} className="overflow-hidden rounded-2xl border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-sm">
                  <button onClick={() => toggleModel(model.code)} className="flex w-full items-center justify-between gap-4 px-6 py-5 text-left transition-colors hover:bg-[var(--surface)]">
                    <div className="flex min-w-0 items-center gap-4">
                      <div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-xl bg-[var(--primary-lighter)] text-[var(--primary)]"><Zap className="h-6 w-6" /></div>
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <h2 className="text-lg font-bold text-[var(--text-primary)]">{model.name}</h2>
                          <span className="rounded-full bg-[var(--primary-lighter)] px-2 py-0.5 text-xs text-[var(--primary)]">{typeLabels[model.type] || model.type}</span>
                        </div>
                        <p className="truncate text-sm text-[var(--text-secondary)]">{model.code} · {model.model_code}</p>
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-3">
                      <span className="text-sm text-[var(--text-secondary)]">{skus.length} 个 SKU</span>
                      {expanded ? <ChevronUp className="h-5 w-5 text-[var(--text-secondary)]" /> : <ChevronDown className="h-5 w-5 text-[var(--text-secondary)]" />}
                    </div>
                  </button>

                  {expanded && <div className="border-t border-[var(--border-soft)]">
                    {model.description && <p className="bg-[var(--surface)] px-5 py-3 text-sm text-[var(--text-secondary)]">{model.description}</p>}
                    {skus.map(sku => <SKUBlock key={sku.id} sku={sku} />)}
                  </div>}
                </section>;
              })}
            </div>
          )}

          <div className="mt-12 rounded-2xl border border-amber-100 bg-amber-50 p-6">
            <h3 className="mb-2 font-semibold text-amber-800">计费说明</h3>
            <ul className="space-y-1 text-sm text-amber-700">
              <li>· 金额保留服务端返回的精度，不在页面端四舍五入。</li>
              <li>· 每个 SKU 的费率组件可以按请求、token、秒或生成数量计量。</li>
              <li>· 具体扣费以调用时选中的模型、操作和 SKU 为准。</li>
            </ul>
          </div>
        </div>
      </main>

      <footer className="border-t border-[var(--border-soft)] px-6 py-8"><div className="mx-auto flex max-w-6xl flex-col items-center justify-between gap-4 sm:flex-row"><span className="text-[var(--text-secondary)]">棱镜 Prism</span><span className="text-sm text-[var(--text-secondary)]">v{APP_VERSION} - AI Gateway</span></div></footer>
    </div>
  );
};

const SKUBlock: React.FC<{ sku: PublicPricingSKU }> = ({ sku }) => (
  <article className="border-t border-[var(--border-soft)] first:border-t-0">
    <div className="flex flex-wrap items-start justify-between gap-3 px-5 py-4">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <code className="rounded-lg bg-[var(--primary-lighter)] px-2 py-1 text-sm font-semibold text-[var(--primary)]">{sku.code}</code>
          <span className="text-xs text-[var(--text-secondary)]">{sku.operation}</span>
          <span className="rounded-full border border-[var(--border-soft)] bg-[var(--surface)] px-2 py-0.5 text-xs text-[var(--text-secondary)]">{sku.delivery_mode}</span>
        </div>
        <div className="mt-2 flex flex-wrap gap-2 text-xs text-[var(--text-secondary)]">
          {(sku.service_tiers || []).map(tier => <span key={tier} className="rounded-full border border-[var(--border-soft)] bg-[var(--surface)] px-2 py-0.5">{tier}</span>)}
          {sku.idempotency_mode && <span>幂等：{sku.idempotency_mode}</span>}
          {sku.max_results > 0 && <span>结果上限：{sku.max_results}</span>}
        </div>
      </div>
      <span className="text-xs text-[var(--text-tertiary)]">{sku.currency?.code || '-'} v{sku.currency?.version || '-'}</span>
    </div>

    <div className="border-t border-[var(--border-soft)] px-5 py-3">
      <div className="mb-2 text-xs font-semibold text-[var(--text-secondary)]">可执行路由</div>
      <div className="flex flex-wrap gap-2">
        {(sku.routes || []).map(route => <span key={`${route.method}:${route.path}`} className="inline-flex items-center gap-2 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] px-2.5 py-1 text-xs"><b className="text-[var(--primary)]">{route.method}</b><code className="text-[var(--text-primary)]">{route.path}</code></span>)}
      </div>
    </div>

    <div className="overflow-x-auto border-t border-[var(--border-soft)]">
      <table className="w-full min-w-[680px] text-sm">
        <thead><tr className="bg-[var(--surface)] text-xs text-[var(--text-secondary)]"><th className="px-5 py-3 text-left">费率组件</th><th className="px-5 py-3 text-left">计量项</th><th className="px-5 py-3 text-left">触发事件</th><th className="px-5 py-3 text-right">单价</th></tr></thead>
        <tbody className="divide-y divide-[var(--border-soft)]">
          {(sku.components || []).map(component => <tr key={component.id} className="hover:bg-[var(--surface)]/70">
            <td className="px-5 py-3"><code className="text-[var(--primary)]">{component.component_code}</code><span className="block text-xs text-[var(--text-tertiary)]">上限 {component.max_quantity}</span></td>
            <td className="px-5 py-3 text-[var(--text-primary)]">{formatSource(component.quantity_source)}<span className="block text-xs text-[var(--text-tertiary)]">单位：{formatUnit(component.unit_code)}</span></td>
            <td className="px-5 py-3 text-[var(--text-secondary)]">{formatEvent(component.charge_event)}</td>
            <td className="whitespace-nowrap px-5 py-3 text-right font-semibold text-[var(--primary)]">{formatPricingRate(component, sku.currency)}</td>
          </tr>)}
        </tbody>
      </table>
    </div>
  </article>
);

export default Pricing;
