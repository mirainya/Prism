import React, { useEffect, useRef, useState } from 'react';
import { CircleDollarSign, LoaderCircle, Plus, ShieldCheck, Trash2 } from 'lucide-react';
import { Button, Drawer, Pagination } from '../../components/ui';
import { useAppDialog } from '../../components/ui/AppDialogProvider';
import {
  deleteUnifiedCatalogProduct,
  deleteUnifiedCatalogRate,
  deleteUnifiedCatalogSKU,
  fetchUnifiedCatalogOptions,
  fetchUnifiedCatalogProducts,
  fetchUnifiedCatalogRates,
  fetchUnifiedCatalogRelease,
  fetchUnifiedCatalogSKUs,
  fetchUnifiedRateEvidence,
  type UnifiedCatalogOptions,
  type UnifiedCatalogProduct,
  type UnifiedCatalogRate,
  type UnifiedCatalogRelease,
  type UnifiedCatalogSKU,
  type UnifiedRateEvidence,
} from '../../services/unifiedGatewayApi';
import { CatalogProductDialog } from './CatalogProductDialog';
import { CatalogRateDialog } from './CatalogRateDialog';
import { CatalogSKUDialog } from './CatalogSKUDialog';
import { ErrorNotice, errorMessage } from './Feedback';
import { OfferingValidationDialog } from './OfferingValidationDialog';
import { StatusBadge } from './UnifiedTable';

type WorkspaceTab = 'skus' | 'products' | 'sell' | 'cost';
type RateTarget = { kind: 'sell'; sku: UnifiedCatalogSKU } | { kind: 'cost'; product: UnifiedCatalogProduct };

