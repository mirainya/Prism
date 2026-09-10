import React, { useEffect, useRef, useState } from 'react';
import { Check, Eye, LoaderCircle, Pause, Play, Plus, RotateCcw, X } from 'lucide-react';
import { Button, Drawer, Modal, Pagination } from '../../components/ui';
import { useAppDialog } from '../../components/ui/AppDialogProvider';
import {
  createUnifiedCatalogSource,
  fetchUnifiedCatalog,
  fetchUnifiedCatalogDiscoveries,
  fetchUnifiedCatalogDiscoveryItems,
  fetchUnifiedCatalogPriceCandidates,
  fetchUnifiedCatalogSourceOptions,
  fetchUnifiedCatalogSources,
  fetchUnifiedCurrencies,
  reviewUnifiedCatalogDiscovery,
  reviewUnifiedCatalogPriceCandidate,
  scheduleUnifiedCatalogDiscovery,
  transitionUnifiedCatalogSource,
  type UnifiedCatalogDiscovery,
  type UnifiedCatalogDiscoveryItem,
  type UnifiedCatalogPriceCandidate,
  type UnifiedCatalogRelease,
  type UnifiedCatalogSource,
  type UnifiedCatalogSourceOptions,
  type UnifiedCurrency,
} from '../../services/unifiedGatewayApi';
import { configurationInputClass as inputClass } from './ConfigurationFields';
import { ErrorNotice, errorMessage } from './Feedback';
import { formatDate } from './presentation';
import { StatusBadge } from './UnifiedTable';

type View = 'sources' | 'discoveries' | 'prices';

