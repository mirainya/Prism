import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Blocks, RefreshCw } from 'lucide-react';
import {
  UnifiedAdapterManifest,
  UnifiedAdapterParamHint,
  UnifiedAdapterSummary,
  UnifiedAdapterVariant,
  fetchUnifiedAdapterManifest,
  fetchUnifiedAdapters,
} from '../services/unifiedGatewayApi';
import { Badge, Button } from '../components/ui';
import { PageHeader } from '../components/shell';

// 填一个 SKU 之前真正要回答的问题——这个变体覆盖哪些上游模型、有哪些参数、
// 取值范围是多少、价格表达式里允许引用哪些量——答案全在 Manifest 里，而目录
// 选项接口只暴露 Descriptor 的 5 个字段。这一页是 Manifest 的第一个出口。

const SECTION = 'rounded-xl border border-[var(--border-soft)] bg-white/60 p-4';
const LABEL = 'text-xs font-semibold text-[var(--text-secondary)]';
const CODE = 'font-mono text-xs text-[var(--text-primary)]';

const layerLabel: Record<string, string> = { common: '通用', capability: '能力', adapter: '适配器' };

const describeHint = (hint: UnifiedAdapterParamHint) => {
  const parts: string[] = [];
  if (hint.options?.length) parts.push(hint.options.join(' / '));
  if (hint.min || hint.max) parts.push(`${hint.min || '-'} ~ ${hint.max || '-'}`);
  if (hint.default) parts.push(`默认 ${hint.default}`);
  if (hint.allowed !== undefined) parts.push(hint.allowed ? '允许' : '不允许');
  return parts.join('，') || '无额外约束';
};

const VariantCard: React.FC<{ variant: UnifiedAdapterVariant }> = ({ variant }) => {
  const hints = Object.entries(variant.param_hints || {}).sort(([a], [b]) => a.localeCompare(b));
  return (
    <div className={SECTION}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-semibold text-[var(--text-primary)]">{variant.display || variant.code}</span>
        <Badge variant="info">{variant.code}</Badge>
        {variant.pricing_default_mode && <Badge>计价 {variant.pricing_default_mode}</Badge>}
      </div>

      {variant.models?.length > 0 && (
        <div className="mt-3">
          <div className={LABEL}>覆盖上游模型</div>
          <div className="mt-1 flex flex-wrap gap-1.5">
            {variant.models.map(model => (
              <span key={model} className={`${CODE} rounded bg-gray-100 px-2 py-0.5`}>{model}</span>
            ))}
          </div>
        </div>
      )}

      {hints.length > 0 && (
        <div className="mt-3">
          <div className={LABEL}>参数</div>
          <div className="mt-1 space-y-1">
            {hints.map(([name, hint]) => (
              <div key={name} className="flex flex-wrap items-baseline gap-2 text-sm">
                <span className={CODE}>{name}</span>
                <Badge>{hint.kind}</Badge>
                {hint.required && <Badge variant="warning">必填</Badge>}
                <span className="text-[var(--text-secondary)]">{describeHint(hint)}</span>
                {hint.cases?.map((c, index) => (
                  <span key={index} className="text-xs text-[var(--text-secondary)]">
                    （{name}={c.value} 时 {c.dependent} 为 {c.min || '-'} ~ {c.max || '-'}）
                  </span>
                ))}
              </div>
            ))}
          </div>
        </div>
      )}

      {variant.hard_constraints?.length ? (
        <div className="mt-3">
          <div className={LABEL}>硬约束（不满足直接拒绝）</div>
          <div className="mt-1 space-y-0.5 text-sm">
            {variant.hard_constraints.map((constraint, index) => (
              <div key={index}><span className={CODE}>{constraint.field}</span> 必须为 <span className={CODE}>{constraint.must_be}</span></div>
            ))}
          </div>
        </div>
      ) : null}

      {variant.ref_media?.length ? (
        <div className="mt-3">
          <div className={LABEL}>参考媒体</div>
          <div className="mt-1 space-y-0.5 text-sm">
            {variant.ref_media.map(media => (
              <div key={media.field}>
                <span className={CODE}>{media.field}</span> 最多 {media.max} 项，单项 {media.item_seconds_min}~{media.item_seconds_max} 秒，合计 {media.total_seconds} 秒
              </div>
            ))}
          </div>
        </div>
      ) : null}

      {variant.tiers && Object.keys(variant.tiers).length > 0 && (
        <div className="mt-3">
          <div className={LABEL}>阶梯</div>
          {Object.entries(variant.tiers).map(([name, rows]) => (
            <div key={name} className="mt-1 text-sm">
              <span className={CODE}>{name}</span>
              <span className="ml-2 text-[var(--text-secondary)]">
                {rows.map(row => `≤${row.up_to} → ${row.value}`).join('，')}
              </span>
            </div>
          ))}
        </div>
      )}

      {variant.pricing_example && (
        <div className="mt-3">
          <div className={LABEL}>价格表达式示例</div>
          <pre className="mt-1 overflow-x-auto rounded bg-gray-50 p-2 font-mono text-xs">{variant.pricing_example}</pre>
        </div>
      )}
    </div>
  );
};

