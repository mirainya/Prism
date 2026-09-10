import React, { useEffect, useRef, useState } from 'react';
import { Activity, AlertTriangle, CheckCircle2, Database, KeyRound, Layers3, LoaderCircle, Plus, RefreshCw } from 'lucide-react';
import { PageHeader, SummaryStrip } from '../components/shell';
import { Button, Pagination } from '../components/ui';
import { useAppDialog } from '../components/ui/AppDialogProvider';
import {
  activateUnifiedCatalog, activateUnifiedDeployment, fetchUnifiedCalls, fetchUnifiedCatalog,
  fetchUnifiedDeployments, fetchUnifiedGatewayOverview, publishUnifiedCatalog,
  proveCurrentUnifiedDeployment, retireUnifiedCatalog, type UnifiedGatewayOverview,
  type UnifiedCatalogRelease,
} from '../services/unifiedGatewayApi';
import { UnifiedTable, type GatewayRows, type GatewayTab } from './unified_gateway/UnifiedTable';
import { UnifiedCallDrawer } from './unified_gateway/UnifiedCallDrawer';
import { DeploymentDialog } from './unified_gateway/DeploymentDialog';
import { gatewayChecks, gatewayStatus } from './unified_gateway/presentation';
import { ErrorNotice, errorMessage } from './unified_gateway/Feedback';
import { ChannelManager } from './unified_gateway/ChannelManager';
import { CredentialManager } from './unified_gateway/CredentialManager';
import { CatalogDraftDialog } from './unified_gateway/CatalogDraftDialog';
import { CatalogWorkspaceDrawer } from './unified_gateway/CatalogWorkspaceDrawer';
import { PricingManager } from './unified_gateway/PricingManager';
import { CatalogSourceManager } from './unified_gateway/CatalogSourceManager';

const tabs: { key: GatewayTab; label: string }[] = [
  { key: 'overview', label: '运行状态' }, { key: 'channels', label: '渠道与凭据池' }, { key: 'catalog', label: '目录发布版' },
  { key: 'sources', label: '目录来源' }, { key: 'pricing', label: '费率与审核' }, { key: 'credentials', label: '凭据' }, { key: 'calls', label: '调用记录' },
  { key: 'deployments', label: '部署代次' },
];

