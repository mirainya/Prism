import React, { useEffect, useState } from 'react';
import { Plus, RefreshCw, Edit3, Trash2, ChevronDown, ChevronRight, Key, Power } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import {
  VideoChannel, VideoChannelKey,
  fetchVideoChannels, deleteVideoChannel,
  fetchVideoKeys, createVideoKey, updateVideoKey, deleteVideoKey,
} from '../services/videoApi';
import { ConfirmDialog } from '../components/ui';
import { VideoKeyModal } from './video/VideoModals';

const STATUS_BADGE: Record<string, string> = {
  active: 'bg-green-100 text-green-700',
  inactive: 'bg-gray-100 text-gray-500',
};

const ChannelKeys: React.FC<{ channelId: number; onAddKey: () => void; onEditKey: (k: VideoChannelKey) => void }> = ({ channelId, onAddKey, onEditKey }) => {
  const [keys, setKeys] = useState<VideoChannelKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [deleteTarget, setDeleteTarget] = useState<VideoChannelKey | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = async () => {
    setLoading(true);
    try { setKeys(await fetchVideoKeys(channelId)); } finally { setLoading(false); }
  };

  useEffect(() => { load(); }, [channelId]);

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

  if (loading) return <div className="py-4 text-center text-xs text-[var(--text-secondary)]">加载中...</div>;

  return (
    <div className="p-4">
      <div className="flex items-center justify-between mb-3">
        <h4 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2"><Key size={14} /> API Keys</h4>
        <button onClick={onAddKey} className="text-xs text-[var(--primary)] hover:opacity-80 flex items-center gap-1"><Plus size={14} /> 添加</button>
      </div>
      {keys.length === 0 ? (
        <p className="text-xs text-[var(--text-secondary)] text-center py-4">暂无 Key，点击添加</p>
      ) : (
        <div className="space-y-2">
          {keys.map(k => (
            <div key={k.id} className="flex items-center justify-between p-3 rounded-lg bg-[var(--surface)] border border-[var(--border-soft)] group">
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
              <div className="flex items-center gap-1 md:opacity-0 md:group-hover:opacity-100 shrink-0">
                <button onClick={() => handleToggle(k)} className="p-1.5 hover:bg-gray-200 rounded" title={k.status === 'active' ? '禁用' : '启用'}><Power size={13} /></button>
                <button onClick={() => onEditKey(k)} className="p-1.5 hover:bg-gray-200 rounded"><Edit3 size={13} /></button>
                <button onClick={() => setDeleteTarget(k)} className="p-1.5 hover:bg-red-100 text-red-500 rounded" title="删除 Key"><Trash2 size={13} /></button>
              </div>
            </div>
          ))}
        </div>
      )}
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
    setKeyModal({ open: false, channelId: 0, channelKey: null });
    // 刷新展开区
    setExpanded(prev => new Set(prev));
  };

  return (
    <div className="space-y-4 md:space-y-6">
      <div className="flex items-start sm:items-center justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">视频渠道</h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm hidden sm:block">视频生成渠道与协议配置</p>
        </div>
        <button onClick={() => navigate('/video-channels/new')}
          className="flex items-center gap-2 px-4 md:px-6 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90 transition-all shadow-sm">
          <Plus size={18} /><span className="hidden sm:inline">新建渠道</span><span className="sm:hidden">新建</span>
        </button>
      </div>

      <div className="bg-[var(--surface-card)] rounded-2xl shadow-sm border border-[var(--border-soft)] overflow-hidden">
        <div className="p-3 md:p-4 border-b border-[var(--border-soft)] flex items-center justify-end bg-[var(--surface)]/50">
          <button onClick={() => load()} className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-semibold text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
            <RefreshCw size={14} className={isLoading ? 'animate-spin' : ''} /><span className="hidden sm:inline">刷新</span>
          </button>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-left min-w-[520px]">
            <thead>
              <tr className="border-b border-[var(--border-soft)]">
                <th className="px-3 md:px-6 py-3 md:py-4 w-10"></th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">名称 / BaseURL</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">协议</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">状态</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-right">操作</th>
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
              ) : channels.length === 0 ? (
                <tr><td colSpan={5} className="px-6 py-12 text-center text-[var(--text-secondary)]">暂无视频渠道</td></tr>
              ) : (
                channels.map(ch => (
                  <React.Fragment key={ch.id}>
                    <tr className="border-b border-[var(--border-soft)] hover:bg-[var(--surface)]/50 cursor-pointer" onClick={() => toggle(ch.id)}>
                      <td className="px-3 md:px-6 py-3 md:py-4">
                        {expanded.has(ch.id) ? <ChevronDown size={16} className="text-[var(--text-secondary)]" /> : <ChevronRight size={16} className="text-[var(--text-secondary)]" />}
                      </td>
                      <td className="px-3 md:px-6 py-3 md:py-4">
                        <div className="font-medium text-[var(--text-primary)]">{ch.name}</div>
                        <div className="text-xs text-[var(--text-secondary)] font-mono mt-0.5 truncate max-w-xs">{ch.base_url}</div>
                      </td>
                      <td className="px-3 md:px-6 py-3 md:py-4">
                        <span className="px-2 py-1 rounded-md text-xs font-bold bg-blue-100 text-blue-700">{ch.adapter_type}</span>
                      </td>
                      <td className="px-3 md:px-6 py-3 md:py-4">
                        <span className={`px-2 py-1 rounded-md text-xs font-bold ${STATUS_BADGE[ch.status] || STATUS_BADGE.inactive}`}>
                          {ch.status === 'active' ? '启用' : '禁用'}
                        </span>
                      </td>
                      <td className="px-3 md:px-6 py-3 md:py-4 text-right">
                        <div className="flex items-center justify-end gap-1">
                          <button onClick={e => { e.stopPropagation(); navigate(`/video-channels/${ch.id}/edit`); }}
                            className="p-1.5 hover:bg-gray-200 rounded" title="编辑"><Edit3 size={14} /></button>
                          <button onClick={e => { e.stopPropagation(); setDeleteTarget(ch); }}
                            className="p-1.5 hover:bg-red-100 text-red-500 rounded" title="删除"><Trash2 size={14} /></button>
                        </div>
                      </td>
                    </tr>
                    {expanded.has(ch.id) && (
                      <tr className="border-b border-[var(--border-soft)]">
                        <td colSpan={5} className="bg-[var(--surface)]/30">
                          <ChannelKeys
                            channelId={ch.id}
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
      </div>

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
