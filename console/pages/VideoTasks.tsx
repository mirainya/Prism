import React, { useEffect, useState } from 'react';
import { RefreshCw, Eye, ChevronLeft, ChevronRight } from 'lucide-react';
import { VideoTask, VideoStats, fetchVideoTasks, getVideoTask, fetchVideoStats } from '../services/videoApi';
import { Modal, Select } from '../components/ui';

const STATUS_CONFIG: Record<string, { label: string; color: string }> = {
  queued: { label: '排队中', color: 'bg-yellow-100 text-yellow-700' },
  submitted: { label: '已提交', color: 'bg-blue-100 text-blue-700' },
  tracking: { label: '处理中', color: 'bg-indigo-100 text-indigo-700' },
  completed: { label: '已完成', color: 'bg-green-100 text-green-700' },
  failed: { label: '失败', color: 'bg-red-100 text-red-700' },
  cancelled: { label: '已取消', color: 'bg-gray-100 text-gray-500' },
};

const formatTime = (ts: string) => {
  if (!ts) return '-';
  const d = new Date(ts);
  const now = Date.now();
  const diff = now - d.getTime();
  if (diff < 60000) return '刚刚';
  if (diff < 3600000) return `${Math.floor(diff / 60000)} 分钟前`;
  if (diff < 86400000) return `${Math.floor(diff / 3600000)} 小时前`;
  return d.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
};

const formatCost = (value: number | string | undefined) => {
  const amount = typeof value === 'number' ? value : Number(value || 0);
  return Number.isFinite(amount) ? amount.toFixed(4) : '0.0000';
};

const StatCard: React.FC<{ label: string; value: number; color: string }> = ({ label, value, color }) => (
  <div className="bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] p-4 flex flex-col items-center">
    <div className={`text-2xl font-bold ${color}`}>{value}</div>
    <div className="text-xs text-[var(--text-secondary)] mt-1">{label}</div>
  </div>
);

const TaskDetail: React.FC<{ taskId: string | null; onClose: () => void }> = ({ taskId, onClose }) => {
  const [task, setTask] = useState<VideoTask | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!taskId) return;
    let cancelled = false;
    setLoading(true);
    getVideoTask(taskId)
      .then(result => { if (!cancelled) setTask(result); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [taskId]);

  const st = task ? (STATUS_CONFIG[task.status] || { label: task.status, color: 'bg-gray-100 text-gray-500' }) : null;

  return (
    <Modal open={Boolean(taskId)} onClose={onClose} title="任务详情" width="max-w-xl">
      {loading ? (
        <div className="flex min-h-32 items-center justify-center"><RefreshCw size={20} className="animate-spin text-[var(--primary)]" /></div>
      ) : task && st ? (
        <div className="p-6 space-y-4">
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div><span className="text-[var(--text-secondary)]">ID：</span><span className="font-mono text-[var(--text-primary)]">{task.id}</span></div>
            <div><span className="text-[var(--text-secondary)]">状态：</span><span className={`px-2 py-0.5 rounded text-xs font-bold ${st.color}`}>{st.label}</span></div>
            <div><span className="text-[var(--text-secondary)]">模型：</span><span className="text-[var(--text-primary)]">{task.model}</span></div>
            <div><span className="text-[var(--text-secondary)]">费用：</span><span className="text-[var(--text-primary)]">¥{formatCost(task.final_cost || task.estimated_cost)}</span></div>
            <div><span className="text-[var(--text-secondary)]">渠道ID：</span><span className="text-[var(--text-primary)]">{task.channel_id}</span></div>
            <div><span className="text-[var(--text-secondary)]">Key ID：</span><span className="text-[var(--text-primary)]">{task.key_id}</span></div>
            <div className="col-span-2"><span className="text-[var(--text-secondary)]">创建时间：</span><span className="text-[var(--text-primary)]">{new Date(task.created_at).toLocaleString('zh-CN')}</span></div>
          </div>

          {task.prompt && (
            <div>
              <div className="text-xs font-medium text-[var(--text-secondary)] mb-1">提示词</div>
              <div className="p-3 rounded-lg bg-[var(--surface)] text-sm text-[var(--text-primary)] whitespace-pre-wrap">{task.prompt}</div>
            </div>
          )}

          {task.provider_task_id && (
            <div>
              <div className="text-xs font-medium text-[var(--text-secondary)] mb-1">上游任务ID</div>
              <div className="text-sm font-mono text-[var(--text-primary)]">{task.provider_task_id}</div>
            </div>
          )}

          {task.result_json && (
            <div>
              <div className="text-xs font-medium text-[var(--text-secondary)] mb-1">结果</div>
              {typeof task.result_json === 'object' && task.result_json.video_url ? (
                <a href={task.result_json.video_url} target="_blank" rel="noreferrer" className="text-sm text-[var(--primary)] hover:underline break-all">{task.result_json.video_url}</a>
              ) : (
                <pre className="p-3 rounded-lg bg-[var(--surface)] text-xs font-mono text-[var(--text-primary)] overflow-x-auto">{JSON.stringify(task.result_json, null, 2)}</pre>
              )}
            </div>
          )}

          {task.error_message && (
            <div>
              <div className="text-xs font-medium text-red-500 mb-1">错误</div>
              <div className="p-3 rounded-lg bg-red-50 text-sm text-red-700 whitespace-pre-wrap">{task.error_message}</div>
            </div>
          )}

          {task.params_json && (
            <div>
              <div className="text-xs font-medium text-[var(--text-secondary)] mb-1">参数</div>
              <pre className="p-3 rounded-lg bg-[var(--surface)] text-xs font-mono text-[var(--text-primary)] overflow-x-auto">{JSON.stringify(task.params_json, null, 2)}</pre>
            </div>
          )}
        </div>
      ) : (
        <div className="py-12 text-center text-sm text-[var(--text-secondary)]">任务详情不可用</div>
      )}
    </Modal>
  );
};

