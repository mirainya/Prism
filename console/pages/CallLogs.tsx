import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  Activity,
  AlertTriangle,
  Ban,
  Check,
  CheckCircle2,
  ChevronRight,
  CircleDot,
  Copy,
  FileJson,
  LayoutList,
  LoaderCircle,
  ReceiptText,
  RefreshCw,
  RotateCcw,
  Route as RouteIcon,
  Search,
  XCircle,
  type LucideIcon,
} from 'lucide-react';
import {
  APICall,
  APICallAttempt,
  APICallBillingLog,
  APICallDetail,
  APICallPayload,
  APICallRouteKind,
  APICallStatus,
  CallListParams,
  fetchChannelAccounts,
  fetchChannelCapabilities,
  fetchCallDetail,
  fetchCalls,
  fetchUsers,
} from '../services';
import { fetchGwChannels, GW_TRANSPORTS, GwChannel } from '../services/gatewayApi';
import { fetchVideoChannels, type VideoChannel } from '../services/videoApi';
import { Drawer, Pagination, Select } from '../components/ui';
import { PageHeader } from '../components/shell';
import { ChannelAccount, ChannelCapability, User, UserRole } from '../types';

const DEFAULT_PAGE_SIZE = 20;
const INPUT_CLASS = 'w-full min-w-0 rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] px-3 py-2 text-sm text-[var(--text-primary)] outline-none transition placeholder:text-[var(--text-tertiary)] focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--focus-ring)]';

const ENDPOINT_LABELS: Record<string, string> = {
  '/v1/chat/completions': 'OpenAI Chat',
  '/v1/messages': 'Anthropic Messages',
  '/v1/responses': 'OpenAI Responses',
};

const TRANSPORT_LABELS: Record<string, string> = {
  openai_chat: 'OpenAI Chat',
  openai_responses: 'OpenAI Responses',
  anthropic_messages: 'Anthropic Messages',
  google_generate_content: 'Google GenerateContent',
  volcengine_responses_v3: 'Volcengine Responses V3',
  video_generation: '视频生成',
};

const CALL_TRANSPORTS = [...GW_TRANSPORTS, 'video_generation'];

const ROUTE_KIND_LABELS: Record<string, string> = {
  gateway_v2: 'Gateway',
  capability: '能力任务',
  video: '视频任务',
};

const PAYLOAD_KIND_LABELS: Record<string, string> = {
  request: '调用参数',
  response: '调用响应',
  upstream_request: '实际上游请求',
  upstream_response: '实际上游响应',
};

const STAGE_LABELS: Record<string, string> = {
  submit: '提交',
  poll: '轮询',
};

const STATUS_META: Record<string, { label: string; className: string; icon: LucideIcon }> = {
  received: { label: '已接收', className: 'bg-gray-100 text-gray-700', icon: CircleDot },
  in_progress: { label: '处理中', className: 'bg-blue-100 text-blue-700', icon: LoaderCircle },
  started: { label: '执行中', className: 'bg-blue-100 text-blue-700', icon: LoaderCircle },
  completed: { label: '成功', className: 'bg-green-100 text-green-700', icon: CheckCircle2 },
  failed: { label: '失败', className: 'bg-red-100 text-red-700', icon: XCircle },
  cancelled: { label: '已取消', className: 'bg-yellow-100 text-yellow-700', icon: Ban },
};

type FilterDraft = {
  call_id: string;
  request_id: string;
  status: APICallStatus | '';
  endpoint: string;
  model: string;
  start_date: string;
  end_date: string;
  user_id: string;
  token_id: string;
  route_kind: APICallRouteKind | '';
  channel_id: string;
  transport: string;
};

const EMPTY_FILTERS: FilterDraft = {
  call_id: '', request_id: '', status: '', endpoint: '', model: '', start_date: '', end_date: '',
  user_id: '', token_id: '', route_kind: '', channel_id: '', transport: '',
};

