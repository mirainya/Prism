import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  Ban,
  Braces,
  CheckCircle2,
  ChevronRight,
  CircleDot,
  Clapperboard,
  ExternalLink,
  FileJson,
  LayoutList,
  LoaderCircle,
  RefreshCw,
  RotateCcw,
  Search,
  Video,
  XCircle,
  type LucideIcon,
} from 'lucide-react';
import {
  fetchVideoChannels,
  fetchVideoTasks,
  getVideoTask,
  type VideoChannel,
  type VideoCallPayload,
  type VideoTask,
  type VideoTaskListParams,
} from '../services/videoApi';
import { Drawer, Pagination, Select } from '../components/ui';
import { PageHeader } from '../components/shell';

const DEFAULT_PAGE_SIZE = 20;
const INPUT_CLASS = 'mt-1 w-full min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm text-[var(--text-primary)] outline-none transition placeholder:text-[var(--text-tertiary)] focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--focus-ring)]';

type FilterDraft = {
  keyword: string;
  model: string;
  status: string;
  task_mode: string;
  service_tier: string;
  channel_id: string;
  user_id: string;
  token_id: string;
  start_date: string;
  end_date: string;
};

type DetailTab = 'overview' | 'request' | 'result' | 'upstream';

const EMPTY_FILTERS: FilterDraft = {
  keyword: '',
  model: '',
  status: '',
  task_mode: '',
  service_tier: '',
  channel_id: '',
  user_id: '',
  token_id: '',
  start_date: '',
  end_date: '',
};

const STATUS_CONFIG: Record<string, { label: string; className: string; icon: LucideIcon }> = {
  queued: { label: '排队中', className: 'bg-gray-100 text-gray-700', icon: CircleDot },
  submitted: { label: '已提交', className: 'bg-blue-100 text-blue-700', icon: LoaderCircle },
  tracking: { label: '处理中', className: 'bg-indigo-100 text-indigo-700', icon: LoaderCircle },
  completed: { label: '已完成', className: 'bg-green-100 text-green-700', icon: CheckCircle2 },
  failed: { label: '失败', className: 'bg-red-100 text-red-700', icon: XCircle },
  cancelled: { label: '已取消', className: 'bg-yellow-100 text-yellow-700', icon: Ban },
};

const TASK_MODE_LABELS: Record<string, string> = {
  text: '文生视频',
  first_frame: '首帧生视频',
  first_last_frame: '首尾帧生视频',
  multimodal: '多模态生视频',
  video_edit: '视频编辑',
  video_extension: '视频延长',
};

const SERVICE_TIER_LABELS: Record<string, string> = {
  standard: '标准',
  priority: '优先',
  vip: 'VIP',
};

const DETAIL_TABS: Array<{ key: DetailTab; label: string; icon: LucideIcon }> = [
  { key: 'overview', label: '概览', icon: LayoutList },
  { key: 'request', label: '请求参数', icon: Braces },
  { key: 'result', label: '结果', icon: Video },
  { key: 'upstream', label: '上游响应', icon: FileJson },
];

const parsePositive = (value: string) => {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
};

const clampProgress = (value: number) => Math.max(0, Math.min(100, Number(value) || 0));

const formatDate = (value?: string | null) => {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const pad = (part: number) => String(part).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
};

const formatMoney = (value: number | string | undefined) => {
  const amount = Number(value || 0);
  return Number.isFinite(amount) ? `¥${amount.toFixed(4)}` : `¥${String(value || 0)}`;
};

const formatJSON = (value: unknown) => {
  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return String(value);
  }
};

const parsePayload = (payload?: VideoCallPayload) => {
  if (!payload?.data) return undefined;
  try {
    return JSON.parse(payload.data);
  } catch {
    return payload.data;
  }
};

const hasValue = (value: unknown) => {
  if (value == null) return false;
  if (Array.isArray(value)) return value.length > 0;
  if (typeof value === 'object') return Object.keys(value).length > 0;
  return value !== '';
};

