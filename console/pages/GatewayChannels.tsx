import React, { useEffect, useState } from 'react';
import { Plus, RefreshCw, Edit3, Trash2, ChevronDown, ChevronRight, Key, Power, Download, MessageSquare, X, GripVertical, Server, Search, Check } from 'lucide-react';
import { DndContext, PointerSensor, closestCenter, useSensor, useSensors, DragEndEvent } from '@dnd-kit/core';
import { SortableContext, arrayMove, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import {
  GwChannel, GwChannelKey, GwAbility,
  fetchGwChannels, createGwChannel, updateGwChannel, deleteGwChannel, reorderGwChannels,
  fetchGwKeys, createGwKey, updateGwKey, deleteGwKey,
  fetchGwAbilities, deleteGwAbility, updateGwAbility,
} from '../services/gatewayApi';
import { GwChannelModal, GwKeyModal, GwPullModal } from './gateway_channels/GwChannelModals';

const PROTOCOL_COLORS: Record<string, string> = {
  openai: 'bg-emerald-100 text-emerald-700',
  anthropic: 'bg-orange-100 text-orange-700',
  volcengine: 'bg-blue-100 text-blue-700',
  google: 'bg-red-100 text-red-700',
};

// 单渠道展开区: 左 keys 列表(点选) + 右该 key 的 abilities
const ChannelDetail: React.FC<{
  channel: GwChannel;
  onAddKey: () => void;
  onEditKey: (k: GwChannelKey) => void;
  onPullKey: (k: GwChannelKey) => void;
  reloadSignal: number;
}> = ({ channel, onAddKey, onEditKey, onPullKey, reloadSignal }) => {
  const [keys, setKeys] = useState<GwChannelKey[]>([]);
  const [selectedKeyId, setSelectedKeyId] = useState<number | null>(null);
  const [abilities, setAbilities] = useState<GwAbility[]>([]);
  const [abLoading, setAbLoading] = useState(false);
  const [abSearch, setAbSearch] = useState('');
  const [editingAb, setEditingAb] = useState<GwAbility | null>(null);

  const loadKeys = async () => setKeys(await fetchGwKeys(channel.id));
  const loadAbilities = async (keyId: number) => {
    setAbLoading(true);
    try {
      setAbilities(await fetchGwAbilities({ key_id: keyId }));
    } finally {
      setAbLoading(false);
    }
  };

  useEffect(() => {
    loadKeys();
    if (selectedKeyId) loadAbilities(selectedKeyId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reloadSignal]);

  const handleSelectKey = (id: number) => {
    if (selectedKeyId === id) {
      setSelectedKeyId(null);
      setAbilities([]);
    } else {
      setSelectedKeyId(id);
      loadAbilities(id);
    }
  };

  const handleDeleteKey = async (id: string | number) => {
    if (!confirm('确定删除此 Key？其路由能力(abilities)一并删除。')) return;
    await deleteGwKey(Number(id));
    if (selectedKeyId === Number(id)) { setSelectedKeyId(null); setAbilities([]); }
    await loadKeys();
  };

  const handleDeleteAbility = async (id: number) => {
    if (!confirm('确定移除此模型能力？(仅删路由索引,可重新拉取导入)')) return;
    await deleteGwAbility(id);
    if (selectedKeyId) loadAbilities(selectedKeyId);
  };

  const handleSaveAbility = async (id: number, data: Record<string, any>) => {
    await updateGwAbility(id, data);
    setEditingAb(null);
    if (selectedKeyId) loadAbilities(selectedKeyId);
  };

  const selectedKey = keys.find(k => k.id === selectedKeyId) || null;

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-4 md:gap-6">
      {/* 左: keys */}
      <div className="bg-[var(--surface-card)] rounded-xl p-4 border border-[var(--border-soft)]">
        <div className="flex items-center justify-between mb-3">
          <h4 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
            <Key size={14} /> Key
          </h4>
          <button onClick={onAddKey} className="text-xs text-[var(--primary)] hover:opacity-80 flex items-center gap-1">
            <Plus size={14} /> 添加
          </button>
        </div>
        {keys.length === 0 ? (
          <p className="text-xs text-[var(--text-secondary)] text-center py-4">暂无 Key</p>
        ) : (
          <div className="space-y-2">
            {keys.map(k => {
              const isSel = selectedKeyId === k.id;
              return (
                <div key={k.id} onClick={() => handleSelectKey(k.id)}
                  className={`flex items-center justify-between p-2 rounded-lg group/k gap-2 cursor-pointer transition-all ${isSel ? 'bg-[var(--primary-lighter)] ring-2 ring-[var(--primary)]' : 'bg-[var(--surface)] hover:bg-[var(--primary-lighter)]/40'}`}>
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium text-[var(--text-primary)]">{k.name || `key#${k.id}`}</span>
                      <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${k.status === 1 ? 'bg-green-100 text-green-700' : 'bg-[var(--primary-lighter)] text-[var(--text-secondary)]'}`}>
                        {k.status === 1 ? '启用' : '禁用'}
                      </span>
                      {isSel && <span className="text-[10px] text-[var(--primary)] font-medium">← 查看模型</span>}
                    </div>
                    <div className="text-xs text-[var(--text-secondary)] font-mono break-all mt-1">{k.api_key}</div>
                    <div className="text-xs text-[var(--text-secondary)] mt-1">权重: {k.weight} | 并发: {k.current_conc}/{k.max_conc || '∞'}</div>
                  </div>
                  <div className="flex items-center gap-1 md:opacity-0 md:group-hover/k:opacity-100 shrink-0">
                    <button onClick={e => { e.stopPropagation(); onPullKey(k); }} className="p-1 hover:bg-gray-200 rounded" title="拉取该 key 的上游模型"><Download size={12} /></button>
                    <button onClick={e => { e.stopPropagation(); onEditKey(k); }} className="p-1 hover:bg-gray-200 rounded"><Edit3 size={12} /></button>
                    <button onClick={e => { e.stopPropagation(); handleDeleteKey(k.id); }} className="p-1 hover:bg-red-100 text-red-500 rounded"><Trash2 size={12} /></button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* 右: 该 key 的 abilities */}
      <div className="bg-[var(--surface-card)] rounded-xl p-4 border border-[var(--border-soft)]">
        <div className="flex items-center justify-between mb-3">
          <h4 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
            <MessageSquare size={14} /> 模型能力
            {selectedKey && <span className="text-xs font-normal text-[var(--text-secondary)]">· {selectedKey.name || `key#${selectedKey.id}`}</span>}
          </h4>
          {selectedKey && (
            <button onClick={() => onPullKey(selectedKey)} className="text-xs text-[var(--primary)] hover:opacity-80 flex items-center gap-1">
              <Download size={14} /> 拉取
            </button>
          )}
        </div>
        {!selectedKey ? (
          <p className="text-xs text-[var(--text-secondary)] text-center py-4">← 选择一个 Key 查看/拉取其模型</p>
        ) : abLoading ? (
          <p className="text-xs text-[var(--text-secondary)] text-center py-4">加载中...</p>
        ) : abilities.length === 0 ? (
          <p className="text-xs text-[var(--text-secondary)] text-center py-4">该 Key 暂无模型，点「拉取」导入</p>
        ) : (() => {
          const kw = abSearch.trim().toLowerCase();
          const filtered = kw
            ? abilities.filter(ab => ab.model_name.toLowerCase().includes(kw) || (ab.vendor_model || '').toLowerCase().includes(kw))
            : abilities;
          return (
            <div className="space-y-2">
              {abilities.length > 6 && (
                <div className="relative">
                  <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" size={13} />
                  <input value={abSearch} onChange={e => setAbSearch(e.target.value)} placeholder="搜索模型..."
                    className="w-full pl-8 pr-2 py-1.5 bg-[var(--surface)] border border-[var(--border-soft)] rounded-lg text-xs focus:outline-none focus:ring-1 focus:ring-[var(--primary)]" />
                </div>
              )}
              <div className="text-[10px] text-[var(--text-secondary)] px-1">共 {abilities.length} 个模型{kw && ` · 命中 ${filtered.length}`}</div>
              <div className="border border-[var(--border-soft)] rounded-lg overflow-hidden divide-y divide-[var(--border-soft)] max-h-80 overflow-y-auto">
                {/* 表头 */}
                <div className="flex items-center gap-2 px-2.5 py-1.5 bg-[var(--surface)]/70 text-[10px] font-bold text-[var(--text-secondary)] uppercase tracking-wider sticky top-0">
                  <span className="flex-1 min-w-0">模型 / 上游名</span>
                  <span className="w-10 text-center">优先级</span>
                  <span className="w-12 text-center">状态</span>
                  <span className="w-14 text-right">操作</span>
                </div>
                {filtered.length === 0 ? (
                  <div className="px-2.5 py-4 text-center text-xs text-[var(--text-secondary)]">无匹配模型</div>
                ) : filtered.map(ab => (
                  <div key={ab.id} className={`flex items-center gap-2 px-2.5 py-1.5 text-xs group/ab hover:bg-[var(--surface)]/60 ${ab.status !== 1 ? 'opacity-55' : ''}`}>
                    <div className="flex-1 min-w-0">
                      <div className="font-medium text-[var(--text-primary)] truncate">{ab.model_name}</div>
                      {ab.vendor_model && ab.vendor_model !== ab.model_name && (
                        <div className="text-[10px] text-[var(--text-secondary)] font-mono truncate">↑ {ab.vendor_model}</div>
                      )}
                    </div>
                    <span className="w-10 text-center text-[var(--text-secondary)]">{ab.priority !== 0 ? `P${ab.priority}` : '—'}</span>
                    <span className="w-12 flex justify-center">
                      <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${ab.status === 1 ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-500'}`}>
                        {ab.status === 1 ? '启用' : '禁用'}
                      </span>
                    </span>
                    <div className="w-14 flex items-center justify-end gap-0.5 md:opacity-0 md:group-hover/ab:opacity-100 transition-opacity">
                      <button onClick={() => setEditingAb(ab)} className="p-1 text-[var(--text-secondary)] hover:text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded" title="编辑"><Edit3 size={12} /></button>
                      <button onClick={() => handleDeleteAbility(ab.id)} className="p-1 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded" title="移除"><Trash2 size={12} /></button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          );
        })()}
      </div>
      {editingAb && (
        <AbilityEditModal ability={editingAb} onClose={() => setEditingAb(null)} onSave={handleSaveAbility} />
      )}
    </div>
  );
};

// 能力编辑弹窗: 编辑 vendor_model/priority/status/price(对接 UpdateGwAbility 白名单)
const AbilityEditModal: React.FC<{
  ability: GwAbility;
  onClose: () => void;
  onSave: (id: number, data: Record<string, any>) => Promise<void>;
}> = ({ ability, onClose, onSave }) => {
  const [vendorModel, setVendorModel] = useState(ability.vendor_model || '');
  const [priority, setPriority] = useState(String(ability.priority ?? 0));
  const [status, setStatus] = useState(ability.status);
  const [priceMode, setPriceMode] = useState(ability.price_mode || 'token');
  const [inputPrice, setInputPrice] = useState(ability.input_price || '0');
  const [outputPrice, setOutputPrice] = useState(ability.output_price || '0');
  const [saving, setSaving] = useState(false);

  const handleSubmit = async () => {
    setSaving(true);
    try {
      await onSave(ability.id, {
        vendor_model: vendorModel.trim(),
        priority: Number(priority) || 0,
        status,
        price_mode: priceMode,
        input_price: inputPrice,
        output_price: outputPrice,
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4" onClick={onClose}>
      <div className="bg-[var(--surface-card)] rounded-2xl shadow-xl w-full max-w-md max-h-[90vh] overflow-y-auto" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between px-5 py-4 border-b border-[var(--border-soft)]">
          <h3 className="text-base font-bold text-[var(--text-primary)]">编辑模型能力</h3>
          <button onClick={onClose} className="p-1 text-[var(--text-secondary)] hover:text-[var(--text-primary)] rounded"><X size={18} /></button>
        </div>
        <div className="p-5 space-y-4">
          <div>
            <label className="block text-xs font-bold text-[var(--text-secondary)] mb-1">模型名(路由标识,不可改)</label>
            <div className="px-3 py-2 bg-[var(--surface)] rounded-lg text-sm font-mono text-[var(--text-primary)]">{ability.model_name}</div>
          </div>
          <div>
            <label className="block text-xs font-bold text-[var(--text-secondary)] mb-1">上游模型名(vendor_model)</label>
            <input value={vendorModel} onChange={e => setVendorModel(e.target.value)} placeholder={`留空=用 ${ability.model_name}`}
              className="w-full px-3 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm font-mono focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
            <p className="text-[10px] text-[var(--text-secondary)] mt-1">上游真实模型名。留空则用模型名本身发给上游。</p>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-bold text-[var(--text-secondary)] mb-1">优先级(降序)</label>
              <input type="number" value={priority} onChange={e => setPriority(e.target.value)}
                className="w-full px-3 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
            </div>
            <div>
              <label className="block text-xs font-bold text-[var(--text-secondary)] mb-1">状态</label>
              <div className="flex gap-2 mt-0.5">
                <button onClick={() => setStatus(1)} className={`flex-1 py-2 rounded-lg text-xs font-bold border ${status === 1 ? 'bg-green-100 text-green-700 border-green-300' : 'bg-[var(--surface)] text-[var(--text-secondary)] border-[var(--border-soft)]'}`}>启用</button>
                <button onClick={() => setStatus(0)} className={`flex-1 py-2 rounded-lg text-xs font-bold border ${status === 0 ? 'bg-gray-200 text-gray-600 border-gray-300' : 'bg-[var(--surface)] text-[var(--text-secondary)] border-[var(--border-soft)]'}`}>禁用</button>
              </div>
            </div>
          </div>
          <div>
            <label className="block text-xs font-bold text-[var(--text-secondary)] mb-1">计价模式</label>
            <div className="flex gap-2">
              <button onClick={() => setPriceMode('token')} className={`flex-1 py-2 rounded-lg text-xs font-bold border ${priceMode === 'token' ? 'bg-[var(--primary-lighter)] text-[var(--primary)] border-[var(--primary)]' : 'bg-[var(--surface)] text-[var(--text-secondary)] border-[var(--border-soft)]'}`}>按 Token</button>
              <button onClick={() => setPriceMode('request')} className={`flex-1 py-2 rounded-lg text-xs font-bold border ${priceMode === 'request' ? 'bg-[var(--primary-lighter)] text-[var(--primary)] border-[var(--primary)]' : 'bg-[var(--surface)] text-[var(--text-secondary)] border-[var(--border-soft)]'}`}>按次</button>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-bold text-[var(--text-secondary)] mb-1">{priceMode === 'request' ? '每次价格' : '输入价格'}</label>
              <input value={inputPrice} onChange={e => setInputPrice(e.target.value)}
                className="w-full px-3 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
            </div>
            <div>
              <label className="block text-xs font-bold text-[var(--text-secondary)] mb-1">输出价格{priceMode === 'request' ? '(按次忽略)' : ''}</label>
              <input value={outputPrice} onChange={e => setOutputPrice(e.target.value)} disabled={priceMode === 'request'}
                className="w-full px-3 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)] disabled:opacity-50" />
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2 px-5 py-4 border-t border-[var(--border-soft)]">
          <button onClick={onClose} className="px-4 py-2 text-sm font-semibold text-[var(--text-secondary)] hover:text-[var(--text-primary)]">取消</button>
          <button onClick={handleSubmit} disabled={saving}
            className="flex items-center gap-1.5 px-5 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90 disabled:opacity-50">
            <Check size={16} /> {saving ? '保存中...' : '保存'}
          </button>
        </div>
      </div>
    </div>
  );
};

// 渠道行
const ChannelRow: React.FC<{
  channel: GwChannel;
  canDrag: boolean;
  expanded: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onToggleStatus: () => void;
  onAddKey: () => void;
  onEditKey: (k: GwChannelKey) => void;
  onPullKey: (k: GwChannelKey) => void;
  reloadSignal: number;
}> = ({ channel, canDrag, expanded, onToggle, onEdit, onDelete, onToggleStatus, onAddKey, onEditKey, onPullKey, reloadSignal }) => {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: String(channel.id), disabled: !canDrag });
  const rowStyle = { transform: CSS.Transform.toString(transform), transition, opacity: isDragging ? 0.4 : 1 };
  const protoColor = PROTOCOL_COLORS[channel.protocol] || 'bg-gray-100 text-gray-600';

  return (
    <>
      <tr ref={setNodeRef} style={rowStyle} className="hover:bg-[var(--surface)] transition-colors group border-b border-[var(--border-soft)]">
        <td className="px-3 md:px-6 py-3 md:py-4">
          <div className="flex items-center gap-1">
            {canDrag && (
              <span {...attributes} {...listeners} className="p-1 text-[var(--text-secondary)] hover:text-[var(--primary)] cursor-grab active:cursor-grabbing touch-none" title="拖拽排序">
                <GripVertical size={14} />
              </span>
            )}
            <button onClick={onToggle} className="p-1 hover:bg-[var(--primary-lighter)] rounded">
              {expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
            </button>
          </div>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4">
          <div className="flex items-center gap-2 md:gap-3">
            <div className="w-8 h-8 md:w-10 md:h-10 rounded-xl bg-indigo-100 flex items-center justify-center text-[var(--primary)] flex-shrink-0">
              <Server size={16} />
            </div>
            <div className="min-w-0">
              <div className="text-sm font-bold text-[var(--text-primary)] truncate">{channel.name}</div>
              <div className="text-xs text-[var(--text-secondary)] font-mono truncate">{channel.base_url}</div>
            </div>
          </div>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4">
          <span className={`px-2 py-1 rounded-full text-[10px] font-bold uppercase tracking-wider ${protoColor}`}>{channel.protocol}</span>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4">
          <span className={`px-2 py-1 rounded-full text-[10px] font-bold ${channel.status === 1 ? 'bg-green-100 text-green-700' : 'bg-[var(--primary-lighter)] text-[var(--text-secondary)]'}`}>
            {channel.status === 1 ? '已启用' : '已禁用'}
          </span>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4 text-right">
          <div className="flex items-center justify-end gap-1 md:opacity-0 md:group-hover:opacity-100 transition-opacity">
            <button onClick={onToggleStatus} className={`p-1.5 md:p-2 rounded-lg ${channel.status === 1 ? 'text-yellow-600 hover:bg-yellow-50' : 'text-green-600 hover:bg-green-50'}`} title={channel.status === 1 ? '禁用' : '启用'}>
              <Power size={14} />
            </button>
            <button onClick={onEdit} className="p-1.5 md:p-2 text-[var(--text-secondary)] hover:text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg" title="编辑"><Edit3 size={14} /></button>
            <button onClick={onDelete} className="p-1.5 md:p-2 text-[var(--text-secondary)] hover:text-red-600 hover:bg-red-50 rounded-lg" title="删除"><Trash2 size={14} /></button>
          </div>
        </td>
      </tr>
      {expanded && (
        <tr>
          <td colSpan={5} className="bg-[var(--surface)]/50 px-3 md:px-6 py-3 md:py-4">
            <ChannelDetail channel={channel} onAddKey={onAddKey} onEditKey={onEditKey} onPullKey={onPullKey} reloadSignal={reloadSignal} />
          </td>
        </tr>
      )}
    </>
  );
};

const GatewayChannels: React.FC = () => {
  const [channels, setChannels] = useState<GwChannel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  // per-channel 重载信号: 增删 key/import 后 +1 触发对应 ChannelDetail 刷新
  const [reloadSignals, setReloadSignals] = useState<Record<number, number>>({});

  const [channelModal, setChannelModal] = useState<{ open: boolean; channel: GwChannel | null }>({ open: false, channel: null });
  const [keyModal, setKeyModal] = useState<{ open: boolean; channelId: number; key: GwChannelKey | null }>({ open: false, channelId: 0, key: null });
  const [pullModal, setPullModal] = useState<{ open: boolean; keyId: number; keyName: string; channelId: number }>({ open: false, keyId: 0, keyName: '', channelId: 0 });

  const bumpReload = (channelId: number) =>
    setReloadSignals(prev => ({ ...prev, [channelId]: (prev[channelId] || 0) + 1 }));

  const load = async (skeleton = true) => {
    if (skeleton) setIsLoading(true);
    try {
      setChannels(await fetchGwChannels());
    } finally {
      if (skeleton) setIsLoading(false);
    }
  };

  useEffect(() => { load(); }, []);

  const toggle = (id: number) => {
    setExpanded(prev => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  };

  const handleSaveChannel = async (data: Partial<GwChannel>) => {
    if (channelModal.channel) await updateGwChannel(channelModal.channel.id, data);
    else await createGwChannel(data);
    setChannelModal({ open: false, channel: null });
    await load(false);
  };

  const handleDeleteChannel = async (id: number) => {
    if (!confirm('确定删除此渠道？其下所有 Key 与路由能力一并删除。')) return;
    await deleteGwChannel(id);
    await load(false);
  };

  const handleToggleChannelStatus = async (ch: GwChannel) => {
    await updateGwChannel(ch.id, { status: ch.status === 1 ? 0 : 1 });
    await load(false);
  };

  const handleSaveKey = async (data: Partial<GwChannelKey>) => {
    const cid = keyModal.channelId;
    if (keyModal.key) await updateGwKey(keyModal.key.id, data);
    else await createGwKey({ ...data, channel_id: cid });
    setKeyModal({ open: false, channelId: 0, key: null });
    bumpReload(cid);
  };

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));

  const handleDragEnd = async (e: DragEndEvent) => {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    const oldIndex = channels.findIndex(c => String(c.id) === active.id);
    const newIndex = channels.findIndex(c => String(c.id) === over.id);
    if (oldIndex < 0 || newIndex < 0) return;
    const reordered = arrayMove<GwChannel>(channels, oldIndex, newIndex);
    setChannels(reordered);
    try {
      await reorderGwChannels(reordered.map(c => c.id));
    } catch {
      load();
    }
  };

  return (
    <div className="space-y-4 md:space-y-6">
      <div className="flex items-start sm:items-center justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">网关渠道</h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm hidden sm:block">Chat 路由:一渠道一协议,渠道→Key→模型能力</p>
        </div>
        <button onClick={() => setChannelModal({ open: true, channel: null })}
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
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragStart={() => setExpanded(new Set())} onDragEnd={handleDragEnd}>
            <table className="w-full text-left min-w-[560px]">
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
                  <tr><td colSpan={5} className="px-6 py-12 text-center text-[var(--text-secondary)]">暂无渠道数据</td></tr>
                ) : (
                  <SortableContext items={channels.map(c => String(c.id))} strategy={verticalListSortingStrategy}>
                    {channels.map(ch => (
                      <ChannelRow key={ch.id} channel={ch} canDrag expanded={expanded.has(ch.id)}
                        onToggle={() => toggle(ch.id)}
                        onEdit={() => setChannelModal({ open: true, channel: ch })}
                        onDelete={() => handleDeleteChannel(ch.id)}
                        onToggleStatus={() => handleToggleChannelStatus(ch)}
                        onAddKey={() => setKeyModal({ open: true, channelId: ch.id, key: null })}
                        onEditKey={k => setKeyModal({ open: true, channelId: ch.id, key: k })}
                        onPullKey={k => setPullModal({ open: true, keyId: k.id, keyName: k.name || `key#${k.id}`, channelId: ch.id })}
                        reloadSignal={reloadSignals[ch.id] || 0}
                      />
                    ))}
                  </SortableContext>
                )}
              </tbody>
            </table>
          </DndContext>
        </div>
      </div>

      <GwChannelModal isOpen={channelModal.open} channel={channelModal.channel}
        onClose={() => setChannelModal({ open: false, channel: null })} onSave={handleSaveChannel} />
      <GwKeyModal isOpen={keyModal.open} channelKey={keyModal.key}
        onClose={() => setKeyModal({ open: false, channelId: 0, key: null })} onSave={handleSaveKey} />
      <GwPullModal isOpen={pullModal.open} keyId={pullModal.keyId} keyName={pullModal.keyName}
        onClose={() => setPullModal({ open: false, keyId: 0, keyName: '', channelId: 0 })}
        onImported={() => bumpReload(pullModal.channelId)} />
    </div>
  );
};

export default GatewayChannels;
