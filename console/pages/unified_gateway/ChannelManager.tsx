import React, { useEffect, useRef, useState } from 'react';
import {
  ArrowLeft,
  ChevronRight,
  KeyRound,
  LoaderCircle,
  Pause,
  PenLine,
  Plus,
  Power,
  Search,
} from 'lucide-react';
import { Button, Pagination } from '../../components/ui';
import { useAppDialog } from '../../components/ui/AppDialogProvider';
import {
  fetchUnifiedChannels,
  fetchUnifiedPools,
  transitionUnifiedPool,
  type UnifiedChannel,
  type UnifiedPool,
} from '../../services/unifiedChannelApi';
import { ChannelDialog, type ChannelEditor } from './ChannelDialog';
import { ErrorNotice, errorMessage } from './Feedback';
import { StatusBadge } from './UnifiedTable';
import { CredentialManager } from './CredentialManager';

export const ChannelManager: React.FC<{
  refresh: number;
  onChanged: () => void;
  readOnly: boolean;
}> = ({ refresh, onChanged, readOnly }) => {
  const { askConfirmation } = useAppDialog();
  const [channel, setChannel] = useState<UnifiedChannel | null>(null);
  const [pool, setPool] = useState<UnifiedPool | null>(null);
  const [query, setQuery] = useState({
    page: 1,
    size: 20,
    search: '',
    status: '',
  });
  const [search, setSearch] = useState('');
  const [channels, setChannels] = useState<UnifiedChannel[]>([]);
  const [pools, setPools] = useState<UnifiedPool[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState('');
  const [editor, setEditor] = useState<ChannelEditor | null>(null);
  const [pending, setPending] = useState(false);
  const actionLock = useRef(false);
  const [revision, setRevision] = useState(0);
  const reload = () => setRevision((value) => value + 1);
  useEffect(() => {
    const timer = setTimeout(
      () =>
        setQuery((current) =>
          current.search === search.trim()
            ? current
            : { ...current, page: 1, search: search.trim() },
        ),
      250,
    );
    return () => clearTimeout(timer);
  }, [search]);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    setTotal(0);
    const load = async () => {
      const result = channel
        ? await fetchUnifiedPools(
            channel.id,
            query.page,
            query.size,
            controller.signal,
          )
        : await fetchUnifiedChannels(
            query.page,
            query.size,
            query.search,
            query.status,
            controller.signal,
          );
      if (controller.signal.aborted) return;
      const last = Math.max(1, Math.ceil(result.total / query.size));
      if (query.page > last) {
        setQuery((current) => ({ ...current, page: last }));
        return;
      }
      if (channel) setPools(result.items as UnifiedPool[]);
      else setChannels(result.items as UnifiedChannel[]);
      setTotal(result.total);
    };
    void load()
      .catch((err) => {
        if (!controller.signal.aborted) setError(errorMessage(err));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [channel, query, refresh, revision]);
  const selectChannel = (value: UnifiedChannel | null) => {
    setChannel(value);
    setQuery((current) => ({ ...current, page: 1 }));
    setActionError('');
  };
  const changeStatus = async (pool: UnifiedPool) => {
    if (readOnly || actionLock.current || pool.status === 'disabled') return;
    actionLock.current = true;
    setPending(true);
    setActionError('');
    try {
      if (
        !(await askConfirmation({
          title: `${pool.status === 'active' ? '停止分配' : '停用'}凭据池？`,
          description: pool.display_name,
          confirmLabel: '确认',
          tone: 'warning',
        }))
      )
        return;
      await transitionUnifiedPool(pool);
      reload();
      onChanged();
    } catch (err: unknown) {
      setActionError(errorMessage(err));
    } finally {
      actionLock.current = false;
      setPending(false);
    }
  };
  if (pool)
    return (
      <CredentialManager
        pool={pool}
        refresh={refresh}
        readOnly={readOnly}
        onBack={() => setPool(null)}
        onChanged={onChanged}
      />
    );
  return (
    <div className="min-w-0 space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        {channel ? (
          <div className="flex min-w-0 items-center gap-2">
            <IconButton
              label="返回渠道"
              disabled={pending}
              onClick={() => selectChannel(null)}
            >
              <ArrowLeft size={17} />
            </IconButton>
            <h2 className="min-w-0 break-all text-sm font-bold">
              {channel.display_name}
            </h2>
            <span className="shrink-0 text-xs text-[var(--text-secondary)]">
              凭据池
            </span>
          </div>
        ) : (
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <label className="relative">
              <Search
                size={15}
                className="pointer-events-none absolute left-3 top-3 text-[var(--text-secondary)]"
              />
              <input
                aria-label="搜索渠道"
                placeholder="渠道名称或标识"
                value={search}
                maxLength={128}
                onChange={(event) => setSearch(event.target.value)}
                className="h-9 w-56 max-w-full rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] pl-9 pr-3 text-sm"
              />
            </label>
            <select
              aria-label="渠道状态"
              value={query.status}
              onChange={(event) =>
                setQuery((current) => ({
                  ...current,
                  page: 1,
                  status: event.target.value,
                }))
              }
              className="h-9 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 text-sm"
            >
              <option value="">全部状态</option>
              <option value="active">启用</option>
              <option value="disabled">停用</option>
            </select>
          </div>
        )}
        <Button
          size="sm"
          disabled={
            readOnly ||
            pending ||
            Boolean(channel && channel.status !== 'active')
          }
          onClick={() =>
            setEditor(
              channel
                ? { kind: 'pool', channelId: channel.id }
                : { kind: 'channel' },
            )
          }
        >
          <Plus size={15} />
          {channel ? '新建凭据池' : '新建渠道'}
        </Button>
      </div>
      {actionError && <ErrorNotice message={actionError} />}
      {error ? (
        <ErrorNotice message={error} onRetry={reload} />
      ) : loading ? (
        <div
          role="status"
          className="flex min-h-64 items-center justify-center gap-2 text-sm text-[var(--text-secondary)]"
        >
          <LoaderCircle size={18} className="animate-spin" />
          正在读取
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)]/50 text-xs text-[var(--text-secondary)]">
                {(channel
                  ? [
                      '凭据池',
                      '标识',
                      '状态',
                      '请求并发',
                      '任务并发',
                      '凭据数',
                      '操作',
                    ]
                  : ['渠道', '标识', '状态', '凭据池', '凭据数', '操作']
                ).map((label) => (
                  <th
                    key={label}
                    className="whitespace-nowrap px-4 py-3 font-semibold"
                  >
                    {label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border-soft)]">
              {total === 0 && (
                <tr>
                  <td
                    colSpan={channel ? 7 : 6}
                    className="h-64 text-center text-[var(--text-secondary)]"
                  >
                    暂无{channel ? '凭据池' : '渠道'}
                  </td>
                </tr>
              )}
              {channel
                ? pools.map((pool) => (
                    <tr
                      key={pool.id}
                      className="hover:bg-[var(--surface-muted)]/50"
                    >
                      <td className="max-w-64 break-words px-4 py-3 font-semibold">
                        {pool.display_name}
                      </td>
                      <td className="max-w-64 break-all px-4 py-3 font-mono text-xs">
                        {pool.pool_code}
                      </td>
                      <td className="px-4 py-3">
                        <StatusBadge status={pool.status} />
                      </td>
                      <td className="px-4 py-3 tabular-nums">
                        {pool.request_limit ?? '不限'}
                      </td>
                      <td className="px-4 py-3 tabular-nums">
                        {pool.task_limit ?? '不限'}
                      </td>
                      <td className="px-4 py-3 tabular-nums">
                        <button
                          type="button"
                          title="查看凭据"
                          disabled={pending}
                          onClick={() => setPool(pool)}
                          className="flex h-8 items-center gap-2 text-[var(--primary)]"
                        >
                          <KeyRound size={15} />
                          {pool.credential_count}
                          <ChevronRight size={14} />
                        </button>
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex gap-1">
                          <IconButton
                            label="编辑凭据池"
                            disabled={
                              readOnly || pending || pool.status !== 'active'
                            }
                            onClick={() =>
                              setEditor({
                                kind: 'pool',
                                channelId: channel.id,
                                item: pool,
                              })
                            }
                          >
                            <PenLine size={16} />
                          </IconButton>
                          {pool.status !== 'disabled' && (
                            <IconButton
                              label={
                                pool.status === 'active'
                                  ? '停止分配'
                                  : '停用凭据池'
                              }
                              disabled={readOnly || pending}
                              onClick={() => void changeStatus(pool)}
                            >
                              {pool.status === 'active' ? (
                                <Pause size={16} />
                              ) : (
                                <Power size={16} />
                              )}
                            </IconButton>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))
                : channels.map((item) => (
                    <tr
                      key={item.id}
                      className="hover:bg-[var(--surface-muted)]/50"
                    >
                      <td className="max-w-64 break-words px-4 py-3 font-semibold">
                        {item.display_name}
                      </td>
                      <td className="max-w-64 break-all px-4 py-3 font-mono text-xs">
                        {item.channel_code}
                      </td>
                      <td className="px-4 py-3">
                        <StatusBadge status={item.status} />
                      </td>
                      <td className="px-4 py-3">
                        <button
                          type="button"
                          title="查看凭据池"
                          onClick={() => selectChannel(item)}
                          className="flex h-8 items-center gap-2 text-[var(--primary)]"
                        >
                          <KeyRound size={15} />
                          {item.pool_count}
                          <ChevronRight size={14} />
                        </button>
                      </td>
                      <td className="px-4 py-3 tabular-nums">
                        {item.credential_count}
                      </td>
                      <td className="px-4 py-3">
                        <IconButton
                          label={`编辑渠道 ${item.display_name}`}
                          disabled={readOnly || pending}
                          onClick={() => setEditor({ kind: 'channel', item })}
                        >
                          <PenLine size={16} />
                        </IconButton>
                      </td>
                    </tr>
                  ))}
            </tbody>
          </table>
        </div>
      )}
      {!error && (
        <Pagination
          page={query.page}
          pageSize={query.size}
          total={total}
          loading={loading || pending}
          onPageChange={(page) => setQuery((current) => ({ ...current, page }))}
          onPageSizeChange={(size) =>
            setQuery((current) => ({ ...current, size, page: 1 }))
          }
        />
      )}
      {editor && (
        <ChannelDialog
          editor={editor}
          readOnly={readOnly}
          onClose={() => setEditor(null)}
          onSaved={() => {
            setEditor(null);
            reload();
            onChanged();
          }}
        />
      )}
    </div>
  );
};
const IconButton: React.FC<{
  label: string;
  disabled?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}> = ({ label, disabled, onClick, children }) => (
  <button
    type="button"
    title={label}
    aria-label={label}
    disabled={disabled}
    onClick={onClick}
    className="grid h-8 w-8 shrink-0 place-items-center rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-tint)] hover:text-[var(--primary)] disabled:opacity-35"
  >
    {children}
  </button>
);