const formatMoney = (value: string | undefined) => {
  const raw = (value || '0').trim();
  const match = /^([+-]?)(\d+)(?:\.(\d*))?$/.exec(raw);
  if (!match) return `¥${raw}`;
  const [, sign, integer, fraction = ''] = match;
  return `¥${sign}${integer}.${fraction.padEnd(8, '0')}`;
};
const formatDuration = (ms: number) => ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${ms || 0}ms`;
const formatDate = (value?: string | null) => {
  if (!value) return '-';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false });
};
const endpointLabel = (endpoint: string) => {
  const knownLabel = ENDPOINT_LABELS[endpoint];
  if (knownLabel) return knownLabel;
  const capabilityPrefix = '/v1/capabilities/';
  if (endpoint.startsWith(capabilityPrefix)) {
    return `动态能力：${endpoint.slice(capabilityPrefix.length) || '-'}`;
  }
  return endpoint || '-';
};
const parsePositive = (value: string) => {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
};
const formatData = (value: unknown) => {
  if (typeof value !== 'string') return JSON.stringify(value, null, 2);
  try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; }
};

const StatusBadge: React.FC<{ status: string }> = ({ status }) => {
  const meta = STATUS_META[status] || { label: status || '未知', className: 'bg-gray-100 text-gray-700', icon: CircleDot };
  const Icon = meta.icon;
  const spinning = status === 'in_progress' || status === 'started';
  return (
    <span className={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-semibold ${meta.className}`}>
      <Icon size={13} className={spinning ? 'animate-spin' : ''} />{meta.label}
    </span>
  );
};

const CALL_TABLE_SKELETON = [
  ['w-40', 'w-32', 'w-40', 'w-16', 'w-20', 'w-16', 'w-20'],
  ['w-36', 'w-40', 'w-36', 'w-20', 'w-16', 'w-20', 'w-16'],
  ['w-44', 'w-36', 'w-44', 'w-16', 'w-20', 'w-16', 'w-20'],
  ['w-40', 'w-32', 'w-36', 'w-20', 'w-16', 'w-20', 'w-16'],
  ['w-36', 'w-40', 'w-40', 'w-16', 'w-20', 'w-16', 'w-20'],
];

