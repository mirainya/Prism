import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Activity,
  AlertTriangle,
  Ban,
  Braces,
  CheckCircle2,
  ChevronLeft,
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
  X,
  XCircle,
  type LucideIcon,
} from 'lucide-react';
import { fetchCapabilities, fetchTaskDetail, fetchTaskLogs, TaskListParams } from '../services/api';
import { Capability, TaskDetail, TaskLog, UserRole } from '../types';

const PAGE_SIZE = 20;
const INPUT_CLASS = 'w-full min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm text-[var(--text-primary)] outline-none transition focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--primary)]/20';

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
  const dialogRef = useRef<HTMLElement | null>(null);
  const returnFocusRef = useRef<HTMLElement | null>(null);

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

    fetchTaskLogs({
      page,
      page_size: PAGE_SIZE,
      ...filters,
      snapshot_at: snapshotAt.current || undefined,
    }).then(response => {
      if (!active || requestNo !== listRequest.current) return;
      setLogs(response.items || []);
      setTotal(response.total || 0);
      snapshotAt.current = response.snapshot_at || snapshotAt.current;
      const lastPage = Math.max(1, Math.ceil((response.total || 0) / PAGE_SIZE));
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
  }, [filters, page, refreshKey]);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

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

  const openDetails = async (task: TaskLog) => {
    const requestNo = ++detailRequest.current;
    returnFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
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
    window.setTimeout(() => returnFocusRef.current?.focus(), 0);
  };

  const handleDialogKeyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      closeDetails();
      return;
    }
    if (event.key !== 'Tab' || !dialogRef.current) return;
    const focusable = Array.from(dialogRef.current.querySelectorAll<HTMLElement>('button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'));
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  };

  const openCall = (callID: string) => {
    closeDetails();
    navigate(`/calls?call_id=${encodeURIComponent(callID)}`);
  };

  return (
    <div className="space-y-5">
      <header className="flex items-center justify-between gap-3">
        <h1 className="flex items-center gap-2 text-xl font-bold text-[var(--text-primary)] md:text-2xl"><Activity size={23} />异步任务</h1>
        <button type="button" title="刷新" aria-label="刷新" onClick={refresh} className="flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] text-[var(--text-primary)] hover:bg-[var(--surface)]">
          <RefreshCw size={17} className={isLoading ? 'animate-spin' : ''} />
        </button>
      </header>

      <section className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-4">
        <form onSubmit={event => { event.preventDefault(); search(); }}>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            <label className="text-xs font-semibold text-[var(--text-secondary)]">任务 ID
              <input value={draft.task_no} onChange={event => updateDraft('task_no', event.target.value)} placeholder="task_..." className={`${INPUT_CLASS} mt-1 font-mono`} />
            </label>
            <label className="text-xs font-semibold text-[var(--text-secondary)]">能力
              <select value={draft.capability} onChange={event => updateDraft('capability', event.target.value)} className={`${INPUT_CLASS} mt-1`}>
                <option value="">全部能力</option>
                {capabilities.map(capability => <option key={capability.code} value={capability.code}>{capability.name}</option>)}
              </select>
            </label>
            <label className="text-xs font-semibold text-[var(--text-secondary)]">状态
              <select value={draft.status} onChange={event => updateDraft('status', event.target.value)} className={`${INPUT_CLASS} mt-1`}>
                <option value="">全部状态</option>
                <option value="pending">等待中</option>
                <option value="processing">处理中</option>
                <option value="success">成功</option>
                <option value="failed">失败</option>
                <option value="cancelled">已取消</option>
              </select>
            </label>
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
            <button type="button" onClick={reset} className="flex items-center gap-2 rounded-lg border border-[var(--border-soft)] px-3 py-2 text-sm font-medium text-[var(--text-secondary)] hover:bg-[var(--surface)]"><RotateCcw size={15} />重置</button>
            <button type="submit" className="flex items-center gap-2 rounded-lg bg-[var(--primary)] px-4 py-2 text-sm font-semibold text-white hover:opacity-90"><Search size={15} />查询</button>
          </div>
        </form>
      </section>

      <section className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)]">
        {loadError && <div className="flex items-center gap-2 border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700"><AlertTriangle size={16} />{loadError}</div>}
        <div className="overflow-x-auto">
          <table className="w-full min-w-[1040px] text-left">
            <thead className="border-b border-[var(--border-soft)] bg-[var(--surface)]/60 text-xs text-[var(--text-secondary)]">
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
              {isLoading ? Array.from({ length: 7 }).map((_, index) => (
                <tr key={index} className="animate-pulse"><td colSpan={7} className="px-4 py-4"><div className="h-4 rounded bg-[var(--primary-lighter)]" /></td></tr>
              )) : logs.length === 0 ? (
                <tr><td colSpan={7} className="px-4 py-16 text-center text-sm text-[var(--text-secondary)]">暂无异步任务</td></tr>
              ) : logs.map(log => {
                const progress = clampProgress(log.progress);
                return (
                  <tr key={log.id} tabIndex={0} onClick={() => openDetails(log)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openDetails(log); } }} className="cursor-pointer transition hover:bg-[var(--primary-lighter)]/50 focus:bg-[var(--primary-lighter)]/50 focus:outline-none">
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
                        <div className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--border-soft)]"><div className="h-full bg-[var(--primary)]" style={{ width: `${progress}%` }} /></div>
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
        <footer className="flex items-center justify-between border-t border-[var(--border-soft)] px-4 py-3 text-sm text-[var(--text-secondary)]">
          <span>共 {total} 条，第 {page}/{totalPages} 页</span>
          <div className="flex gap-1">
            <button type="button" title="上一页" aria-label="上一页" disabled={page <= 1 || isLoading} onClick={() => setPage(value => Math.max(1, value - 1))} className="rounded-lg p-2 hover:bg-[var(--surface)] disabled:cursor-not-allowed disabled:opacity-40"><ChevronLeft size={17} /></button>
            <button type="button" title="下一页" aria-label="下一页" disabled={page >= totalPages || isLoading} onClick={() => setPage(value => Math.min(totalPages, value + 1))} className="rounded-lg p-2 hover:bg-[var(--surface)] disabled:cursor-not-allowed disabled:opacity-40"><ChevronRight size={17} /></button>
          </div>
        </footer>
      </section>

      {isDrawerOpen && (
        <div className="fixed inset-0 z-50 flex justify-end bg-black/40" onClick={closeDetails}>
          <aside ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby="task-detail-title" onKeyDown={handleDialogKeyDown} className="flex h-full w-full max-w-4xl flex-col bg-[var(--surface)] shadow-2xl" onClick={event => event.stopPropagation()}>
            <div className="flex items-start justify-between border-b border-[var(--border-soft)] px-5 py-4">
              <div className="min-w-0">
                <h2 id="task-detail-title" className="text-lg font-bold text-[var(--text-primary)]">异步任务详情</h2>
                <p className="mt-1 truncate font-mono text-xs text-[var(--text-secondary)]">{selectedTask?.task_no || '加载中'}</p>
              </div>
              <button type="button" autoFocus title="关闭" aria-label="关闭" onClick={closeDetails} className="shrink-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-2 text-[var(--text-primary)] hover:bg-[var(--primary-lighter)]"><X size={18} /></button>
            </div>
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
                <div className="space-y-4 animate-pulse"><div className="h-24 rounded-lg bg-[var(--primary-lighter)]" /><div className="h-48 rounded-lg bg-[var(--primary-lighter)]" /></div>
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
          </aside>
        </div>
      )}
    </div>
  );
};

export default Logs;
