import React, { useEffect, useMemo, useState } from 'react';
import { Plus, RefreshCw, Edit3, Trash2, ChevronDown, ChevronRight, Key, Power, Search, Film, CircleCheck, Boxes, Archive } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import {
  VideoChannel, VideoChannelKey,
  fetchVideoChannels, deleteVideoChannel,
  fetchVideoKeys, createVideoKey, updateVideoKey, deleteVideoKey,
} from '../services/videoApi';
import { ConfirmDialog, Select } from '../components/ui';
import { PageHeader, SummaryStrip } from '../components/shell';
import { VideoKeyModal } from './video/VideoModals';

const STATUS_BADGE: Record<string, string> = {
  active: 'bg-green-100 text-green-700',
  inactive: 'bg-gray-100 text-gray-500',
};

const ADAPTER_LABELS: Record<string, string> = {
  generic: '通用 JSON 任务协议',
  seedance: 'Seedance 官方协议',
};

const ADAPTER_BADGES: Record<string, string> = {
  generic: 'bg-sky-50 text-sky-700',
  seedance: 'bg-violet-50 text-violet-700',
};

const ChannelKeys: React.FC<{ channelId: number; reloadSignal: number; onAddKey: () => void; onEditKey: (k: VideoChannelKey) => void }> = ({ channelId, reloadSignal, onAddKey, onEditKey }) => {
  const [keys, setKeys] = useState<VideoChannelKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteTarget, setDeleteTarget] = useState<VideoChannelKey | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = async () => {
    setLoading(true);
    try { setKeys(await fetchVideoKeys(channelId)); } finally { setLoading(false); }
  };

  useEffect(() => { load(); }, [channelId, reloadSignal]);

  const confirmDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await deleteVideoKey(deleteTarget.id);
      setDeleteTarget(null);
      await load();
    } finally {
      setDeleting(false);
    }
  };

  const handleToggle = async (k: VideoChannelKey) => {
    await updateVideoKey(k.id, { status: k.status === 'active' ? 'inactive' : 'active' });
    load();
  };

  return (
    <div className="channel-detail-grid channel-detail-grid-single">
      <div className="channel-detail-pane">
      <div className="channel-detail-header">
        <h4 className="channel-detail-title">
          <Key size={14} /> API Keys
          {!loading && <span className="rounded-full bg-[var(--primary-lighter)] px-1.5 py-0.5 text-[10px] text-[var(--primary)]">{keys.length}</span>}
        </h4>
        <button onClick={onAddKey} className="channel-detail-action"><Plus size={14} /> 添加</button>
      </div>
      {loading ? (
        <div className="channel-detail-empty">加载中...</div>
      ) : keys.length === 0 ? (
        <div className="channel-detail-empty">暂无 Key</div>
      ) : (
        <div className="channel-detail-list">
          {keys.map(k => (
            <div key={k.id} className="channel-detail-item group">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium text-[var(--text-primary)]">{k.label || `Key #${k.id}`}</span>
                  <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${STATUS_BADGE[k.status] || STATUS_BADGE.inactive}`}>
                    {k.status === 'active' ? '启用' : '禁用'}
                  </span>
                </div>
                <div className="text-xs text-[var(--text-secondary)] font-mono mt-1">{k.masked_key}</div>
                <div className="text-xs text-[var(--text-secondary)] mt-1">
                  权重: {k.weight} | 并发: {k.current_concurrency}/{k.max_concurrency || '∞'} | 调用: {k.total_calls}
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-1 md:opacity-0 md:group-hover:opacity-100">
                <button onClick={() => handleToggle(k)} className="channel-icon-button text-[var(--text-secondary)] hover:bg-amber-50 hover:text-amber-600" title={k.status === 'active' ? '禁用' : '启用'}><Power size={13} /></button>
                <button onClick={() => onEditKey(k)} className="channel-icon-button text-[var(--text-secondary)] hover:bg-[var(--primary-lighter)] hover:text-[var(--primary)]" title="编辑"><Edit3 size={13} /></button>
                <button onClick={() => setDeleteTarget(k)} className="channel-icon-button text-[var(--text-secondary)] hover:bg-red-50 hover:text-red-600" title="删除 Key"><Trash2 size={13} /></button>
              </div>
            </div>
          ))}
        </div>
      )}
      </div>
      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title="删除 API Key？"
        description={`删除“${deleteTarget?.label || `Key #${deleteTarget?.id || ''}`}”后，该凭据将无法继续提交视频任务。`}
        confirmLabel="删除 Key"
        cancelLabel="取消"
        tone="danger"
        busy={deleting}
        onConfirm={confirmDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </div>
  );
};

const VideoChannels: React.FC = () => {
  const navigate = useNavigate();
  const [channels, setChannels] = useState<VideoChannel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [reloadSignals, setReloadSignals] = useState<Record<number, number>>({});
  const [searchTerm, setSearchTerm] = useState('');
  const [statusFilter, setStatusFilter] = useState('all');
  const [adapterFilter, setAdapterFilter] = useState('');
  const [keyModal, setKeyModal] = useState<{ open: boolean; channelId: number; channelKey: VideoChannelKey | null }>({ open: false, channelId: 0, channelKey: null });
  const [deleteTarget, setDeleteTarget] = useState<VideoChannel | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = async (skeleton = true) => {
    if (skeleton) setIsLoading(true);
    try { setChannels(await fetchVideoChannels()); } finally { if (skeleton) setIsLoading(false); }
  };

  useEffect(() => { load(); }, []);

  const toggle = (id: number) => {
    setExpanded(prev => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  };

  const confirmDeleteChannel = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await deleteVideoChannel(deleteTarget.id);
      setDeleteTarget(null);
      await load(false);
    } finally {
      setDeleting(false);
    }
  };

  const handleSaveKey = async (data: any) => {
    if (keyModal.channelKey) {
      await updateVideoKey(keyModal.channelKey.id, data);
    } else {
      await createVideoKey(keyModal.channelId, data);
    }
    const channelId = keyModal.channelId;
    setKeyModal({ open: false, channelId: 0, channelKey: null });
    setReloadSignals(previous => ({ ...previous, [channelId]: (previous[channelId] || 0) + 1 }));
  };

  const adapterOptions = useMemo(() => Array.from(new Set(channels.map(channel => channel.adapter_type).filter(Boolean)))
    .sort()
    .map(adapter => ({ label: ADAPTER_LABELS[adapter] || adapter, value: adapter })), [channels]);

  const channelStats = useMemo(() => ({
    total: channels.length,
    enabled: channels.filter(channel => channel.status === 'active').length,
    adapters: adapterOptions.length,
    persistence: channels.filter(channel => channel.result_storage_enabled).length,
  }), [channels, adapterOptions]);

  const filteredChannels = useMemo(() => {
    const keyword = searchTerm.trim().toLowerCase();
    return channels.filter(channel => {
      const matchesKeyword = !keyword
        || channel.name.toLowerCase().includes(keyword)
        || channel.base_url.toLowerCase().includes(keyword)
        || (ADAPTER_LABELS[channel.adapter_type] || channel.adapter_type).toLowerCase().includes(keyword);
      const matchesStatus = statusFilter === 'all' || channel.status === statusFilter;
      return matchesKeyword && matchesStatus && (!adapterFilter || channel.adapter_type === adapterFilter);
    });
  }, [channels, searchTerm, statusFilter, adapterFilter]);

  const filterActive = Boolean(searchTerm.trim()) || statusFilter !== 'all' || Boolean(adapterFilter);

  return (
    <div className="space-y-4">
      <PageHeader
        icon={Film}
        title="视频渠道"
        meta="管理视频生成协议、模型能力、结果存储与 API Key"
        actions={(
          <>
            <button
              type="button"
              onClick={() => load()}
              disabled={isLoading}
              title="刷新"
              aria-label="刷新视频渠道"
              className="flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] text-[var(--text-secondary)] shadow-[var(--shadow-soft)] transition hover:border-[var(--primary)] hover:text-[var(--primary)] disabled:opacity-60"
            >
              <RefreshCw size={17} className={isLoading ? 'animate-spin' : ''} />
            </button>
            <button
              type="button"
              onClick={() => navigate('/video-channels/new')}
              className="inline-flex h-9 items-center gap-2 rounded-lg [background:var(--brand-gradient)] px-3.5 text-sm font-bold text-white shadow-[0_6px_16px_var(--glow-color)] transition hover:-translate-y-0.5"
            >
              <Plus size={17} /><span className="hidden sm:inline">新建渠道</span><span className="sm:hidden">新建</span>
            </button>
          </>
        )}
      />

      <SummaryStrip items={[
        { label: '渠道总数', value: channelStats.total, icon: Film, color: 'var(--candy-pink)' },
        { label: '启用渠道', value: channelStats.enabled, icon: CircleCheck, color: 'var(--candy-mint)', note: channelStats.total ? `${Math.round(channelStats.enabled / channelStats.total * 100)}%` : '0%' },
        { label: '协议类型', value: channelStats.adapters, icon: Boxes, color: 'var(--candy-blue)' },
        { label: '结果持久化', value: channelStats.persistence, icon: Archive, color: 'var(--candy-yellow)' },
      ]} />

      <section className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)]">
        <div className="grid gap-3 border-b border-[var(--border-soft)] bg-[var(--surface-muted)] p-3 sm:grid-cols-[minmax(240px,1fr)_200px_160px_auto] md:p-4">
          <div className="relative min-w-0">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" size={16} />
            <input
              type="text"
              value={searchTerm}
              onChange={event => setSearchTerm(event.target.value)}
              placeholder="搜索名称、地址或协议"
              className="h-9 w-full rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] pl-9 pr-3 text-sm outline-none transition focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--focus-ring)]"
            />
          </div>
          <Select
            value={adapterFilter}
            onChange={setAdapterFilter}
            placeholder="全部协议"
            options={[{ label: '全部协议', value: '' }, ...adapterOptions]}
          />
          <Select
            value={statusFilter}
            onChange={setStatusFilter}
            options={[
              { label: '全部状态', value: 'all' },
              { label: '仅启用', value: 'active' },
              { label: '仅禁用', value: 'inactive' },
            ]}
          />
          <button
            type="button"
            onClick={() => { setSearchTerm(''); setAdapterFilter(''); setStatusFilter('all'); }}
            disabled={!filterActive}
            className="h-9 rounded-lg px-3 text-xs font-bold text-[var(--text-secondary)] transition hover:bg-[var(--surface-tint)] hover:text-[var(--primary)] disabled:opacity-40"
          >
            重置
          </button>
        </div>

        <div className="border-b border-[var(--border-soft)] px-4 py-2 text-xs text-[var(--text-secondary)]">
          显示 {filteredChannels.length} / {channels.length} 个渠道
        </div>

        <div className="overflow-x-auto">
          <table className="channel-data-table w-full min-w-[520px] text-left">
            <thead>
              <tr>
                <th className="px-3 md:px-6 py-3 md:py-4 w-10"></th>
                <th className="px-3 md:px-6">名称 / Base URL</th>
                <th className="px-3 md:px-6">协议</th>
                <th className="px-3 md:px-6">状态</th>
                <th className="px-3 text-right md:px-6">操作</th>
              </tr>
            </thead>
            <tbody>
              {isLoading ? (
                Array.from({ length: 3 }).map((_, i) => (
                  <tr key={i} className="animate-pulse border-b border-[var(--border-soft)]">
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-4"></div></td>
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-48"></div></td>
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-16"></div></td>
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-16"></div></td>
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-10 ml-auto"></div></td>
                  </tr>
                ))
              ) : filteredChannels.length === 0 ? (
                <tr><td colSpan={5} className="px-6 py-14 text-center text-[var(--text-secondary)]">
                  <Film size={28} className="mx-auto mb-3 text-[var(--text-tertiary)]" />
                  <div className="font-semibold text-[var(--text-primary)]">{channels.length ? '没有匹配的视频渠道' : '暂无视频渠道'}</div>
                  <div className="mt-1 text-xs">{channels.length ? '调整搜索或筛选条件' : '新建渠道后即可配置协议、模型与 API Key'}</div>
                </td></tr>
              ) : (
                filteredChannels.map(ch => (
                  <React.Fragment key={ch.id}>
                    <tr className={`channel-data-row group cursor-pointer ${expanded.has(ch.id) ? 'channel-data-row-expanded' : ''}`} onClick={() => toggle(ch.id)}>
                      <td className="px-3 md:px-6 py-3 md:py-4">
                        <button
                          type="button"
                          onClick={event => { event.stopPropagation(); toggle(ch.id); }}
                          title={expanded.has(ch.id) ? '收起详情' : '展开详情'}
                          aria-label={expanded.has(ch.id) ? '收起详情' : '展开详情'}
                          className="channel-expand-button"
                        >
                          {expanded.has(ch.id) ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                        </button>
                      </td>
                      <td className="px-3 md:px-6 py-3 md:py-4">
                        <div className="flex items-center gap-3">
                          <span className="channel-provider-mark"><Film size={17} /></span>
                          <div className="min-w-0">
                            <div className="font-bold text-[var(--text-primary)]">{ch.name}</div>
                            <div className="mt-0.5 max-w-xs truncate font-mono text-xs text-[var(--text-secondary)]" title={ch.base_url}>{ch.base_url}</div>
                          </div>
                        </div>
                      </td>
                      <td className="px-3 md:px-6 py-3 md:py-4">
                        <span className={`inline-flex rounded-full px-2.5 py-1 text-xs font-bold ring-1 ring-inset ring-black/5 ${ADAPTER_BADGES[ch.adapter_type] || 'bg-[var(--surface-muted)] text-[var(--text-secondary)]'}`}>
                          {ADAPTER_LABELS[ch.adapter_type] || ch.adapter_type}
                        </span>
                      </td>
                      <td className="px-3 md:px-6 py-3 md:py-4">
                          <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-1 text-xs font-bold ${STATUS_BADGE[ch.status] || STATUS_BADGE.inactive}`}>
                            <span className={`h-1.5 w-1.5 rounded-full ${ch.status === 'active' ? 'bg-emerald-500' : 'bg-[var(--text-tertiary)]'}`} />
                          {ch.status === 'active' ? '启用' : '禁用'}
                        </span>
                      </td>
                      <td className="px-3 md:px-6 py-3 md:py-4 text-right">
                        <div className="channel-row-actions">
                          <button onClick={e => { e.stopPropagation(); navigate(`/video-channels/${ch.id}/edit`); }}
                            className="channel-icon-button text-[var(--text-secondary)] hover:bg-[var(--primary-lighter)] hover:text-[var(--primary)]" title="编辑"><Edit3 size={14} /></button>
                          <button onClick={e => { e.stopPropagation(); setDeleteTarget(ch); }}
                            className="channel-icon-button text-[var(--text-secondary)] hover:bg-red-50 hover:text-red-600" title="删除"><Trash2 size={14} /></button>
                        </div>
                      </td>
                    </tr>
                    {expanded.has(ch.id) && (
                      <tr className="channel-detail-row">
                        <td colSpan={5} className="bg-[var(--surface)]/30">
                          <ChannelKeys
                            channelId={ch.id}
                            reloadSignal={reloadSignals[ch.id] || 0}
                            onAddKey={() => setKeyModal({ open: true, channelId: ch.id, channelKey: null })}
                            onEditKey={k => setKeyModal({ open: true, channelId: ch.id, channelKey: k })}
                          />
                        </td>
                      </tr>
                    )}
                  </React.Fragment>
                ))
              )}
            </tbody>
          </table>
        </div>
      </section>

      <VideoKeyModal isOpen={keyModal.open} channelKey={keyModal.channelKey}
        onClose={() => setKeyModal({ open: false, channelId: 0, channelKey: null })} onSave={handleSaveKey} />
      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title="删除视频渠道？"
        description={`删除“${deleteTarget?.name || ''}”后，其下全部 API Key 也会一并删除。`}
        confirmLabel="删除渠道"
        cancelLabel="取消"
        tone="danger"
        busy={deleting}
        onConfirm={confirmDeleteChannel}
        onCancel={() => setDeleteTarget(null)}
      />
    </div>
  );
};

export default VideoChannels;