const extractVideoURLs = (value: unknown) => {
  const urls: string[] = [];
  const seen = new Set<string>();
  const visit = (current: unknown, hint = '') => {
    if (typeof current === 'string') {
      const path = current.toLowerCase().split(/[?#]/, 1)[0];
      const looksLikeVideo = /^https?:\/\//i.test(current) && (/\.(mp4|webm|mov|m4v|mkv)$/.test(path) || /video|output|result|url/i.test(hint));
      if (looksLikeVideo && !seen.has(current)) {
        seen.add(current);
        urls.push(current);
      }
      return;
    }
    if (Array.isArray(current)) {
      current.forEach(item => visit(item, hint));
      return;
    }
    if (!current || typeof current !== 'object') return;
    Object.entries(current as Record<string, unknown>).forEach(([key, item]) => visit(item, `${hint} ${key}`));
  };
  visit(value);
  return urls;
};

const StatusBadge: React.FC<{ status: string }> = ({ status }) => {
  const meta = STATUS_CONFIG[status] || {
    label: status || '未知',
    className: 'bg-gray-100 text-gray-700',
    icon: CircleDot,
  };
  const Icon = meta.icon;
  return (
    <span className={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-semibold ${meta.className}`}>
      <Icon size={13} className={status === 'submitted' || status === 'tracking' ? 'animate-spin' : ''} />
      {meta.label}
    </span>
  );
};

const Info: React.FC<{ label: string; children: React.ReactNode; mono?: boolean }> = ({ label, children, mono }) => (
  <div className="min-w-0 border-b border-[var(--border-soft)] py-3 last:border-b-0 md:px-3">
    <div className="text-xs font-medium text-[var(--text-secondary)]">{label}</div>
    <div className={`mt-1 min-w-0 break-words text-sm text-[var(--text-primary)] ${mono ? 'font-mono text-xs' : ''}`}>{children || '-'}</div>
  </div>
);

const JsonPanel: React.FC<{ value: unknown; emptyText: string }> = ({ value, emptyText }) => (
  hasValue(value) ? (
    <pre className="max-h-[38rem] overflow-auto whitespace-pre-wrap break-all rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-4 font-mono text-xs leading-5 text-[var(--text-primary)]">
      {formatJSON(value)}
    </pre>
  ) : (
    <div className="py-16 text-center text-sm text-[var(--text-secondary)]">{emptyText}</div>
  )
);

const VideoPreview: React.FC<{ url: string }> = ({ url }) => {
  const [loaded, setLoaded] = useState(false);
  return (
    <div className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-black">
      <div className="flex aspect-video items-center justify-center">
        {loaded ? (
          <video src={url} controls preload="metadata" className="h-full w-full object-contain" />
        ) : (
          <button type="button" onClick={() => setLoaded(true)} className="inline-flex items-center gap-2 rounded-lg bg-white/10 px-4 py-2 text-sm font-semibold text-white hover:bg-white/20">
            <Video size={17} />加载预览
          </button>
        )}
      </div>
      <a href={url} target="_blank" rel="noreferrer" className="flex items-center gap-2 border-t border-white/10 px-3 py-2 text-xs text-gray-200 hover:bg-white/10">
        <span className="min-w-0 flex-1 truncate">{url}</span><ExternalLink size={14} />
      </a>
    </div>
  );
};

const TABLE_SKELETON_ROWS = [
  ['w-40', 'w-32', 'w-28', 'w-24', 'w-28', 'w-20', 'w-16', 'w-32'],
  ['w-36', 'w-40', 'w-24', 'w-28', 'w-24', 'w-24', 'w-14', 'w-28'],
  ['w-44', 'w-28', 'w-32', 'w-20', 'w-32', 'w-20', 'w-16', 'w-36'],
  ['w-40', 'w-36', 'w-28', 'w-24', 'w-24', 'w-24', 'w-14', 'w-32'],
  ['w-36', 'w-32', 'w-24', 'w-28', 'w-28', 'w-20', 'w-16', 'w-28'],
];

const VideoTaskTableSkeleton: React.FC = () => (
  <>
    {TABLE_SKELETON_ROWS.map((widths, rowIndex) => (
      <tr key={rowIndex} aria-hidden="true">
        {widths.map((width, columnIndex) => (
          <td key={columnIndex} className="px-4 py-4">
            <div className={`candy-skeleton h-3 rounded-md ${width}`} />
            {columnIndex < 6 && <div className="candy-skeleton mt-2 h-2.5 w-20 rounded-md opacity-70" />}
          </td>
        ))}
        <td className="px-3 py-4"><div className="candy-skeleton h-5 w-5 rounded-md" /></td>
      </tr>
    ))}
  </>
);

const VideoTaskDetailSkeleton: React.FC = () => (
  <div role="status" aria-label="任务详情加载中" className="space-y-6">
    <div className="space-y-3 border-b border-[var(--border-soft)] pb-6">
      <div className="candy-skeleton h-6 w-24 rounded-md" />
      <div className="candy-skeleton h-2 w-full rounded-full" />
    </div>
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

const TaskOverview: React.FC<{ task: VideoTask; channelName: string }> = ({ task, channelName }) => {
  const progress = clampProgress(task.progress);
  const chargedCost = Number(task.final_cost || 0) > 0 ? task.final_cost : task.estimated_cost;
  return (
    <div className="space-y-6">
      <section>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <StatusBadge status={task.status} />
          <span className="text-sm font-semibold text-[var(--text-primary)]">{progress}%</span>
        </div>
        <div className="mt-3 h-2 overflow-hidden rounded-full bg-[var(--border-soft)]">
          <div className="h-full bg-[var(--primary)] transition-all" style={{ width: `${progress}%` }} />
        </div>
      </section>

      {task.error_message && (
        <section className="rounded-lg border border-red-200 bg-red-50 p-4">
          <div className="flex items-center gap-2 text-sm font-bold text-red-700"><AlertTriangle size={16} />任务失败</div>
          <p className="mt-2 whitespace-pre-wrap break-words text-sm text-red-700">{task.error_message}</p>
        </section>
      )}

      <section>
        <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">任务</h3>
        <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2 xl:grid-cols-3">
          <Info label="任务 ID" mono>{task.id}</Info>
          <Info label="调用 ID" mono>{task.call_id}</Info>
          <Info label="上游任务 ID" mono>{task.provider_task_id}</Info>
          <Info label="模型">{task.model}</Info>
          <Info label="上游模型">{task.vendor_model}</Info>
          <Info label="任务类型">{TASK_MODE_LABELS[task.task_mode] || task.task_mode}</Info>
          <Info label="服务档位">{SERVICE_TIER_LABELS[task.service_tier] || task.service_tier || '标准'}</Info>
          <Info label="协议">{task.adapter_type}</Info>
          <Info label="轮询次数">{task.poll_count ?? 0}</Info>
        </div>
      </section>

      <section>
        <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">生成规格</h3>
        <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2 xl:grid-cols-3">
          <Info label="分辨率">{task.resolution}</Info>
          <Info label="画面比例">{task.ratio}</Info>
          <Info label="时长">{task.duration ? `${task.duration} 秒` : '-'}</Info>
          <Info label="生成音频">{task.generate_audio ? '是' : '否'}</Info>
        </div>
      </section>

      {task.prompt && (
        <section>
          <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">提示词</h3>
          <div className="whitespace-pre-wrap break-words border-y border-[var(--border-soft)] py-3 text-sm leading-6 text-[var(--text-primary)]">{task.prompt}</div>
        </section>
      )}

      <section>
        <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">归属与计费</h3>
        <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2 xl:grid-cols-3">
          <Info label="渠道">{channelName || `#${task.channel_id}`}</Info>
          <Info label="渠道 Key">#{task.key_id}</Info>
          <Info label="用户 ID">#{task.user_id}</Info>
          <Info label="Token ID">#{task.token_id}</Info>
          <Info label="费用">{formatMoney(chargedCost)}</Info>
          <Info label="计费状态">{task.billing_status}</Info>
        </div>
      </section>

      <section>
        <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">执行时间</h3>
        <div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2 xl:grid-cols-3">
          <Info label="创建时间">{formatDate(task.created_at)}</Info>
          <Info label="提交时间">{formatDate(task.submitted_at)}</Info>
          <Info label="完成时间">{formatDate(task.completed_at)}</Info>
        </div>
      </section>
    </div>
  );
};

const TaskResult: React.FC<{ task: VideoTask }> = ({ task }) => {
  const videoURLs = useMemo(() => extractVideoURLs(task.result_json), [task.result_json]);
  return (
    <div className="space-y-5">
      {videoURLs.length > 0 && (
        <section className="grid gap-4 xl:grid-cols-2">
          {videoURLs.map(url => <VideoPreview key={url} url={url} />)}
        </section>
      )}
      <JsonPanel value={task.result_json} emptyText="暂无任务结果" />
    </div>
  );
};

const TaskRequest: React.FC<{ task: VideoTask }> = ({ task }) => {
  const payloads = task.call_payloads || [];
  const clientPayload = [...payloads].reverse().find(payload => payload.kind === 'request');
  const upstreamPayloads = payloads.filter(payload => payload.kind === 'upstream_request');
  const clientRequest = parsePayload(clientPayload) ?? {
    model: task.model,
    prompt: task.prompt,
    resolution: task.resolution,
    ratio: task.ratio,
    duration: task.duration,
    generate_audio: task.generate_audio,
    task_mode: task.task_mode,
    service_tier: task.service_tier,
    content: task.content_json,
    params: task.params_json,
  };
  return (
    <div className="space-y-6">
      <section>
        <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">调用参数</h3>
        <JsonPanel value={clientRequest} emptyText="未保存调用参数" />
      </section>
      <section>
        <h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">实际上游请求</h3>
        {upstreamPayloads.length === 0 ? (
          <div className="py-12 text-center text-sm text-[var(--text-secondary)]">该任务未记录实际上游请求</div>
        ) : (
          <div className="space-y-4">
            {upstreamPayloads.map(payload => (
              <div key={payload.id}>
                <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-[var(--text-secondary)]">
                  <span>尝试 #{payload.attempt_id || '-'}</span>
                  <span>·</span>
                  <span>{formatDate(payload.created_at)}</span>
                  {payload.truncated && <span className="rounded-md bg-yellow-100 px-2 py-1 font-semibold text-yellow-700">已截断</span>}
                </div>
                <JsonPanel value={parsePayload(payload)} emptyText="上游请求为空" />
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
};

const VideoTasks: React.FC = () => {
  const [tasks, setTasks] = useState<VideoTask[]>([]);
  const [channels, setChannels] = useState<VideoChannel[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [draft, setDraft] = useState<FilterDraft>({ ...EMPTY_FILTERS });
  const [filters, setFilters] = useState<VideoTaskListParams>({});
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [refreshKey, setRefreshKey] = useState(0);
  const [selectedTask, setSelectedTask] = useState<VideoTask | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState('');
  const [activeTab, setActiveTab] = useState<DetailTab>('overview');
  const listRequest = useRef(0);
  const detailRequest = useRef(0);
  const snapshotAt = useRef('');

  useEffect(() => {
    let active = true;
    fetchVideoChannels().then(items => { if (active) setChannels(items); }).catch(() => {});
    return () => { active = false; };
  }, []);

  useEffect(() => {
    let active = true;
    const requestNo = ++listRequest.current;
    setLoading(true);
    setLoadError('');
    fetchVideoTasks({
      page,
      page_size: pageSize,
      ...filters,
      snapshot_at: snapshotAt.current || undefined,
    }).then(response => {
      if (!active || requestNo !== listRequest.current) return;
      setTasks(response.items || []);
      setTotal(response.total || 0);
      snapshotAt.current = response.snapshot_at || snapshotAt.current;
      const lastPage = Math.max(1, Math.ceil((response.total || 0) / pageSize));
      if (page > lastPage) setPage(lastPage);
    }).catch(error => {
      if (!active || requestNo !== listRequest.current) return;
      setTasks([]);
      setTotal(0);
      setLoadError(error instanceof Error ? error.message : '视频任务加载失败');
    }).finally(() => {
      if (active && requestNo === listRequest.current) setLoading(false);
    });
    return () => { active = false; };
  }, [filters, page, pageSize, refreshKey]);

  const channelNames = useMemo(() => new Map(channels.map(channel => [channel.id, channel.name])), [channels]);

  const updateDraft = <K extends keyof FilterDraft>(key: K, value: FilterDraft[K]) => {
    setDraft(current => ({ ...current, [key]: value }));
  };

  const search = () => {
    snapshotAt.current = '';
    setPage(1);
    setFilters({
      keyword: draft.keyword.trim() || undefined,
      model: draft.model.trim() || undefined,
      status: draft.status || undefined,
      task_mode: draft.task_mode || undefined,
      service_tier: draft.service_tier || undefined,
      channel_id: parsePositive(draft.channel_id),
      user_id: parsePositive(draft.user_id),
      token_id: parsePositive(draft.token_id),
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
    fetchVideoChannels().then(setChannels).catch(() => {});
  };

  const changePageSize = (value: number) => {
    snapshotAt.current = '';
    setPageSize(value);
    setPage(1);
  };

  const openDetails = async (task: VideoTask) => {
    const requestNo = ++detailRequest.current;
    setDrawerOpen(true);
    setSelectedTask(task);
    setDetailLoading(true);
    setDetailError('');
    setActiveTab('overview');
    try {
      const detail = await getVideoTask(task.id);
      if (requestNo === detailRequest.current) setSelectedTask(detail);
    } catch (error) {
      if (requestNo === detailRequest.current) {
        setDetailError(error instanceof Error ? error.message : '视频任务详情加载失败');
      }
    } finally {
      if (requestNo === detailRequest.current) setDetailLoading(false);
    }
  };

  const closeDetails = () => {
    detailRequest.current += 1;
    setDrawerOpen(false);
    setSelectedTask(null);
    setDetailLoading(false);
    setDetailError('');
  };

  return (
    <div className="space-y-4">
      <PageHeader
        icon={Clapperboard}
        title="视频任务"
        meta={loading ? '正在同步任务数据' : `共 ${total} 条记录`}
        actions={(
          <button
            type="button"
            title="刷新"
            aria-label="刷新"
            onClick={refresh}
            className="flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] text-[var(--text-secondary)] shadow-[var(--shadow-soft)] transition hover:border-[var(--primary)] hover:text-[var(--primary)]"
          >
            <RefreshCw size={17} className={loading ? 'animate-spin' : ''} />
          </button>
        )}
      />

      <section className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-4 shadow-[var(--shadow-soft)]">
        <form onSubmit={event => { event.preventDefault(); search(); }}>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-5">
            <label className="text-xs font-semibold text-[var(--text-secondary)]">任务 ID
              <input value={draft.keyword} onChange={event => updateDraft('keyword', event.target.value)} placeholder="任务 / 调用 / 上游 ID" className={`${INPUT_CLASS} font-mono`} />
            </label>
            <label className="text-xs font-semibold text-[var(--text-secondary)]">模型
              <input value={draft.model} onChange={event => updateDraft('model', event.target.value)} placeholder="模型关键字" className={INPUT_CLASS} />
            </label>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">状态
              <Select value={draft.status} onChange={value => updateDraft('status', value)} className="mt-1" options={[{ label: '全部状态', value: '' }, ...Object.entries(STATUS_CONFIG).map(([value, meta]) => ({ label: meta.label, value }))]} />
            </div>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">任务类型
              <Select value={draft.task_mode} onChange={value => updateDraft('task_mode', value)} className="mt-1" options={[{ label: '全部类型', value: '' }, ...Object.entries(TASK_MODE_LABELS).map(([value, label]) => ({ label, value }))]} />
            </div>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">服务档位
              <Select value={draft.service_tier} onChange={value => updateDraft('service_tier', value)} className="mt-1" options={[{ label: '全部档位', value: '' }, ...Object.entries(SERVICE_TIER_LABELS).map(([value, label]) => ({ label, value }))]} />
            </div>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">渠道
              <Select value={draft.channel_id} onChange={value => updateDraft('channel_id', value)} className="mt-1" options={[{ label: '全部渠道', value: '' }, ...channels.map(channel => ({ label: channel.name, value: String(channel.id) }))]} />
            </div>
            <label className="text-xs font-semibold text-[var(--text-secondary)]">用户 ID
              <input type="number" min="1" value={draft.user_id} onChange={event => updateDraft('user_id', event.target.value)} placeholder="用户 ID" className={INPUT_CLASS} />
            </label>
            <label className="text-xs font-semibold text-[var(--text-secondary)]">Token ID
              <input type="number" min="1" value={draft.token_id} onChange={event => updateDraft('token_id', event.target.value)} placeholder="Token ID" className={INPUT_CLASS} />
            </label>
            <label className="text-xs font-semibold text-[var(--text-secondary)]">开始日期
              <input type="date" value={draft.start_date} onChange={event => updateDraft('start_date', event.target.value)} className={INPUT_CLASS} />
            </label>
            <label className="text-xs font-semibold text-[var(--text-secondary)]">结束日期
              <input type="date" value={draft.end_date} onChange={event => updateDraft('end_date', event.target.value)} className={INPUT_CLASS} />
            </label>
          </div>
          <div className="mt-4 flex justify-end gap-2">
            <button type="button" onClick={reset} className="flex items-center gap-2 rounded-lg border border-[var(--border-soft)] px-3 py-2 text-sm font-medium text-[var(--text-secondary)] transition hover:bg-[var(--surface-muted)] hover:text-[var(--text-primary)]"><RotateCcw size={15} />重置</button>
            <button type="submit" className="flex items-center gap-2 rounded-lg [background:var(--brand-gradient)] px-4 py-2 text-sm font-semibold text-white shadow-[0_5px_14px_var(--glow-color)] transition hover:brightness-[0.98]"><Search size={15} />查询</button>
          </div>
        </form>
      </section>

      <section aria-busy={loading} className="relative overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)]">
        {loading && (
          <div className="absolute inset-x-0 top-0 z-10 h-1 overflow-hidden bg-[var(--border-soft)]">
            <div className="candy-skeleton h-full w-2/5" />
          </div>
        )}
        {loadError && <div className="flex items-center gap-2 border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700"><AlertTriangle size={16} />{loadError}</div>}
        <div className="overflow-x-auto">
          <table className="w-full min-w-[1180px] text-left">
            <thead className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)] text-xs font-semibold text-[var(--text-secondary)]">
              <tr>
                <th className="px-4 py-3">时间 / 任务 ID</th>
                <th className="px-4 py-3">模型 / 类型</th>
                <th className="w-40 min-w-40 whitespace-nowrap px-4 py-3">生成规格</th>
                <th className="px-4 py-3">状态 / 进度</th>
                <th className="px-4 py-3">渠道 / Key</th>
                <th className="px-4 py-3">用户 / Token</th>
                <th className="px-4 py-3 text-right">费用</th>
                <th className="px-4 py-3">完成时间</th>
                <th className="w-10 px-3 py-3" />
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--border-soft)]">
              {loading ? <VideoTaskTableSkeleton /> : tasks.length === 0 ? (
                <tr><td colSpan={9} className="px-4 py-16 text-center text-sm text-[var(--text-secondary)]">暂无视频任务</td></tr>
              ) : tasks.map(task => {
                const progress = clampProgress(task.progress);
                const channelName = channelNames.get(task.channel_id) || `渠道 #${task.channel_id}`;
                const cost = Number(task.final_cost || 0) > 0 ? task.final_cost : task.estimated_cost;
                return (
                  <tr key={task.id} tabIndex={0} onClick={() => openDetails(task)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openDetails(task); } }} className="cursor-pointer transition hover:bg-[var(--surface-tint)] focus:bg-[var(--surface-tint)] focus:outline-none">
                    <td className="px-4 py-3">
                      <div className="text-sm text-[var(--text-primary)]">{formatDate(task.created_at)}</div>
                      <div className="mt-1 max-w-56 truncate font-mono text-[11px] text-[var(--text-secondary)]" title={task.id}>{task.id}</div>
                    </td>
                    <td className="px-4 py-3">
                      <div className="max-w-56 truncate text-sm font-semibold text-[var(--text-primary)]" title={task.model}>{task.model || '-'}</div>
                      <div className="mt-1 text-xs text-[var(--text-secondary)]">{TASK_MODE_LABELS[task.task_mode] || task.task_mode || '-'}</div>
                    </td>
                    <td className="w-40 min-w-40 px-4 py-3">
                      <div className="whitespace-nowrap text-sm text-[var(--text-primary)]">{task.resolution || '-'} · {task.ratio || '-'}</div>
                      <div className="mt-1 whitespace-nowrap text-xs text-[var(--text-secondary)]">{task.duration ? `${task.duration} 秒` : '-'} · {task.generate_audio ? '含音频' : '无音频'}</div>
                    </td>
                    <td className="px-4 py-3">
                      <StatusBadge status={task.status} />
                      <div className="mt-2 flex w-36 items-center gap-2">
                        <div className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-[var(--border-soft)]"><div className="h-full [background:var(--brand-gradient)]" style={{ width: `${progress}%` }} /></div>
                        <span className="w-9 text-right text-xs text-[var(--text-secondary)]">{progress}%</span>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <div className="max-w-48 truncate text-sm text-[var(--text-primary)]" title={channelName}>{channelName}</div>
                      <div className="mt-1 text-xs text-[var(--text-secondary)]">Key #{task.key_id}</div>
                    </td>
                    <td className="px-4 py-3 text-xs text-[var(--text-secondary)]">
                      <div>用户 #{task.user_id}</div><div className="mt-1">Token #{task.token_id}</div>
                    </td>
                    <td className="px-4 py-3 text-right">
                      <div className="text-sm font-semibold text-[var(--text-primary)]">{formatMoney(cost)}</div>
                      {task.billing_status === 'refunded' && <div className="mt-1 text-xs font-semibold text-orange-700">已退回</div>}
                    </td>
                    <td className="px-4 py-3 text-sm text-[var(--text-secondary)]">{formatDate(task.completed_at)}</td>
                    <td className="px-3 py-3 text-right"><ChevronRight size={17} className="text-[var(--text-secondary)]" /></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} onPageSizeChange={changePageSize} />
      </section>

      <Drawer open={drawerOpen} onClose={closeDetails} title="视频任务详情" subtitle={<span className="font-mono">{selectedTask?.id || '加载中'}</span>} panelClassName="bg-[var(--surface-card)]">
        {selectedTask && (
          <div role="tablist" className="flex overflow-x-auto border-b border-[var(--border-soft)] px-4">
            {DETAIL_TABS.map(tab => {
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
          {detailLoading ? (
            <VideoTaskDetailSkeleton />
          ) : detailError ? (
            <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700"><AlertTriangle size={17} />{detailError}</div>
          ) : selectedTask && activeTab === 'overview' ? (
            <TaskOverview task={selectedTask} channelName={channelNames.get(selectedTask.channel_id) || ''} />
          ) : selectedTask && activeTab === 'request' ? (
            <TaskRequest task={selectedTask} />
          ) : selectedTask && activeTab === 'result' ? (
            <TaskResult task={selectedTask} />
          ) : selectedTask && activeTab === 'upstream' ? (
            <JsonPanel value={{ response: selectedTask.provider_response, metadata: selectedTask.provider_metadata }} emptyText="未保存上游响应" />
          ) : (
            <div className="py-16 text-center text-sm text-[var(--text-secondary)]">任务详情不可用</div>
          )}
        </div>
      </Drawer>
    </div>
  );
};

export default VideoTasks;