const UnifiedGateway: React.FC = () => {
  const { askConfirmation } = useAppDialog();
  const [overview, setOverview] = useState<UnifiedGatewayOverview | null>(null);
  const [overviewError, setOverviewError] = useState('');
  const [overviewLoading, setOverviewLoading] = useState(true);
  const [query, setQuery] = useState({ tab: 'overview' as GatewayTab, page: 1, pageSize: 20 });
  const [refresh, setRefresh] = useState(0);
  const [rows, setRows] = useState<GatewayRows | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState('');
  const [pending, setPending] = useState(false);
  const actionLock = useRef(false);
  const [callId, setCallId] = useState<number | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [catalogCreateOpen, setCatalogCreateOpen] = useState(false);
  const [catalogRelease, setCatalogRelease] = useState<UnifiedCatalogRelease | null>(null);
  const reload = () => setRefresh(value => value + 1);

  useEffect(() => {
    const controller = new AbortController();
    setOverviewLoading(true);
    setOverviewError('');
    fetchUnifiedGatewayOverview(controller.signal).then(value => {
      if (!controller.signal.aborted) setOverview(value);
    }).catch((err: unknown) => {
      if (!controller.signal.aborted) setOverviewError(errorMessage(err));
    }).finally(() => {
      if (!controller.signal.aborted) setOverviewLoading(false);
    });
    return () => controller.abort();
  }, [refresh]);

  useEffect(() => {
    const controller = new AbortController();
    setRows(null);
    setError('');
    if (query.tab === 'overview' || query.tab === 'channels' || query.tab === 'credentials' || query.tab === 'sources' || query.tab === 'pricing') { setLoading(false); return () => controller.abort(); }
    setLoading(true);
    const load = async (): Promise<GatewayRows> => {
      const args = [query.page, query.pageSize, controller.signal] as const;
      switch (query.tab) {
        case 'catalog': return { tab: 'catalog', ...await fetchUnifiedCatalog(...args) };
        case 'deployments': return { tab: 'deployments', ...await fetchUnifiedDeployments(...args) };
        default: return { tab: 'calls', ...await fetchUnifiedCalls(...args) };
      }
    };
    load().then(value => {
      if (controller.signal.aborted) return;
      const lastPage = Math.max(1, Math.ceil(value.total / value.page_size));
      if (value.page > lastPage) setQuery(current => ({ ...current, page: lastPage }));
      else setRows(value);
    }).catch((err: unknown) => {
      if (!controller.signal.aborted) setError(errorMessage(err));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [query, refresh]);

  const runAction = async (title: string, action: () => Promise<unknown>) => {
    if (actionLock.current) return;
    if (overview?.runtime_ready === undefined || overviewLoading || overviewError) return;
    actionLock.current = true;
    setPending(true);
    setActionError('');
    try {
      if (!await askConfirmation({ title, description: title, confirmLabel: '确认执行', tone: 'warning' })) return;
      await action();
      reload();
    } catch (err: unknown) {
      setActionError(errorMessage(err));
    } finally {
      actionLock.current = false;
      setPending(false);
    }
  };

  const status = gatewayStatus(overview, Boolean(overviewError));
  const displayedRows = rows?.tab === query.tab ? rows : null;
  const busy = loading || pending;
  const managementAvailable = overview?.runtime_ready !== undefined && !overviewLoading && !overviewError;

  return (
    <div className="min-w-0 space-y-5">
      <PageHeader icon={Layers3} title="统一网关" actions={<>
        {query.tab === 'deployments' && <Button size="sm" disabled={pending || !managementAvailable} onClick={() => setCreateOpen(true)}><Plus size={15} />新建代次</Button>}
        {query.tab === 'catalog' && <Button size="sm" disabled={pending || !managementAvailable} onClick={() => setCatalogCreateOpen(true)}><Plus size={15} />新建草稿</Button>}
        <button type="button" aria-label="刷新" title="刷新" onClick={reload} disabled={loading || overviewLoading || pending} className="grid h-9 w-9 place-items-center rounded-lg border border-[var(--border-soft)] text-[var(--text-secondary)] hover:bg-[var(--surface-muted)] disabled:opacity-40">
          <RefreshCw size={16} className={loading || overviewLoading ? 'animate-spin' : ''} />
        </button>
      </>} />
      {overviewError && <ErrorNotice message={overviewError} onRetry={reload} />}
      {actionError && <ErrorNotice message={actionError} />}
      <div role="status" className={`flex min-h-12 items-center gap-3 border-y px-1 py-3 text-sm ${status.ready ? 'border-emerald-200 text-emerald-600' : 'border-[var(--border-soft)] text-[var(--text-secondary)]'}`}>
        {overviewLoading ? <LoaderCircle size={18} className="shrink-0 animate-spin" /> : status.ready ? <CheckCircle2 size={18} className="shrink-0" /> : <AlertTriangle size={18} className="shrink-0 text-amber-500" />}
        <span className="font-semibold">{overviewLoading ? '正在核对运行状态' : status.label}</span>
        {overview?.runtime.active_release_id && <span className="ml-auto shrink-0 text-xs text-[var(--text-secondary)]">目录 #{overview.runtime.active_release_id}</span>}
      </div>
      <SummaryStrip items={[
        { label: '渠道', value: overview?.target.channels ?? '-', icon: Database, color: '#14b8a6' },
        { label: '公开模型', value: overview?.target.models ?? '-', icon: Layers3, color: '#ec4899' },
        { label: '凭据', value: overview?.target.credentials ?? '-', icon: KeyRound, color: '#eab308' },
        { label: '统一调用', value: overview?.target.calls ?? '-', icon: Activity, color: '#3b82f6' },
      ]} />
      <div role="tablist" aria-label="网关视图" className="flex gap-1 overflow-x-auto border-b border-[var(--border-soft)]">
        {tabs.map(({ key, label }) => <button key={key} id={`gateway-tab-${key}`} type="button" role="tab" aria-selected={query.tab === key} aria-controls="gateway-panel" onClick={() => setQuery(current => ({ ...current, tab: key, page: 1 }))} className={`shrink-0 border-b-2 px-4 py-3 text-sm font-semibold transition-colors ${query.tab === key ? 'border-[var(--primary)] text-[var(--primary)]' : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>{label}</button>)}
      </div>
      <section id="gateway-panel" role="tabpanel" aria-labelledby={`gateway-tab-${query.tab}`} aria-busy={loading} className="min-w-0">
        {query.tab === 'channels' ? <ChannelManager refresh={refresh} onChanged={reload} readOnly={!managementAvailable || !overview?.management_revision} /> : query.tab === 'credentials' ? <CredentialManager refresh={refresh} onChanged={reload} readOnly={!managementAvailable || !overview?.management_revision} /> : query.tab === 'sources' ? <CatalogSourceManager refresh={refresh} onChanged={reload} readOnly={!managementAvailable || (overview?.management_revision ?? 0) < 3} /> : query.tab === 'pricing' ? <PricingManager refresh={refresh} onChanged={reload} readOnly={!managementAvailable || (overview?.management_revision ?? 0) < 2} /> : query.tab === 'overview' ? <div className="grid gap-x-10 gap-y-6 xl:grid-cols-2">
          <section className="min-w-0"><h2 className="mb-3 text-sm font-bold text-[var(--text-primary)]">运行条件</h2><dl className="divide-y divide-[var(--border-soft)]">
            {gatewayChecks(overview).map(check => <div key={check.label} className="flex items-center justify-between gap-4 py-3 text-sm"><dt className="text-[var(--text-secondary)]">{check.label}</dt><dd className="flex min-w-0 items-center gap-2 font-semibold text-[var(--text-primary)]">{check.value}{check.ok ? <CheckCircle2 size={15} className="shrink-0 text-emerald-500" /> : <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-amber-400" />}</dd></div>)}
          </dl></section>
          <section className="min-w-0"><h2 className="mb-3 text-sm font-bold text-[var(--text-primary)]">迁移记录</h2><dl className="divide-y divide-[var(--border-soft)]">
            <OverviewRow label="原渠道" value={overview?.legacy.channels ?? '-'} />
            <OverviewRow label="原能力映射" value={overview?.legacy.abilities ?? '-'} />
            <OverviewRow label="目标线路" value={overview?.target.routes ?? '-'} />
            <OverviewRow label="活动部署" value={overview?.runtime.deployment_id ? `#${overview.runtime.deployment_id}` : '未激活'} />
            <OverviewRow label="当前实例" value={overview?.process ? <code title={`${overview.process.instance_id} · ${overview.process.role}`} className="block max-w-[min(28rem,55vw)] truncate text-xs">{overview.process.instance_id} · {overview.process.role}</code> : '-'} />
            <OverviewRow label="目录状态版本" value={overview?.runtime.release_state_version ?? '-'} />
          </dl></section>
        </div> : <>
          {error ? <ErrorNotice message={error} onRetry={reload} /> : loading || !displayedRows ? <div role="status" className="flex min-h-64 items-center justify-center gap-2 text-sm text-[var(--text-secondary)]"><LoaderCircle size={18} className="animate-spin" />正在读取</div> : <UnifiedTable data={displayedRows} busy={pending || !managementAvailable} activeReleaseId={overview?.runtime.active_release_id} onCall={setCallId}
            onCatalog={setCatalogRelease}
            onPublish={release => void runAction(`发布目录 #${release.release_no}？`, () => publishUnifiedCatalog(release.id))}
            onRetire={release => void runAction(`退役目录 #${release.release_no}？`, () => retireUnifiedCatalog(release.id))}
            canActivate={Boolean(overview?.runtime.deployment_id) && overview?.runtime.deployment_status === 'active'}
            onActivate={release => void runAction(`激活目录 #${release.release_no}？`, async () => {
              await proveCurrentUnifiedDeployment(overview!.runtime.deployment_id, release.id);
              return activateUnifiedCatalog(release.id, overview!.runtime.deployment_id, overview!.runtime.release_state_version);
            })}
            onProveDeployment={deployment => void runAction(`验证当前实例并提交部署 #${deployment.generation_no} 的证明？`, () => proveCurrentUnifiedDeployment(deployment.id, overview?.runtime.active_release_id))}
            onActivateDeployment={deployment => void runAction(`验证并激活部署 #${deployment.generation_no}？`, async () => {
              await proveCurrentUnifiedDeployment(deployment.id, overview?.runtime.active_release_id);
              return activateUnifiedDeployment(deployment.id);
            })} />}
          {!error && <Pagination page={query.page} pageSize={query.pageSize} total={displayedRows?.total ?? 0} loading={busy || !displayedRows} onPageChange={page => setQuery(current => ({ ...current, page }))} onPageSizeChange={pageSize => setQuery(current => ({ ...current, page: 1, pageSize }))} />}
        </>}
      </section>
      <UnifiedCallDrawer callId={callId} onClose={() => setCallId(null)} />
      <DeploymentDialog open={createOpen} nextGeneration={(overview?.runtime.latest_generation_no ?? 0) + 1} onClose={() => setCreateOpen(false)} onCreated={() => { setCreateOpen(false); setQuery(current => ({ ...current, page: 1 })); reload(); }} />
      <CatalogDraftDialog open={catalogCreateOpen} onClose={() => setCatalogCreateOpen(false)} onCreated={() => { setCatalogCreateOpen(false); setQuery(current => ({ ...current, page: 1 })); reload(); }} />
      <CatalogWorkspaceDrawer release={catalogRelease} readOnly={!managementAvailable || (overview?.management_revision ?? 0) < 2} onClose={() => setCatalogRelease(null)} onChanged={reload} />
    </div>
  );
};

const OverviewRow: React.FC<{ label: string; value: React.ReactNode }> = ({ label, value }) => <div className="flex items-center justify-between gap-4 py-3 text-sm"><dt className="text-[var(--text-secondary)]">{label}</dt><dd className="font-semibold text-[var(--text-primary)]">{value}</dd></div>;
export default UnifiedGateway;