export const CatalogSourceManager: React.FC<{
  refresh: number;
  onChanged: () => void;
  readOnly: boolean;
}> = ({ refresh, onChanged, readOnly }) => {
  const { askConfirmation } = useAppDialog();
  const [view, setView] = useState<View>('sources');
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [status, setStatus] = useState('');
  const [releaseId, setReleaseId] = useState(0);
  const [releases, setReleases] = useState<UnifiedCatalogRelease[]>([]);
  const [sources, setSources] = useState<UnifiedCatalogSource[]>([]);
  const [discoveries, setDiscoveries] = useState<UnifiedCatalogDiscovery[]>([]);
  const [prices, setPrices] = useState<UnifiedCatalogPriceCandidate[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState('');
  const [revision, setRevision] = useState(0);
  const [createOpen, setCreateOpen] = useState(false);
  const [snapshot, setSnapshot] = useState<UnifiedCatalogDiscovery | null>(null);
  const [candidate, setCandidate] = useState<UnifiedCatalogPriceCandidate | null>(null);
  const lock = useRef(false);
  const reload = () => setRevision(value => value + 1);

  useEffect(() => {
    const controller = new AbortController();
    void fetchUnifiedCatalog(1, 100, controller.signal).then(result => {
      if (controller.signal.aborted) return;
      const drafts = result.items.filter(item => item.status === 'draft');
      setReleases(drafts);
      setReleaseId(current => drafts.some(item => item.id === current) ? current : drafts[0]?.id ?? 0);
    }).catch(reason => {
      if (!controller.signal.aborted) setActionError(errorMessage(reason));
    });
    return () => controller.abort();
  }, [refresh, revision]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    const load = async () => {
      if (view === 'sources') {
        const result = await fetchUnifiedCatalogSources(page, pageSize, status, controller.signal);
        if (!controller.signal.aborted) setSources(result.items);
        return result;
      }
      if (!releaseId) return { items: [], total: 0, page, page_size: pageSize };
      if (view === 'discoveries') {
        const result = await fetchUnifiedCatalogDiscoveries(releaseId, page, pageSize, controller.signal);
        if (!controller.signal.aborted) setDiscoveries(result.items);
        return result;
      }
      const result = await fetchUnifiedCatalogPriceCandidates(releaseId, page, pageSize, status, controller.signal);
      if (!controller.signal.aborted) setPrices(result.items);
      return result;
    };
    void load().then(result => {
      if (controller.signal.aborted) return;
      const last = Math.max(1, Math.ceil(result.total / result.page_size));
      if (page > last) setPage(last);
      else setTotal(result.total);
    }).catch(reason => {
      if (!controller.signal.aborted) setError(errorMessage(reason));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [view, page, pageSize, status, releaseId, refresh, revision]);

  const changed = () => {
    reload();
    onChanged();
  };
  const selectView = (next: View) => {
    setView(next);
    setPage(1);
    setStatus('');
    setActionError('');
  };
  const runDiscovery = async (source: UnifiedCatalogSource) => {
    if (readOnly || lock.current || !releaseId || source.status !== 'active') return;
    if (!await askConfirmation({ title: `从 ${source.source_code} 读取目录？`, description: `写入草稿目录 #${releaseId}`, confirmLabel: '开始读取', tone: 'warning' })) return;
    lock.current = true;
    setActionError('');
    try {
      await scheduleUnifiedCatalogDiscovery(releaseId, source.id);
      setView('discoveries');
      setPage(1);
      changed();
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      lock.current = false;
    }
  };
  const transition = async (source: UnifiedCatalogSource) => {
    if (readOnly || lock.current || source.status === 'disabled') return;
    const label = source.status === 'active' ? '停止新任务' : '停用来源';
    if (!await askConfirmation({ title: `${label}？`, description: source.source_code, confirmLabel: label, tone: 'warning' })) return;
    lock.current = true;
    setActionError('');
    try {
      await transitionUnifiedCatalogSource(source);
      changed();
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      lock.current = false;
    }
  };
  const reviewSnapshot = async (item: UnifiedCatalogDiscovery, decision: 'accepted' | 'rejected') => {
    if (readOnly || lock.current || !item.snapshot_id || !item.review_version) return;
    const label = decision === 'accepted' ? '通过快照' : '驳回快照';
    if (!await askConfirmation({ title: `${label}？`, description: `${item.source_code} · ${item.item_count ?? 0} 项`, confirmLabel: label, tone: decision === 'accepted' ? 'warning' : 'danger' })) return;
    lock.current = true;
    setActionError('');
    try {
      await reviewUnifiedCatalogDiscovery(item.snapshot_id, { decision, reason_code: decision === 'accepted' ? 'admin_accept' : 'admin_reject', expected_version: item.review_version });
      changed();
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      lock.current = false;
    }
  };
  const retryDiscovery = async (item: UnifiedCatalogDiscovery) => {
    if (readOnly || lock.current || !releaseId || item.state !== 'failed') return;
    if (!await askConfirmation({ title: '重试目录读取？', description: `${item.source_code} · 任务 #${item.id}`, confirmLabel: '重试', tone: 'warning' })) return;
    lock.current = true;
    setActionError('');
    try {
      await scheduleUnifiedCatalogDiscovery(releaseId, item.source_id);
      changed();
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      lock.current = false;
    }
  };
  const dismissCandidate = async (item: UnifiedCatalogPriceCandidate) => {
    if (readOnly || lock.current || item.state !== 'pending') return;
    if (!await askConfirmation({ title: '忽略价格候选？', description: `${item.model_code} · ${item.model_price}`, confirmLabel: '忽略', tone: 'danger' })) return;
    lock.current = true;
    try {
      await reviewUnifiedCatalogPriceCandidate(item.id, { decision: 'dismissed', reason_code: 'admin_dismiss' });
      changed();
    } catch (reason: unknown) {
      setActionError(errorMessage(reason));
    } finally {
      lock.current = false;
    }
  };

  return <div className="min-w-0 space-y-4">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div role="tablist" aria-label="目录来源视图" className="flex items-center gap-1 border-b border-[var(--border-soft)]">
        {([['sources', '来源'], ['discoveries', '发现快照'], ['prices', '价格候选']] as const).map(([key, label]) => <button key={key} type="button" role="tab" aria-selected={view === key} onClick={() => selectView(key)} className={`border-b-2 px-4 py-2 text-sm font-semibold ${view === key ? 'border-[var(--primary)] text-[var(--primary)]' : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>{label}</button>)}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <select aria-label="目录草稿" value={releaseId || ''} onChange={event => { setReleaseId(Number(event.target.value)); setPage(1); }} className="h-9 max-w-52 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 text-sm">
          <option value="">选择目录草稿</option>
          {releases.map(item => <option key={item.id} value={item.id}>#{item.release_no} · {item.semantic_version}</option>)}
        </select>
        {view === 'sources' && <Button size="sm" disabled={readOnly || loading} onClick={() => setCreateOpen(true)}><Plus size={15} />新建来源</Button>}
      </div>
    </div>
    <div className="flex min-h-9 items-center justify-end">
      {view === 'sources' && <select aria-label="来源状态" value={status} onChange={event => { setStatus(event.target.value); setPage(1); }} className="h-9 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 text-sm"><option value="">全部状态</option><option value="active">启用</option><option value="draining">停止分配</option><option value="disabled">停用</option></select>}
      {view === 'prices' && <select aria-label="候选状态" value={status} onChange={event => { setStatus(event.target.value); setPage(1); }} className="h-9 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 text-sm"><option value="">全部状态</option><option value="pending">待确认</option><option value="confirmed">已确认</option><option value="dismissed">已忽略</option></select>}
    </div>
    {actionError && <ErrorNotice message={actionError} />}
    {error ? <ErrorNotice message={error} onRetry={reload} /> : loading ? <Loading /> : view === 'sources' ? <SourceTable items={sources} readOnly={readOnly} hasDraft={Boolean(releaseId)} onDiscover={source => void runDiscovery(source)} onTransition={source => void transition(source)} /> : view === 'discoveries' ? <DiscoveryTable items={discoveries} readOnly={readOnly} onOpen={setSnapshot} onReview={(item, decision) => void reviewSnapshot(item, decision)} onRetry={item => void retryDiscovery(item)} /> : <PriceTable items={prices} readOnly={readOnly} onConfirm={setCandidate} onDismiss={item => void dismissCandidate(item)} />}
    <Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} onPageSizeChange={value => { setPage(1); setPageSize(value); }} />
    {createOpen && <SourceDialog onClose={() => setCreateOpen(false)} onSaved={() => { setCreateOpen(false); changed(); }} />}
    <DiscoveryDrawer discovery={snapshot} onClose={() => setSnapshot(null)} />
    {candidate && <PriceCandidateDialog candidate={candidate} onClose={() => setCandidate(null)} onSaved={() => { setCandidate(null); changed(); }} />}
  </div>;
};

const SourceTable: React.FC<{ items: UnifiedCatalogSource[]; readOnly: boolean; hasDraft: boolean; onDiscover: (item: UnifiedCatalogSource) => void; onTransition: (item: UnifiedCatalogSource) => void }> = ({ items, readOnly, hasDraft, onDiscover, onTransition }) => <Table headers={['来源', '渠道', '凭据', '契约与分组', '地址', '状态', '最近任务', '操作']} empty={items.length === 0}>{items.map(item => <tr key={item.id} className="hover:bg-[var(--surface-muted)]/50"><Cell><strong>{item.source_code}</strong><small>#{item.id}</small></Cell><Cell>{item.channel_name}<small>{item.channel_code}</small></Cell><Cell><code>{item.credential_code}</code></Cell><Cell>{item.contract_code}<small>{item.external_group}</small></Cell><Cell><code title={item.base_url} className="max-w-52 truncate">{item.base_url}</code><small>{item.request_timeout_ms} ms</small></Cell><Cell><StatusBadge status={item.status} /><small>v{item.state_version} · {item.nonterminal_run_count} 运行中</small></Cell><Cell>{item.last_run_id ? `#${item.last_run_id}` : '-'}<small>{item.last_run_state && <StatusBadge status={item.last_run_state} />}{item.last_run_reason ? ` · ${item.last_run_reason}` : ''}</small></Cell><Cell><div className="flex gap-1"><Icon label="读取目录" disabled={readOnly || !hasDraft || item.status !== 'active'} onClick={() => onDiscover(item)}><Play size={16} /></Icon><Icon label={item.status === 'active' ? '停止新任务' : '停用来源'} disabled={readOnly || item.status === 'disabled'} onClick={() => onTransition(item)} danger><Pause size={16} /></Icon></div></Cell></tr>)}</Table>;

const DiscoveryTable: React.FC<{ items: UnifiedCatalogDiscovery[]; readOnly: boolean; onOpen: (item: UnifiedCatalogDiscovery) => void; onReview: (item: UnifiedCatalogDiscovery, decision: 'accepted' | 'rejected') => void; onRetry: (item: UnifiedCatalogDiscovery) => void }> = ({ items, readOnly, onOpen, onReview, onRetry }) => <Table headers={['任务', '来源', '运行状态', '快照', '审核', '观测时间', '操作']} empty={items.length === 0}>{items.map(item => <tr key={item.id} className="hover:bg-[var(--surface-muted)]/50"><Cell><strong>#{item.id}</strong><small>{item.contract_code}</small></Cell><Cell>{item.source_code}<small>{item.external_group}</small></Cell><Cell><StatusBadge status={item.state} /><small>{item.attempt_count} 次 · {item.last_reason_code}</small></Cell><Cell>{item.snapshot_id ? `#${item.snapshot_id}` : '-'}<small>{item.item_count == null ? '' : `${item.item_count} 项`}</small></Cell><Cell>{item.review_state ? <StatusBadge status={item.review_state} /> : '-'}<small>{item.review_version ? `v${item.review_version}` : ''}</small></Cell><Cell>{formatDate(item.observed_at || item.updated_at || item.created_at)}</Cell><Cell><div className="flex gap-1"><Icon label="查看快照" disabled={!item.snapshot_id} onClick={() => onOpen(item)}><Eye size={16} /></Icon>{item.state === 'failed' && <Icon label="重试" disabled={readOnly} onClick={() => onRetry(item)}><RotateCcw size={16} /></Icon>}{item.review_state === 'submitted' && <><Icon label="通过" disabled={readOnly} onClick={() => onReview(item, 'accepted')}><Check size={16} /></Icon><Icon label="驳回" disabled={readOnly} onClick={() => onReview(item, 'rejected')} danger><X size={16} /></Icon></>}</div></Cell></tr>)}</Table>;

const PriceTable: React.FC<{ items: UnifiedCatalogPriceCandidate[]; readOnly: boolean; onConfirm: (item: UnifiedCatalogPriceCandidate) => void; onDismiss: (item: UnifiedCatalogPriceCandidate) => void }> = ({ items, readOnly, onConfirm, onDismiss }) => <Table headers={['模型', '来源', '上游价格', '上游类型', '正式计费', '状态', '观测时间', '操作']} empty={items.length === 0}>{items.map(item => <tr key={item.id} className="hover:bg-[var(--surface-muted)]/50"><Cell><strong>{item.model_code}</strong><small>候选 #{item.id}</small></Cell><Cell>{item.source_code}<small>{item.external_group}</small></Cell><Cell><strong className="tabular-nums">{item.model_price}</strong></Cell><Cell>{item.provider_quota_type ?? '-'}</Cell><Cell>{item.unit_code ? `${item.currency_code}/${item.unit_code}` : '-'}<small>{item.rate_evidence_id ? `证据 #${item.rate_evidence_id}` : ''}</small></Cell><Cell><StatusBadge status={item.state} /></Cell><Cell>{formatDate(item.observed_at)}</Cell><Cell><div className="flex gap-1">{item.state === 'pending' && <><Icon label="确认计费单位" disabled={readOnly} onClick={() => onConfirm(item)}><Check size={16} /></Icon><Icon label="忽略" disabled={readOnly} onClick={() => onDismiss(item)} danger><X size={16} /></Icon></>}</div></Cell></tr>)}</Table>;

const SourceDialog: React.FC<{ onClose: () => void; onSaved: () => void }> = ({ onClose, onSaved }) => {
  const [options, setOptions] = useState<UnifiedCatalogSourceOptions | null>(null);
  const [fields, setFields] = useState({ channel_id: 0, credential_id: 0, source_code: '', contract_code: 'aicost_models_v1' as UnifiedCatalogSource['contract_code'], base_url: 'https://www.aicost.me', external_group: 'default', request_timeout_ms: 30000 });
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const lock = useRef(false);
  useEffect(() => {
    const controller = new AbortController();
    void fetchUnifiedCatalogSourceOptions(controller.signal).then(value => {
      if (controller.signal.aborted) return;
      setOptions(value);
      const channel = value.channels[0];
      const credential = value.credentials.find(item => item.channel_id === channel?.id);
      setFields(current => ({ ...current, channel_id: channel?.id ?? 0, credential_id: credential?.id ?? 0 }));
    }).catch(reason => !controller.signal.aborted && setError(errorMessage(reason)));
    return () => controller.abort();
  }, []);
  const setChannel = (channel_id: number) => setFields(current => ({ ...current, channel_id, credential_id: options?.credentials.find(item => item.channel_id === channel_id)?.id ?? 0 }));
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (lock.current || !fields.channel_id || !fields.credential_id) return;
    lock.current = true;
    setPending(true);
    setError('');
    try {
      await createUnifiedCatalogSource({ ...fields, source_code: fields.source_code.trim().toLowerCase(), base_url: fields.base_url.trim(), external_group: fields.external_group.trim() });
      onSaved();
    } catch (reason: unknown) {
      setError(errorMessage(reason));
    } finally {
      lock.current = false;
      setPending(false);
    }
  };
  const credentials = options?.credentials.filter(item => item.channel_id === fields.channel_id) ?? [];
  return <Modal open title="新建目录来源" onClose={() => !pending && onClose()}><form onSubmit={submit} className="space-y-4">{error && <ErrorNotice message={error} />} {!options ? <Loading compact /> : <fieldset disabled={pending} className="space-y-4"><div className="grid gap-4 sm:grid-cols-2"><Field label="来源标识"><input autoFocus required pattern="[a-z][a-z0-9._-]{0,127}" maxLength={128} className={`${inputClass} font-mono`} value={fields.source_code} onChange={event => setFields({ ...fields, source_code: event.target.value })} /></Field><Field label="契约"><select required className={inputClass} value={fields.contract_code} onChange={event => setFields({ ...fields, contract_code: event.target.value as UnifiedCatalogSource['contract_code'] })}>{options.contracts.map(item => <option key={item.code} value={item.code}>{item.name}</option>)}</select></Field><Field label="渠道"><select required className={inputClass} value={fields.channel_id || ''} onChange={event => setChannel(Number(event.target.value))}>{options.channels.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></Field><Field label="目录凭据"><select required className={inputClass} value={fields.credential_id || ''} onChange={event => setFields({ ...fields, credential_id: Number(event.target.value) })}>{credentials.map(item => <option key={item.id} value={item.id}>{item.code}</option>)}</select></Field></div><Field label="服务地址"><input required type="url" maxLength={2048} className={inputClass} value={fields.base_url} onChange={event => setFields({ ...fields, base_url: event.target.value })} /></Field><div className="grid gap-4 sm:grid-cols-2"><Field label="外部分组"><input required maxLength={128} className={inputClass} value={fields.external_group} onChange={event => setFields({ ...fields, external_group: event.target.value })} /></Field><Field label="超时（毫秒）"><input required type="number" min={1000} max={120000} step={1000} className={inputClass} value={fields.request_timeout_ms} onChange={event => setFields({ ...fields, request_timeout_ms: Number(event.target.value) })} /></Field></div></fieldset>}<div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button><Button type="submit" loading={pending} disabled={!options}><Plus size={15} />创建</Button></div></form></Modal>;
};

const DiscoveryDrawer: React.FC<{ discovery: UnifiedCatalogDiscovery | null; onClose: () => void }> = ({ discovery, onClose }) => {
  const [items, setItems] = useState<UnifiedCatalogDiscoveryItem[]>([]);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => { setPage(1); }, [discovery?.snapshot_id]);
  useEffect(() => {
    if (!discovery?.snapshot_id) return;
    const controller = new AbortController();
    setLoading(true); setError('');
    void fetchUnifiedCatalogDiscoveryItems(discovery.snapshot_id, page, pageSize, controller.signal).then(result => { if (!controller.signal.aborted) { setItems(result.items); setTotal(result.total); } }).catch(reason => !controller.signal.aborted && setError(errorMessage(reason))).finally(() => !controller.signal.aborted && setLoading(false));
    return () => controller.abort();
  }, [discovery?.snapshot_id, page, pageSize]);
  return <Drawer open={Boolean(discovery)} onClose={onClose} width="max-w-6xl" title={discovery?.snapshot_id ? `发现快照 #${discovery.snapshot_id}` : '发现快照'} subtitle={discovery ? `${discovery.source_code} · ${discovery.item_count ?? 0} 项` : undefined}><div className="flex min-h-0 flex-1 flex-col p-5">{error ? <ErrorNotice message={error} /> : loading ? <Loading /> : <div className="min-h-0 flex-1 overflow-auto"><Table headers={['序号', '模型', '说明', '分组', '端点', '价格信息', '选中']} empty={items.length === 0}>{items.map(item => <tr key={item.id} className="hover:bg-[var(--surface-muted)]/50"><Cell>{item.ordinal}</Cell><Cell><strong>{item.model_code}</strong><small>{item.owner_by}</small></Cell><Cell><span className="max-w-64 whitespace-normal">{item.description || '-'}</span><small>{item.tags}</small></Cell><Cell>{item.groups.join(', ') || '-'}</Cell><Cell>{item.endpoint_types.join(', ') || '-'}</Cell><Cell>{item.model_price ?? '-'}<small>类型 {item.provider_quota_type ?? '-'}</small></Cell><Cell>{item.selected_group_enabled ? <StatusBadge status="active" /> : '-'}</Cell></tr>)}</Table></div>}<div className="shrink-0 border-t border-[var(--border-soft)] pt-3"><Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} onPageSizeChange={value => { setPage(1); setPageSize(value); }} /></div></div></Drawer>;
};

const PriceCandidateDialog: React.FC<{ candidate: UnifiedCatalogPriceCandidate; onClose: () => void; onSaved: () => void }> = ({ candidate, onClose, onSaved }) => {
  const [currencies, setCurrencies] = useState<UnifiedCurrency[]>([]);
  const [currency, setCurrency] = useState('');
  const [unit, setUnit] = useState('request');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const lock = useRef(false);
  useEffect(() => {
    const controller = new AbortController();
    void fetchUnifiedCurrencies(1, 100, controller.signal).then(result => { if (controller.signal.aborted) return; const active = result.items.filter(item => item.status === 'active' || item.is_settlement); setCurrencies(active); setCurrency(active[0] ? `${active[0].currency_code}:${active[0].definition_version}` : ''); }).catch(reason => !controller.signal.aborted && setError(errorMessage(reason)));
    return () => controller.abort();
  }, []);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    const [currency_code, version] = currency.split(':');
    if (lock.current || !currency_code || !Number(version)) return;
    lock.current = true; setPending(true); setError('');
    try {
      await reviewUnifiedCatalogPriceCandidate(candidate.id, { decision: 'confirmed', unit_code: unit, currency_code, currency_version: Number(version), reason_code: 'admin_confirm' });
      onSaved();
    } catch (reason: unknown) { setError(errorMessage(reason)); } finally { lock.current = false; setPending(false); }
  };
  return <Modal open title="确认价格候选" onClose={() => !pending && onClose()}><form onSubmit={submit} className="space-y-4">{error && <ErrorNotice message={error} />}<dl className="divide-y divide-[var(--border-soft)] text-sm"><Summary label="模型" value={candidate.model_code} /><Summary label="上游价格" value={candidate.model_price} /><Summary label="来源" value={`${candidate.source_code} · ${candidate.external_group}`} /></dl><div className="grid gap-4 sm:grid-cols-2"><Field label="计费单位"><select className={inputClass} value={unit} onChange={event => setUnit(event.target.value)}><option value="request">每次</option><option value="second">每秒</option></select></Field><Field label="币种"><select required className={inputClass} value={currency} onChange={event => setCurrency(event.target.value)}><option value="">选择币种</option>{currencies.map(item => <option key={item.id} value={`${item.currency_code}:${item.definition_version}`}>{item.currency_code} v{item.definition_version}</option>)}</select></Field></div><div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4"><Button type="button" variant="ghost" disabled={pending} onClick={onClose}>取消</Button><Button type="submit" loading={pending} disabled={!currency}><Check size={15} />确认</Button></div></form></Modal>;
};

const Table: React.FC<{ headers: string[]; empty: boolean; children: React.ReactNode }> = ({ headers, empty, children }) => <div className="overflow-x-auto"><table className="w-full min-w-[880px] text-left text-sm"><thead><tr className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)]/50 text-xs text-[var(--text-secondary)]">{headers.map(label => <th key={label} className="whitespace-nowrap px-4 py-3 font-semibold">{label}</th>)}</tr></thead><tbody className="divide-y divide-[var(--border-soft)]">{empty ? <tr><td colSpan={headers.length} className="h-64 text-center text-[var(--text-secondary)]">暂无记录</td></tr> : children}</tbody></table></div>;
const Cell: React.FC<{ children: React.ReactNode }> = ({ children }) => <td className="px-4 py-3 text-[var(--text-primary)]"><div className="flex min-h-8 flex-col justify-center whitespace-nowrap">{children}</div></td>;
const Icon: React.FC<{ label: string; disabled?: boolean; danger?: boolean; onClick: () => void; children: React.ReactNode }> = ({ label, disabled, danger, onClick, children }) => <button type="button" title={label} aria-label={label} disabled={disabled} onClick={onClick} className={`grid h-8 w-8 shrink-0 place-items-center rounded-lg text-[var(--text-secondary)] disabled:opacity-30 ${danger ? 'hover:bg-red-50 hover:text-red-500' : 'hover:bg-[var(--surface-tint)] hover:text-[var(--primary)]'}`}>{children}</button>;
const Field: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => <label className="block space-y-2 text-sm font-semibold"><span>{label}</span>{children}</label>;
const Summary: React.FC<{ label: string; value: React.ReactNode }> = ({ label, value }) => <div className="flex justify-between gap-4 py-3"><dt className="text-[var(--text-secondary)]">{label}</dt><dd className="break-all text-right font-semibold">{value}</dd></div>;
const Loading: React.FC<{ compact?: boolean }> = ({ compact }) => <div role="status" className={`flex items-center justify-center gap-2 text-sm text-[var(--text-secondary)] ${compact ? 'min-h-24' : 'min-h-64'}`}><LoaderCircle size={18} className="animate-spin" />正在读取</div>;
