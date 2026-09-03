import React, { useEffect, useMemo, useState } from 'react';
import { Plus, Search, RefreshCw, Edit3, Trash2, Shield, ChevronDown, ChevronRight, Key, Cpu, X, Power, Copy, GripVertical, ScanSearch, Layers3, CircleCheck, KeyRound, Boxes } from 'lucide-react';
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
import AccountEndpointModelImportModal from './capabilities/AccountEndpointModelImportModal';
import { getEndpointOperationLabel } from './capabilities/CapabilityEndpointList';
import { Select, useAppDialog } from '../components/ui';
import { PageHeader, SummaryStrip } from '../components/shell';

const STATUS_MAP: Record<number, { label: string; color: string }> = {
  1: { label: '已启用', color: 'bg-emerald-50 text-emerald-700' },
  0: { label: '已禁用', color: 'bg-[var(--surface-muted)] text-[var(--text-secondary)]' },
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
  onDiscoverAccount: (acc: ChannelAccount) => void;
    onAddCapability: (acc: ChannelAccount) => void;
    onEditCapability: (c: ChannelCapability) => void;
    onDeleteCapability: (id: string) => void;
    onToggleCapabilityStatus: (c: ChannelCapability) => void;
    onClearCircuit: (accountId: string, modelCode: string) => void;
}> = ({
  channel, canDrag, expanded, onToggle, onEdit, onDelete, onToggleStatus,
          accounts, capabilities, onAddAccount, onEditAccount, onDeleteAccount, onToggleAccountStatus,
          onAddCapability, onEditCapability, onDeleteCapability, onToggleCapabilityStatus, onClearCircuit, onDiscoverAccount
}) => {
  const status = STATUS_MAP[channel.status] || STATUS_MAP[0];
  const discoveryEnabled = channel.config?.endpoint_discovery?.enabled === true;
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
      <tr ref={setNodeRef} style={rowStyle} className={`channel-data-row group ${expanded ? 'channel-data-row-expanded' : ''}`}>
        <td className="px-3 md:px-6 py-3 md:py-4">
          <div className="flex items-center gap-1">
            {canDrag && (
              <span {...attributes} {...listeners}
                className="p-1 text-[var(--text-secondary)] hover:text-[var(--primary)] cursor-grab active:cursor-grabbing touch-none"
                title="拖拽排序">
                <GripVertical size={14} />
              </span>
            )}
            <button onClick={onToggle} className="channel-expand-button" title={expanded ? '收起详情' : '展开详情'}>
              {expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
            </button>
          </div>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4">
          <div className="flex items-center gap-2 md:gap-3">
            <div className="channel-provider-mark text-xs font-extrabold uppercase">
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
          <span className={`inline-flex items-center gap-1.5 rounded-full px-2 py-1 text-[10px] font-bold ${status.color}`}>
            <span className={`h-1.5 w-1.5 rounded-full ${channel.status === 1 ? 'bg-emerald-500' : 'bg-[var(--text-tertiary)]'}`} />
            {status.label}
          </span>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4 text-center hidden sm:table-cell">
          <span className="text-sm font-semibold text-[var(--text-primary)]">{channel.accountsCount}</span>
        </td>
        <td className="px-3 md:px-6 py-3 md:py-4 text-right">
          <div className="channel-row-actions">
            <button onClick={onToggleStatus} className={`channel-icon-button ${channel.status === 1 ? 'text-amber-600 hover:bg-amber-50' : 'text-emerald-600 hover:bg-emerald-50'}`} title={channel.status === 1 ? '禁用' : '启用'}>
              <Power size={14} />
            </button>
            <button onClick={onEdit} className="channel-icon-button text-[var(--text-secondary)] hover:bg-[var(--primary-lighter)] hover:text-[var(--primary)]" title="编辑">
              <Edit3 size={14} />
            </button>
            <button onClick={onDelete} className="channel-icon-button text-[var(--text-secondary)] hover:bg-red-50 hover:text-red-600" title="删除">
              <Trash2 size={14} />
            </button>
          </div>
        </td>
      </tr>
      {expanded && (
        <tr className="channel-detail-row">
          <td colSpan={5}>
            <div className="channel-detail-grid">
              {/* 账号列表(点击选中 → 右侧过滤该 key 的模型) */}
              <div className="channel-detail-pane">
                <div className="channel-detail-header">
                  <h4 className="channel-detail-title">
                    <Key size={14} /> 账号 (Key)
                    <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${discoveryEnabled ? 'bg-sky-50 text-sky-700' : 'bg-gray-100 text-gray-600'}`}>
                      {discoveryEnabled ? '模型发现' : '手动配置'}
                    </span>
                  </h4>
                  <button onClick={onAddAccount} className="channel-detail-action">
                    <Plus size={14} /> 添加
                  </button>
                </div>
                {accounts.length === 0 ? (
                  <div className="channel-detail-empty">暂无账号</div>
                ) : (
                  <div className="channel-detail-list">
                    {accounts.map(acc => {
                      const isSelected = selectedAccountId === acc.id;
                      return (
                      <div key={acc.id}
                        onClick={() => handleSelectKey(acc.id)}
                        className={`channel-detail-item group/acc cursor-pointer ${isSelected ? 'channel-detail-item-selected' : ''}`}>
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-medium text-[var(--text-primary)]">{acc.name}</span>
                            <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${acc.status === 1 ? 'bg-green-100 text-green-700' : 'bg-[var(--primary-lighter)] text-[var(--text-secondary)]'}`}>
                              {acc.status === 1 ? '启用' : '禁用'}
                            </span>
                            {isSelected && <span className="text-[10px] font-medium text-[var(--primary)]">已选</span>}
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
                        <div className="flex items-center gap-1 shrink-0">
                          {discoveryEnabled && <button type="button" disabled={acc.status !== 1} onClick={e => { e.stopPropagation(); onDiscoverAccount(acc); }} className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs font-medium text-sky-700 hover:bg-sky-50 disabled:cursor-not-allowed disabled:opacity-40" title="发现上游模型"><ScanSearch size={12} /><span className="hidden lg:inline">发现模型</span></button>}
                          <div className="flex items-center gap-1 md:opacity-0 md:group-hover/acc:opacity-100">
                          <button onClick={e => { e.stopPropagation(); handleCopyApiKey(acc.id, acc.apiKey || acc.maskedKey || ''); }} className="channel-icon-button text-[var(--text-secondary)] hover:bg-sky-50 hover:text-sky-600" title="复制 API Key"><Copy size={12} /></button>
                          {copiedAccountId === acc.id && <span className="text-[10px] font-medium text-green-600 px-1">已复制</span>}
                          <button onClick={e => { e.stopPropagation(); onToggleAccountStatus(acc); }} className="channel-icon-button text-[var(--text-secondary)] hover:bg-amber-50 hover:text-amber-600" title={acc.status === 1 ? '禁用' : '启用'}><Power size={12} /></button>
                          <button onClick={e => { e.stopPropagation(); onEditAccount(acc); }} className="channel-icon-button text-[var(--text-secondary)] hover:bg-[var(--primary-lighter)] hover:text-[var(--primary)]" title="编辑"><Edit3 size={12} /></button>
                          <button onClick={e => { e.stopPropagation(); onDeleteAccount(acc.id); }} className="channel-icon-button text-[var(--text-secondary)] hover:bg-red-50 hover:text-red-600" title="删除"><Trash2 size={12} /></button>
                          </div>
                        </div>
                      </div>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* 右侧: 能力端点(image/video),跟随选中 key */}
              <div className="channel-detail-pane">
                {/* 能力端点(image/video),跟随选中 key */}
                <div>
                  <div className="channel-detail-header">
                    <h4 className="channel-detail-title">
                        <Cpu size={14}/> 能力端点 <span className="text-[10px] font-normal px-1.5 py-0.5 rounded bg-[var(--primary-lighter)] text-[var(--text-secondary)]">图像/视频</span>
                        {selectedAccount && <span className="text-xs font-normal text-[var(--text-secondary)]">· {selectedAccount.name}</span>}
                    </h4>
                      {selectedAccount && (
                        <button onClick={() => onAddCapability(selectedAccount)}
                                className="channel-detail-action">
                          <Plus size={14} /> 手动添加
                        </button>
                      )}
                  </div>
                    {!selectedAccount ? (
                        <div className="channel-detail-empty">选择左侧账号查看能力端点</div>
                    ) : keyCapabilities.length === 0 ? (
                        <div className="channel-detail-empty">该 Key 暂无能力端点</div>
                  ) : (
                    <div className="channel-detail-list">
                        {keyCapabilities.map(c => (
                            <div key={c.id}
                                 className="channel-detail-item group/cap">
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
                                  <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${c.routeOperation ? 'bg-emerald-50 text-emerald-700' : 'bg-amber-50 text-amber-700'}`}>
                                    {getEndpointOperationLabel(c)}
                                  </span>
                              </div>
                            <div className="text-xs text-[var(--text-secondary)]">
                                {c.capabilityCode}{c.model ? ` → ${c.model}` : ''} | ¥{c.price}
                            </div>
                          </div>
                                <div className="flex shrink-0 items-center gap-1 md:opacity-0 md:group-hover/cap:opacity-100">
                                    <button onClick={() => onToggleCapabilityStatus(c)}
                                            className="channel-icon-button text-[var(--text-secondary)] hover:bg-amber-50 hover:text-amber-600" title={c.status === 1 ? '禁用' : '启用'}><Power size={12}/></button>
                                    <button onClick={() => onEditCapability(c)} className="channel-icon-button text-[var(--text-secondary)] hover:bg-[var(--primary-lighter)] hover:text-[var(--primary)]" title="编辑">
                                        <Edit3 size={12}/></button>
                                    <button onClick={() => onDeleteCapability(c.id)}
                                            className="channel-icon-button text-[var(--text-secondary)] hover:bg-red-50 hover:text-red-600" title="删除"><Trash2 size={12}/>
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
  const { askConfirmation } = useAppDialog();
  const [channels, setChannels] = useState<Channel[]>([]);
  const [accounts, setAccounts] = useState<Record<string, ChannelAccount[]>>({});
    const [capabilities, setCapabilities] = useState<Record<string, ChannelCapability[]>>({});
  const [isLoading, setIsLoading] = useState(true);
  const [expandedChannels, setExpandedChannels] = useState<Set<string>>(new Set());
  const [searchTerm, setSearchTerm] = useState('');
  const [statusFilter, setStatusFilter] = useState('all');
  const [typeFilter, setTypeFilter] = useState('');
  const [capabilityDefs, setCapabilityDefs] = useState<Capability[]>([]);

  // Modal states
  const [channelModal, setChannelModal] = useState<{ open: boolean; channel: Channel | null }>({ open: false, channel: null });
  const [accountModal, setAccountModal] = useState<{ open: boolean; channelId: string; account: ChannelAccount | null }>({ open: false, channelId: '', account: null });
  const [ccModal, setCcModal] = useState<{ open: boolean; channelId: string; accountId: string; cc: ChannelCapability | null }>({ open: false, channelId: '', accountId: '', cc: null });
  const [accountDiscovery, setAccountDiscovery] = useState<{ accountId: string | null; name?: string }>({ accountId: null });

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
    const confirmed = await askConfirmation({
      title: '删除渠道？',
      description: '该渠道及其关联配置将被删除，此操作无法撤销。',
      confirmLabel: '删除渠道',
      tone: 'danger',
    });
    if (!confirmed) return;
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
    const confirmed = await askConfirmation({
      title: '删除账号？',
      description: '该账号及其关联路由配置将被删除。',
      confirmLabel: '删除账号',
      tone: 'danger',
    });
    if (!confirmed) return;
    await deleteChannelAccount(accountId);
    await loadChannelDetails(channelId);
    await loadData(false);
  };

  const handleToggleAccountStatus = async (channelId: string, account: ChannelAccount) => {
    await updateChannelAccount(account.id, { status: account.status === 1 ? 0 : 1 });
    await loadChannelDetails(channelId);
  };

    const handleDeleteCapability = async (channelId: string, capabilityId: string) => {
      const confirmed = await askConfirmation({
        title: '删除能力配置？',
        description: '删除后，该账号将不能再通过此配置提供对应能力。',
        confirmLabel: '删除配置',
        tone: 'danger',
      });
      if (!confirmed) return;
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

  const typeOptions = useMemo(() => Array.from(new Set(channels.map(channel => channel.type).filter(Boolean)))
    .sort()
    .map(type => ({ label: type, value: type })), [channels]);

  const channelStats = useMemo(() => ({
    total: channels.length,
    enabled: channels.filter(channel => channel.status === 1).length,
    accounts: channels.reduce((sum, channel) => sum + (channel.accountsCount || 0), 0),
    types: typeOptions.length,
  }), [channels, typeOptions]);

  const filteredChannels = useMemo(() => {
    const keyword = searchTerm.trim().toLowerCase();
    return channels.filter(channel => {
      const matchesKeyword = !keyword
        || channel.name.toLowerCase().includes(keyword)
        || channel.type.toLowerCase().includes(keyword);
      const matchesStatus = statusFilter === 'all'
        || (statusFilter === 'enabled' ? channel.status === 1 : channel.status !== 1);
      return matchesKeyword && matchesStatus && (!typeFilter || channel.type === typeFilter);
    });
  }, [channels, searchTerm, statusFilter, typeFilter]);

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));
  const filterActive = Boolean(searchTerm.trim()) || statusFilter !== 'all' || Boolean(typeFilter);
  const canDrag = !filterActive;

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
    <div className="space-y-4">
      <PageHeader
        icon={Layers3}
        title="能力渠道"
        meta="管理异步能力的上游服务、账号池与模型端点"
        actions={(
          <>
            <button
              type="button"
              onClick={() => loadData()}
              disabled={isLoading}
              title="刷新"
              aria-label="刷新能力渠道"
              className="flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] text-[var(--text-secondary)] shadow-[var(--shadow-soft)] transition hover:border-[var(--primary)] hover:text-[var(--primary)] disabled:opacity-60"
            >
              <RefreshCw size={17} className={isLoading ? 'animate-spin' : ''} />
            </button>
            <button
              type="button"
              onClick={() => setChannelModal({ open: true, channel: null })}
              className="inline-flex h-9 items-center gap-2 rounded-lg [background:var(--brand-gradient)] px-3.5 text-sm font-bold text-white shadow-[0_6px_16px_var(--glow-color)] transition hover:-translate-y-0.5"
            >
              <Plus size={17} />
              <span className="hidden sm:inline">新建渠道</span><span className="sm:hidden">新建</span>
            </button>
          </>
        )}
      />

      <SummaryStrip items={[
        { label: '渠道总数', value: channelStats.total, icon: Layers3, color: 'var(--candy-pink)' },
        { label: '启用渠道', value: channelStats.enabled, icon: CircleCheck, color: 'var(--candy-mint)', note: channelStats.total ? `${Math.round(channelStats.enabled / channelStats.total * 100)}%` : '0%' },
        { label: '账号总数', value: channelStats.accounts, icon: KeyRound, color: 'var(--candy-blue)' },
        { label: '渠道类型', value: channelStats.types, icon: Boxes, color: 'var(--candy-yellow)' },
      ]} />

      <section className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)]">
        <div className="grid gap-3 border-b border-[var(--border-soft)] bg-[var(--surface-muted)] p-3 sm:grid-cols-[minmax(240px,1fr)_160px_160px_auto] md:p-4">
          <div className="relative min-w-0">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" size={16} />
            <input
              type="text"
              value={searchTerm}
              onChange={e => setSearchTerm(e.target.value)}
              placeholder="搜索渠道名称或类型"
              className="h-9 w-full rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] pl-9 pr-3 text-sm outline-none transition focus:border-[var(--primary)] focus:ring-2 focus:ring-[var(--focus-ring)]"
            />
          </div>
          <Select
            value={typeFilter}
            onChange={setTypeFilter}
            placeholder="全部类型"
            options={[{ label: '全部类型', value: '' }, ...typeOptions]}
          />
          <Select
            value={statusFilter}
            onChange={setStatusFilter}
            options={[
              { label: '全部状态', value: 'all' },
              { label: '仅启用', value: 'enabled' },
              { label: '仅禁用', value: 'disabled' },
            ]}
          />
          <button
            type="button"
            onClick={() => { setSearchTerm(''); setTypeFilter(''); setStatusFilter('all'); }}
            disabled={!filterActive}
            className="h-9 rounded-lg px-3 text-xs font-bold text-[var(--text-secondary)] transition hover:bg-[var(--surface-tint)] hover:text-[var(--primary)] disabled:opacity-40"
          >
            重置
          </button>
        </div>

        <div className="flex items-center justify-between border-b border-[var(--border-soft)] px-4 py-2 text-xs text-[var(--text-secondary)]">
          <span>显示 {filteredChannels.length} / {channels.length} 个渠道</span>
          <span>{canDrag ? '可拖拽调整顺序' : '筛选时暂停排序'}</span>
        </div>

        <div className="overflow-x-auto">
          <DndContext sensors={sensors} collisionDetection={closestCenter} onDragStart={handleDragStart} onDragEnd={handleDragEnd}>
          <table className="channel-data-table w-full min-w-[480px] text-left">
            <thead>
              <tr>
                <th className="px-3 md:px-6 py-3 md:py-4 w-10"></th>
                <th className="px-3 md:px-6">名称 / 类型</th>
                <th className="px-3 md:px-6">状态</th>
                <th className="hidden px-3 text-center sm:table-cell md:px-6">账号数</th>
                <th className="px-3 text-right md:px-6">操作</th>
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
                    <td className="px-6 py-4"><div className="h-4 bg-[var(--primary-lighter)] rounded w-10 ml-auto"></div></td>
                  </tr>
                ))
              ) : filteredChannels.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-6 py-14 text-center text-[var(--text-secondary)]">
                    <Layers3 size={28} className="mx-auto mb-3 text-[var(--text-tertiary)]" />
                    <div className="font-semibold text-[var(--text-primary)]">{channels.length ? '没有匹配的渠道' : '暂无能力渠道'}</div>
                    <div className="mt-1 text-xs">{channels.length ? '调整搜索或筛选条件' : '新建渠道后即可配置账号与能力端点'}</div>
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
                    onDiscoverAccount={acc => setAccountDiscovery({ accountId: acc.id, name: acc.name })}
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
      </section>

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
      <AccountEndpointModelImportModal
        accountId={accountDiscovery.accountId}
        accountName={accountDiscovery.name}
        onClose={() => setAccountDiscovery({ accountId: null })}
        onImported={async () => {
          const current = accountDiscovery.accountId;
          if (current) {
            const channel = channels.find(item => (accounts[item.id] || []).some(account => account.id === current));
            if (channel) await loadChannelDetails(channel.id);
          }
        }}
      />
    </div>
  );
};

export default Channels;
