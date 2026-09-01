import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Activity,
  AlertTriangle,
  Ban,
  Braces,
  CheckCircle2,
  ChevronRight,
  CircleDot,
  ExternalLink,
  FileJson,
  LayoutList,
  LoaderCircle,
  RefreshCw,
  RotateCcw,
  Route as RouteIcon,
  Search,
  XCircle,
  type LucideIcon,
} from 'lucide-react';
import { fetchCapabilities, fetchTaskDetail, fetchTaskLogs, TaskListParams } from '../services/api';
import { Capability, TaskDetail, TaskLog, UserRole } from '../types';
import { Drawer, Pagination, Select } from '../components/ui';
import { PageHeader } from '../components/shell';

const DEFAULT_PAGE_SIZE = 20;
const INPUT_CLASS = 'w-full min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm text-[var(--text-primary)] outline-none transition placeholder:text-[var(--text-tertiary)] focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--focus-ring)]';

type FilterDraft = {
  task_no: string;
  capability: string;
  status: string;
  token_id: string;
  start_date: string;
  end_date: string;
};

type DetailTab = 'overview' | 'request' | 'result' | 'upstream';
type MediaKind = 'image' | 'video' | 'audio';

type MediaItem = {
  kind: MediaKind;
  url: string;
  label: string;
};

const EMPTY_FILTERS: FilterDraft = {
  task_no: '',
  capability: '',
  status: '',
  token_id: '',
  start_date: '',
  end_date: '',
};

const STATUS_MAP: Record<string, { label: string; className: string; icon: LucideIcon }> = {
  pending: { label: '等待中', className: 'bg-gray-100 text-gray-700', icon: CircleDot },
  processing: { label: '处理中', className: 'bg-blue-100 text-blue-700', icon: LoaderCircle },
  success: { label: '成功', className: 'bg-green-100 text-green-700', icon: CheckCircle2 },
  failed: { label: '失败', className: 'bg-red-100 text-red-700', icon: XCircle },
  cancelled: { label: '已取消', className: 'bg-yellow-100 text-yellow-700', icon: Ban },
};

const DETAIL_TABS: Array<{ key: DetailTab; label: string; icon: LucideIcon; adminOnly?: boolean }> = [
  { key: 'overview', label: '概览', icon: LayoutList },
  { key: 'request', label: '请求参数', icon: Braces },
  { key: 'result', label: '结果', icon: FileJson },
  { key: 'upstream', label: '上游响应', icon: RouteIcon, adminOnly: true },
];

const getIsAdmin = () => {
  try {
    const user = JSON.parse(localStorage.getItem('prism_user') || '{}');
    return user.role === UserRole.ADMIN;
  } catch {
    return false;
  }
};

const parsePositive = (value: string) => {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
};

const formatMoney = (value: string | number | undefined) => {
  const raw = String(value ?? '0').trim();
  const match = /^([+-]?)(\d+)(?:\.(\d*))?$/.exec(raw);
  if (match) {
    const [, sign, integer, fraction = ''] = match;
    return `¥${sign}${integer}.${fraction.padEnd(8, '0').slice(0, 8)}`;
  }
  const parsed = Number(raw);
  return Number.isFinite(parsed) ? `¥${parsed.toFixed(8)}` : `¥${raw}`;
};

const clampProgress = (value: number) => Math.max(0, Math.min(100, Number(value) || 0));

const formatDate = (value?: string | null) => {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const pad = (part: number) => String(part).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
};

const hasValue = (value: unknown) => {
  if (value == null) return false;
  if (Array.isArray(value)) return value.length > 0;
  if (typeof value === 'object') return Object.keys(value).length > 0;
  return true;
};

const formatJSON = (value: unknown) => {
  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return String(value);
  }
};

