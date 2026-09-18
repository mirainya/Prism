import React, { useEffect, useState } from 'react';
import { ChevronDown, ChevronRight, LoaderCircle, RefreshCw } from 'lucide-react';
import { Badge, Pagination } from '../../components/ui';
import {
  fetchUnifiedRequestLogPayloads,
  fetchUnifiedRequestLogs,
  type UnifiedGatewayPage,
  type UnifiedRequestLog,
  type UnifiedRequestLogPayloads,
} from '../../services/unifiedGatewayApi';
import { ErrorNotice, errorMessage } from './Feedback';
import { formatDate, statusLabel } from './presentation';

const actions: Record<string, string> = { submit: '提交', query: '查询', recover: '恢复', cancel: '取消', result_fetch: '下载结果', named_action: '任务操作' };

const StatusBadge: React.FC<{ status: string }> = ({ status }) => (
  <Badge variant={['active', 'completed', 'succeeded', 'response_recorded', 'accepted', 'confirmed'].includes(status) ? 'success' : ['failed', 'rejected'].includes(status) ? 'error' : ['preparing', 'draining', 'submission_unknown', 'manual_review', 'unknown', 'terminated_unknown', 'indeterminate', 'scheduled', 'submitted', 'pending'].includes(status) ? 'warning' : 'default'}>
    {statusLabel(status)}
  </Badge>
);

export const UnifiedRequestLogs: React.FC<{ callId: number }> = ({ callId }) => {
  const [query, setQuery] = useState({ page: 1, pageSize: 20 });
  const [data, setData] = useState<UnifiedGatewayPage<UnifiedRequestLog> | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    fetchUnifiedRequestLogs(callId, query.page, query.pageSize, controller.signal).then(value => {
      if (controller.signal.aborted) return;
      const last = Math.max(1, Math.ceil(value.total / value.page_size));
      if (value.page > last) setQuery(current => ({ ...current, page: last }));
      else setData(value);
    }).catch((err: unknown) => {
      if (!controller.signal.aborted) setError(errorMessage(err));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [callId, query, refresh]);

  return <section aria-label="上游请求" aria-busy={loading} className="flex min-h-0 flex-1 flex-col">
    <div className="flex shrink-0 items-center justify-between px-5 py-3">
      <h3 className="text-sm font-bold text-[var(--text-primary)]">上游请求<span className="ml-2 text-xs font-normal text-[var(--text-secondary)]">{data?.total ?? '-'}</span></h3>
      <button type="button" title="刷新请求" aria-label="刷新请求" disabled={loading} onClick={() => setRefresh(value => value + 1)} className="grid h-8 w-8 place-items-center rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-muted)] disabled:opacity-40"><RefreshCw size={15} /></button>
    </div>
    <div className="min-h-0 flex-1 overflow-y-auto">{error ? <div className="p-5"><ErrorNotice message={error} onRetry={() => setRefresh(value => value + 1)} /></div> : loading ? <div role="status" className="flex min-h-48 items-center justify-center gap-2 text-sm text-[var(--text-secondary)]"><LoaderCircle size={18} className="animate-spin" />正在读取</div> : data?.items.length ? <ol className="divide-y divide-[var(--border-soft)] px-5">{data.items.map(item => <RequestLogItem key={item.id} callId={callId} item={item} />)}</ol> : <div className="py-12 text-center text-sm text-[var(--text-secondary)]">暂无上游请求</div>}</div>
    {!error && <Pagination className="shrink-0" page={query.page} pageSize={query.pageSize} total={data?.total ?? 0} loading={loading || !data} onPageChange={page => setQuery(current => ({ ...current, page }))} onPageSizeChange={pageSize => setQuery({ page: 1, pageSize })} />}
  </section>;
};

const RequestLogItem: React.FC<{ callId: number; item: UnifiedRequestLog }> = ({ callId, item }) => {
  const available = item.request_payload_available || item.response_payload_available;
  const [open, setOpen] = useState(false);
  const [reload, setReload] = useState(0);
  const [payloads, setPayloads] = useState<UnifiedRequestLogPayloads | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    if (!open || !available || payloads) return;
    const controller = new AbortController();
    setLoading(true);
    setError('');
    fetchUnifiedRequestLogPayloads(callId, item.id, controller.signal).then(value => {
      if (!controller.signal.aborted) setPayloads(value);
    }).catch((err: unknown) => {
      if (!controller.signal.aborted) setError(errorMessage(err));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [available, callId, item.id, open, payloads, reload]);
  return <li className="py-5">
    <div className="flex flex-wrap items-center gap-2 text-xs"><span className="font-bold text-[var(--text-primary)]">{actions[item.action] ?? item.action} #{item.request_seq}</span><StatusBadge status={item.status} /><span className="ml-auto font-mono text-[var(--text-secondary)]">{item.http_status === null ? '-' : `HTTP ${item.http_status}`}</span></div>
    <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-xs">
      <Field label="发起时间" value={formatDate(item.created_at)} /><Field label="耗时" value={item.duration_ms === null ? '-' : `${item.duration_ms} ms`} />
      <Field label="执行尝试" value={`#${item.attempt_no}`} /><Field label="当前任务状态" value={item.async_state ? statusLabel(item.async_state) : '-'} />
      <Field label="请求发送" value={item.request_complete ? '完整' : '未确认完整'} /><Field label="响应接收" value={item.response_complete ? '完整' : '未确认完整'} />
    </dl>
    {item.error_code && <code className="mt-3 block break-all border-l-2 border-rose-300 pl-3 text-xs text-rose-600">{item.error_code}</code>}
    {available && <button type="button" aria-expanded={open} onClick={() => setOpen(value => !value)} className="mt-4 flex items-center gap-1.5 text-xs font-semibold text-[var(--primary)] hover:underline">{open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}实际 HTTP 正文</button>}
    {open && <div className="mt-3 space-y-4">{loading ? <div className="flex items-center gap-2 py-4 text-xs text-[var(--text-secondary)]"><LoaderCircle size={14} className="animate-spin" />正在解密</div> : error ? <ErrorNotice message={error} onRetry={() => setReload(value => value + 1)} /> : payloads ? <><PayloadPanel label="上游请求" value={payloads.request_body} /><PayloadPanel label="上游响应" value={payloads.response_body} /></> : null}</div>}
  </li>;
};

const Field: React.FC<{ label: string; value: string }> = ({ label, value }) => <div className="min-w-0"><dt className="text-[var(--text-secondary)]">{label}</dt><dd className="mt-1 break-words font-semibold text-[var(--text-primary)]">{value}</dd></div>;

const PayloadPanel: React.FC<{ label: string; value: string | null }> = ({ label, value }) => <section><h4 className="mb-2 text-xs font-bold text-[var(--text-primary)]">{label}</h4><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-lg border border-[var(--border-soft)] bg-[var(--surface-muted)] p-3 font-mono text-xs leading-5 text-[var(--text-primary)]">{formatPayload(value)}</pre></section>;

const formatPayload = (value: string | null) => {
  if (!value) return '未保存正文';
  try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; }
};
