import React, { useEffect, useRef, useState } from 'react';
import {
  Globe2,
  RefreshCw,
  RotateCcw,
  Search,
  ShieldCheck,
  WalletCards,
} from 'lucide-react';
import {
  APIAccessLog,
  AuditEvent,
  BalanceEntry,
  fetchAPIAccessLogs,
  fetchAuditEvents,
  fetchBalanceEntries,
  fetchUsers,
} from '../services';
import { User, UserRole } from '../types';
import { Pagination, Select } from '../components/ui';

const DEFAULT_PAGE_SIZE = 20;
const INPUT_CLASS = 'min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm text-[var(--text-primary)] outline-none transition focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--primary)]/20';
const TH_CLASS = 'px-4 py-3 text-left text-xs font-semibold text-[var(--text-secondary)]';
const TD_CLASS = 'px-4 py-3 align-top text-sm text-[var(--text-primary)]';

type Tab = 'access' | 'audit' | 'balance';

const emptySnapshots = (): Record<Tab, number | undefined> => ({
  access: undefined,
  audit: undefined,
  balance: undefined,
});

type FilterState = {
  user_id: string;
  token_id: string;
  start_date: string;
  end_date: string;
  request_id: string;
  call_id: string;
  error_code: string;
  path: string;
  method: string;
  status_code: string;
  action: string;
  resource_type: string;
  outcome: string;
  account_type: string;
  direction: string;
  category: string;
};

const EMPTY_FILTERS: FilterState = {
  user_id: '', token_id: '', start_date: '', end_date: '',
  request_id: '', call_id: '', error_code: '',
  path: '', method: '', status_code: '',
  action: '', resource_type: '', outcome: '',
  account_type: '', direction: '', category: '',
};

const CATEGORY_LABELS: Record<string, string> = {
  opening_balance: '迁移基线',
  initial_credit: '初始额度',
  recharge: '充值',
  deduction: '扣费',
  reservation: '预授权',
  settlement: '结算',
  refund: '退款',
};

const formatDate = (value: string) => {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value || '-' : date.toLocaleString('zh-CN', { hour12: false });
};

const formatMoney = (value: string) => {
  const raw = (value || '0').trim();
  const match = /^([+-]?)(\d+)(?:\.(\d*))?$/.exec(raw);
  if (!match) return raw;
  const [, sign, integer, fraction = ''] = match;
  return `${sign}${integer}.${fraction.padEnd(8, '0')}`;
};

const parsePositive = (value: string) => {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
};

const HTTPStatus: React.FC<{ status: number }> = ({ status }) => {
  const ok = status >= 200 && status < 400;
  return (
    <span className={`inline-flex rounded-md px-2 py-1 text-xs font-semibold ${ok ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'}`}>
      {status || '-'}
    </span>
  );
};

const Outcome: React.FC<{ outcome: string }> = ({ outcome }) => (
  <span className={`inline-flex rounded-md px-2 py-1 text-xs font-semibold ${outcome === 'success' ? 'bg-green-100 text-green-700' : 'bg-red-100 text-red-700'}`}>
    {outcome === 'success' ? '成功' : outcome === 'failed' ? '失败' : outcome || '-'}
  </span>
);