export const CatalogWorkspaceDrawer: React.FC<{
  release: UnifiedCatalogRelease | null;
  readOnly: boolean;
  onClose: () => void;
  onChanged: () => void;
}> = ({ release, readOnly, onClose, onChanged }) => {
  const { askConfirmation } = useAppDialog();
  const [current, setCurrent] = useState<UnifiedCatalogRelease | null>(release);
  const [tab, setTab] = useState<WorkspaceTab>('skus');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [items, setItems] = useState<(UnifiedCatalogSKU | UnifiedCatalogProduct | UnifiedCatalogRate)[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState('');
  const [revision, setRevision] = useState(0);
  const [skuOpen, setSKUOpen] = useState(false);
  const [productOptions, setProductOptions] = useState<UnifiedCatalogOptions | null>(null);
  const [rateTarget, setRateTarget] = useState<RateTarget | null>(null);
  const [validationTarget, setValidationTarget] = useState<UnifiedCatalogProduct | null>(null);
  const [evidence, setEvidence] = useState<UnifiedRateEvidence[]>([]);
  const actionLock = useRef(false);
  const editable = !readOnly && current?.status === 'draft';
  const reload = () => setRevision(value => value + 1);

  useEffect(() => {
    setCurrent(release);
    setTab('skus');
    setPage(1);
    setRevision(value => value + 1);
  }, [release?.id]);

  useEffect(() => {
    if (!release) return;
    const controller = new AbortController();
    setLoading(true);
    setError('');
    const load = async () => {
      const detail = await fetchUnifiedCatalogRelease(release.id, controller.signal);
      const result = tab === 'skus'
        ? await fetchUnifiedCatalogSKUs(release.id, page, pageSize, controller.signal)
        : tab === 'products'
          ? await fetchUnifiedCatalogProducts(release.id, page, pageSize, controller.signal)
          : await fetchUnifiedCatalogRates(release.id, tab, page, pageSize, controller.signal);
      if (controller.signal.aborted) return;
      const last = Math.max(1, Math.ceil(result.total / result.page_size));
      if (page > last) {
        setPage(last);
        return;
      }
      setCurrent(detail);
      setItems(result.items);
      setTotal(result.total);
    };
    void load().catch(reason => {
      if (!controller.signal.aborted) setError(errorMessage(reason));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [release?.id, tab, page, pageSize, revision]);

  const changed = () => {
    setSKUOpen(false);
    setProductOptions(null);
    setRateTarget(null);
    reload();
    onChanged();
  };
  const openProduct = async () => {
    if (!current || actionLock.current) return;
    actionLock.current = true;
    setActionError('');
    try {
      const value = await fetchUnifiedCatalogOptions(current.id);
      if (!value.channels.length || !value.skus.length || !value.adapters.length) throw new Error('请先配置渠道、凭据池和公开规格');
      setProductOptions(value);
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      actionLock.current = false;
    }
  };
  const openRate = async (target: RateTarget) => {
    if (actionLock.current) return;
    actionLock.current = true;
    setActionError('');
    try {
      const result = await fetchUnifiedRateEvidence(1, 100, 'accepted');
      const unit = target.kind === 'sell' ? undefined : undefined;
      void unit;
      if (!result.items.length) throw new Error('请先提交并通过价格证据审核');
      setEvidence(result.items);
      setRateTarget(target);
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      actionLock.current = false;
    }
  };
  const remove = async (kind: 'sku' | 'product' | 'sell' | 'cost', id: number, label: string) => {
    if (!current || !editable || actionLock.current) return;
    if (!await askConfirmation({ title: `删除${label}？`, description: label, confirmLabel: '删除', tone: 'danger' })) return;
    actionLock.current = true;
    setActionError('');
    try {
      if (kind === 'sku') await deleteUnifiedCatalogSKU(current.id, id, current.config_version);
      else if (kind === 'product') await deleteUnifiedCatalogProduct(current.id, id, current.config_version);
      else await deleteUnifiedCatalogRate(current.id, kind, id, current.config_version);
      changed();
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      actionLock.current = false;
    }
  };

  return <Drawer open={Boolean(release)} onClose={onClose} width="max-w-6xl" title={current ? `目录 #${current.release_no}` : '目录配置'} subtitle={current ? `${current.semantic_version} · 配置版本 ${current.config_version}` : undefined} headerActions={current && <StatusBadge status={current.status} />}>
    <div className="flex min-h-0 flex-1 flex-col">
      <div role="tablist" aria-label="目录配置" className="flex shrink-0 overflow-x-auto border-b border-[var(--border-soft)] px-4">
        {([['skus', '公开规格'], ['products', '上游产品'], ['sell', '销售费率'], ['cost', '成本费率']] as const).map(([key, label]) => <button key={key} type="button" role="tab" aria-selected={tab === key} onClick={() => { setTab(key); setPage(1); }} className={`shrink-0 border-b-2 px-4 py-3 text-sm font-semibold ${tab === key ? 'border-[var(--primary)] text-[var(--primary)]' : 'border-transparent text-[var(--text-secondary)]'}`}>{label}</button>)}
      </div>
      <div className="flex shrink-0 items-center justify-end gap-2 px-5 py-3">
        {tab === 'skus' && <Button size="sm" disabled={!editable || loading} onClick={() => setSKUOpen(true)}><Plus size={15} />新建规格</Button>}
        {tab === 'products' && <Button size="sm" disabled={!editable || loading} onClick={() => void openProduct()}><Plus size={15} />新建产品</Button>}
      </div>
      {actionError && <div className="px-5 pb-3"><ErrorNotice message={actionError} /></div>}
      <div className="min-h-0 flex-1 overflow-auto px-5">
        {error ? <ErrorNotice message={error} onRetry={reload} /> : loading ? <div role="status" className="flex min-h-64 items-center justify-center gap-2 text-sm text-[var(--text-secondary)]"><LoaderCircle size={18} className="animate-spin" />正在读取</div> : <WorkspaceTable tab={tab} items={items} editable={editable} canValidate={!readOnly && current?.status === 'published'} onValidate={setValidationTarget} onRate={target => void openRate(target)} onDelete={(kind, id, label) => void remove(kind, id, label)} />}
      </div>
      <div className="shrink-0 border-t border-[var(--border-soft)] px-5 py-3"><Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} onPageSizeChange={value => { setPage(1); setPageSize(value); }} /></div>
    </div>
    {skuOpen && current && <CatalogSKUDialog release={current} onClose={() => setSKUOpen(false)} onSaved={changed} />}
    {productOptions && current && <CatalogProductDialog release={current} options={productOptions} onClose={() => setProductOptions(null)} onSaved={changed} />}
    {rateTarget && current && <CatalogRateDialog release={current} kind={rateTarget.kind} skus={rateTarget.kind === 'sell' ? [rateTarget.sku] : []} products={rateTarget.kind === 'cost' ? [rateTarget.product] : []} evidence={evidence} onClose={() => setRateTarget(null)} onSaved={changed} />}
    {validationTarget && <OfferingValidationDialog product={validationTarget} onClose={() => setValidationTarget(null)} onSaved={() => { setValidationTarget(null); changed(); }} />}
  </Drawer>;
};

const WorkspaceTable: React.FC<{
  tab: WorkspaceTab;
  items: (UnifiedCatalogSKU | UnifiedCatalogProduct | UnifiedCatalogRate)[];
  editable: boolean;
  canValidate: boolean;
  onValidate: (product: UnifiedCatalogProduct) => void;
  onRate: (target: RateTarget) => void;
  onDelete: (kind: 'sku' | 'product' | 'sell' | 'cost', id: number, label: string) => void;
}> = ({ tab, items, editable, canValidate, onValidate, onRate, onDelete }) => {
  const headers = tab === 'skus' ? ['规格', '公开模型', '操作', '交付', '服务等级', '费率', '线路', ''] : tab === 'products' ? ['产品', '渠道与凭据池', '传输', '任务策略', '验证', '线路', '成本', ''] : ['组件', '计费对象', '单价', '计量来源', '事件', '上限', '证据', ''];
  return <table className="w-full min-w-[920px] text-left text-sm"><thead><tr className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)]/50 text-xs text-[var(--text-secondary)]">{headers.map(label => <th key={label} className="whitespace-nowrap px-3 py-3 font-semibold">{label}</th>)}</tr></thead><tbody className="divide-y divide-[var(--border-soft)]">{items.length === 0 ? <tr><td colSpan={headers.length} className="h-56 text-center text-[var(--text-secondary)]">暂无配置</td></tr> : tab === 'skus' ? (items as UnifiedCatalogSKU[]).map(item => <tr key={item.id} className="hover:bg-[var(--surface-muted)]/50"><Cell><code>{item.sku_code}</code></Cell><Cell><strong>{item.display_name}</strong><small>{item.api_name}</small></Cell><Cell>{item.operation_code}</Cell><Cell>{item.delivery_mode === 'managed_copy' ? '托管副本' : '原始引用'}</Cell><Cell>{item.service_tiers.join(', ')}</Cell><Cell>{item.sell_rate_count}</Cell><Cell>{item.route_count}</Cell><Cell><Actions editable={editable} onRate={() => onRate({ kind: 'sell', sku: item })} onDelete={() => onDelete('sku', item.id, item.sku_code)} /></Cell></tr>) : tab === 'products' ? (items as UnifiedCatalogProduct[]).map(item => <tr key={item.id} className="hover:bg-[var(--surface-muted)]/50"><Cell><strong>{item.product_code}</strong><small>{item.vendor_model}</small></Cell><Cell>{item.channel_name}<small>{item.pool_name}</small></Cell><Cell><code>{item.adapter_code}@{item.adapter_version}</code><small>{item.request_path}</small></Cell><Cell>{item.task_scope}<small>{item.cancel_mode}</small></Cell><Cell><StatusBadge status={item.commercial_state} /><small>{item.entitled_credential_count} 个有效凭据</small></Cell><Cell>{item.route_count}</Cell><Cell>{item.cost_rate_count}</Cell><Cell><ProductActions editable={editable} canValidate={canValidate} onValidate={() => onValidate(item)} onRate={() => onRate({ kind: 'cost', product: item })} onDelete={() => onDelete('product', item.id, item.product_code)} /></Cell></tr>) : (items as UnifiedCatalogRate[]).map(item => <tr key={item.id} className="hover:bg-[var(--surface-muted)]/50"><Cell><code>{item.component_code}</code></Cell><Cell>{item.parent_label}</Cell><Cell><strong className="tabular-nums">{item.unit_price}</strong><small>{item.currency_code}/{item.unit_code}</small></Cell><Cell>{item.quantity_source}<small>10^{item.unit_scale}</small></Cell><Cell>{item.charge_event}</Cell><Cell>{item.max_quantity}</Cell><Cell>#{item.evidence_id}<small>{item.evidence_state}</small></Cell><Cell><button type="button" title="删除费率" aria-label="删除费率" disabled={!editable} onClick={() => onDelete(tab, item.id, item.component_code)} className="grid h-8 w-8 place-items-center rounded-lg text-[var(--text-secondary)] hover:bg-red-50 hover:text-red-500 disabled:opacity-30"><Trash2 size={16} /></button></Cell></tr>)}</tbody></table>;
};

const Cell: React.FC<{ children: React.ReactNode }> = ({ children }) => <td className="px-3 py-3 text-[var(--text-primary)]"><div className="flex min-h-8 flex-col justify-center whitespace-nowrap">{children}</div></td>;
const Actions: React.FC<{ editable: boolean; onRate: () => void; onDelete: () => void }> = ({ editable, onRate, onDelete }) => <div className="flex gap-1"><button type="button" title="添加费率" aria-label="添加费率" disabled={!editable} onClick={onRate} className="grid h-8 w-8 place-items-center rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-tint)] hover:text-[var(--primary)] disabled:opacity-30"><CircleDollarSign size={16} /></button><button type="button" title="删除" aria-label="删除" disabled={!editable} onClick={onDelete} className="grid h-8 w-8 place-items-center rounded-lg text-[var(--text-secondary)] hover:bg-red-50 hover:text-red-500 disabled:opacity-30"><Trash2 size={16} /></button></div>;
const ProductActions: React.FC<{ editable: boolean; canValidate: boolean; onValidate: () => void; onRate: () => void; onDelete: () => void }> = ({ editable, canValidate, onValidate, onRate, onDelete }) => <div className="flex gap-1"><button type="button" title="验证权益与商业信息" aria-label="验证权益与商业信息" disabled={!canValidate} onClick={onValidate} className="grid h-8 w-8 place-items-center rounded-lg text-[var(--text-secondary)] hover:bg-emerald-50 hover:text-emerald-600 disabled:opacity-30"><ShieldCheck size={16} /></button><Actions editable={editable} onRate={onRate} onDelete={onDelete} /></div>;