const inferMediaKind = (url: string, hint: string): MediaKind | null => {
  const normalizedURL = url.toLowerCase();
  if (normalizedURL.startsWith('data:image/')) return 'image';
  if (normalizedURL.startsWith('data:video/')) return 'video';
  if (normalizedURL.startsWith('data:audio/')) return 'audio';
  if (!/^https?:\/\//i.test(url)) return null;

  const path = normalizedURL.split(/[?#]/, 1)[0];
  if (/\.(png|jpe?g|gif|webp|bmp|svg|avif)$/.test(path)) return 'image';
  if (/\.(mp4|webm|mov|m4v|mkv|avi)$/.test(path)) return 'video';
  if (/\.(mp3|wav|m4a|aac|ogg|flac|opus)$/.test(path)) return 'audio';

  const normalizedHint = hint.toLowerCase();
  if (normalizedHint.includes('image')) return 'image';
  if (normalizedHint.includes('video')) return 'video';
  if (normalizedHint.includes('audio')) return 'audio';
  return null;
};

const canAutoLoadMedia = (value: string) => {
  if (value.startsWith('blob:')) return true;
  if (value.startsWith('data:')) return false;
  try {
    return new URL(value, window.location.origin).origin === window.location.origin;
  } catch {
    return false;
  }
};

const extractMediaItems = (value: unknown, capability: string): MediaItem[] => {
  const items: MediaItem[] = [];
  // Provider 结果结构不统一，递归扫描时同时使用字段名和能力类型推断媒体种类。
  const seen = new Set<string>();

  const visit = (current: unknown, path: string, hint: string) => {
    if (typeof current === 'string') {
      const kind = inferMediaKind(current, hint);
      if (kind && !seen.has(current)) {
        seen.add(current);
        items.push({ kind, url: current, label: path });
      }
      return;
    }
    if (Array.isArray(current)) {
      current.forEach((item, index) => visit(item, `${path}[${index}]`, hint));
      return;
    }
    if (!current || typeof current !== 'object') return;

    const record = current as Record<string, unknown>;
    const contextHint = Object.entries(record)
      .filter(([key, item]) => typeof item === 'string' && /(type|mime|format|content)/i.test(key))
      .map(([, item]) => String(item))
      .join(' ');

    Object.entries(record).forEach(([key, item]) => {
      const likelyOutput = /^(url|uri)$|image|video|audio|media|file|output|result/i.test(key);
      const capabilityHint = likelyOutput ? capability : '';
      visit(item, `${path}.${key}`, `${hint} ${key} ${contextHint} ${capabilityHint}`);
    });
  };

  visit(value, 'result', capability);
  return items;
};

const StatusBadge: React.FC<{ status: string }> = ({ status }) => {
  const meta = STATUS_MAP[status] || {
    label: status || '未知',
    className: 'bg-gray-100 text-gray-700',
    icon: CircleDot,
  };
  const Icon = meta.icon;
  return (
    <span className={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-semibold ${meta.className}`}>
      <Icon size={13} className={status === 'processing' ? 'animate-spin' : ''} />
      {meta.label}
    </span>
  );
};

const TASK_TABLE_SKELETON = [
  ['w-40', 'w-32', 'w-24', 'w-36', 'w-16', 'w-32'],
  ['w-36', 'w-40', 'w-28', 'w-28', 'w-14', 'w-36'],
  ['w-44', 'w-28', 'w-24', 'w-40', 'w-16', 'w-28'],
  ['w-40', 'w-36', 'w-28', 'w-32', 'w-14', 'w-32'],
  ['w-36', 'w-32', 'w-24', 'w-36', 'w-16', 'w-28'],
];

const TaskTableSkeleton: React.FC = () => (
  <>
    {TASK_TABLE_SKELETON.map((widths, rowIndex) => (
      <tr key={rowIndex} aria-hidden="true">
        {widths.map((width, columnIndex) => (
          <td key={columnIndex} className="px-4 py-4">
            <div className={`candy-skeleton h-3 rounded-md ${width}`} />
            {columnIndex < 3 && <div className="candy-skeleton mt-2 h-2.5 w-20 rounded-md opacity-70" />}
          </td>
        ))}
        <td className="px-3 py-4"><div className="candy-skeleton h-5 w-5 rounded-md" /></td>
      </tr>
    ))}
  </>
);

const TaskDetailSkeleton: React.FC = () => (
  <div role="status" aria-label="异步任务详情加载中" className="space-y-6">
    {[0, 1, 2].map(section => (
      <section key={section}>
        <div className="candy-skeleton mb-3 h-4 w-24 rounded-md" />
        <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2 xl:grid-cols-3">
          {[0, 1, 2, 3, 4, 5].map(item => (
            <div key={item} className="space-y-2 border-b border-[var(--border-soft)] py-3 last:border-b-0 md:px-3">
              <div className="candy-skeleton h-2.5 w-16 rounded-md" />
              <div className="candy-skeleton h-3 w-28 rounded-md" />
            </div>
          ))}
        </div>
      </section>
    ))}
  </div>
);

const Info: React.FC<{ label: string; children: React.ReactNode; mono?: boolean }> = ({ label, children, mono }) => (
  <div className="min-w-0 border-b border-[var(--border-soft)] py-3 last:border-b-0 md:px-3">
    <div className="text-xs font-medium text-[var(--text-secondary)]">{label}</div>
    <div className={`mt-1 min-w-0 break-words text-sm text-[var(--text-primary)] ${mono ? 'font-mono text-xs' : ''}`}>{children}</div>
  </div>
);

const JsonPanel: React.FC<{ value: unknown; emptyText: string }> = ({ value, emptyText }) => (
  hasValue(value) ? (
    <pre className="max-h-[38rem] overflow-auto whitespace-pre-wrap break-all rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] p-4 font-mono text-xs leading-5 text-[var(--text-primary)]">
      {formatJSON(value)}
    </pre>
  ) : (
    <div className="py-16 text-center text-sm text-[var(--text-secondary)]">{emptyText}</div>
  )
);

const MediaPreview: React.FC<{ item: MediaItem }> = ({ item }) => {
  const [loaded, setLoaded] = useState(() => canAutoLoadMedia(item.url));
  return (
    <article className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)]">
      <div className="flex items-center justify-between gap-3 border-b border-[var(--border-soft)] px-3 py-2">
        <span className="min-w-0 truncate font-mono text-xs text-[var(--text-secondary)]" title={item.label}>{item.label}</span>
        <a href={item.url} target="_blank" rel="noopener noreferrer" title="打开原始媒体" className="shrink-0 rounded-lg p-1.5 text-[var(--text-secondary)] hover:bg-[var(--surface)] hover:text-[var(--primary)]">
          <ExternalLink size={15} />
        </a>
      </div>
      {!loaded ? (
        <div className="flex h-40 items-center justify-center bg-[var(--surface)] p-4">
          <button type="button" onClick={() => setLoaded(true)} className="inline-flex items-center gap-2 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm font-semibold text-[var(--primary)] hover:bg-[var(--primary-lighter)]">
            <FileJson size={16} />加载媒体预览
          </button>
        </div>
      ) : item.kind === 'image' ? (
        <a href={item.url} target="_blank" rel="noopener noreferrer" className="block bg-[var(--surface)]">
          <img src={item.url} alt={item.label} loading="lazy" className="h-64 w-full object-contain" />
        </a>
      ) : item.kind === 'video' ? (
        <video controls preload="metadata" className="aspect-video w-full bg-black" src={item.url} />
      ) : (
        <div className="p-4">
          <audio controls preload="metadata" className="w-full" src={item.url} />
        </div>
      )}
    </article>
  );
};

const TaskResult: React.FC<{ task: TaskDetail }> = ({ task }) => {
  const mediaItems = useMemo(() => extractMediaItems(task.result, task.capability), [task.capability, task.result]);
  return (
    <div className="space-y-5">
      {mediaItems.length > 0 && (
        <section>
          <h3 className="mb-3 text-sm font-bold text-[var(--text-primary)]">媒体预览</h3>
          <div className="grid gap-3 md:grid-cols-2">
            {mediaItems.map(item => <MediaPreview key={`${item.kind}:${item.url}`} item={item} />)}
          </div>
        </section>
      )}
      <section>
        <h3 className="mb-3 text-sm font-bold text-[var(--text-primary)]">结果数据</h3>
        <JsonPanel value={task.result} emptyText="任务尚未产生结果" />
      </section>
    </div>
  );
};

const TaskOverview: React.FC<{ task: TaskDetail; admin: boolean; onOpenCall: (callID: string) => void }> = ({ task, admin, onOpenCall }) => (
  <div className="space-y-5">
    <section>
      <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">任务</h3>
      <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2 xl:grid-cols-3">
        <Info label="状态"><StatusBadge status={task.status} /></Info>
        <Info label="进度">{clampProgress(task.progress)}%</Info>
        <Info label="费用">
          <span className="font-semibold">{formatMoney(task.cost)}</span>
          {task.refunded && <span className="ml-2 rounded-md bg-orange-100 px-2 py-1 text-xs font-semibold text-orange-700">已退回</span>}
        </Info>
        <Info label="能力">{task.capability_name || task.capability || '-'}</Info>
        <Info label="渠道">{task.channel || '-'}</Info>
        <Info label="任务 ID" mono>{task.task_no}</Info>
        {admin && <Info label="供应商任务 ID" mono>{task.vendor_task_id || '-'}</Info>}
        <Info label="调用 ID" mono>
          {task.call_id ? (
            <span className="flex min-w-0 items-center gap-2">
              <span className="min-w-0 flex-1 truncate" title={task.call_id}>{task.call_id}</span>
              <button type="button" onClick={() => onOpenCall(task.call_id!)} className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-[var(--border-soft)] px-2.5 py-1.5 font-sans text-xs font-semibold text-[var(--primary)] hover:bg-[var(--primary-lighter)]">
                <Activity size={13} />查看调用
              </button>
            </span>
          ) : '-'}
        </Info>
      </div>
    </section>

    {task.error && (
      <section className="rounded-lg border border-red-200 bg-red-50 p-4">
        <div className="flex items-center gap-2 text-sm font-bold text-red-700"><AlertTriangle size={16} />任务失败</div>
        <p className="mt-2 whitespace-pre-wrap break-words text-sm text-red-700">{task.error}</p>
      </section>
    )}

    <section>
      <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">执行</h3>
      <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2 xl:grid-cols-3">
        <Info label="创建时间">{formatDate(task.created_at)}</Info>
        <Info label="开始时间">{formatDate(task.started_at)}</Info>
        <Info label="完成时间">{formatDate(task.completed_at)}</Info>
        <Info label="回调状态">{task.callback_status ? <StatusBadge status={task.callback_status} /> : '-'}</Info>
        <Info label="回调尝试">{task.callback_attempts ?? 0} 次</Info>
      </div>
    </section>
  </div>
);

const Logs: React.FC = () => {
  const navigate = useNavigate();
  const admin = useMemo(getIsAdmin, []);
  const [logs, setLogs] = useState<TaskLog[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [isLoading, setIsLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [refreshKey, setRefreshKey] = useState(0);
  const [capabilities, setCapabilities] = useState<Capability[]>([]);
  const [draft, setDraft] = useState<FilterDraft>({ ...EMPTY_FILTERS });
  const [filters, setFilters] = useState<TaskListParams>({});
  const [selectedTask, setSelectedTask] = useState<TaskDetail | null>(null);
  const [isDrawerOpen, setIsDrawerOpen] = useState(false);
  const [loadingDetail, setLoadingDetail] = useState(false);
  const [detailError, setDetailError] = useState('');
  const [activeTab, setActiveTab] = useState<DetailTab>('overview');
  const listRequest = useRef(0);
  const detailRequest = useRef(0);
  const snapshotAt = useRef('');

  useEffect(() => {
    let active = true;
    fetchCapabilities()
      .then(items => { if (active) setCapabilities(items); })
      .catch(() => {});
    return () => { active = false; };
  }, []);

  useEffect(() => {
    let active = true;
    const requestNo = ++listRequest.current;
    setIsLoading(true);
    setLoadError('');

    // snapshot_at 冻结首次查询视图，后台任务持续写入时翻页仍不会重复或跳项。
    fetchTaskLogs({
      page,
      page_size: pageSize,
      ...filters,
      snapshot_at: snapshotAt.current || undefined,
    }).then(response => {
      if (!active || requestNo !== listRequest.current) return;
      setLogs(response.items || []);
      setTotal(response.total || 0);
      snapshotAt.current = response.snapshot_at || snapshotAt.current;
      const lastPage = Math.max(1, Math.ceil((response.total || 0) / pageSize));
      if (page > lastPage) setPage(lastPage);
    }).catch(error => {
      if (!active || requestNo !== listRequest.current) return;
      setLogs([]);
      setTotal(0);
      setLoadError(error instanceof Error ? error.message : '异步任务加载失败');
    }).finally(() => {
      if (active && requestNo === listRequest.current) setIsLoading(false);
    });

    return () => { active = false; };
  }, [filters, page, pageSize, refreshKey]);


  const updateDraft = <K extends keyof FilterDraft>(key: K, value: FilterDraft[K]) => {
    setDraft(current => ({ ...current, [key]: value }));
  };

  const search = () => {
    snapshotAt.current = '';
    setPage(1);
    setFilters({
      keyword: draft.task_no.trim() || undefined,
      capability: draft.capability || undefined,
      status: draft.status || undefined,
      token_id: admin ? parsePositive(draft.token_id) : undefined,
      start_date: draft.start_date || undefined,
      end_date: draft.end_date || undefined,
    });
  };

  const reset = () => {
    snapshotAt.current = '';
    setPage(1);
    setDraft({ ...EMPTY_FILTERS });
    setFilters({});
  };

  const refresh = () => {
    snapshotAt.current = '';
    setPage(1);
    setRefreshKey(value => value + 1);
  };

  const changePageSize = (value: number) => {
    snapshotAt.current = '';
    setPageSize(value);
    setPage(1);
  };

  const openDetails = async (task: TaskLog) => {
    const requestNo = ++detailRequest.current;
    // 关闭抽屉或切换任务会递增序号，迟到的详情响应不得覆盖当前选择。
    setIsDrawerOpen(true);
    setSelectedTask(null);
    setDetailError('');
    setLoadingDetail(true);
    setActiveTab('overview');
    try {
      const detail = await fetchTaskDetail(task.task_no);
      if (requestNo === detailRequest.current) setSelectedTask({ ...task, ...detail });
    } catch (error) {
      if (requestNo === detailRequest.current) {
        setDetailError(error instanceof Error ? error.message : '异步任务详情加载失败');
      }
    } finally {
      if (requestNo === detailRequest.current) setLoadingDetail(false);
    }
  };

  const closeDetails = () => {
    detailRequest.current += 1;
    setIsDrawerOpen(false);
    setSelectedTask(null);
    setDetailError('');
    setLoadingDetail(false);
  };

  const openCall = (callID: string) => {
    closeDetails();
    navigate(`/calls?call_id=${encodeURIComponent(callID)}`);
  };

  return (
    <div className="space-y-4">
      <PageHeader
        icon={Activity}
        title="异步任务"
        meta={isLoading ? '正在同步任务数据' : `共 ${total} 条记录`}
        actions={(
          <button
            type="button"
            title="刷新"
            aria-label="刷新"
            onClick={refresh}
            className="flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] text-[var(--text-secondary)] shadow-[var(--shadow-soft)] transition hover:border-[var(--primary)] hover:text-[var(--primary)]"
          >
            <RefreshCw size={17} className={isLoading ? 'animate-spin' : ''} />
          </button>
        )}
      />

      <section className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-4 shadow-[var(--shadow-soft)]">
        <form onSubmit={event => { event.preventDefault(); search(); }}>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            <label className="text-xs font-semibold text-[var(--text-secondary)]">任务 ID
              <input value={draft.task_no} onChange={event => updateDraft('task_no', event.target.value)} placeholder="task_..." className={`${INPUT_CLASS} mt-1 font-mono`} />
            </label>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">能力
              <Select value={draft.capability} onChange={v => updateDraft('capability', v)} className="mt-1" options={[{ label: '全部能力', value: '' }, ...capabilities.map(capability => ({ label: capability.name, value: capability.code }))]} />
            </div>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">状态
              <Select value={draft.status} onChange={v => updateDraft('status', v)} className="mt-1" options={[{ label: '全部状态', value: '' }, { label: '等待中', value: 'pending' }, { label: '处理中', value: 'processing' }, { label: '成功', value: 'success' }, { label: '失败', value: 'failed' }, { label: '已取消', value: 'cancelled' }]} />
            </div>
            {admin && (
              <label className="text-xs font-semibold text-[var(--text-secondary)]">Token ID
                <input type="number" min="1" value={draft.token_id} onChange={event => updateDraft('token_id', event.target.value)} placeholder="Token ID" className={`${INPUT_CLASS} mt-1`} />
              </label>
            )}
            <label className="text-xs font-semibold text-[var(--text-secondary)]">开始日期
              <input type="date" value={draft.start_date} onChange={event => updateDraft('start_date', event.target.value)} className={`${INPUT_CLASS} mt-1`} />
            </label>
            <label className="text-xs font-semibold text-[var(--text-secondary)]">结束日期
              <input type="date" value={draft.end_date} onChange={event => updateDraft('end_date', event.target.value)} className={`${INPUT_CLASS} mt-1`} />
            </label>
          </div>
          <div className="mt-4 flex justify-end gap-2">
            <button type="button" onClick={reset} className="flex items-center gap-2 rounded-lg border border-[var(--border-soft)] px-3 py-2 text-sm font-medium text-[var(--text-secondary)] transition hover:bg-[var(--surface-muted)] hover:text-[var(--text-primary)]"><RotateCcw size={15} />重置</button>
            <button type="submit" className="flex items-center gap-2 rounded-lg [background:var(--brand-gradient)] px-4 py-2 text-sm font-semibold text-white shadow-[0_5px_14px_var(--glow-color)] transition hover:brightness-[0.98]"><Search size={15} />查询</button>
          </div>
        </form>
      </section>

      <section aria-busy={isLoading} className="relative overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)]">
        {isLoading && (
          <div className="absolute inset-x-0 top-0 z-10 h-1 overflow-hidden bg-[var(--border-soft)]">
            <div className="candy-skeleton h-full w-2/5" />
          </div>
        )}
        {loadError && <div className="flex items-center gap-2 border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700"><AlertTriangle size={16} />{loadError}</div>}
        <div className="overflow-x-auto">
          <table className="w-full min-w-[1040px] text-left">
            <thead className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)] text-xs font-semibold text-[var(--text-secondary)]">
              <tr>
                <th className="px-4 py-3">时间 / 任务 ID</th>
                <th className="px-4 py-3">能力 / 渠道</th>
                <th className="px-4 py-3">状态 / 进度</th>
                <th className="px-4 py-3">关联调用</th>
                <th className="px-4 py-3 text-right">费用</th>
                <th className="px-4 py-3">完成时间</th>
                <th className="w-10 px-3 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border-soft)]">
              {isLoading ? <TaskTableSkeleton /> : logs.length === 0 ? (
                <tr><td colSpan={7} className="px-4 py-16 text-center text-sm text-[var(--text-secondary)]">暂无异步任务</td></tr>
              ) : logs.map(log => {
                const progress = clampProgress(log.progress);
                return (
                  <tr key={log.id} tabIndex={0} onClick={() => openDetails(log)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openDetails(log); } }} className="cursor-pointer transition hover:bg-[var(--surface-tint)] focus:bg-[var(--surface-tint)] focus:outline-none">
                    <td className="px-4 py-3">
                      <div className="text-sm text-[var(--text-primary)]">{formatDate(log.created_at)}</div>
                      <div className="mt-1 max-w-56 truncate font-mono text-[11px] text-[var(--text-secondary)]" title={log.task_no}>{log.task_no}</div>
                    </td>
                    <td className="px-4 py-3">
                      <div className="max-w-56 truncate text-sm font-semibold text-[var(--text-primary)]" title={log.capability_name || log.capability}>{log.capability_name || log.capability || '-'}</div>
                      <div className="mt-1 text-xs text-[var(--text-secondary)]">{log.channel || '-'}</div>
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge status={log.status} />
                      <div className="mt-2 flex w-36 items-center gap-2">
                        <div className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--border-soft)]"><div className="h-full [background:var(--brand-gradient)]" style={{ width: `${progress}%` }} /></div>
                        <span className="w-9 text-right text-xs text-[var(--text-secondary)]">{progress}%</span>
                      </div>
                      {log.error && <div className="mt-1 max-w-48 truncate text-xs text-red-600" title={log.error}>{log.error}</div>}
                    </td>
                    <td className="px-4 py-3"><div className="max-w-56 truncate font-mono text-xs text-[var(--text-secondary)]" title={log.call_id}>{log.call_id || '-'}</div></td>
                    <td className="px-4 py-3 text-right">
                      <div className="text-sm font-semibold text-[var(--text-primary)]">{formatMoney(log.cost)}</div>
                      {log.refunded && <div className="mt-1 text-xs font-semibold text-orange-700">已退回</div>}
                    </td>
                    <td className="px-4 py-3 text-sm text-[var(--text-secondary)]">{formatDate(log.completed_at)}</td>
                    <td className="px-3 py-3 text-right"><ChevronRight size={17} className="text-[var(--text-secondary)]" /></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <Pagination page={page} pageSize={pageSize} total={total} loading={isLoading} onPageChange={setPage} onPageSizeChange={changePageSize} />
      </section>

      <Drawer open={isDrawerOpen} onClose={closeDetails} title="异步任务详情" subtitle={<span className="font-mono">{selectedTask?.task_no || '加载中'}</span>} panelClassName="bg-[var(--surface-card)]">
            {selectedTask && (
              <div role="tablist" className="flex overflow-x-auto border-b border-[var(--border-soft)] px-4">
                {DETAIL_TABS.filter(tab => !tab.adminOnly || admin).map(tab => {
                  const Icon = tab.icon;
                  return (
                    <button key={tab.key} type="button" role="tab" aria-selected={activeTab === tab.key} onClick={() => setActiveTab(tab.key)} className={`flex shrink-0 items-center gap-2 border-b-2 px-4 py-3 text-sm font-semibold ${activeTab === tab.key ? 'border-[var(--primary)] text-[var(--primary)]' : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>
                      <Icon size={16} />{tab.label}
                    </button>
                  );
                })}
              </div>
            )}
            <div className="flex-1 overflow-y-auto p-5">
              {loadingDetail ? (
                <TaskDetailSkeleton />
              ) : detailError ? (
                <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700"><AlertTriangle size={17} />{detailError}</div>
              ) : selectedTask && activeTab === 'overview' ? (
                <TaskOverview task={selectedTask} admin={admin} onOpenCall={openCall} />
              ) : selectedTask && activeTab === 'request' ? (
                <JsonPanel value={selectedTask.raw_params} emptyText="未保存请求参数" />
              ) : selectedTask && activeTab === 'result' ? (
                <TaskResult task={selectedTask} />
              ) : selectedTask && activeTab === 'upstream' && admin ? (
                <JsonPanel value={selectedTask.vendor_response} emptyText="未保存上游响应" />
              ) : (
                <div className="py-16 text-center text-sm text-[var(--text-secondary)]">任务详情不可用</div>
              )}
            </div>
      </Drawer>
    </div>
  );
};

export default Logs;
