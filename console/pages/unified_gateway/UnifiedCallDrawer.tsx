import React, { useEffect, useState } from 'react';
import { LoaderCircle } from 'lucide-react';
import { Drawer, Pagination } from '../../components/ui';
import { fetchUnifiedCallDetail, type UnifiedCallDetail } from '../../services/unifiedGatewayApi';
import { ErrorNotice, errorMessage } from './Feedback';
import { formatDate } from './presentation';
import { StatusBadge } from './UnifiedTable';
import { UnifiedRequestLogs } from './UnifiedRequestLogs';

export const UnifiedCallDrawer: React.FC<{ callId: number | null; onClose: () => void }> = ({ callId, onClose }) => {
  // A new resource gets independent pagination and cancels the previous load.
  return <Drawer open={callId !== null} onClose={onClose} title="调用详情" width="max-w-2xl">{callId !== null && <CallDetail key={callId} callId={callId} />}</Drawer>;
};

const CallDetail: React.FC<{ callId: number }> = ({ callId }) => {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [refresh, setRefresh] = useState(0);
  const [data, setData] = useState<UnifiedCallDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [tab, setTab] = useState<'attempts' | 'requests'>('attempts');
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    fetchUnifiedCallDetail(callId, page, pageSize, controller.signal).then(value => {
      if (!controller.signal.aborted) setData(value);
    }).catch((err: unknown) => {
      if (!controller.signal.aborted) setError(errorMessage(err));
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [callId, page, pageSize, refresh]);

  return <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
    {error ? <div className="p-5"><ErrorNotice message={error} onRetry={() => setRefresh(value => value + 1)} /></div> : <>
      {data && <section className="shrink-0 border-b border-[var(--border-soft)] px-5 py-5">
        <div className="flex flex-wrap items-start gap-3"><code className="min-w-0 flex-1 break-all text-sm text-[var(--text-primary)]">{data.call.public_id}</code><StatusBadge status={data.call.status} /></div>
        <dl className="mt-5 grid grid-cols-2 gap-x-5 gap-y-4 text-sm">
          <Detail label="报价" value={`${data.call.quoted_amount} ${data.call.price_currency}`} />
          <Detail label="创建时间" value={formatDate(data.call.created_at)} />
          <Detail label="目录发布版" value={`#${data.call.catalog_release_id}`} />
          <Detail label="SKU" value={`#${data.call.sku_id}`} />
        </dl>
      </section>}
      {data?.request_logs_available && <div className="flex shrink-0 border-b border-[var(--border-soft)] px-5" role="tablist" aria-label="调用记录视图">{([{ key: 'attempts', label: '执行尝试' }, { key: 'requests', label: '上游请求' }] as const).map(item => <button key={item.key} type="button" role="tab" aria-selected={tab === item.key} onClick={() => setTab(item.key)} className={`border-b-2 px-4 py-3 text-sm font-semibold ${tab === item.key ? 'border-[var(--primary)] text-[var(--primary)]' : 'border-transparent text-[var(--text-secondary)]'}`}>{item.label}</button>)}</div>}
      {tab === 'requests' ? <UnifiedRequestLogs key={callId} callId={callId} /> : <><section className="min-h-0 flex-1 overflow-y-auto px-5 py-5" aria-busy={loading}>
        <h3 className="mb-4 text-sm font-bold text-[var(--text-primary)]">执行尝试</h3>
        {loading ? <div role="status" className="flex min-h-40 items-center justify-center gap-2 text-sm text-[var(--text-secondary)]"><LoaderCircle size={18} className="animate-spin" />正在读取</div> : data?.attempts.items.length ? <ol className="divide-y divide-[var(--border-soft)]">{data.attempts.items.map(attempt => <li key={attempt.id} className="py-4 first:pt-0">
          <div className="flex flex-wrap items-center justify-between gap-2 text-xs"><span className="font-bold text-[var(--text-primary)]">#{attempt.attempt_no}</span><StatusBadge status={attempt.state} /><time className="ml-auto text-[var(--text-secondary)]">{formatDate(attempt.created_at)}</time></div>
          <dl className="mt-3 grid grid-cols-2 gap-x-5 gap-y-3 text-xs"><Detail label="线路" value={`#${attempt.route_id}`} /><Detail label="供应配置" value={`#${attempt.offering_id}`} /><Detail label="凭据" value={`#${attempt.credential_id}`} /><Detail label="凭据版本" value={`#${attempt.credential_version_id}`} /></dl>
        </li>)}</ol> : <div className="py-10 text-center text-sm text-[var(--text-secondary)]">暂无执行尝试</div>}
      </section>
      {data && <Pagination page={page} pageSize={pageSize} total={data.attempts.total} loading={loading} onPageChange={setPage} onPageSizeChange={size => { setPageSize(size); setPage(1); }} />}</>}
    </>}
  </div>;
};
const Detail: React.FC<{ label: string; value: string }> = ({ label, value }) => <div className="min-w-0"><dt className="text-[var(--text-secondary)]">{label}</dt><dd className="mt-1 break-words font-semibold text-[var(--text-primary)]">{value}</dd></div>;
