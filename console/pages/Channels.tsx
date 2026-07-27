import React, { useEffect, useState } from 'react';
import { Plus, Search, RefreshCw, Edit3, Trash2, Shield, ChevronDown, ChevronRight, Key, Cpu, X, Power, Copy, GripVertical } from 'lucide-react';
import { DndContext, PointerSensor, closestCenter, useSensor, useSensors, DragStartEvent, DragEndEvent } from '@dnd-kit/core';
import { SortableContext, arrayMove, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import {
    fetchChannels,
    createChannel,
    updateChannel,
    deleteChannel,
    reorderChannels,
    fetchChannelAccounts,
    createChannelAccount,
    updateChannelAccount,
    deleteChannelAccount,
    fetchChannelCapabilities,
    updateChannelCapability,
    deleteChannelCapability,
    fetchCapabilities,
    clearCircuitState,
} from '../services/api';
import {Channel, ChannelAccount, ChannelCapability, Capability} from '../types';
import { ChannelModal, AccountModal } from './ChannelModals';
import ChannelCapabilityModal from './capabilities/ChannelCapabilityModal';

const STATUS_MAP: Record<number, { label: string; color: string }> = {
  1: { label: '已启用', color: 'bg-green-100 text-green-700' },
  0: { label: '已禁用', color: 'bg-[var(--primary-lighter)] text-[var(--text-primary)]' },
};

// formatCircuitCountdown 距离熔断到期的剩余时间(人类可读)
const formatCircuitCountdown = (disabledUntil: string): string => {
  const ms = new Date(disabledUntil).getTime() - Date.now();
  if (ms <= 0) return '即将';
  const min = Math.ceil(ms / 60000);
  if (min < 60) return `${min}分钟`;
  const h = Math.floor(min / 60);
  const m = min % 60;
  return m > 0 ? `${h}时${m}分` : `${h}小时`;
};

// 渠道行组件
const ChannelRow: React.FC<{
  channel: Channel;
  canDrag: boolean;
  expanded: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onDelete: () => void;
  onToggleStatus: () => void;
  accounts: ChannelAccount[];
    capabilities: ChannelCapability[];
  onAddAccount: () => void;
  onEditAccount: (acc: ChannelAccount) => void;
  onDeleteAccount: (id: string) => void;
  onToggleAccountStatus: (acc: ChannelAccount) => void;
    onAddCapability: (acc: ChannelAccount) => void;
    onEditCapability: (c: ChannelCapability) => void;
    onDeleteCapability: (id: string) => void;
    onToggleCapabilityStatus: (c: ChannelCapability) => void;
    onClearCircuit: (accountId: string, modelCode: string) => void;
}> = ({
  channel, canDrag, expanded, onToggle, onEdit, onDelete, onToggleStatus,
          accounts, capabilities, onAddAccount, onEditAccount, onDeleteAccount, onToggleAccountStatus,
          onAddCapability, onEditCapability, onDeleteCapability, onToggleCapabilityStatus, onClearCircuit
}) => {
  const status = STATUS_MAP[channel.status] || STATUS_MAP[0];
  const [copiedAccountId, setCopiedAccountId] = useState<string | null>(null);
  // 选中的 key: 选中后右侧「能力端点」区只展示该 key 的端点
  const [selectedAccountId, setSelectedAccountId] = useState<string | null>(null);
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: channel.id, disabled: !canDrag });
  const rowStyle = { transform: CSS.Transform.toString(transform), transition, opacity: isDragging ? 0.4 : 1 };

  const handleSelectKey = (accId: string) => {
    if (selectedAccountId === accId) {
      setSelectedAccountId(null); // 再点一次取消选中
    } else {
      setSelectedAccountId(accId);
    }
  };

  const selectedAccount = accounts.find(a => a.id === selectedAccountId) || null;
  // 能力端点跟随选中 Key，关联表是唯一来源。
  const keyCapabilities = selectedAccountId
    ? capabilities.filter(c => c.accountBindings.some(binding => binding.accountId === selectedAccountId))
    : [];

  const handleCopyApiKey = async (accountId: string, apiKey: string) => {
    if (!apiKey) return;
    try {
      await navigator.clipboard.writeText(apiKey);
      setCopiedAccountId(accountId);
      window.setTimeout(() => {
        setCopiedAccountId(current => current === accountId ? null : current);
      }, 2000);
    } catch (error) {
      console.error('Failed to copy API key:', error);
    }
  };

  return (
    <>
      <tr ref={setNodeRef} style={rowStyle} className="hover:bg-[var(--surface)] transition-colors group border-b border-[var(--border-soft)]">
        <td className="px-3 md:px-6 py-3 md:py-4">
          <div className="flex items-center gap-1">
            {canDrag && (
              <span {...attributes} {...listeners}
                className="p-1 text-[var(--text-secondary)] hover:text-[var(--primary)] cursor-grab active:cursor-grabbing touch-none"
                title="拖拽排序">
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
            <div className="w-8 h-8 md:w-10 md:h-10 rounded-xl bg-indigo-100 flex items-center justify-center text-[var(--primary)] font-bold uppercase text-xs flex-shrink-0">
              {channel.type.substring(0, 2)}
            </div>
            <div className="min-w-0">
              <div className="text-sm font-bold text-[var(--text-primary)] truncate">{channel.name}</div>
              <div className="text-xs text-[var(--text-secondary)] flex items-center gap-1">
                <Shield size={10} />
                {channel.type}
              </div>
            </div>
          </div>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4">
          <span className={`px-2 py-1 rounded-full text-[10px] font-bold uppercase tracking-wider ${status.color}`}>
            {status.label}
          </span>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4 text-center hidden sm:table-cell">
          <span className="text-sm font-semibold text-[var(--text-primary)]">{channel.accountsCount}</span>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4 text-right">
          <div className="flex items-center justify-end gap-1 md:opacity-0 md:group-hover:opacity-100 transition-opacity">
            <button onClick={onToggleStatus} className={`p-1.5 md:p-2 rounded-lg ${channel.status === 1 ? 'text-yellow-600 hover:bg-yellow-50' : 'text-green-600 hover:bg-green-50'}`} title={channel.status === 1 ? '禁用' : '启用'}>
              <Power size={14} />
            </button>
            <button onClick={onEdit} className="p-1.5 md:p-2 text-[var(--text-secondary)] hover:text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg" title="编辑">
              <Edit3 size={14} />
            </button>
            <button onClick={onDelete} className="p-1.5 md:p-2 text-[var(--text-secondary)] hover:text-red-600 hover:bg-red-50 rounded-lg" title="删除">
              <Trash2 size={14} />
            </button>
          </div>
        </td>
      </tr>
      {expanded && (
        <tr>
          <td colSpan={5} className="bg-[var(--surface)]/50 px-3 md:px-6 py-3 md:py-4">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4 md:gap-6">
              {/* 账号列表(点击选中 → 右侧过滤该 key 的模型) */}
              <div className="bg-[var(--surface-card)] rounded-xl p-4 border border-[var(--border-soft)]">
                <div className="flex items-center justify-between mb-3">
                  <h4 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
                    <Key size={14} /> 账号 (Key)
                  </h4>
                  <button onClick={onAddAccount} className="text-xs text-[var(--primary)] hover:text-[var(--primary)] flex items-center gap-1">
                    <Plus size={14} /> 添加
                  </button>
                </div>
                {accounts.length === 0 ? (
                  <p className="text-xs text-[var(--text-secondary)] text-center py-4">暂无账号</p>
                ) : (
                  <div className="space-y-2">
                    {accounts.map(acc => {
                      const isSelected = selectedAccountId === acc.id;
                      return (
                      <div key={acc.id}
                        onClick={() => handleSelectKey(acc.id)}
                        className={`flex items-center justify-between p-2 rounded-lg group/acc gap-2 cursor-pointer transition-all ${isSelected ? 'bg-[var(--primary-lighter)] ring-2 ring-[var(--primary)]' : 'bg-[var(--surface)] hover:bg-[var(--primary-lighter)]/40'}`}>
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-medium text-[var(--text-primary)]">{acc.name}</span>
                            <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${acc.status === 1 ? 'bg-green-100 text-green-700' : 'bg-[var(--primary-lighter)] text-[var(--text-secondary)]'}`}>
                              {acc.status === 1 ? '启用' : '禁用'}
                            </span>
                            {isSelected && <span className="text-[10px] text-[var(--primary)] font-medium">← 查看模型</span>}
                          </div>
                          <div className="text-xs text-[var(--text-secondary)] font-mono break-all mt-1">{acc.apiKey || acc.maskedKey || '-'}</div>
                          <div className={`text-xs mt-1 ${acc.maxTasks > 0 && acc.currentTasks >= acc.maxTasks ? 'text-red-500 font-bold' : 'text-[var(--text-secondary)]'}`}>权重: {acc.weight} | 并发: {acc.currentTasks}/{acc.maxTasks || '∞'}</div>
                          {acc.circuitStates && acc.circuitStates.length > 0 && (
                            <div className="flex flex-col gap-0.5 mt-1">
                              {acc.circuitStates.map(cs => (
                                <div key={cs.modelCode} className="flex items-center gap-1 text-[10px] text-yellow-700">
                                  <span className="px-1.5 py-0.5 rounded bg-yellow-100 font-medium">熔断 {cs.modelCode}</span>
                                  <span>{cs.statusCode || '?'} · {formatCircuitCountdown(cs.disabledUntil)}后恢复</span>
                                  <button onClick={e => { e.stopPropagation(); onClearCircuit(acc.id, cs.modelCode); }} className="text-[var(--text-secondary)] hover:text-red-500" title="立即解除熔断"><X size={10} /></button>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>
                        <div className="flex items-center gap-1 md:opacity-0 md:group-hover/acc:opacity-100 shrink-0">
                          <button onClick={e => { e.stopPropagation(); handleCopyApiKey(acc.id, acc.apiKey || acc.maskedKey || ''); }} className="p-1 hover:bg-gray-200 rounded" title="复制 API Key"><Copy size={12} /></button>
                          {copiedAccountId === acc.id && <span className="text-[10px] font-medium text-green-600 px-1">已复制</span>}
                          <button onClick={e => { e.stopPropagation(); onToggleAccountStatus(acc); }} className="p-1 hover:bg-gray-200 rounded"><Power size={12} /></button>
                          <button onClick={e => { e.stopPropagation(); onEditAccount(acc); }} className="p-1 hover:bg-gray-200 rounded"><Edit3 size={12} /></button>
                          <button onClick={e => { e.stopPropagation(); onDeleteAccount(acc.id); }} className="p-1 hover:bg-red-100 text-red-500 rounded"><Trash2 size={12} /></button>
                        </div>
                      </div>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* 右侧: 能力端点(image/video),跟随选中 key */}
              <div className="space-y-4">
                {/* 能力端点(image/video),跟随选中 key */}
                <div className="bg-[var(--surface-card)] rounded-xl p-4 border border-[var(--border-soft)]">
                  <div className="flex items-center justify-between mb-3">
                    <h4 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
                        <Cpu size={14}/> 能力端点 <span className="text-[10px] font-normal px-1.5 py-0.5 rounded bg-[var(--primary-lighter)] text-[var(--text-secondary)]">图像/视频</span>
                        {selectedAccount && <span className="text-xs font-normal text-[var(--text-secondary)]">· {selectedAccount.name}</span>}
                    </h4>
                      {selectedAccount && (
                        <button onClick={() => onAddCapability(selectedAccount)}
                                className="text-xs text-[var(--primary)] hover:text-[var(--primary)] flex items-center gap-1">
                          <Plus size={14} /> 添加
                        </button>
                      )}
                  </div>
                    {!selectedAccount ? (
                        <p className="text-xs text-[var(--text-secondary)] text-center py-4">← 选择一个账号查看/添加其能力端点</p>
                    ) : keyCapabilities.length === 0 ? (
                        <p className="text-xs text-[var(--text-secondary)] text-center py-4">该 key 暂无能力端点</p>
                  ) : (
                    <div className="space-y-2">
                        {keyCapabilities.map(c => (
                            <div key={c.id}
                                 className="flex items-center justify-between p-2 bg-[var(--surface)] rounded-lg group/cap gap-2">
                          <div className="min-w-0 flex-1">
                              <div className="flex items-center gap-2 flex-wrap">
                                  <span className="text-sm font-medium text-[var(--text-primary)]">{c.name || c.model || c.capabilityCode}</span>
                                  <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${c.status === 1 ? 'bg-green-100 text-green-700' : 'bg-[var(--primary-lighter)] text-[var(--text-secondary)]'}`}>
                                    {c.status === 1 ? '启用' : '禁用'}
                                  </span>
                                  {c.modelType && (
                                      <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${
                                          c.modelType === 'chat' ? 'bg-sky-100 text-sky-700' :
                                          c.modelType === 'image' ? 'bg-pink-100 text-pink-700' :
                                          c.modelType === 'video' ? 'bg-violet-100 text-violet-700' :
                                          'bg-gray-100 text-gray-600'
                                      }`}>{c.modelType}</span>
                                  )}
                              </div>
                            <div className="text-xs text-[var(--text-secondary)]">
                                {c.capabilityCode}{c.model ? ` → ${c.model}` : ''} | ¥{c.price}
                            </div>
                          </div>
                                <div className="flex items-center gap-1 md:opacity-0 md:group-hover/cap:opacity-100 shrink-0">
                                    <button onClick={() => onToggleCapabilityStatus(c)}
                                            className="p-1 hover:bg-gray-200 rounded"><Power size={12}/></button>
                                    <button onClick={() => onEditCapability(c)} className="p-1 hover:bg-gray-200 rounded">
                                        <Edit3 size={12}/></button>
                                    <button onClick={() => onDeleteCapability(c.id)}
                                            className="p-1 hover:bg-red-100 text-red-500 rounded"><Trash2 size={12}/>
                                    </button>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            </div>
          </td>
        </tr>
      )}
    </>
  );
};

const Channels: React.FC = () => {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [accounts, setAccounts] = useState<Record<string, ChannelAccount[]>>({});
    const [capabilities, setCapabilities] = useState<Record<string, ChannelCapability[]>>({});
  const [isLoading, setIsLoading] = useState(true);
  const [expandedChannels, setExpandedChannels] = useState<Set<string>>(new Set());
  const [searchTerm, setSearchTerm] = useState('');
  const [capabilityDefs, setCapabilityDefs] = useState<Capability[]>([]);

  // Modal states
  const [channelModal, setChannelModal] = useState<{ open: boolean; channel: Channel | null }>({ open: false, channel: null });
  const [accountModal, setAccountModal] = useState<{ open: boolean; channelId: string; account: ChannelAccount | null }>({ open: false, channelId: '', account: null });
  const [ccModal, setCcModal] = useState<{ open: boolean; channelId: string; accountId: string; cc: ChannelCapability | null }>({ open: false, channelId: '', accountId: '', cc: null });

  // showSkeleton=false: 静默刷新(不显示骨架屏),避免编辑/保存后表格整体重渲染导致滚动跳顶
  const loadData = async (showSkeleton = true) => {
    if (showSkeleton) setIsLoading(true);
    try {
      const [channelsData, capDefs] = await Promise.all([fetchChannels(), fetchCapabilities()]);
      setChannels(channelsData);
      setCapabilityDefs(capDefs);
    } finally {
      if (showSkeleton) setIsLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, []);

  const loadChannelDetails = async (channelId: string) => {
      const [accountsData, capabilitiesData] = await Promise.all([
      fetchChannelAccounts(channelId),
          fetchChannelCapabilities(channelId),
    ]);
    setAccounts(prev => ({ ...prev, [channelId]: accountsData }));
      setCapabilities(prev => ({...prev, [channelId]: capabilitiesData}));
  };

  const toggleExpand = async (channelId: string) => {
    const newExpanded = new Set(expandedChannels);
    if (newExpanded.has(channelId)) {
      newExpanded.delete(channelId);
    } else {
      newExpanded.add(channelId);
      if (!accounts[channelId]) {
        await loadChannelDetails(channelId);
      }
    }
    setExpandedChannels(newExpanded);
  };

  const handleSaveChannel = async (data: any) => {
    if (channelModal.channel) {
      await updateChannel(channelModal.channel.id, data);
    } else {
      await createChannel(data);
    }
    await loadData(false);
  };

  const handleDeleteChannel = async (id: string) => {
    if (!confirm('确定删除此渠道？')) return;
    await deleteChannel(id);
    await loadData(false);
  };

  const handleToggleChannelStatus = async (channel: Channel) => {
    await updateChannel(channel.id, { status: channel.status === 1 ? 0 : 1 });
    await loadData(false);
  };

  const handleSaveAccount = async (data: any) => {
    const channelId = accountModal.channelId;
    if (accountModal.account) {
      await updateChannelAccount(accountModal.account.id, data);
    } else {
      await createChannelAccount(data);
    }
    await loadChannelDetails(channelId);
    await loadData(false);
  };

  const handleDeleteAccount = async (channelId: string, accountId: string) => {
    if (!confirm('确定删除此账号？')) return;
    await deleteChannelAccount(accountId);
    await loadChannelDetails(channelId);
    await loadData(false);
  };

  const handleToggleAccountStatus = async (channelId: string, account: ChannelAccount) => {
    await updateChannelAccount(account.id, { status: account.status === 1 ? 0 : 1 });
    await loadChannelDetails(channelId);
  };

    const handleDeleteCapability = async (channelId: string, capabilityId: string) => {
        if (!confirm('确定删除此能力配置？')) return;
        await deleteChannelCapability(capabilityId);
    await loadChannelDetails(channelId);
    await loadData(false);
  };

    const handleToggleCapabilityStatus = async (channelId: string, capability: ChannelCapability) => {
        await updateChannelCapability(capability.id, {status: capability.status === 1 ? 0 : 1});
    await loadChannelDetails(channelId);
  };

    const handleClearCircuit = async (channelId: string, accountId: string, modelCode: string) => {
        try {
            await clearCircuitState(accountId, modelCode);
            await loadChannelDetails(channelId);
        } catch (err) {
            console.error('解除熔断失败:', err);
        }
    };

  const filteredChannels = channels.filter(ch =>
    ch.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
    ch.type.toLowerCase().includes(searchTerm.toLowerCase())
  );

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));
  const canDrag = !searchTerm.trim();

  const handleDragStart = (_e: DragStartEvent) => {
    setExpandedChannels(new Set()); // 拖拽时收起展开行，避免行错位
  };

  const handleDragEnd = async (e: DragEndEvent) => {
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    const oldIndex = channels.findIndex(ch => ch.id === active.id);
    const newIndex = channels.findIndex(ch => ch.id === over.id);
    if (oldIndex < 0 || newIndex < 0) return;
    const reordered = arrayMove<Channel>(channels, oldIndex, newIndex);
    setChannels(reordered); // 乐观更新
    try {
      await reorderChannels(reordered.map(ch => Number(ch.id)));
    } catch {
      loadData(); // 失败回滚
    }
  };

  return (
    <div className="space-y-4 md:space-y-6">
      <div className="flex items-start sm:items-center justify-between gap-3 flex-wrap">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">能力渠道</h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm hidden sm:block">配置图像/视频等能力端点的上游服务商与账号池</p>
        </div>
        <button
          onClick={() => setChannelModal({ open: true, channel: null })}
          className="flex items-center gap-2 px-4 md:px-6 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90 transition-all shadow-sm"
        >
          <Plus size={18} />
          <span className="hidden sm:inline">新建渠道</span><span className="sm:hidden">新建</span>
        </button>
      </div>

      <div className="bg-[var(--surface-card)] rounded-2xl shadow-sm border border-[var(--border-soft)] overflow-hidden">
        <div className="p-3 md:p-4 border-b border-[var(--border-soft)] flex items-center gap-3 md:gap-4 bg-[var(--surface)]/50">
          <div className="relative flex-1">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" size={16} />
            <input
              type="text"
              value={searchTerm}
              onChange={e => setSearchTerm(e.target.value)}
              placeholder="搜索..."
              className="w-full pl-9 pr-3 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)] focus:border-transparent"
            />
          </div>
          <button
            onClick={() => loadData()}
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-semibold text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors flex-shrink-0"
          >
            <RefreshCw size={14} className={isLoading ? 'animate-spin' : ''} />
            <span className="hidden sm:inline">刷新</span>
          </button>
        </div>

        <div className="overflow-x-auto">
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragStart={handleDragStart} onDragEnd={handleDragEnd}>
          <table className="w-full text-left min-w-[480px]">
            <thead>
              <tr className="border-b border-[var(--border-soft)]">
                <th className="px-3 md:px-6 py-3 md:py-4 w-10"></th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">名称 / 类型</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">状态</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-center hidden sm:table-cell">账号数</th>
                <th className="px-3 md:px-6 py-3 md:py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              {isLoading ? (
                Array.from({ length: 4 }).map((_, i) => (
                  <tr key={i} className="animate-pulse border-b border-[var(--border-soft)]">
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-4"></div></td>
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-48"></div></td>
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-20"></div></td>
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-12 mx-auto"></div></td>
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-12 mx-auto"></div></td>
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-10 ml-auto"></div></td>
                  </tr>
                ))
              ) : filteredChannels.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-6 py-12 text-center text-[var(--text-secondary)]">
                    暂无渠道数据
                  </td>
                </tr>
              ) : (
                <SortableContext items={filteredChannels.map(ch => ch.id)} strategy={verticalListSortingStrategy}>
                {filteredChannels.map(channel => (
                  <ChannelRow
                    key={channel.id}
                    channel={channel}
                    canDrag={canDrag}
                    expanded={expandedChannels.has(channel.id)}
                    onToggle={() => toggleExpand(channel.id)}
                    onEdit={() => setChannelModal({ open: true, channel })}
                    onDelete={() => handleDeleteChannel(channel.id)}
                    onToggleStatus={() => handleToggleChannelStatus(channel)}
                    accounts={accounts[channel.id] || []}
                    capabilities={capabilities[channel.id] || []}
                    onAddAccount={() => setAccountModal({ open: true, channelId: channel.id, account: null })}
                    onEditAccount={acc => setAccountModal({ open: true, channelId: channel.id, account: acc })}
                    onDeleteAccount={id => handleDeleteAccount(channel.id, id)}
                    onToggleAccountStatus={acc => handleToggleAccountStatus(channel.id, acc)}
                    onAddCapability={acc => setCcModal({ open: true, channelId: channel.id, accountId: acc.id, cc: null })}
                    onEditCapability={c => setCcModal({ open: true, channelId: channel.id, accountId: c.accountId, cc: c })}
                    onDeleteCapability={id => handleDeleteCapability(channel.id, id)}
                    onToggleCapabilityStatus={c => handleToggleCapabilityStatus(channel.id, c)}
                    onClearCircuit={(accountId, modelCode) => handleClearCircuit(channel.id, accountId, modelCode)}
                  />
                ))}
                </SortableContext>
              )}
            </tbody>
          </table>
          </DndContext>
        </div>
      </div>

      {/* Modals */}
      <ChannelModal
        isOpen={channelModal.open}
        channel={channelModal.channel}
        onClose={() => setChannelModal({ open: false, channel: null })}
        onSave={handleSaveChannel}
      />
      <AccountModal
        isOpen={accountModal.open}
        channelId={accountModal.channelId}
        account={accountModal.account}
        availableModels={capabilityDefs.map(c => ({ code: c.code, name: c.name, type: c.type }))}
        onClose={() => setAccountModal({ open: false, channelId: '', account: null })}
        onSave={handleSaveAccount}
      />
      <ChannelCapabilityModal
        isOpen={ccModal.open}
        capabilityCode={ccModal.cc?.capabilityCode || ''}
        channelCapability={ccModal.cc}
        channels={channels}
        capabilities={capabilityDefs}
        defaultChannelId={ccModal.channelId ? Number(ccModal.channelId) : undefined}
        defaultAccountId={ccModal.accountId ? Number(ccModal.accountId) : undefined}
        onClose={() => setCcModal({ open: false, channelId: '', accountId: '', cc: null })}
        onSave={async () => {
          const cid = ccModal.channelId;
          setCcModal({ open: false, channelId: '', accountId: '', cc: null });
          if (cid) await loadChannelDetails(cid);
          await loadData(false);
        }}
      />
    </div>
  );
};

export default Channels;
