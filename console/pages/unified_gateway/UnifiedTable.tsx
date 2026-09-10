import React from 'react';
import { Archive, Eye, Play, Settings2, ShieldCheck, Upload } from 'lucide-react';
import { Badge } from '../../components/ui';
import type { UnifiedCall, UnifiedCatalogRelease, UnifiedCredential, UnifiedDeployment, UnifiedGatewayPage } from '../../services/unifiedGatewayApi';
import { formatDate, statusLabel } from './presentation';

export type GatewayTab = 'overview' | 'channels' | 'catalog' | 'sources' | 'pricing' | 'credentials' | 'calls' | 'deployments';
export type GatewayRows =
  | ({ tab: 'catalog' } & UnifiedGatewayPage<UnifiedCatalogRelease>)
  | ({ tab: 'credentials' } & UnifiedGatewayPage<UnifiedCredential>)
  | ({ tab: 'calls' } & UnifiedGatewayPage<UnifiedCall>)
  | ({ tab: 'deployments' } & UnifiedGatewayPage<UnifiedDeployment>);
interface Props {
  data: GatewayRows;
  busy: boolean;
  activeReleaseId?: number | null;
  canActivate: boolean;
  onPublish: (release: UnifiedCatalogRelease) => void;
  onRetire: (release: UnifiedCatalogRelease) => void;
  onActivate: (release: UnifiedCatalogRelease) => void;
  onActivateDeployment: (deployment: UnifiedDeployment) => void;
  onProveDeployment: (deployment: UnifiedDeployment) => void;
  onCall: (id: number) => void;
  onCatalog: (release: UnifiedCatalogRelease) => void;
}

export const StatusBadge: React.FC<{ status: string }> = ({ status }) => <Badge variant={['published', 'active', 'completed', 'succeeded', 'response_recorded', 'accepted', 'confirmed'].includes(status) ? 'success' : ['failed', 'rejected'].includes(status) ? 'error' : ['draft', 'preparing', 'draining', 'submission_unknown', 'manual_review', 'unknown', 'terminated_unknown', 'indeterminate', 'scheduled', 'submitted', 'pending'].includes(status) ? 'warning' : 'default'}>{statusLabel(status)}</Badge>;

export const UnifiedTable: React.FC<Props> = ({ data, busy, activeReleaseId, canActivate, onPublish, onRetire, onActivate, onActivateDeployment, onProveDeployment, onCall, onCatalog }) => {
  const headers = data.tab === 'catalog' ? ['发布版', '状态', '语义版本', '内容摘要', '发布时间', '操作']
    : data.tab === 'credentials' ? ['凭据', '凭据池', '状态', '版本', '请求并发', '任务并发']
    : data.tab === 'deployments' ? ['代次', '状态', '语义版本', '成员', '当前实例', '创建时间', '操作']
    : ['调用 ID', '状态', '报价', '交付方式', '创建时间', '操作'];
  const rows: { id: number; cells: React.ReactNode[] }[] = data.tab === 'catalog' ? data.items.map(item => ({ id: item.id, cells: [
    <strong>#{item.release_no}</strong>, <StatusBadge status={item.status} />, item.semantic_version,
    <code title={item.content_hash} className="text-xs">{item.content_hash.slice(0, 12)}</code>, formatDate(item.published_at || item.created_at),
    <div className="flex items-center gap-1">
      <Action title={item.status === 'draft' ? '配置目录' : '查看目录'} disabled={busy} onClick={() => onCatalog(item)}><Settings2 size={16} /></Action>
      {item.id === activeReleaseId ? <Badge variant="success">活动目录</Badge> : <>
        {item.status === 'draft' && <Action title="发布目录" disabled={busy} onClick={() => onPublish(item)}><Upload size={16} /></Action>}
        {item.status === 'published' && <Action title={canActivate ? '激活目录' : '尚无活动部署'} disabled={busy || !canActivate} onClick={() => onActivate(item)}><Play size={16} /></Action>}
        {['draft', 'published'].includes(item.status) && <Action title="退役目录" disabled={busy} onClick={() => onRetire(item)}><Archive size={16} /></Action>}
      </>}
    </div>,
  ] })) : data.tab === 'credentials' ? data.items.map(item => ({ id: item.id, cells: [
    <span className="font-semibold">{item.credential_code}</span>, item.pool_name || item.pool_code || '-', <StatusBadge status={item.status} />,
    item.current_version_id ? `#${item.current_version_id}` : '未激活', item.request_limit ?? '不限', item.task_limit ?? '不限',
  ] })) : data.tab === 'deployments' ? data.items.map(item => ({ id: item.id, cells: [
    <strong>#{item.generation_no}</strong>, <StatusBadge status={item.status} />, item.semantic_version, item.member_count,
    item.current_member_id ? <span className="font-mono text-xs">#{item.current_member_id}</span> : <span className="text-[var(--text-secondary)]">未登记</span>, formatDate(item.created_at),
    ['preparing', 'active'].includes(item.status) ? <div className="flex items-center gap-1">
      <Action title="验证当前实例" disabled={busy} onClick={() => onProveDeployment(item)}><ShieldCheck size={16} /></Action>
      {item.status === 'preparing' && <Action title="验证并激活部署" disabled={busy} onClick={() => onActivateDeployment(item)}><Play size={16} /></Action>}
    </div> : '-',
  ] })) : data.items.map(item => ({ id: item.id, cells: [
    <button type="button" onClick={() => onCall(item.id)} className="max-w-80 whitespace-normal break-all text-left font-mono text-xs text-[var(--primary)] hover:underline">{item.public_id}</button>,
    <StatusBadge status={item.status} />, <span className="tabular-nums">{item.quoted_amount} <span className="text-xs text-[var(--text-secondary)]">{item.price_currency}</span></span>,
    item.delivery_mode === 'managed_copy' ? '托管' : item.delivery_mode === 'reference' ? '原始引用' : item.delivery_mode, formatDate(item.created_at),
    <Action title="查看调用" onClick={() => onCall(item.id)}><Eye size={16} /></Action>,
  ] }));
  return <div className="overflow-x-auto">
    <table className="w-full min-w-[720px] text-left text-sm">
      <thead><tr className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)]/50 text-xs text-[var(--text-secondary)]">{headers.map(header => <th key={header} className="whitespace-nowrap px-4 py-3 font-semibold">{header}</th>)}</tr></thead>
      <tbody className="divide-y divide-[var(--border-soft)]">{rows.length === 0 ? <tr><td colSpan={headers.length} className="h-64 text-center text-sm text-[var(--text-secondary)]">暂无记录</td></tr> : rows.map(row => <tr key={row.id} className="transition-colors hover:bg-[var(--surface-muted)]/50">{row.cells.map((cell, index) => <td key={index} className="px-4 py-3 text-[var(--text-primary)]"><div className="flex min-h-8 items-center whitespace-nowrap">{cell}</div></td>)}</tr>)}</tbody>
    </table>
  </div>;
};
const Action: React.FC<{ title: string; disabled?: boolean; onClick: () => void; children: React.ReactNode }> = ({ title, disabled, onClick, children }) => <button type="button" title={title} aria-label={title} disabled={disabled} onClick={onClick} className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-tint)] hover:text-[var(--primary)] disabled:cursor-not-allowed disabled:opacity-35">{children}</button>;