const VideoTasks: React.FC = () => {
  const [stats, setStats] = useState<VideoStats | null>(null);
  const [tasks, setTasks] = useState<VideoTask[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(20);
  const [status, setStatus] = useState('');
  const [model, setModel] = useState('');
  const [loading, setLoading] = useState(true);
  const [detailId, setDetailId] = useState<string | null>(null);

  const loadStats = () => fetchVideoStats().then(setStats).catch(() => {});

  const loadTasks = async () => {
    setLoading(true);
    try {
      const res = await fetchVideoTasks({ page, page_size: pageSize, status: status || undefined, model: model || undefined });
      setTasks(res.items || []);
      setTotal(res.total);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { loadStats(); }, []);
  useEffect(() => { loadTasks(); }, [page, status, model]);

  const totalPages = Math.ceil(total / pageSize);

  return (
    <div className="space-y-4 md:space-y-6">
      <div className="flex items-start sm:items-center justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">视频任务</h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm hidden sm:block">视频生成任务监控与管理</p>
        </div>
        <button onClick={() => { loadStats(); loadTasks(); }}
          className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-semibold text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
          <RefreshCw size={14} className={loading ? 'animate-spin' : ''} /> 刷新
        </button>
      </div>

      {/* Stats */}
      {stats && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          <StatCard label="总任务" value={stats.total_tasks} color="text-[var(--text-primary)]" />
          <StatCard label="进行中" value={stats.active_tasks} color="text-blue-600" />
          <StatCard label="渠道数" value={stats.channels} color="text-indigo-600" />
          <StatCard label="Key 数" value={stats.keys} color="text-emerald-600" />
        </div>
      )}

      {/* Filters */}
      <div className="flex items-center gap-3 flex-wrap">
        <Select
          value={status}
          onChange={v => { setStatus(v); setPage(1); }}
          placeholder="全部状态"
          options={[{ label: '全部状态', value: '' }, ...Object.entries(STATUS_CONFIG).map(([k, v]) => ({ label: v.label, value: k }))]}
          className="w-32"
        />
        <input value={model} onChange={e => { setModel(e.target.value); setPage(1); }} placeholder="按模型筛选..."
          className="px-3 py-1.5 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)] w-48" />
      </div>

      {/* Table */}
      <div className="bg-[var(--surface-card)] rounded-2xl shadow-sm border border-[var(--border-soft)] overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-left min-w-[580px]">
            <thead>
              <tr className="border-b border-[var(--border-soft)]">
                <th className="px-4 py-3 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">ID</th>
                <th className="px-4 py-3 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">模型</th>
                <th className="px-4 py-3 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">状态</th>
                <th className="px-4 py-3 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">时间</th>
                <th className="px-4 py-3 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i} className="animate-pulse border-b border-[var(--border-soft)]">
                    <td className="px-4 py-3"><div className="h-4 bg-[var(--primary-lighter)] rounded w-24"></div></td>
                    <td className="px-4 py-3"><div className="h-4 bg-[var(--primary-lighter)] rounded w-20"></div></td>
                    <td className="px-4 py-3"><div className="h-4 bg-[var(--primary-lighter)] rounded w-16"></div></td>
                    <td className="px-4 py-3"><div className="h-4 bg-[var(--primary-lighter)] rounded w-16"></div></td>
                    <td className="px-4 py-3"><div className="h-4 bg-[var(--primary-lighter)] rounded w-8 ml-auto"></div></td>
                  </tr>
                ))
              ) : tasks.length === 0 ? (
                <tr><td colSpan={5} className="px-4 py-12 text-center text-[var(--text-secondary)]">暂无任务数据</td></tr>
              ) : tasks.map(task => {
                const st = STATUS_CONFIG[task.status] || { label: task.status, color: 'bg-gray-100 text-gray-500' };
                return (
                  <tr key={task.id} className="border-b border-[var(--border-soft)] hover:bg-[var(--surface)]/50">
                    <td className="px-4 py-3 text-sm font-mono text-[var(--text-primary)] truncate max-w-[120px]">{task.id.slice(0, 12)}...</td>
                    <td className="px-4 py-3 text-sm text-[var(--text-primary)]">{task.model}</td>
                    <td className="px-4 py-3">
                      <span className={`px-2 py-0.5 rounded text-xs font-bold ${st.color}`}>{st.label}</span>
                    </td>
                    <td className="px-4 py-3 text-xs text-[var(--text-secondary)]">{formatTime(task.created_at)}</td>
                    <td className="px-4 py-3 text-right">
                      <button onClick={() => setDetailId(task.id)} className="p-1.5 hover:bg-[var(--primary-lighter)] rounded text-[var(--text-secondary)] hover:text-[var(--primary)]" title="详情"><Eye size={14} /></button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        {/* Pagination */}
        {totalPages > 1 && (
          <div className="flex items-center justify-between px-4 py-3 border-t border-[var(--border-soft)]">
            <div className="text-xs text-[var(--text-secondary)]">共 {total} 条</div>
            <div className="flex items-center gap-2">
              <button onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page === 1}
                className="p-1.5 rounded hover:bg-[var(--surface)] disabled:opacity-30"><ChevronLeft size={16} /></button>
              <span className="text-sm text-[var(--text-primary)]">{page} / {totalPages}</span>
              <button onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={page === totalPages}
                className="p-1.5 rounded hover:bg-[var(--surface)] disabled:opacity-30"><ChevronRight size={16} /></button>
            </div>
          </div>
        )}
      </div>

      <TaskDetail taskId={detailId} onClose={() => setDetailId(null)} />
    </div>
  );
};

export default VideoTasks;