const CallTableSkeleton: React.FC = () => (
  <>
    {CALL_TABLE_SKELETON.map((widths, rowIndex) => (
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

const CallDetailSkeleton: React.FC = () => (
  <div role="status" aria-label="调用详情加载中" className="space-y-6">
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
  <div className="min-w-0 py-2">
    <div className="mb-1 text-[11px] font-semibold text-[var(--text-secondary)]">{label}</div>
    <div className={`break-words text-sm text-[var(--text-primary)] ${mono ? 'font-mono' : ''}`}>{children || '-'}</div>
  </div>
);

const CallLogs: React.FC = () => {
  const [searchParams] = useSearchParams();
  const initialCallID = searchParams.get('call_id')?.trim() || '';
  const [calls, setCalls] = useState<APICall[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [draft, setDraft] = useState<FilterDraft>({ ...EMPTY_FILTERS, call_id: initialCallID });
  const [filters, setFilters] = useState<FilterDraft>({ ...EMPTY_FILTERS, call_id: initialCallID });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshKey, setRefreshKey] = useState(0);
  const [detail, setDetail] = useState<APICallDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState('');
  const [activeTab, setActiveTab] = useState<'overview' | 'attempts' | 'billing' | 'payloads'>('overview');
  const [copied, setCopied] = useState(false);
  const [channels, setChannels] = useState<GwChannel[]>([]);
  const [videoChannels, setVideoChannels] = useState<VideoChannel[]>([]);
  const [capabilityEndpoints, setCapabilityEndpoints] = useState<ChannelCapability[]>([]);
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const detailRequest = useRef(0);
  const snapshotAt = useRef('');

  const isAdmin = useMemo(() => {
    try { return JSON.parse(localStorage.getItem('prism_user') || '{}').role === UserRole.ADMIN; } catch { return false; }
  }, []);
  const channelNames = useMemo(() => new Map(channels.map(channel => [channel.id, channel.name])), [channels]);
  const videoChannelNames = useMemo(() => new Map(videoChannels.map(channel => [channel.id, channel.name])), [videoChannels]);
  const endpointMap = useMemo(
    () => new Map(capabilityEndpoints.map(endpoint => [Number(endpoint.id), endpoint])),
    [capabilityEndpoints],
  );
  const accountNames = useMemo(
    () => new Map(accounts.map(account => [Number(account.id), account.name])),
    [accounts],
  );
  const capabilityChannels = useMemo(() => {
    const items = new Map<number, string>();
    capabilityEndpoints.forEach(endpoint => {
      const channelID = Number(endpoint.channelId);
      if (channelID > 0 && !items.has(channelID)) {
        items.set(channelID, endpoint.channel?.name || `能力渠道 ${channelID}`);
      }
    });
    return Array.from(items, ([id, name]) => ({ id, name })).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'));
  }, [capabilityEndpoints]);
  const filterChannels = draft.route_kind === 'gateway_v2' ? channels : draft.route_kind === 'capability' ? capabilityChannels : draft.route_kind === 'video' ? videoChannels : [];

  useEffect(() => {
    if (!isAdmin) return;
    Promise.all([
      fetchGwChannels().then(setChannels).catch(() => setChannels([])),
      fetchVideoChannels().then(setVideoChannels).catch(() => setVideoChannels([])),
      fetchChannelCapabilities().then(setCapabilityEndpoints).catch(() => setCapabilityEndpoints([])),
      fetchChannelAccounts().then(setAccounts).catch(() => setAccounts([])),
      fetchUsers().then(setUsers).catch(() => setUsers([])),
    ]);
  }, [isAdmin]);

  useEffect(() => {
    let active = true;
    // 服务端返回的 snapshot_at 在本轮筛选内复用，刷新或重新查询时才重新取快照。
    const params: CallListParams = {
      page, page_size: pageSize,
      snapshot_at: snapshotAt.current || undefined,
      call_id: filters.call_id.trim(), request_id: filters.request_id.trim(), status: filters.status,
      endpoint: filters.endpoint.trim(), model: filters.model.trim(),
      start_date: filters.start_date, end_date: filters.end_date,
      ...(isAdmin ? {
        user_id: parsePositive(filters.user_id), token_id: parsePositive(filters.token_id),
        route_kind: filters.route_kind, channel_id: parsePositive(filters.channel_id), transport: filters.transport,
      } : {}),
    };
    setLoading(true);
    setError('');
    fetchCalls(params).then(response => {
      if (!active) return;
      setCalls(response.items || []);
      setTotal(response.total || 0);
      snapshotAt.current = response.snapshot_at || snapshotAt.current;
      const lastPage = Math.max(1, Math.ceil((response.total || 0) / pageSize));
      if (page > lastPage) setPage(lastPage);
    }).catch(err => {
      if (active) setError(err instanceof Error ? err.message : '调用记录加载失败');
    }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [filters, isAdmin, page, pageSize, refreshKey]);

  const updateDraft = (key: keyof FilterDraft, value: string) => setDraft(current => ({ ...current, [key]: value }));
  const updateRouteKind = (value: APICallRouteKind | '') => setDraft(current => ({
    ...current,
    route_kind: value,
    channel_id: '',
  }));
  const search = () => { snapshotAt.current = ''; setPage(1); setFilters({ ...draft }); };
  const reset = () => { snapshotAt.current = ''; setPage(1); setDraft({ ...EMPTY_FILTERS }); setFilters({ ...EMPTY_FILTERS }); };
  const refresh = () => { snapshotAt.current = ''; setPage(1); setRefreshKey(value => value + 1); };
  const changePageSize = (value: number) => { snapshotAt.current = ''; setPageSize(value); setPage(1); };

  const openDetail = async (id: string) => {
    const requestNo = ++detailRequest.current;
    // requestNo 代替取消不可中断的请求，防止连续点击时旧详情覆盖新详情。
    setDetail(null);
    setDetailError('');
    setDetailLoading(true);
    setActiveTab('overview');
    try {
      const result = await fetchCallDetail(id);
      if (requestNo === detailRequest.current) setDetail(result);
    } catch (err) {
      if (requestNo === detailRequest.current) setDetailError(err instanceof Error ? err.message : '调用详情加载失败');
    } finally {
      if (requestNo === detailRequest.current) setDetailLoading(false);
    }
  };

  const closeDetail = () => { detailRequest.current += 1; setDetail(null); setDetailError(''); setDetailLoading(false); };
  const copyRequestID = async () => {
    if (!detail?.call.request_id) return;
    try {
      await navigator.clipboard.writeText(detail.call.request_id);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    } catch {
      setCopied(false);
    }
  };

  return (
    <div className="space-y-4">
      <PageHeader
        icon={Activity}
        title="调用记录"
        meta={loading ? '正在同步调用数据' : `共 ${total} 条记录`}
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
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          <label className="text-xs font-semibold text-[var(--text-secondary)]">调用 ID
            <input value={draft.call_id} onChange={e => updateDraft('call_id', e.target.value)} placeholder="call_..." className={`${INPUT_CLASS} mt-1`} />
          </label>
          <label className="text-xs font-semibold text-[var(--text-secondary)]">请求 ID
            <input value={draft.request_id} onChange={e => updateDraft('request_id', e.target.value)} placeholder="req_..." className={`${INPUT_CLASS} mt-1`} />
          </label>
          <label className="text-xs font-semibold text-[var(--text-secondary)]">模型
            <input value={draft.model} onChange={e => updateDraft('model', e.target.value)} placeholder="模型名称" className={`${INPUT_CLASS} mt-1`} />
          </label>
          <div className="text-xs font-semibold text-[var(--text-secondary)]">状态
            <Select value={draft.status} onChange={v => updateDraft('status', v)} className="mt-1"
              options={[{ label: '全部状态', value: '' }, { label: '已接收', value: 'received' }, { label: '处理中', value: 'in_progress' }, { label: '成功', value: 'completed' }, { label: '失败', value: 'failed' }, { label: '已取消', value: 'cancelled' }]} />
          </div>
          <label className="text-xs font-semibold text-[var(--text-secondary)]">端点
            <input value={draft.endpoint} onChange={e => updateDraft('endpoint', e.target.value)} placeholder="/v1/capabilities/image-generation" className={`${INPUT_CLASS} mt-1 font-mono`} />
          </label>
          <label className="text-xs font-semibold text-[var(--text-secondary)]">开始日期
            <input type="date" value={draft.start_date} onChange={e => updateDraft('start_date', e.target.value)} className={`${INPUT_CLASS} mt-1`} />
          </label>
          <label className="text-xs font-semibold text-[var(--text-secondary)]">结束日期
            <input type="date" value={draft.end_date} onChange={e => updateDraft('end_date', e.target.value)} className={`${INPUT_CLASS} mt-1`} />
          </label>
          {isAdmin && <>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">用户 ID
              <Select value={draft.user_id} onChange={v => updateDraft('user_id', v)} className="mt-1"
                options={[{ label: '全部用户', value: '' }, ...users.map(user => ({ label: `${user.username} (#${user.id})`, value: String(user.id) }))]} />
            </div>
            <label className="text-xs font-semibold text-[var(--text-secondary)]">Token ID
              <input type="number" min="1" value={draft.token_id} onChange={e => updateDraft('token_id', e.target.value)} placeholder="Token ID" className={`${INPUT_CLASS} mt-1`} />
            </label>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">路由类型
              <Select value={draft.route_kind} onChange={v => updateRouteKind(v as APICallRouteKind | '')} className="mt-1"
                options={[{ label: '全部路由', value: '' }, { label: 'Gateway', value: 'gateway_v2' }, { label: '能力任务', value: 'capability' }, { label: '视频任务', value: 'video' }]} />
            </div>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">渠道
              <Select value={draft.channel_id} onChange={v => updateDraft('channel_id', v)} disabled={!draft.route_kind} className="mt-1"
                options={[{ label: draft.route_kind ? '全部渠道' : '先选择路由类型', value: '' }, ...filterChannels.map(channel => ({ label: `${channel.name} (#${channel.id})`, value: String(channel.id) }))]} />
            </div>
            <div className="text-xs font-semibold text-[var(--text-secondary)]">Transport
              <Select value={draft.transport} onChange={v => updateDraft('transport', v)} className="mt-1"
                options={[{ label: '全部 Transport', value: '' }, ...CALL_TRANSPORTS.map(item => ({ label: TRANSPORT_LABELS[item], value: item }))]} />
            </div>
          </>}
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
        {error && <div className="flex items-center gap-2 border-b border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700"><AlertTriangle size={16} />{error}</div>}
        <div className="overflow-x-auto">
          <table className="w-full min-w-[980px] text-left">
            <thead className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)] text-xs font-semibold text-[var(--text-secondary)]">
              <tr><th className="px-4 py-3">时间 / 请求 ID</th><th className="px-4 py-3">模型</th><th className="px-4 py-3">协议</th><th className="px-4 py-3">状态</th><th className="px-4 py-3 text-right">Usage</th><th className="px-4 py-3 text-right">费用</th><th className="px-4 py-3 text-right">耗时</th><th className="w-10 px-3 py-3" /></tr>
            </thead>
            <tbody className="divide-y divide-[var(--border-soft)]">
              {loading ? <CallTableSkeleton /> :
                calls.length === 0 ? <tr><td colSpan={8} className="px-4 py-16 text-center text-sm text-[var(--text-secondary)]">暂无调用记录</td></tr> :
                calls.map(call => <tr key={call.id} tabIndex={0} onClick={() => openDetail(call.id)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openDetail(call.id); } }} className="cursor-pointer transition hover:bg-[var(--surface-tint)] focus:bg-[var(--surface-tint)] focus:outline-none">
                  <td className="px-4 py-3"><div className="text-sm text-[var(--text-primary)]">{formatDate(call.created_at)}</div><div title={call.request_id} className="mt-1 max-w-52 truncate font-mono text-[11px] text-[var(--text-secondary)]">{call.request_id}</div></td>
                  <td className="px-4 py-3"><div className="max-w-52 truncate text-sm font-semibold text-[var(--text-primary)]" title={call.model}>{call.model || '-'}</div><div className="mt-1 text-xs text-[var(--text-secondary)]">{call.is_stream ? '流式' : '非流式'} · {call.attempt_count} 次尝试</div></td>
                  <td className="px-4 py-3"><div className="text-sm text-[var(--text-primary)]">{endpointLabel(call.endpoint)}</div><div className="mt-1 font-mono text-[11px] text-[var(--text-secondary)]">{call.endpoint}</div></td>
                  <td className="px-4 py-3"><StatusBadge status={call.status} />{call.http_status > 0 && <div className="mt-1 text-xs text-[var(--text-secondary)]">HTTP {call.http_status}</div>}</td>
                  <td className="px-4 py-3 text-right"><div className="text-sm text-[var(--text-primary)]">{call.input_tokens} / {call.output_tokens}</div><div className="mt-1 text-xs text-[var(--text-secondary)]">共 {call.total_tokens}</div></td>
                  <td className="px-4 py-3 text-right text-sm font-semibold text-[var(--text-primary)]">{formatMoney(call.final_cost)}</td>
                  <td className="px-4 py-3 text-right"><div className="text-sm text-[var(--text-primary)]">{formatDuration(call.duration_ms)}</div><div className="mt-1 text-xs text-[var(--text-secondary)]">TTFT {formatDuration(call.ttft_ms)}</div></td>
                  <td className="px-3 py-3 text-right"><ChevronRight size={17} className="text-[var(--text-secondary)]" /></td>
                </tr>)}
            </tbody>
          </table>
        </div>
        <Pagination page={page} pageSize={pageSize} total={total} loading={loading} onPageChange={setPage} onPageSizeChange={changePageSize} />
      </section>

      <Drawer
        open={detailLoading || Boolean(detail) || Boolean(detailError)}
        onClose={closeDetail}
        title="调用详情"
        subtitle={<span className="font-mono">{detail?.call.id || '加载中'}</span>}
        panelClassName="bg-[var(--surface-card)]"
      >
          {detail && <div className="flex overflow-x-auto border-b border-[var(--border-soft)] bg-[var(--surface-muted)] px-4">
            {([
                ['overview', '概览', LayoutList], ['attempts', '上游尝试', RouteIcon], ['billing', '计费', ReceiptText], ['payloads', '请求与响应', FileJson],
            ] as const).map(([key, label, Icon]) => <button key={key} onClick={() => setActiveTab(key)} className={`flex shrink-0 items-center gap-2 border-b-2 px-4 py-3 text-sm font-semibold transition ${activeTab === key ? 'border-[var(--primary)] bg-[var(--surface-tint)] text-[var(--primary)]' : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}><Icon size={16} />{label}</button>)}
          </div>}
          <div className="flex-1 overflow-y-auto p-5">
            {detailLoading ? <CallDetailSkeleton /> :
              detailError ? <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700"><AlertTriangle size={17} />{detailError}</div> :
              detail && activeTab === 'overview' ? <Overview call={detail.call} isAdmin={isAdmin} copied={copied} onCopy={copyRequestID} /> :
              detail && activeTab === 'attempts' ? <Attempts attempts={detail.attempts || []} isAdmin={isAdmin} channelNames={channelNames} videoChannelNames={videoChannelNames} endpointMap={endpointMap} accountNames={accountNames} /> :
              detail && activeTab === 'billing' ? <Billing call={detail.call} logs={detail.billing_logs || []} /> :
              detail && <Payloads payloads={detail.payloads || []} />}
          </div>
      </Drawer>
    </div>
  );
};

const Overview: React.FC<{ call: APICall; isAdmin: boolean; copied: boolean; onCopy: () => void }> = ({ call, isAdmin, copied, onCopy }) => <div className="space-y-5">
  <section><h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">请求</h3><div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-2 xl:grid-cols-3">
    <Info label="状态"><StatusBadge status={call.status} /></Info><Info label="模型">{call.model}</Info><Info label="协议">{endpointLabel(call.endpoint)}</Info>
    <Info label="调用 ID" mono>{call.id}</Info><Info label="请求 ID"><span className="inline-flex max-w-full items-center gap-2 font-mono"><span className="truncate">{call.request_id}</span><button title="复制请求 ID" onClick={onCopy} className="shrink-0 rounded-md p-1 transition hover:bg-[var(--surface-muted)]">{copied ? <Check size={14} className="text-green-600" /> : <Copy size={14} />}</button></span></Info><Info label="操作">{call.operation || '-'}</Info>
    {isAdmin && <><Info label="用户 ID">{call.user_id}</Info><Info label="Token ID">{call.token_id}</Info><Info label="最终尝试 ID">{call.final_attempt_id || '-'}</Info></>}
    <Info label="模式">{call.is_stream ? '流式' : '非流式'}{call.background ? ' · 后台' : ''}{call.store ? ' · 存储' : ''}</Info><Info label="HTTP 状态">{call.http_status || '-'}</Info><Info label="尝试次数">{call.attempt_count}</Info>
  </div></section>
  {(call.error_message || call.error_code) && <section className="rounded-lg border border-red-200 bg-red-50 p-4"><div className="flex items-center gap-2 text-sm font-bold text-red-700"><AlertTriangle size={16} />{call.error_code || call.error_type || '调用失败'}</div><p className="mt-2 whitespace-pre-wrap break-words text-sm text-red-700">{call.error_message}</p><div className="mt-2 text-xs text-red-600">{call.error_retryable ? '可重试' : '不可重试'}{call.client_disconnected ? ' · 客户端已断开' : ''}</div></section>}
  <section><h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">Usage 与费用</h3><div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-3">
    <Info label="输入 / 输出">{call.input_tokens} / {call.output_tokens}</Info><Info label="总 Token">{call.total_tokens}</Info><Info label="缓存 / 推理">{call.cached_input_tokens} / {call.reasoning_output_tokens}</Info>
    <Info label="预留金额">{formatMoney(call.reserved_amount)}</Info><Info label="最终费用">{formatMoney(call.final_cost)}</Info><Info label="退款金额">{formatMoney(call.refunded_amount)}</Info>
  </div></section>
  <section><h3 className="mb-2 text-sm font-bold text-[var(--text-primary)]">时间</h3><div className="grid gap-x-6 border-y border-[var(--border-soft)] md:grid-cols-3">
    <Info label="创建时间">{formatDate(call.created_at)}</Info><Info label="开始时间">{formatDate(call.started_at)}</Info><Info label="首字节时间">{formatDate(call.first_byte_at)}</Info><Info label="完成时间">{formatDate(call.completed_at)}</Info><Info label="总耗时">{formatDuration(call.duration_ms)}</Info><Info label="TTFT">{formatDuration(call.ttft_ms)}</Info>
  </div></section>
</div>;

const formatInternalEntity = (id: number, name?: string) => id > 0 ? (name ? `${name} (#${id})` : `ID ${id}`) : '-';

type AttemptsProps = {
  attempts: APICallAttempt[];
  isAdmin: boolean;
  channelNames: Map<number, string>;
  videoChannelNames: Map<number, string>;
  endpointMap: Map<number, ChannelCapability>;
  accountNames: Map<number, string>;
};

const Attempts: React.FC<AttemptsProps> = ({ attempts, isAdmin, channelNames, videoChannelNames, endpointMap, accountNames }) => {
  if (attempts.length === 0) {
    return <div className="py-16 text-center text-sm text-[var(--text-secondary)]">暂无上游尝试</div>;
  }
  return <div className="divide-y divide-[var(--border-soft)] border-y border-[var(--border-soft)]">{attempts.map(attempt => {
    const routeKind = attempt.route_kind || 'gateway_v2';
    const endpoint = endpointMap.get(attempt.endpoint_id);
    const capabilityChannelID = Number(endpoint?.channelId || 0);
    const channelID = routeKind === 'capability' ? capabilityChannelID || attempt.channel_id : attempt.channel_id;
    const channelName = routeKind === 'capability' ? endpoint?.channel?.name : routeKind === 'video' ? videoChannelNames.get(channelID) : channelNames.get(channelID);
    const endpointName = endpoint?.name || endpoint?.capabilityCode || endpoint?.model;
    const accountID = attempt.account_id || Number(endpoint?.accountId || 0);
    return <section key={attempt.id} className="py-4 first:pt-3">
      <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="text-sm font-bold text-[var(--text-primary)]">第 {attempt.attempt_no} 次尝试</h3><StatusBadge status={attempt.status} /></div>
      <div className="mt-2 grid gap-x-6 md:grid-cols-2 xl:grid-cols-4"><Info label="Transport">{TRANSPORT_LABELS[attempt.transport] || attempt.transport}</Info><Info label="上游模型">{attempt.vendor_model}</Info><Info label="协议">{attempt.protocol}</Info><Info label="HTTP 状态">{attempt.http_status || '-'}</Info>{isAdmin && <><Info label="路由">{ROUTE_KIND_LABELS[routeKind] || routeKind}</Info><Info label="阶段">{STAGE_LABELS[attempt.stage] || attempt.stage || '-'}</Info><Info label="渠道">{formatInternalEntity(channelID, channelName)}</Info><Info label="端点">{formatInternalEntity(attempt.endpoint_id, endpointName)}</Info><Info label="账号">{formatInternalEntity(accountID, accountNames.get(accountID))}</Info><Info label="Key ID">{attempt.key_id || '-'}</Info><Info label="能力 ID">{attempt.ability_id || '-'}</Info><Info label="尝试 ID">{attempt.id}</Info></>}<Info label="请求路径" mono>{attempt.request_path}</Info><Info label="Provider Response ID" mono>{attempt.provider_response_id || '-'}</Info><Info label="输入 / 输出">{attempt.input_tokens} / {attempt.output_tokens}</Info><Info label="总 Token">{attempt.total_tokens}</Info><Info label="耗时">{formatDuration(attempt.duration_ms)}</Info><Info label="TTFT">{formatDuration(attempt.ttft_ms)}</Info><Info label="开始时间">{formatDate(attempt.started_at)}</Info><Info label="完成时间">{formatDate(attempt.completed_at)}</Info></div>
      {attempt.error_message && <div className="mt-2 rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700"><div className="font-semibold">{attempt.error_code || attempt.error_type || '上游错误'} · {attempt.error_retryable ? '可重试' : '不可重试'}</div><div className="mt-1 whitespace-pre-wrap break-words">{attempt.error_message}</div></div>}
    </section>;
  })}</div>;
};

const Billing: React.FC<{ call: APICall; logs: APICallBillingLog[] }> = ({ call, logs }) => <div className="space-y-5">
  <div className="grid border-y border-[var(--border-soft)] md:grid-cols-3"><Info label="预留金额">{formatMoney(call.reserved_amount)}</Info><Info label="最终费用">{formatMoney(call.final_cost)}</Info><Info label="退款金额">{formatMoney(call.refunded_amount)}</Info></div>
  {logs.length === 0 ? <div className="py-12 text-center text-sm text-[var(--text-secondary)]">暂无计费流水</div> : <div className="overflow-x-auto rounded-lg border border-[var(--border-soft)]"><table className="w-full min-w-[680px] text-left text-sm"><thead className="border-b border-[var(--border-soft)] bg-[var(--surface-muted)] text-xs font-semibold text-[var(--text-secondary)]"><tr><th className="px-3 py-3">时间</th><th className="px-3 py-3">阶段</th><th className="px-3 py-3">类型</th><th className="px-3 py-3">尝试 ID</th><th className="px-3 py-3 text-right">金额</th><th className="px-3 py-3">状态</th></tr></thead><tbody className="divide-y divide-[var(--border-soft)]">{logs.map(log => <React.Fragment key={log.id}><tr><td className="px-3 py-3 text-[var(--text-secondary)]">{formatDate(log.created_at)}</td><td className="px-3 py-3 text-[var(--text-primary)]">{log.phase || '-'}</td><td className="px-3 py-3 text-[var(--text-primary)]">{log.type}</td><td className="px-3 py-3 text-[var(--text-primary)]">{log.attempt_id || '-'}</td><td className="px-3 py-3 text-right font-semibold text-[var(--text-primary)]">{formatMoney(log.amount)}</td><td className="px-3 py-3 text-[var(--text-primary)]">{log.status}</td></tr>{(log.remark || log.pricing_snapshot) && <tr><td colSpan={6} className="bg-[var(--surface-muted)] px-3 py-2 text-xs text-[var(--text-secondary)]">{log.remark && <div>{log.remark}</div>}{log.pricing_snapshot != null && <details className="mt-1"><summary className="cursor-pointer text-[var(--primary)]">定价快照</summary><pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all font-mono">{formatData(log.pricing_snapshot)}</pre></details>}</td></tr>}</React.Fragment>)}</tbody></table></div>}
</div>;

const Payloads: React.FC<{ payloads: APICallPayload[] }> = ({ payloads }) => payloads.length === 0 ? <div className="py-16 text-center text-sm text-[var(--text-secondary)]">未保留请求或响应内容</div> : <div className="divide-y divide-[var(--border-soft)] border-y border-[var(--border-soft)]">{payloads.map(payload => <section key={payload.id} className="py-4">
  <div className="flex flex-wrap items-center justify-between gap-2"><div><h3 className="text-sm font-bold text-[var(--text-primary)]">{PAYLOAD_KIND_LABELS[payload.kind] || payload.kind}</h3><p className="mt-1 text-xs text-[var(--text-secondary)]">{payload.content_type} · {payload.original_bytes} bytes{payload.attempt_id ? ` · 尝试 #${payload.attempt_id}` : ''}</p></div><div className="flex gap-2">{payload.encrypted && <span className="rounded-md bg-blue-100 px-2 py-1 text-xs font-semibold text-blue-700">已加密存储</span>}{payload.truncated && <span className="rounded-md bg-yellow-100 px-2 py-1 text-xs font-semibold text-yellow-700">已截断</span>}</div></div>
  <pre className="mt-3 max-h-[32rem] overflow-auto whitespace-pre-wrap break-all rounded-lg border border-[var(--border-soft)] bg-[var(--surface-muted)] p-4 font-mono text-xs leading-5 text-[var(--text-primary)]">{payload.data ? formatData(payload.data) : '(无内容)'}</pre>
  {payload.expires_at && <div className="mt-2 text-xs text-[var(--text-secondary)]">过期时间：{formatDate(payload.expires_at)}</div>}
</section>)}</div>;

export default CallLogs;