const Adapters: React.FC = () => {
  const [list, setList] = useState<UnifiedAdapterSummary[]>([]);
  const [digest, setDigest] = useState('');
  const [selected, setSelected] = useState('');
  const [manifest, setManifest] = useState<UnifiedAdapterManifest | null>(null);
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [error, setError] = useState('');
  const abortRef = useRef<AbortController | null>(null);

  const loadList = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await fetchUnifiedAdapters();
      setList(data.items || []);
      setDigest(data.semantic_digest || '');
      setSelected(current => current || data.items?.[0]?.code || '');
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载 Adapter 列表失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { loadList(); }, [loadList]);

  useEffect(() => {
    if (!selected) return;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    const version = list.find(item => item.code === selected)?.version || 1;
    setDetailLoading(true);
    fetchUnifiedAdapterManifest(selected, version, controller.signal)
      .then(data => setManifest(data))
      .catch(err => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : '加载 Manifest 失败'); })
      .finally(() => { if (!controller.signal.aborted) setDetailLoading(false); });
    return () => controller.abort();
  }, [selected, list]);

  const current = list.find(item => item.code === selected);
  const billingVars = manifest?.billing_vars || [];

  return (
    <div className="space-y-6">
      <PageHeader
        icon={Blocks}
        title="Adapter 契约"
        meta="这个二进制里编译进了哪些 adapter、各自收什么参数、哪些量可以进价格表达式"
        actions={<Button variant="secondary" onClick={loadList} disabled={loading}><RefreshCw size={16} className={loading ? 'animate-spin' : ''} />刷新</Button>}
      />

      {error && <div className="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-600">{error}</div>}

      {digest && (
        <div className="text-xs text-[var(--text-secondary)]">
          语义摘要 <span className={CODE}>{digest}</span>
          <span className="ml-2">用于确认当前运行的 Adapter 实现；摘要变化表示实现代码已更新。</span>
        </div>
      )}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[260px_1fr]">
        <div className="space-y-1.5">
          {list.map(item => (
            <button
              key={item.code}
              onClick={() => setSelected(item.code)}
              className={`w-full rounded-xl border px-3 py-2.5 text-left transition-colors ${item.code === selected ? 'border-[var(--primary)] bg-[var(--primary-lighter)]' : 'border-[var(--border-soft)] bg-white/60 hover:border-[var(--primary)]/40'}`}
            >
              <div className="font-mono text-sm text-[var(--text-primary)]">{item.code}@{item.version}</div>
              <div className="mt-1 flex flex-wrap gap-1">
                <Badge>{item.protocol}</Badge>
                {!item.selectable_for_product && <Badge variant="warning">不可上架</Badge>}
                {!item.has_manifest && <Badge variant="default">无 Manifest</Badge>}
              </div>
            </button>
          ))}
          {!loading && list.length === 0 && <div className="text-sm text-[var(--text-secondary)]">没有已注册的 adapter。</div>}
        </div>

        <div className="space-y-4">
          {detailLoading && <div className="text-sm text-[var(--text-secondary)]">加载中…</div>}

          {current && !current.selectable_for_product && (
            <div className="rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700">
              这是 catalog_discovery 协议的 adapter，用于模型/价格发现源，Product 校验会显式拒绝它——填在 SKU 上会拿到 400。
            </div>
          )}

          {manifest && !manifest.has_manifest && (
            <div className={SECTION}>
              <div className="text-sm text-[var(--text-secondary)]">
                这个 adapter 有 Descriptor 但没有 Manifest：它能正常执行请求，只是还不能用价格表达式计价。
              </div>
            </div>
          )}

          {manifest?.has_manifest && (
            <>
              <div className={SECTION}>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-semibold text-[var(--text-primary)]">{manifest.adapter}@{manifest.adapter_version}</span>
                  <Badge variant="info">{manifest.protocol}</Badge>
                  {manifest.capability && <Badge>{manifest.capability}</Badge>}
                </div>
                <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <div>
                    <div className={LABEL}>下游路径</div>
                    <div className="mt-1 space-y-0.5">
                      {(manifest.downstream_paths || []).map(path => <div key={path} className={CODE}>{path}</div>)}
                    </div>
                  </div>
                  <div>
                    <div className={LABEL}>最低语义版本 / 实现摘要</div>
                    <div className={`mt-1 ${CODE}`}>{manifest.minimum_semantic_version || '-'}</div>
                    <div className={`${CODE} break-all text-[var(--text-secondary)]`}>{manifest.implementation_digest || '-'}</div>
                  </div>
                </div>
                {/* adapter_code 写进 Product 时不带 @1，这是上架时最常踩的一脚。 */}
                <div className="mt-3 text-xs text-[var(--text-secondary)]">
                  上架填 <span className={CODE}>adapter_code = "{manifest.adapter}"</span>（不带 @{manifest.adapter_version}，版本是单独的字段）。
                </div>
              </div>

              {manifest.adapter === 'generic' && (
                <div className="rounded-xl border border-[var(--primary)]/30 bg-[var(--primary-lighter)] px-4 py-3 text-sm text-[var(--text-primary)]">
                  只有 <span className={CODE}>generic</span> 会在运行时反序列化 <span className={CODE}>capability_constraints</span>；
                  其余 adapter 填 <span className={CODE}>{'{}'}</span> 即可，写了也不生效。
                  它的 JSON 同时要承载两套互不重叠的 schema：<span className={CODE}>adapter.*</span> 给运行时读，
                  顶层 <span className={CODE}>resolutions / duration_min / task_types / parameters</span> 给 Playground 读，两套都要给。
                  逐字段说明见 <span className={CODE}>docs/MODEL_ONBOARDING.md</span>。
                </div>
              )}

              {billingVars.length > 0 && (
                <div className={SECTION}>
                  <div className={LABEL}>计费变量（价格表达式里能引用的全部标识符）</div>
                  <div className="mt-2 space-y-1.5">
                    {billingVars.map(variable => (
                      <div key={`${variable.layer}.${variable.name}`} className="flex flex-wrap items-baseline gap-2 text-sm">
                        <span className={CODE}>{variable.name}</span>
                        <Badge variant={variable.layer === 'adapter' ? 'info' : 'default'}>{layerLabel[variable.layer] || variable.layer}</Badge>
                        <span className="text-[var(--text-secondary)]">{variable.desc}</span>
                        {variable.from && <span className="text-xs text-[var(--text-secondary)]">取自 <span className={CODE}>{variable.from}</span></span>}
                        {variable.domain && (
                          <span className="text-xs text-[var(--text-secondary)]">
                            {variable.domain.options?.length
                              ? variable.domain.options.join(' / ')
                              : variable.domain.numbers?.length
                                ? variable.domain.numbers.join(' / ')
                                : `${variable.domain.min || '-'} ~ ${variable.domain.max || '-'}`}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {manifest.billing_funcs?.length ? (
                <div className={SECTION}>
                  <div className={LABEL}>计费函数</div>
                  <div className="mt-1 flex flex-wrap gap-1.5">
                    {manifest.billing_funcs.map(fn => <span key={fn} className={`${CODE} rounded bg-gray-100 px-2 py-0.5`}>{fn}</span>)}
                  </div>
                </div>
              ) : null}

              {(manifest.variants || []).map(variant => <VariantCard key={variant.code} variant={variant} />)}
            </>
          )}
        </div>
      </div>
    </div>
  );
};

export default Adapters;