const Observability: React.FC = () => {
  const [activeTab, setActiveTab] = useState<Tab>('access');
  const [accessLogs, setAccessLogs] = useState<APIAccessLog[]>([]);
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([]);
  const [balanceEntries, setBalanceEntries] = useState<BalanceEntry[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [draft, setDraft] = useState<FilterState>({ ...EMPTY_FILTERS });
  const [filters, setFilters] = useState<FilterState>({ ...EMPTY_FILTERS });
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshKey, setRefreshKey] = useState(0);
  const snapshotIds = useRef<Record<Tab, number | undefined>>(emptySnapshots());
  // 三张表增长速度不同，各自维护快照；切换标签不会把另一张表的游标带过来。
  const [isAdmin] = useState(() => {
    try {
      return JSON.parse(localStorage.getItem('prism_user') || '{}').role === UserRole.ADMIN;
    } catch {
      return false;
    }
  });

  useEffect(() => {
    if (!isAdmin) return;
    fetchUsers().then(setUsers).catch(() => setUsers([]));
  }, [isAdmin]);

  useEffect(() => {
    let active = true;
    const common = {
      page,
      page_size: pageSize,
      snapshot_id: snapshotIds.current[activeTab],
      start_date: filters.start_date,
      end_date: filters.end_date,
      ...(isAdmin ? {
        user_id: parsePositive(filters.user_id),
        token_id: parsePositive(filters.token_id),
      } : {}),
    };

    setLoading(true);
    setError('');
    const load = async () => {
      if (activeTab === 'access') {
        const response = await fetchAPIAccessLogs({
          ...common,
          request_id: filters.request_id.trim(),
          call_id: filters.call_id.trim(),
          error_code: filters.error_code.trim(),
          path: filters.path.trim(),
          method: filters.method,
          status_code: parsePositive(filters.status_code),
        });
        if (active) {
          snapshotIds.current.access = response.snapshot_id;
          setAccessLogs(response.items || []);
        }
        return response.total || 0;
      }
      if (activeTab === 'audit') {
        const response = await fetchAuditEvents({
          ...common,
          request_id: filters.request_id.trim(),
          action: filters.action.trim(),
          resource_type: filters.resource_type.trim(),
          outcome: filters.outcome,
        });
        if (active) {
          snapshotIds.current.audit = response.snapshot_id;
          setAuditEvents(response.items || []);
        }
        return response.total || 0;
      }
      const response = await fetchBalanceEntries({
        ...common,
        call_id: filters.call_id.trim(),
        account_type: filters.account_type,
        direction: filters.direction,
        category: filters.category.trim(),
      });
      if (active) {
        snapshotIds.current.balance = response.snapshot_id;
        setBalanceEntries(response.items || []);
      }
      return response.total || 0;
    };

    load().then(value => {
      if (!active) return;
      const lastPage = Math.max(1, Math.ceil(value / pageSize));
      if (page > lastPage) {
        setPage(lastPage);
        return;
      }
      setTotal(value);
    }).catch(cause => {
      if (active) setError(cause instanceof Error ? cause.message : '记录加载失败');
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [activeTab, filters, isAdmin, page, pageSize, refreshKey]);

  const updateDraft = (key: keyof FilterState, value: string) => {
    setDraft(current => ({ ...current, [key]: value }));
  };
  const selectTab = (tab: Tab) => {
    snapshotIds.current[tab] = undefined;
    setActiveTab(tab);
    setPage(1);
    setTotal(0);
    setRefreshKey(value => value + 1);
  };
  const search = () => {
    // 筛选条件变化会改变结果集合，所有标签的旧快照都必须失效。
    snapshotIds.current = emptySnapshots();
    setFilters({ ...draft });
    setPage(1);
    setRefreshKey(value => value + 1);
  };
  const reset = () => {
    snapshotIds.current = emptySnapshots();
    setDraft({ ...EMPTY_FILTERS });
    setFilters({ ...EMPTY_FILTERS });
    setPage(1);
    setRefreshKey(value => value + 1);
  };
  const refresh = () => {
    snapshotIds.current[activeTab] = undefined;
    setPage(1);
    setRefreshKey(value => value + 1);
  };
  const changePageSize = (value: number) => {
    snapshotIds.current[activeTab] = undefined;
    setPageSize(value);
    setPage(1);
  };

  const renderAccessLogs = () => (
    <table className="w-full min-w-[960px] table-fixed">
      <thead className="border-b border-[var(--border-soft)] bg-[var(--surface)]">
        <tr>
          <th className={`${TH_CLASS} w-[170px]`}>时间</th>
          <th className={`${TH_CLASS} w-[350px]`}>请求</th>
          <th className={`${TH_CLASS} w-[120px]`}>状态</th>
          <th className={`${TH_CLASS} w-[140px]`}>身份</th>
          <th className={TH_CLASS}>客户端</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-[var(--border-soft)]">
        {accessLogs.map(item => (
          <tr key={item.id} className="hover:bg-[var(--surface)]/70">
            <td className={TD_CLASS}>{formatDate(item.created_at)}</td>
            <td className={TD_CLASS}>
              <div className="flex min-w-0 items-center gap-2">
                <span className="rounded bg-[var(--primary-lighter)] px-1.5 py-0.5 font-mono text-xs font-semibold text-[var(--primary)]">{item.method}</span>
                <span className="truncate font-mono" title={item.path}>{item.path}</span>
              </div>
              <div className="mt-1 truncate font-mono text-xs text-[var(--text-secondary)]" title={item.request_id}>
                {item.request_id || '-'}{item.call_id ? ` · ${item.call_id}` : ''}
              </div>
              {item.query && <div className="mt-1 truncate text-xs text-[var(--text-secondary)]" title={item.query}>?{item.query}</div>}
            </td>
            <td className={TD_CLASS}>
              <HTTPStatus status={item.status_code} />
              <div className="mt-1 text-xs text-[var(--text-secondary)]">{item.duration_ms} ms</div>
              {item.error_code && <div className="mt-1 break-words text-xs text-red-600">{item.error_code}</div>}
            </td>
            <td className={TD_CLASS}>
              <div>用户 #{item.user_id || '-'}</div>
              <div className="mt-1 text-xs text-[var(--text-secondary)]">Token #{item.token_id || '-'}</div>
            </td>
            <td className={TD_CLASS}>
              <div className="font-mono text-xs">{item.ip || '-'}</div>
              <div className="mt-1 truncate text-xs text-[var(--text-secondary)]" title={item.user_agent}>{item.user_agent || '-'}</div>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );

  const renderAuditEvents = () => (
    <table className="w-full min-w-[900px] table-fixed">
      <thead className="border-b border-[var(--border-soft)] bg-[var(--surface)]">
        <tr>
          <th className={`${TH_CLASS} w-[170px]`}>时间</th>
          <th className={`${TH_CLASS} w-[330px]`}>操作</th>
          <th className={`${TH_CLASS} w-[190px]`}>资源</th>
          <th className={`${TH_CLASS} w-[130px]`}>结果</th>
          <th className={TH_CLASS}>操作者</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-[var(--border-soft)]">
        {auditEvents.map(item => (
          <tr key={item.id} className="hover:bg-[var(--surface)]/70">
            <td className={TD_CLASS}>{formatDate(item.created_at)}</td>
            <td className={TD_CLASS}>
              <div className="truncate font-mono" title={item.action}>{item.action}</div>
              <div className="mt-1 truncate font-mono text-xs text-[var(--text-secondary)]" title={item.request_id}>{item.request_id || '-'}</div>
            </td>
            <td className={TD_CLASS}>
              <div>{item.resource_type || '-'}</div>
              <div className="mt-1 break-words font-mono text-xs text-[var(--text-secondary)]">{item.resource_id || '-'}</div>
            </td>
            <td className={TD_CLASS}>
              <Outcome outcome={item.outcome} />
              <div className="mt-1 text-xs text-[var(--text-secondary)]">HTTP {item.http_status || '-'}</div>
            </td>
            <td className={TD_CLASS}>
              <div>用户 #{item.actor_user_id || '-'}</div>
              <div className="mt-1 text-xs text-[var(--text-secondary)]">Token #{item.actor_token_id || '-'}</div>
              <div className="mt-1 font-mono text-xs text-[var(--text-secondary)]">{item.ip || '-'}</div>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );

  const renderBalanceEntries = () => (
    <table className="w-full min-w-[980px] table-fixed">
      <thead className="border-b border-[var(--border-soft)] bg-[var(--surface)]">
        <tr>
          <th className={`${TH_CLASS} w-[170px]`}>时间</th>
          <th className={`${TH_CLASS} w-[150px]`}>账户</th>
          <th className={`${TH_CLASS} w-[170px]`}>变动</th>
          <th className={`${TH_CLASS} w-[180px]`}>类别</th>
          <th className={`${TH_CLASS} w-[210px]`}>余额</th>
          <th className={TH_CLASS}>关联</th>
        </tr>
      </thead>
      <tbody className="divide-y divide-[var(--border-soft)]">
        {balanceEntries.map(item => {
          const credit = item.direction === 'credit';
          return (
            <tr key={item.id} className="hover:bg-[var(--surface)]/70">
              <td className={TD_CLASS}>{formatDate(item.created_at)}</td>
              <td className={TD_CLASS}>
                <div>{item.account_type === 'user' ? '用户账户' : item.account_type === 'token' ? 'Token 账户' : item.account_type}</div>
                <div className="mt-1 text-xs text-[var(--text-secondary)]">#{item.account_id}</div>
              </td>
              <td className={TD_CLASS}>
                <span className={`font-mono font-semibold ${credit ? 'text-green-600' : 'text-red-600'}`}>
                  {credit ? '+' : '-'}{formatMoney(item.amount)}
                </span>
                <div className="mt-1 text-xs text-[var(--text-secondary)]">{credit ? '入账' : '出账'}</div>
              </td>
              <td className={TD_CLASS}>
                <div>{CATEGORY_LABELS[item.category] || item.category || '-'}</div>
                <div className="mt-1 truncate font-mono text-xs text-[var(--text-secondary)]" title={item.source_key}>{item.source_key || '-'}</div>
              </td>
              <td className={TD_CLASS}>
                <div className="font-mono text-xs">{formatMoney(item.balance_before)}</div>
                <div className="my-0.5 text-xs text-[var(--text-secondary)]">→</div>
                <div className="font-mono text-xs font-semibold">{formatMoney(item.balance_after)}</div>
              </td>
              <td className={TD_CLASS}>
                <div className="truncate font-mono text-xs" title={item.call_id}>{item.call_id || '-'}</div>
                <div className="mt-1 text-xs text-[var(--text-secondary)]">
                  用户 #{item.user_id || '-'} · Token #{item.token_id || '-'}
                </div>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );

  const rowCount = activeTab === 'access' ? accessLogs.length : activeTab === 'audit' ? auditEvents.length : balanceEntries.length;

  return (
    <div className="space-y-5">
      <header className="flex items-center justify-between gap-3">
        <h1 className="flex items-center gap-2 text-xl font-bold text-[var(--text-primary)] md:text-2xl">
          <ShieldCheck size={23} />审计与流水
        </h1>
        <button
          type="button"
          title="刷新"
          aria-label="刷新"
          onClick={refresh}
          className="flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] text-[var(--text-primary)] hover:bg-[var(--surface)]"
        >
          <RefreshCw size={17} className={loading ? 'animate-spin' : ''} />
        </button>
      </header>

      <div className="inline-flex max-w-full gap-1 overflow-x-auto rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] p-1" role="tablist">
        {([
          { key: 'access' as const, label: '访问日志', icon: Globe2 },
          { key: 'audit' as const, label: '审计事件', icon: ShieldCheck },
          { key: 'balance' as const, label: '余额流水', icon: WalletCards },
        ]).map(tab => {
          const Icon = tab.icon;
          const selected = activeTab === tab.key;
          return (
            <button
              key={tab.key}
              type="button"
              role="tab"
              aria-selected={selected}
              onClick={() => selectTab(tab.key)}
              className={`flex min-w-[112px] items-center justify-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition ${selected ? 'bg-[var(--surface-card)] text-[var(--primary)] shadow-sm' : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}
            >
              <Icon size={16} />{tab.label}
            </button>
          );
        })}
      </div>

      <section className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-4">
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {isAdmin && (
            <>
              <Select
                value={draft.user_id}
                onChange={v => updateDraft('user_id', v)}
                options={[{ label: '所有用户', value: '' }, ...users.map(user => ({ label: `${user.username} (#${user.id})`, value: String(user.id) }))]}
              />
              <input type="number" min="1" value={draft.token_id} onChange={event => updateDraft('token_id', event.target.value)} className={INPUT_CLASS} placeholder="Token ID" aria-label="Token ID" />
            </>
          )}
          <input type="date" value={draft.start_date} onChange={event => updateDraft('start_date', event.target.value)} className={INPUT_CLASS} aria-label="开始日期" />
          <input type="date" value={draft.end_date} onChange={event => updateDraft('end_date', event.target.value)} className={INPUT_CLASS} aria-label="结束日期" />

          {activeTab === 'access' && (
            <>
              <input value={draft.request_id} onChange={event => updateDraft('request_id', event.target.value)} className={INPUT_CLASS} placeholder="请求 ID" aria-label="请求 ID" />
              <input value={draft.call_id} onChange={event => updateDraft('call_id', event.target.value)} className={INPUT_CLASS} placeholder="调用 ID" aria-label="调用 ID" />
              <input value={draft.error_code} onChange={event => updateDraft('error_code', event.target.value)} className={INPUT_CLASS} placeholder="错误码" aria-label="错误码" />
              <input value={draft.path} onChange={event => updateDraft('path', event.target.value)} className={INPUT_CLASS} placeholder="请求路径" aria-label="请求路径" />
              <Select
                value={draft.method}
                onChange={v => updateDraft('method', v)}
                options={[{ label: '所有方法', value: '' }, ...['GET', 'POST', 'PUT', 'DELETE', 'PATCH'].map(method => ({ label: method, value: method }))]}
              />
              <input type="number" min="100" max="599" value={draft.status_code} onChange={event => updateDraft('status_code', event.target.value)} className={INPUT_CLASS} placeholder="HTTP 状态码" aria-label="HTTP 状态码" />
            </>
          )}
          {activeTab === 'audit' && (
            <>
              <input value={draft.request_id} onChange={event => updateDraft('request_id', event.target.value)} className={INPUT_CLASS} placeholder="请求 ID" aria-label="请求 ID" />
              <input value={draft.action} onChange={event => updateDraft('action', event.target.value)} className={INPUT_CLASS} placeholder="操作" aria-label="操作" />
              <input value={draft.resource_type} onChange={event => updateDraft('resource_type', event.target.value)} className={INPUT_CLASS} placeholder="资源类型" aria-label="资源类型" />
              <Select
                value={draft.outcome}
                onChange={v => updateDraft('outcome', v)}
                options={[{ label: '所有结果', value: '' }, { label: '成功', value: 'success' }, { label: '失败', value: 'failed' }]}
              />
            </>
          )}
          {activeTab === 'balance' && (
            <>
              <input value={draft.call_id} onChange={event => updateDraft('call_id', event.target.value)} className={INPUT_CLASS} placeholder="调用 ID" aria-label="调用 ID" />
              <Select
                value={draft.account_type}
                onChange={v => updateDraft('account_type', v)}
                options={[{ label: '所有账户', value: '' }, { label: '用户账户', value: 'user' }, { label: 'Token 账户', value: 'token' }]}
              />
              <Select
                value={draft.direction}
                onChange={v => updateDraft('direction', v)}
                options={[{ label: '所有方向', value: '' }, { label: '入账', value: 'credit' }, { label: '出账', value: 'debit' }]}
              />
              <Select
                value={draft.category}
                onChange={v => updateDraft('category', v)}
                options={[{ label: '所有类别', value: '' }, ...Object.entries(CATEGORY_LABELS).map(([value, label]) => ({ label, value }))]}
              />
            </>
          )}
        </div>
        <div className="mt-3 flex justify-end gap-2">
          <button type="button" onClick={reset} className="flex items-center gap-2 rounded-lg border border-[var(--border-soft)] px-3 py-2 text-sm text-[var(--text-secondary)] hover:bg-[var(--surface)]">
            <RotateCcw size={15} />重置
          </button>
          <button type="button" onClick={search} className="flex items-center gap-2 rounded-lg bg-[var(--primary)] px-3 py-2 text-sm font-semibold text-white hover:opacity-90">
            <Search size={15} />查询
          </button>
        </div>
      </section>

      <section className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)]">
        {error && <div className="border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{error}</div>}
        <div className="overflow-x-auto">
          {loading ? (
            <div className="flex h-64 items-center justify-center text-sm text-[var(--text-secondary)]">
              <RefreshCw size={18} className="mr-2 animate-spin" />加载中
            </div>
          ) : rowCount === 0 ? (
            <div className="flex h-64 items-center justify-center text-sm text-[var(--text-secondary)]">暂无记录</div>
          ) : activeTab === 'access' ? renderAccessLogs() : activeTab === 'audit' ? renderAuditEvents() : renderBalanceEntries()}
        </div>
        <Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} onPageSizeChange={changePageSize} />
      </section>
    </div>
  );
};

export default Observability;
