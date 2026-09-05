import React, { useEffect, useState } from 'react';
import { Activity, AlertTriangle, CheckCircle2, Database, KeyRound, Layers3, RefreshCw, ShieldCheck } from 'lucide-react';
import { PageHeader, SummaryStrip } from '../components/shell';
import { Badge, Card } from '../components/ui';
import { fetchUnifiedCallDetail, fetchUnifiedCalls, fetchUnifiedCatalog, fetchUnifiedCredentials, fetchUnifiedGatewayOverview, publishUnifiedCatalog, retireUnifiedCatalog, UnifiedCall, UnifiedCallDetail, UnifiedCatalogRelease, UnifiedCredential, UnifiedGatewayOverview } from '../services/unifiedGatewayApi';

const stateMeta: Record<string, { label: string; tone: 'success' | 'warning' | 'danger'; description: string }> = {
  legacy_runtime: { label: '旧路径运行中', tone: 'warning', description: '线上请求仍由旧网关表和旧写入路径处理。' },
  target_empty: { label: '目标结构未配置', tone: 'danger', description: '规范化目录尚未建立，不能切换流量。' },
  target_configured: { label: '目标结构已配置', tone: 'success', description: '已发现规范化目录和运行指针，可继续检查就绪证明。' },
};

const UnifiedGateway: React.FC = () => {
  const [data, setData] = useState<UnifiedGatewayOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [tab, setTab] = useState<'overview' | 'catalog' | 'credentials' | 'calls'>('overview');
  const [catalog, setCatalog] = useState<UnifiedCatalogRelease[]>([]);
  const [credentials, setCredentials] = useState<UnifiedCredential[]>([]);
  const [calls, setCalls] = useState<UnifiedCall[]>([]);
  const [pageInfo, setPageInfo] = useState({ page: 1, pageSize: 20, total: 0 });
  const [selectedCall, setSelectedCall] = useState<UnifiedCallDetail | null>(null);

  const load = async () => {
    setLoading(true);
    setError('');
    try {
      setData(await fetchUnifiedGatewayOverview());
    } catch (err: any) {
      setError(err?.message || '读取网关状态失败');
    } finally {
      setLoading(false);
    }
  };

  const loadTab = async (nextTab: typeof tab, page = 1) => {
    setTab(nextTab);
    if (nextTab === 'overview') return;
    try {
      if (nextTab === 'catalog') {
        const result = await fetchUnifiedCatalog(page);
        setCatalog(result.items || []);
        setPageInfo({ page: result.page, pageSize: result.page_size, total: result.total });
      } else if (nextTab === 'credentials') {
        const result = await fetchUnifiedCredentials(page);
        setCredentials(result.items || []);
        setPageInfo({ page: result.page, pageSize: result.page_size, total: result.total });
      } else {
        const result = await fetchUnifiedCalls(page);
        setCalls(result.items || []);
        setPageInfo({ page: result.page, pageSize: result.page_size, total: result.total });
      }
    } catch (err: any) {
      setError(err?.message || '读取统一网关数据失败');
    }
  };

  const changeCatalogState = async (release: UnifiedCatalogRelease, action: 'publish' | 'retire') => {
    try {
      if (action === 'publish') await publishUnifiedCatalog(release.id);
      else await retireUnifiedCatalog(release.id);
      await loadTab('catalog', pageInfo.page);
    } catch (err: any) {
      setError(err?.message || '更新目录状态失败');
    }
  };

  useEffect(() => { void load(); }, []);

  const meta = stateMeta[data?.state || 'target_empty'];

  return (
    <div className="space-y-5">
      <PageHeader
        icon={Layers3}
        title="统一网关"
        meta="目录、凭据、执行和运行切换状态"
        actions={(
          <button type="button" onClick={() => void load()} disabled={loading} className="inline-flex items-center gap-2 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm font-semibold text-[var(--text-secondary)] transition hover:text-[var(--text-primary)] disabled:opacity-50">
            <RefreshCw size={15} className={loading ? 'animate-spin' : ''} />刷新
          </button>
        )}
      />

      {error && <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>}

      <div className={`flex items-start gap-3 rounded-lg border px-4 py-3 ${meta.tone === 'success' ? 'border-emerald-200 bg-emerald-50 text-emerald-800' : meta.tone === 'warning' ? 'border-amber-200 bg-amber-50 text-amber-800' : 'border-red-200 bg-red-50 text-red-800'}`}>
        {meta.tone === 'success' ? <CheckCircle2 size={19} className="mt-0.5 shrink-0" /> : <AlertTriangle size={19} className="mt-0.5 shrink-0" />}
        <div><div className="font-bold">{meta.label}</div><div className="mt-0.5 text-sm opacity-85">{meta.description}</div></div>
      </div>

      <SummaryStrip items={[
        { label: '目标渠道', value: data?.target.channels ?? '—', icon: Database, color: '#8b5cf6', note: 'gateway_channels' },
        { label: '目标模型', value: data?.target.models ?? '—', icon: Layers3, color: '#06b6d4', note: 'gw_models' },
        { label: '凭据数量', value: data?.target.credentials ?? '—', icon: KeyRound, color: '#f59e0b', note: '密钥不回显' },
        { label: '统一调用', value: data?.target.calls ?? '—', icon: Activity, color: '#ec4899', note: 'gw_api_calls' },
      ]} />

      <div className="flex flex-wrap gap-2 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-2">
        {([['overview', '状态总览'], ['catalog', '目录发布版'], ['credentials', '凭据池'], ['calls', '统一调用']] as const).map(([key, label]) => (
          <button key={key} type="button" onClick={() => void loadTab(key)} className={`rounded-lg px-3 py-2 text-sm font-bold transition ${tab === key ? '[background:var(--brand-gradient)] text-white shadow-[0_5px_12px_var(--glow-color)]' : 'text-[var(--text-secondary)] hover:bg-[var(--surface-muted)]'}`}>{label}</button>
        ))}
      </div>

      {tab === 'overview' ? <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <SectionTitle icon={Activity} title="运行指针" />
          <div className="grid grid-cols-2 gap-3 text-sm">
            <Info label="活动发布版" value={data?.runtime.active_release_id == null ? '未激活' : `#${data.runtime.active_release_id}`} />
            <Info label="发布版版本" value={data?.runtime.release_state_version ?? '—'} />
            <Info label="部署代次" value={data?.runtime.deployment_id ? `#${data.runtime.deployment_id}` : '未登记'} />
            <Info label="部署状态" value={data?.runtime.deployment_status || '未登记'} />
          </div>
        </Card>
        <Card>
          <SectionTitle icon={ShieldCheck} title="迁移对照" />
          <div className="space-y-3 text-sm">
            <Compare label="旧渠道" value={data?.legacy.channels ?? 0} note="gw_channels" />
            <Compare label="旧能力路由" value={data?.legacy.abilities ?? 0} note="gw_abilities" />
            <Compare label="目录发布版" value={data?.target.catalog_releases ?? 0} note="gw_catalog_releases" />
            <Compare label="旧路径状态" value={data?.legacy.runtime_active ? '仍在使用' : '未发现'} note="运行时审计" />
          </div>
        </Card>
      </div> : <UnifiedTable tab={tab} catalog={catalog} credentials={credentials} calls={calls} pageInfo={pageInfo} onPage={page => void loadTab(tab, page)} onCatalogAction={changeCatalogState} onCallClick={async call => setSelectedCall(await fetchUnifiedCallDetail(call.id))} />}
      {selectedCall && <div className="fixed inset-0 z-50 flex justify-end bg-black/20" onClick={() => setSelectedCall(null)}><aside className="h-full w-full max-w-xl overflow-y-auto bg-[var(--surface-card)] p-6 shadow-2xl" onClick={event => event.stopPropagation()}><div className="mb-5 flex items-center justify-between"><div><div className="text-xs text-[var(--text-secondary)]">统一调用详情</div><h2 className="text-lg font-extrabold text-[var(--text-primary)]">{selectedCall.call.public_id}</h2></div><button type="button" onClick={() => setSelectedCall(null)} className="rounded-md border border-[var(--border-soft)] px-3 py-1.5 text-sm">关闭</button></div><div className="grid grid-cols-2 gap-3 text-sm"><Info label="状态" value={selectedCall.call.status} /><Info label="报价" value={`${selectedCall.call.quoted_amount} ${selectedCall.call.price_currency}`} /><Info label="发布版" value={selectedCall.call.catalog_release_id} /><Info label="SKU" value={selectedCall.call.sku_id} /></div><h3 className="mb-2 mt-6 text-sm font-bold text-[var(--text-primary)]">Attempts</h3><div className="space-y-2">{selectedCall.attempts.items.map(attempt => <div key={attempt.id} className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-muted)] p-3 text-xs"><div className="flex justify-between font-bold"><span>#{attempt.attempt_no} · {attempt.state}</span><span>{formatDate(attempt.created_at)}</span></div><div className="mt-1 text-[var(--text-secondary)]">线路 {attempt.route_id} · Offering {attempt.offering_id} · 凭据版本 {attempt.credential_version_id}</div></div>)}</div></aside></div>}
    </div>
  );
};

type UnifiedTableProps = { tab: 'catalog' | 'credentials' | 'calls'; catalog: UnifiedCatalogRelease[]; credentials: UnifiedCredential[]; calls: UnifiedCall[]; pageInfo: { page: number; pageSize: number; total: number }; onPage: (page: number) => void; onCatalogAction: (release: UnifiedCatalogRelease, action: 'publish' | 'retire') => void; onCallClick: (call: UnifiedCall) => void };
const UnifiedTable: React.FC<UnifiedTableProps> = ({ tab, catalog, credentials, calls, pageInfo, onPage, onCatalogAction, onCallClick }) => {
  const headers = tab === 'catalog' ? ['版本', '状态', '语义版本', '内容摘要', '发布时间', '操作'] : tab === 'credentials' ? ['凭据', '凭据池', '状态', '版本', '并发限制'] : ['调用 ID', '状态', '报价', '交付方式', '创建时间'];
  const rows = tab === 'catalog' ? catalog.map(item => [<span className="font-bold">#{item.release_no}</span>, <Badge variant={item.status === 'published' ? 'success' : item.status === 'draft' ? 'warning' : 'default'}>{item.status}</Badge>, item.semantic_version, <code className="text-xs">{item.content_hash.slice(0, 12)}…</code>, formatDate(item.published_at || item.created_at), item.status === 'draft' ? <button type="button" onClick={() => onCatalogAction(item, 'publish')} className="font-bold text-[var(--primary)] hover:underline">发布</button> : item.status === 'published' ? <button type="button" onClick={() => onCatalogAction(item, 'retire')} className="font-bold text-amber-600 hover:underline">退役</button> : <span className="text-[var(--text-tertiary)]">—</span>]) : tab === 'credentials' ? credentials.map(item => [<span className="font-bold">{item.credential_code}</span>, item.pool_name || item.pool_code || '—', <Badge variant={item.status === 'active' ? 'success' : item.status === 'draining' ? 'warning' : 'default'}>{item.status}</Badge>, item.current_version_id ? `v${item.current_version_id}` : '未激活', `${item.request_limit ?? '∞'} / ${item.task_limit ?? '∞'}`]) : calls.map(item => [<button type="button" onClick={() => onCallClick(item)} className="font-mono text-xs text-[var(--primary)] hover:underline">{item.public_id}</button>, <Badge variant={item.status === 'completed' ? 'success' : item.status === 'failed' ? 'error' : 'info'}>{item.status}</Badge>, `${item.quoted_amount} ${item.price_currency}`, item.delivery_mode, formatDate(item.created_at)]);
  const totalPages = Math.max(1, Math.ceil(pageInfo.total / pageInfo.pageSize));
  return <div className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)]"><div className="overflow-x-auto"><table className="w-full min-w-[720px] text-left text-sm"><thead className="bg-[var(--surface-muted)] text-xs text-[var(--text-secondary)]"><tr>{headers.map(header => <th key={header} className="px-4 py-3 font-bold">{header}</th>)}</tr></thead><tbody className="divide-y divide-[var(--border-soft)]">{rows.length === 0 ? <tr><td colSpan={headers.length} className="px-4 py-12 text-center text-[var(--text-secondary)]">暂无数据</td></tr> : rows.map((row, index) => <tr key={index} className="transition hover:bg-[var(--surface-muted)]">{row.map((cell, cellIndex) => <td key={cellIndex} className="px-4 py-3 text-[var(--text-primary)]">{cell}</td>)}</tr>)}</tbody></table></div><div className="flex items-center justify-between border-t border-[var(--border-soft)] px-4 py-3 text-xs text-[var(--text-secondary)]"><span>共 {pageInfo.total} 条</span><div className="flex items-center gap-2"><button type="button" disabled={pageInfo.page <= 1} onClick={() => onPage(pageInfo.page - 1)} className="rounded-md border border-[var(--border-soft)] px-2.5 py-1.5 disabled:opacity-40">上一页</button><span>{pageInfo.page} / {totalPages}</span><button type="button" disabled={pageInfo.page >= totalPages} onClick={() => onPage(pageInfo.page + 1)} className="rounded-md border border-[var(--border-soft)] px-2.5 py-1.5 disabled:opacity-40">下一页</button></div></div></div>;
};
const formatDate = (value?: string | null) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—';

const Info: React.FC<{ label: string; value: React.ReactNode }> = ({ label, value }) => <div className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-muted)] px-3 py-2"><div className="text-[11px] text-[var(--text-secondary)]">{label}</div><div className="mt-1 font-bold text-[var(--text-primary)]">{value}</div></div>;
const SectionTitle: React.FC<{ icon: React.ComponentType<{ size?: number }>; title: string }> = ({ icon: Icon, title }) => <div className="mb-4 flex items-center gap-2 text-sm font-bold text-[var(--text-primary)]"><span className="flex h-8 w-8 items-center justify-center rounded-lg bg-[var(--surface-tint)] text-[var(--primary)]"><Icon size={16} /></span>{title}</div>;
const Compare: React.FC<{ label: string; value: React.ReactNode; note: string }> = ({ label, value, note }) => <div className="flex items-center justify-between rounded-lg border border-[var(--border-soft)] bg-[var(--surface-muted)] px-3 py-2"><div><div className="font-semibold text-[var(--text-primary)]">{label}</div><div className="text-[11px] text-[var(--text-secondary)]">{note}</div></div><Badge>{value}</Badge></div>;

export default UnifiedGateway;
