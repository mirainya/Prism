import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { RefreshCw, ShieldAlert, Trash2, Unplug } from 'lucide-react';
import {
  UnifiedRouteState,
  clearUnifiedCredentialRouteStates,
  clearUnifiedRouteState,
  fetchUnifiedRouteStates,
} from '../services/unifiedGatewayApi';
import { Badge, Button, Pagination, useAppDialog } from '../components/ui';
import { PageHeader } from '../components/shell';

// 这一页存在的理由：选路候选 SQL 用 `rs.id IS NULL` 把熔断中的路由整行滤掉，
// 所以一个配置完全正确的模型可以连续 6 小时返回 503，而控制台此前没有任何地方
// 能看到 gw_route_states。更糟的是熔断键是 credential id 而不是密钥本身——
// 换了密钥不换 id，熔断照旧，只能干等窗口过期。

const DEFAULT_PAGE_SIZE = 20;
const TH_CLASS = 'px-4 py-3 text-left text-xs font-semibold text-[var(--text-secondary)]';
const TD_CLASS = 'px-4 py-3 align-top text-sm text-[var(--text-primary)]';

const formatRemaining = (seconds: number) => {
  if (seconds <= 0) return '已到期';
  if (seconds < 60) return `${seconds} 秒`;
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} 分钟`;
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.ceil((seconds % 3600) / 60);
  return minutes ? `${hours} 小时 ${minutes} 分钟` : `${hours} 小时`;
};

const formatTime = (value: string | null) => (value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-');

const CircuitBreakers: React.FC = () => {
  const { askConfirmation, showAlert } = useAppDialog();
  const [rows, setRows] = useState<UnifiedRouteState[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [includeExpired, setIncludeExpired] = useState(false);
  // 从模型列表的「有线路被熔断」标记跳过来时会带上对外模型名，直接落到相关行。
  const initialModel = useSearchParams()[0].get('model') || '';
  const [modelFilter, setModelFilter] = useState(initialModel);
  const [appliedModel, setAppliedModel] = useState(initialModel);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setLoading(true);
    setError('');
    try {
      const data = await fetchUnifiedRouteStates(page, pageSize, { includeExpired, modelName: appliedModel || undefined }, controller.signal);
      setRows(data.items || []);
      setTotal(data.total || 0);
    } catch (err) {
      if (controller.signal.aborted) return;
      setError(err instanceof Error ? err.message : '加载熔断状态失败');
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [page, pageSize, includeExpired, appliedModel]);

  useEffect(() => {
    load();
    return () => abortRef.current?.abort();
  }, [load]);

  const runClear = async (label: string, description: string, action: () => Promise<{ cleared: number }>) => {
    if (!(await askConfirmation({ title: label, description, confirmLabel: '解除熔断' }))) return;
    setBusy(true);
    try {
      const result = await action();
      await load();
      await showAlert({ title: '已解除', description: `清除 ${result.cleared} 条熔断记录，相关路由在下一个请求即可重新参与选路。` });
    } catch (err) {
      await showAlert({ title: '解除失败', description: err instanceof Error ? err.message : '请稍后重试' });
    } finally {
      setBusy(false);
    }
  };

  const activeCount = rows.filter(row => row.active).length;

  return (
    <div className="space-y-4">
      <PageHeader
        icon={ShieldAlert}
        title="熔断状态"
        meta={<>上游连续拒绝后该路由会被暂时摘除；在此解除即刻恢复，无需等退避窗口走完。本页共 {total} 条{activeCount ? `，其中 ${activeCount} 条仍在生效` : ''}</>}
        actions={<Button variant="secondary" onClick={load} disabled={loading || busy}><RefreshCw size={16} className={loading ? 'animate-spin' : ''} />刷新</Button>}
      />

      <div className="flex flex-wrap items-center gap-2 rounded-xl border border-[var(--border-soft)] bg-[var(--surface-card)] p-3">
        <input
          value={modelFilter}
          onChange={event => setModelFilter(event.target.value)}
          onKeyDown={event => { if (event.key === 'Enter') { setPage(1); setAppliedModel(modelFilter.trim()); } }}
          placeholder="按模型名筛选，回车生效"
          className="min-w-56 flex-1 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm text-[var(--text-primary)] outline-none transition focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--primary)]/20"
        />
        <Button variant="secondary" onClick={() => { setPage(1); setAppliedModel(modelFilter.trim()); }}>筛选</Button>
        <label className="flex items-center gap-2 px-2 text-sm text-[var(--text-secondary)]">
          <input type="checkbox" checked={includeExpired} onChange={event => { setPage(1); setIncludeExpired(event.target.checked); }} />
          含已到期（作为失败历史查看）
        </label>
      </div>

      {error && <div className="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-600">{error}</div>}

      <div className="overflow-x-auto rounded-xl border border-[var(--border-soft)] bg-[var(--surface-card)]">
        <table className="w-full min-w-[960px] text-left text-sm">
          <thead className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)]/50">
            <tr>
              <th className={TH_CLASS}>模型 / 通道</th>
              <th className={TH_CLASS}>API Key</th>
              <th className={TH_CLASS}>剩余</th>
              <th className={TH_CLASS}>上游原因</th>
              <th className={TH_CLASS}>失败次数</th>
              <th className={TH_CLASS}>最近更新</th>
              <th className={TH_CLASS}>操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--border-soft)]">
            {rows.length === 0 ? (
              <tr>
                <td colSpan={7} className="h-64 text-center text-sm text-[var(--text-secondary)]">
                  {loading ? '加载中…' : includeExpired ? '没有任何熔断记录' : '当前没有路由处于熔断状态'}
                </td>
              </tr>
            ) : rows.map(row => (
              <tr key={row.id} className="transition-colors hover:bg-[var(--surface-muted)]/50">
                <td className={TD_CLASS}>
                  <div className="font-semibold">{row.model_name}</div>
                  <div className="text-xs text-[var(--text-secondary)]">{row.transport}</div>
                </td>
                <td className={TD_CLASS}>
                  <div className="font-mono text-xs">{row.credential_code || `#${row.credential_id}`}</div>
                  <div className="text-xs text-[var(--text-secondary)]">{[row.channel_name, row.pool_name || row.pool_code].filter(Boolean).join(' / ') || '-'}</div>
                </td>
                <td className={TD_CLASS}>
                  {row.active
                    ? <Badge variant="warning">{formatRemaining(row.remaining_seconds)}</Badge>
                    : <Badge variant="default">已到期</Badge>}
                  <div className="mt-1 text-xs text-[var(--text-secondary)]">{formatTime(row.disabled_until)}</div>
                </td>
                <td className={TD_CLASS}>
                  <div className="max-w-96 whitespace-normal break-words text-xs">{row.reason || '-'}</div>
                  {row.status_code > 0 && <div className="mt-1 text-xs text-[var(--text-secondary)]">HTTP {row.status_code}</div>}
                </td>
                <td className={`${TD_CLASS} tabular-nums`}>{row.fail_count}</td>
                <td className={`${TD_CLASS} text-xs text-[var(--text-secondary)]`}>{formatTime(row.updated_at)}</td>
                <td className={TD_CLASS}>
                  <div className="flex items-center gap-1">
                    <button
                      type="button"
                      title="解除这一条"
                      aria-label="解除这一条"
                      disabled={busy}
                      onClick={() => runClear('解除该路由的熔断', `${row.model_name} / ${row.transport} 将在下一个请求重新参与选路。若上游仍在拒绝，它会很快再次被熔断。`, () => clearUnifiedRouteState(row.id))}
                      className="grid h-8 w-8 place-items-center rounded-lg text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-tint)] hover:text-[var(--primary)] disabled:cursor-not-allowed disabled:opacity-35"
                    ><Trash2 size={16} /></button>
                    <button
                      type="button"
                      title="解除该 Key 的全部熔断"
                      aria-label="解除该 Key 的全部熔断"
                      disabled={busy || !row.credential_id}
                      onClick={() => runClear('解除该 API Key 的全部熔断', `换过密钥就用这个：熔断记在 API Key 的 id 上，只改密钥不会自动解除。将清除 ${row.credential_code || `#${row.credential_id}`} 在所有模型上的熔断记录。`, () => clearUnifiedCredentialRouteStates(row.credential_id))}
                      className="grid h-8 w-8 place-items-center rounded-lg text-[var(--text-secondary)] transition-colors hover:bg-[var(--surface-tint)] hover:text-[var(--primary)] disabled:cursor-not-allowed disabled:opacity-35"
                    ><Unplug size={16} /></button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} onPageSizeChange={next => { setPage(1); setPageSize(next); }} />
    </div>
  );
};

export default CircuitBreakers;
