import React, { useEffect, useRef, useState } from 'react';
import {
    Search,
    Clock,
    ChevronRight,
    Code,
    RefreshCw,
    ChevronLeft,
    AlertCircle,
    CheckCircle
} from 'lucide-react';
import { Drawer, Select } from '../components/ui';
import {
    fetchRequestLogs,
    fetchRequestLogDetail,
    fetchCapabilities,
    fetchChannels,
    RequestLogListParams
} from '../services/api';
import { ChannelRequestLog, Capability, Channel } from '../types';

const REQUEST_TYPE_MAP: Record<string, { label: string; color: string }> = {
  submit: { label: '提交', color: 'bg-blue-100 text-blue-700' },
  poll: { label: '轮询', color: 'bg-purple-100 text-purple-700' },
  callback: { label: '回调', color: 'bg-orange-100 text-orange-700' },
    chat: {label: 'Chat', color: 'bg-green-100 text-green-700'},
};

const RequestLogs: React.FC = () => {
  const [logs, setLogs] = useState<ChannelRequestLog[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize] = useState(20);
  const snapshotId = useRef<number | undefined>(undefined);
  const [isLoading, setIsLoading] = useState(true);
  const [refreshKey, setRefreshKey] = useState(0);
  const [selectedLog, setSelectedLog] = useState<ChannelRequestLog | null>(null);
  const [isDrawerOpen, setIsDrawerOpen] = useState(false);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState('');
  const [capabilities, setCapabilities] = useState<Capability[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const detailRequest = useRef(0);

  // 过滤条件
  const [filters, setFilters] = useState<RequestLogListParams>({});
  const [keyword, setKeyword] = useState('');

  useEffect(() => {
    Promise.all([
      fetchCapabilities().then(setCapabilities).catch(() => {}),
      fetchChannels().then(setChannels).catch(() => {}),
    ]);
  }, []);

  useEffect(() => {
    let active = true;
    setIsLoading(true);
    // snapshot_id 在同一筛选条件下跨页复用，避免新日志插入导致 OFFSET 页面移动。
    fetchRequestLogs({ page, page_size: pageSize, snapshot_id: snapshotId.current, ...filters })
      .then(resp => {
        if (!active) return;
        setLogs(resp.items);
        setTotal(resp.total);
        snapshotId.current = resp.snapshot_id || undefined;
      })
      .catch(e => {
        if (active) console.error('Failed to load request logs', e);
      })
      .finally(() => {
        if (active) setIsLoading(false);
      });
    return () => { active = false; };
  }, [page, pageSize, filters, refreshKey]);

  const openDetails = async (log: ChannelRequestLog) => {
    const requestNo = ++detailRequest.current;
    // 列表行先提供即时概览，详情返回后仅更新仍然打开的同一次请求。
    setSelectedLog(log);
    setIsDrawerOpen(true);
    setDetailLoading(true);
    setDetailError('');
    try {
      const detail = await fetchRequestLogDetail(log.id);
      if (requestNo === detailRequest.current) setSelectedLog(detail);
    } catch (error) {
      if (requestNo === detailRequest.current) {
        setDetailError(error instanceof Error ? error.message : '请求详情加载失败');
      }
    } finally {
      if (requestNo === detailRequest.current) setDetailLoading(false);
    }
  };

  const closeDetails = () => {
    detailRequest.current += 1;
    setIsDrawerOpen(false);
    setDetailLoading(false);
    setDetailError('');
  };

  const handleSearch = () => {
    setPage(1);
    snapshotId.current = undefined;
    setFilters(current => ({ ...current, task_no: keyword.trim() || undefined }));
  };

  const totalPages = Math.ceil(total / pageSize);

  const formatJson = (str: string) => {
    try {
      return JSON.stringify(JSON.parse(str), null, 2);
    } catch {
      return str;
    }
  };

  return (
    <div className="space-y-4 md:space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">渠道请求日志</h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm md:text-base">查看所有与第三方渠道的 HTTP 交互记录</p>
        </div>
        <button
          onClick={() => {
            snapshotId.current = undefined;
            setPage(1);
            setRefreshKey(value => value + 1);
          }}
          className="px-3 md:px-4 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm font-medium hover:bg-[var(--surface)] transition-colors flex items-center gap-2"
        >
          <RefreshCw size={16} className={isLoading ? 'animate-spin' : ''} />
          <span className="hidden md:inline">刷新</span>
        </button>
      </div>

      <div className="bg-[var(--surface-card)] rounded-2xl shadow-sm border border-[var(--border-soft)] overflow-hidden">
        <div className="p-3 md:p-4 border-b border-[var(--border-soft)] bg-[var(--surface)]/50 flex items-center gap-3 md:gap-4 flex-wrap">
          <div className="relative flex-1 min-w-[160px] md:max-w-md">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" size={16} />
            <input
              type="text"
              value={keyword}
              onChange={e => setKeyword(e.target.value)}
              onKeyDown={e => e.key === 'Enter' && handleSearch()}
              placeholder="搜索任务编号..."
              className="w-full pl-9 pr-4 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
            />
          </div>
          <Select
            value={String(filters.channel_id || '')}
            onChange={v => {
              snapshotId.current = undefined;
              setFilters(current => ({ ...current, channel_id: v ? Number(v) : undefined }));
              setPage(1);
            }}
            options={[{ label: '所有渠道', value: '' }, ...channels.map(ch => ({ label: ch.name, value: String(ch.id) }))]}
          />
          <Select
            value={filters.capability_code || ''}
            onChange={v => {
              snapshotId.current = undefined;
              setFilters(current => ({ ...current, capability_code: v || undefined }));
              setPage(1);
            }}
            options={[{ label: '所有能力', value: '' }, ...capabilities.map(c => ({ label: c.name, value: c.code }))]}
          />
          <Select
            value={filters.request_type || ''}
            onChange={v => {
              snapshotId.current = undefined;
              setFilters(current => ({ ...current, request_type: v || undefined }));
              setPage(1);
            }}
            options={[
              { label: '所有类型', value: '' },
              { label: '提交', value: 'submit' },
              { label: '轮询', value: 'poll' },
              { label: '回调', value: 'callback' },
              { label: 'Chat', value: 'chat' },
            ]}
          />
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-left min-w-[600px]">
            <thead>
              <tr className="border-b border-[var(--border-soft)]">
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">任务 / 时间</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">渠道</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-center">类型</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-center">状态</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-right hidden sm:table-cell">耗时</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-right">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {isLoading ? (
                Array.from({ length: 8 }).map((_, i) => (
                  <tr key={i} className="animate-pulse">
                    <td colSpan={6} className="px-3 md:px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-full"></div></td>
                  </tr>
                ))
              ) : logs.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-3 md:px-6 py-12 text-center text-[var(--text-secondary)]">暂无数据</td>
                </tr>
              ) : logs.map(log => {
                const typeInfo = REQUEST_TYPE_MAP[log.request_type] || { label: log.request_type, color: 'bg-[var(--primary-lighter)] text-[var(--text-primary)]' };
                const isSuccess = log.status_code >= 200 && log.status_code < 300;
                const hasError = !!log.error_message;
                return (
                  <tr key={log.id} className="hover:bg-[var(--primary-lighter)]/30 transition-colors cursor-pointer group" onClick={() => openDetails(log)}>
                    <td className="px-3 md:px-6 py-3 md:py-4">
                        <div className="text-xs font-bold text-[var(--primary)] font-mono truncate max-w-[100px] md:max-w-none">
                            {log.request_type === 'chat' ? (log.conversation_id ? `对话#${log.conversation_id}` : `LOG#${log.id}`) : log.task_no}
                        </div>
                      <div className="text-[10px] text-[var(--text-secondary)] mt-0.5">{log.request_at}</div>
                    </td>
                    <td className="px-3 md:px-6 py-3 md:py-4">
                      <div className="text-sm font-medium text-[var(--text-primary)]">{log.channel_name || '-'}</div>
                      <div className="text-[10px] text-[var(--text-secondary)]">{log.capability_name || log.capability_code}</div>
                    </td>
                    <td className="px-3 md:px-6 py-3 md:py-4 text-center">
                      <span className={`inline-flex px-2 py-0.5 rounded-full text-[10px] font-bold ${typeInfo.color}`}>
                        {typeInfo.label}
                      </span>
                    </td>
                    <td className="px-3 md:px-6 py-3 md:py-4 text-center">
                      <div className="flex items-center justify-center gap-1">
                        {hasError ? (
                          <AlertCircle size={14} className="text-red-500" />
                        ) : isSuccess ? (
                          <CheckCircle size={14} className="text-green-500" />
                        ) : (
                          <AlertCircle size={14} className="text-yellow-500" />
                        )}
                        <span className={`text-sm font-mono ${hasError ? 'text-red-600' : isSuccess ? 'text-green-600' : 'text-yellow-600'}`}>
                          {log.status_code || '-'}
                        </span>
                      </div>
                    </td>
                    <td className="px-3 md:px-6 py-3 md:py-4 text-right hidden sm:table-cell">
                      <div className="text-xs text-[var(--text-secondary)] flex items-center justify-end gap-1">
                        <Clock size={10} />
                        {log.duration_ms}ms
                      </div>
                    </td>
                    <td className="px-3 md:px-6 py-3 md:py-4 text-right">
                        <ChevronRight size={16}
                                      className="ml-auto text-gray-300 group-hover:text-indigo-500 group-hover:translate-x-1 transition-all"/>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        {/* Pagination */}
        {totalPages > 1 && (
          <div className="p-3 md:p-4 border-t border-[var(--border-soft)] flex items-center justify-between">
            <div className="text-xs md:text-sm text-[var(--text-secondary)]">
              共 {total} 条，第 {page}/{totalPages} 页
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={() => setPage(p => Math.max(1, p - 1))}
                disabled={page <= 1}
                className="p-2 rounded-lg hover:bg-[var(--primary-lighter)] disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <ChevronLeft size={16} />
              </button>
              <button
                onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                disabled={page >= totalPages}
                className="p-2 rounded-lg hover:bg-[var(--primary-lighter)] disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <ChevronRight size={16} />
              </button>
            </div>
          </div>
        )}
      </div>

      <Drawer open={isDrawerOpen && Boolean(selectedLog)} onClose={closeDetails} title="请求详情" subtitle={<span className="font-mono text-indigo-500">{selectedLog?.task_no}</span>} width="md:max-w-3xl">
          {selectedLog && (
            <div className="flex-1 overflow-y-auto p-6 space-y-6">
              {detailLoading && (
                <div className="flex items-center gap-2 rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] px-4 py-3 text-sm text-[var(--text-secondary)]">
                  <RefreshCw size={16} className="animate-spin" />正在加载完整详情
                </div>
              )}
              {detailError && (
                <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
                  <AlertCircle size={16} />{detailError}
                </div>
              )}
              <div className="grid grid-cols-2 gap-4 bg-[var(--surface)] p-5 rounded-2xl border border-[var(--border-soft)]">
                <div className="space-y-1">
                  <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">请求类型</label>
                  <p className="text-sm font-bold text-[var(--text-primary)]">
                    <span className={`inline-flex px-2 py-0.5 rounded-full text-[10px] font-bold ${REQUEST_TYPE_MAP[selectedLog.request_type]?.color || 'bg-[var(--primary-lighter)]'}`}>
                      {REQUEST_TYPE_MAP[selectedLog.request_type]?.label || selectedLog.request_type}
                    </span>
                  </p>
                </div>
                <div className="space-y-1">
                  <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">HTTP 方法</label>
                  <p className="text-sm font-bold text-[var(--text-primary)]">{selectedLog.method}</p>
                </div>
                <div className="space-y-1">
                  <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">渠道</label>
                  <p className="text-sm font-bold text-[var(--text-primary)]">{selectedLog.channel_name || '-'}</p>
                </div>
                <div className="space-y-1">
                  <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">能力</label>
                  <p className="text-sm font-bold text-[var(--text-primary)]">{selectedLog.capability_name || selectedLog.capability_code}</p>
                </div>
                <div className="space-y-1">
                  <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">状态码</label>
                  <p className={`text-sm font-bold ${selectedLog.status_code >= 200 && selectedLog.status_code < 300 ? 'text-green-600' : 'text-red-600'}`}>
                    {selectedLog.status_code || '-'}
                  </p>
                </div>
                <div className="space-y-1">
                  <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">耗时</label>
                  <p className="text-sm font-bold text-[var(--text-primary)]">{selectedLog.duration_ms}ms</p>
                </div>
                <div className="col-span-2 space-y-1">
                  <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">请求时间</label>
                  <p className="text-sm text-[var(--text-primary)]">{selectedLog.request_at}</p>
                </div>
                <div className="col-span-2 space-y-1">
                  <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">请求 URL</label>
                  <p className="text-sm text-[var(--text-primary)] font-mono break-all">{selectedLog.url}</p>
                </div>
                {selectedLog.error_message && (
                  <div className="col-span-2 space-y-1">
                    <label className="text-[10px] font-bold text-[var(--text-secondary)] uppercase">错误信息</label>
                    <p className="text-sm text-red-600">{selectedLog.error_message}</p>
                  </div>
                )}
              </div>

              {selectedLog.request_headers && (
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-widest">
                    <Code size={14} />
                    请求头
                  </div>
                  <div className="bg-[var(--surface)] rounded-2xl p-4 text-[var(--text-primary)] font-mono text-sm leading-relaxed whitespace-pre-wrap border border-[var(--border-soft)] overflow-x-auto max-h-40 overflow-y-auto">
                    {formatJson(selectedLog.request_headers)}
                  </div>
                </div>
              )}

              {selectedLog.request_body && (
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-widest">
                    <Code size={14} />
                    请求体
                  </div>
                  <div className="bg-[var(--surface)] rounded-2xl p-4 text-[var(--text-primary)] font-mono text-sm leading-relaxed whitespace-pre-wrap border border-[var(--border-soft)] overflow-x-auto max-h-64 overflow-y-auto">
                    {formatJson(selectedLog.request_body)}
                  </div>
                </div>
              )}

              {selectedLog.response_body && (
                <div className="space-y-3">
                  <div className="flex items-center gap-2 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-widest">
                    <Code size={14} />
                    响应体
                  </div>
                  <div className="bg-gray-900 rounded-2xl p-4 text-green-300 font-mono text-sm leading-relaxed whitespace-pre-wrap border border-gray-800 overflow-x-auto max-h-96 overflow-y-auto">
                    {formatJson(selectedLog.response_body)}
                  </div>
                </div>
              )}
            </div>
          )}
      </Drawer>

    </div>
  );
};

export default RequestLogs;
