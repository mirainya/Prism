import React, { useEffect, useRef, useState } from 'react';
import {
  ArrowLeft,
  LoaderCircle,
  Pause,
  PenLine,
  Plus,
  Power,
} from 'lucide-react';
import { Button, Pagination } from '../../components/ui';
import { useAppDialog } from '../../components/ui/AppDialogProvider';
import {
  fetchManagedCredentials,
  transitionManagedCredential,
} from '../../services/unifiedCredentialApi';
import type { UnifiedCredential } from '../../services/unifiedGatewayApi';
import type { UnifiedPool } from '../../services/unifiedChannelApi';
import { CredentialDialog } from './CredentialDialog';
import { ErrorNotice, errorMessage } from './Feedback';
import { StatusBadge } from './UnifiedTable';

export const CredentialManager: React.FC<{
  pool?: UnifiedPool;
  refresh: number;
  readOnly: boolean;
  onBack?: () => void;
  onChanged: () => void;
}> = ({ pool, refresh, readOnly, onBack, onChanged }) => {
  const { askConfirmation } = useAppDialog();
  const [query, setQuery] = useState({ page: 1, size: 20 });
  const [items, setItems] = useState<UnifiedCredential[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [actionError, setActionError] = useState('');
  const [revision, setRevision] = useState(0);
  const [editor, setEditor] = useState<{ item?: UnifiedCredential } | null>(
    null,
  );
  const [pending, setPending] = useState(false);
  const locked = useRef(false);
  const reload = () => setRevision((value) => value + 1);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError('');
    setTotal(0);
    fetchManagedCredentials(query.page, query.size, pool?.id, controller.signal)
      .then((result) => {
        if (controller.signal.aborted) return;
        const last = Math.max(1, Math.ceil(result.total / query.size));
        if (query.page > last) {
          setQuery((current) => ({ ...current, page: last }));
          return;
        }
        setItems(result.items);
        setTotal(result.total);
      })
      .catch((err: unknown) => {
        if (!controller.signal.aborted) setError(errorMessage(err));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [pool?.id, query, refresh, revision]);
  const transition = async (item: UnifiedCredential) => {
    if (
      readOnly ||
      locked.current ||
      !['active', 'draining'].includes(item.status)
    )
      return;
    locked.current = true;
    setPending(true);
    setActionError('');
    try {
      if (
        !(await askConfirmation({
          title: item.status === 'active' ? '停止分配凭据？' : '停用凭据？',
          description: item.credential_code,
          confirmLabel: '确认',
          tone: 'warning',
        }))
      )
        return;
      await transitionManagedCredential(item);
      reload();
      onChanged();
    } catch (err: unknown) {
      setActionError(errorMessage(err));
    } finally {
      locked.current = false;
      setPending(false);
    }
  };
  const actionClass =
    'grid h-8 w-8 shrink-0 place-items-center rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface-tint)] hover:text-[var(--primary)] disabled:opacity-35';
  return (
    <div className="min-w-0 space-y-4">
      {pool && (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2">
            <button
              type="button"
              title="返回凭据池"
              aria-label="返回凭据池"
              className={actionClass}
              disabled={pending}
              onClick={onBack}
            >
              <ArrowLeft size={17} />
            </button>
            <h2 className="min-w-0 break-all text-sm font-bold">
              {pool.display_name}
            </h2>
          </div>
          <Button
            size="sm"
            disabled={readOnly || pending || pool.status !== 'active'}
            onClick={() => setEditor({})}
          >
            <Plus size={15} />
            新建凭据
          </Button>
        </div>
      )}
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
          <table className="w-full min-w-[800px] text-left text-sm">
            <thead>
              <tr className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)]/50 text-xs text-[var(--text-secondary)]">
                {[
                  '凭据',
                  '凭据池',
                  '状态',
                  '授权用途',
                  '请求并发',
                  '任务并发',
                  '权重',
                  '操作',
                ].map((label) => (
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
                    colSpan={8}
                    className="h-64 text-center text-[var(--text-secondary)]"
                  >
                    暂无凭据
                  </td>
                </tr>
              )}
              {items.map((item) => (
                <tr
                  key={item.id}
                  className="hover:bg-[var(--surface-muted)]/50"
                >
                  <td className="max-w-64 break-all px-4 py-3 font-mono text-xs">
                    {item.credential_code}
                  </td>
                  <td className="max-w-64 break-words px-4 py-3">
                    {item.pool_name || '-'}
                  </td>
                  <td className="px-4 py-3">
                    <StatusBadge status={item.status} />
                  </td>
                  <td className="max-w-48 px-4 py-3 text-xs text-[var(--text-secondary)]">
                    {item.purposes
                      ?.map(
                        (value) =>
                          ({
                            execution: '模型调用',
                            catalog_discovery: '目录发现',
                            upstream_callback_verify: '回调验签',
                          })[value] ?? value,
                      )
                      .join(' / ') || '-'}
                  </td>
                  <td className="px-4 py-3 tabular-nums">
                    {item.request_limit ?? '不限'}
                  </td>
                  <td className="px-4 py-3 tabular-nums">
                    {item.task_limit ?? '不限'}
                  </td>
                  <td className="px-4 py-3 tabular-nums">{item.weight}</td>
                  <td className="px-4 py-3">
                    <div className="flex gap-1">
                      <button
                        type="button"
                        title="编辑凭据"
                        aria-label="编辑凭据"
                        className={actionClass}
                        disabled={
                          readOnly || pending || item.status !== 'active'
                        }
                        onClick={() => setEditor({ item })}
                      >
                        <PenLine size={16} />
                      </button>
                      {['active', 'draining'].includes(item.status) && (
                        <button
                          type="button"
                          title={
                            item.status === 'active'
                              ? '停止分配凭据'
                              : '停用凭据'
                          }
                          aria-label={
                            item.status === 'active'
                              ? '停止分配凭据'
                              : '停用凭据'
                          }
                          className={actionClass}
                          disabled={readOnly || pending}
                          onClick={() => void transition(item)}
                        >
                          {item.status === 'active' ? (
                            <Pause size={16} />
                          ) : (
                            <Power size={16} />
                          )}
                        </button>
                      )}
                    </div>
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
          onPageSizeChange={(size) => setQuery({ page: 1, size })}
        />
      )}
      {editor && (
        <CredentialDialog
          item={editor.item}
          poolId={pool?.id}
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
