import React, { useEffect, useRef, useState } from 'react';
import { Archive, Check, LoaderCircle, Plus, Power, X } from 'lucide-react';
import { Button, Pagination } from '../../components/ui';
import { useAppDialog } from '../../components/ui/AppDialogProvider';
import {
  activateUnifiedCurrency,
  fetchUnifiedCurrencies,
  fetchUnifiedRateEvidence,
  reviewUnifiedRateEvidence,
  type UnifiedCurrency,
  type UnifiedRateEvidence,
} from '../../services/unifiedGatewayApi';
import { CurrencyDialog } from './CurrencyDialog';
import { ErrorNotice, errorMessage } from './Feedback';
import { formatDate } from './presentation';
import { RateEvidenceDialog } from './RateEvidenceDialog';
import { StatusBadge } from './UnifiedTable';

export const PricingManager: React.FC<{ refresh: number; readOnly: boolean; onChanged: () => void }> = ({ refresh, readOnly, onChanged }) => {
  const { askConfirmation } = useAppDialog();
  const [tab, setTab] = useState<'evidence' | 'currencies'>('evidence');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [state, setState] = useState('');
  const [evidence, setEvidence] = useState<UnifiedRateEvidence[]>([]);
  const [currencies, setCurrencies] = useState<UnifiedCurrency[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState('');
  const [revision, setRevision] = useState(0);
  const [currencyOpen, setCurrencyOpen] = useState(false);
  const [evidenceOpen, setEvidenceOpen] = useState(false);
  const lock = useRef(false);
  const reload = () => setRevision(value => value + 1);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    const load = async () => {
      if (tab === 'evidence') {
        const [pageData, currencyData] = await Promise.all([
          fetchUnifiedRateEvidence(page, pageSize, state, controller.signal),
          fetchUnifiedCurrencies(1, 100, controller.signal),
        ]);
        if (controller.signal.aborted) return;
        setEvidence(pageData.items);
        setCurrencies(currencyData.items);
        setTotal(pageData.total);
        if (page > Math.max(1, Math.ceil(pageData.total / pageData.page_size))) setPage(Math.max(1, Math.ceil(pageData.total / pageData.page_size)));
      } else {
        const pageData = await fetchUnifiedCurrencies(page, pageSize, controller.signal);
        if (controller.signal.aborted) return;
        setCurrencies(pageData.items);
        setTotal(pageData.total);
        if (page > Math.max(1, Math.ceil(pageData.total / pageData.page_size))) setPage(Math.max(1, Math.ceil(pageData.total / pageData.page_size)));
      }
    };
    void load().catch(reason => {
      if (!controller.signal.aborted) setError(errorMessage(reason));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [tab, page, pageSize, state, refresh, revision]);
  const changed = () => {
    setCurrencyOpen(false);
    setEvidenceOpen(false);
    reload();
    onChanged();
  };
  const review = async (item: UnifiedRateEvidence, decision: 'accepted' | 'rejected' | 'superseded') => {
    if (readOnly || lock.current) return;
    const label = decision === 'accepted' ? '通过' : decision === 'rejected' ? '驳回' : '标记失效';
    if (!await askConfirmation({ title: `${label}价格证据 #${item.id}？`, description: `${item.unit_price} ${item.currency_code}/${item.unit_code}`, confirmLabel: label, tone: decision === 'accepted' ? 'warning' : 'danger' })) return;
    lock.current = true;
    setActionError('');
    try {
      await reviewUnifiedRateEvidence(item.id, { decision, reason_code: decision === 'superseded' ? 'price_superseded' : 'admin_review', expected_version: item.state_version });
      changed();
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      lock.current = false;
    }
  };
  const activate = async (item: UnifiedCurrency) => {
    if (readOnly || lock.current || item.is_settlement) return;
    if (!await askConfirmation({ title: `启用 ${item.currency_code} v${item.definition_version} 作为结算币种？`, description: '仅空账本允许更换结算币种', confirmLabel: '启用', tone: 'warning' })) return;
    lock.current = true;
    setActionError('');
    try {
      await activateUnifiedCurrency(item.currency_code, item.definition_version);
      changed();
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      lock.current = false;
    }
  };
  return <div className="min-w-0 space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex items-center gap-1 border-b border-[var(--border-soft)]">
        {([['evidence', '价格证据'], ['currencies', '结算币种']] as const).map(([key, label]) => <button key={key} type="button" onClick={() => { setTab(key); setPage(1); }} className={`border-b-2 px-4 py-2 text-sm font-semibold ${tab === key ? 'border-[var(--primary)] text-[var(--primary)]' : 'border-transparent text-[var(--text-secondary)]'}`}>{label}</button>)}
      </div>
      <div className="flex items-center gap-2">
        {tab === 'evidence' && <select aria-label="审核状态" value={state} onChange={event => { setState(event.target.value); setPage(1); }} className="h-9 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 text-sm"><option value="">全部状态</option><option value="submitted">待审核</option><option value="accepted">已通过</option><option value="rejected">已驳回</option><option value="superseded">已失效</option></select>}
        <Button size="sm" disabled={readOnly || loading || (tab === 'evidence' && !currencies.some(item => item.status === 'active'))} onClick={() => tab === 'evidence' ? setEvidenceOpen(true) : setCurrencyOpen(true)}><Plus size={15} />{tab === 'evidence' ? '提交证据' : '新建币种'}</Button>
      </div>
    </div>
    {actionError && <ErrorNotice message={actionError} />}
    {error ? <ErrorNotice message={error} onRetry={reload} /> : loading ? <div role="status" className="flex min-h-64 items-center justify-center gap-2 text-sm text-[var(--text-secondary)]"><LoaderCircle size={18} className="animate-spin" />正在读取</div> : tab === 'evidence' ? <EvidenceTable items={evidence} readOnly={readOnly} onReview={(item, decision) => void review(item, decision)} /> : <CurrencyTable items={currencies} readOnly={readOnly} onActivate={item => void activate(item)} />}
    <Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} onPageSizeChange={value => { setPage(1); setPageSize(value); }} />
    {currencyOpen && <CurrencyDialog onClose={() => setCurrencyOpen(false)} onSaved={changed} />}
    {evidenceOpen && <RateEvidenceDialog currencies={currencies} onClose={() => setEvidenceOpen(false)} onSaved={changed} />}
  </div>;
};

const EvidenceTable: React.FC<{ items: UnifiedRateEvidence[]; readOnly: boolean; onReview: (item: UnifiedRateEvidence, decision: 'accepted' | 'rejected' | 'superseded') => void }> = ({ items, readOnly, onReview }) => <div className="overflow-x-auto"><table className="w-full min-w-[860px] text-left text-sm"><TableHead labels={['证据', '来源', '单价', '观测时间', '状态', '审核', '操作']} /><tbody className="divide-y divide-[var(--border-soft)]">{items.length === 0 ? <Empty columns={7} /> : items.map(item => <tr key={item.id} className="hover:bg-[var(--surface-muted)]/50"><td className="px-4 py-3 font-semibold">#{item.id}</td><td className="max-w-72 px-4 py-3"><div className="truncate">{item.source_reference}</div><small className="text-[var(--text-secondary)]">{item.source_type} · {item.authority_level}</small></td><td className="px-4 py-3"><strong className="tabular-nums">{item.unit_price}</strong><small className="block text-[var(--text-secondary)]">{item.currency_code}/{item.unit_code}</small></td><td className="whitespace-nowrap px-4 py-3">{formatDate(item.observed_at)}</td><td className="px-4 py-3"><StatusBadge status={item.state} /></td><td className="px-4 py-3"><span>{item.reason_code}</span><small className="block text-[var(--text-secondary)]">v{item.state_version}</small></td><td className="px-4 py-3"><div className="flex gap-1">{item.state === 'submitted' && <><Icon label="通过" disabled={readOnly} onClick={() => onReview(item, 'accepted')}><Check size={16} /></Icon><Icon label="驳回" disabled={readOnly} onClick={() => onReview(item, 'rejected')} danger><X size={16} /></Icon></>}{item.state === 'accepted' && <Icon label="标记失效" disabled={readOnly} onClick={() => onReview(item, 'superseded')} danger><Archive size={16} /></Icon>}</div></td></tr>)}</tbody></table></div>;
const CurrencyTable: React.FC<{ items: UnifiedCurrency[]; readOnly: boolean; onActivate: (item: UnifiedCurrency) => void }> = ({ items, readOnly, onActivate }) => <div className="overflow-x-auto"><table className="w-full min-w-[700px] text-left text-sm"><TableHead labels={['币种', '版本', '小数位', '舍入', '最大金额', '状态', '操作']} /><tbody className="divide-y divide-[var(--border-soft)]">{items.length === 0 ? <Empty columns={7} /> : items.map(item => <tr key={item.id} className="hover:bg-[var(--surface-muted)]/50"><td className="px-4 py-3 font-bold">{item.currency_code}</td><td className="px-4 py-3">v{item.definition_version}</td><td className="px-4 py-3">{item.fraction_digits}</td><td className="px-4 py-3">{item.rounding_mode}</td><td className="px-4 py-3 tabular-nums">{item.max_amount}</td><td className="px-4 py-3">{item.is_settlement ? <StatusBadge status="active" /> : <StatusBadge status={item.status} />}</td><td className="px-4 py-3"><Icon label="设为结算币种" disabled={readOnly || item.is_settlement} onClick={() => onActivate(item)}><Power size={16} /></Icon></td></tr>)}</tbody></table></div>;
const TableHead: React.FC<{ labels: string[] }> = ({ labels }) => <thead><tr className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)]/50 text-xs text-[var(--text-secondary)]">{labels.map(label => <th key={label} className="whitespace-nowrap px-4 py-3 font-semibold">{label}</th>)}</tr></thead>;
const Empty: React.FC<{ columns: number }> = ({ columns }) => <tr><td colSpan={columns} className="h-64 text-center text-[var(--text-secondary)]">暂无记录</td></tr>;
const Icon: React.FC<{ label: string; disabled?: boolean; danger?: boolean; onClick: () => void; children: React.ReactNode }> = ({ label, disabled, danger, onClick, children }) => <button type="button" title={label} aria-label={label} disabled={disabled} onClick={onClick} className={`grid h-8 w-8 place-items-center rounded-lg text-[var(--text-secondary)] disabled:opacity-30 ${danger ? 'hover:bg-red-50 hover:text-red-500' : 'hover:bg-[var(--surface-tint)] hover:text-[var(--primary)]'}`}>{children}</button>;
