import React, { useEffect, useState } from 'react';
import { Plus, Search, RefreshCw, Edit3, Trash2, Shield, ChevronDown, ChevronRight, Key, Cpu, X, Power, Copy } from 'lucide-react';
import {
    fetchChannels,
    createChannel,
    updateChannel,
    deleteChannel,
    fetchChannelAccounts,
    createChannelAccount,
    updateChannelAccount,
    deleteChannelAccount,
    fetchChannelCapabilities,
    createChannelCapability,
    updateChannelCapability,
    deleteChannelCapability
} from '../services/api';
import {Channel, ChannelAccount, ChannelCapability} from '../types';
import { ChannelModal, AccountModal } from './ChannelModals';

const STATUS_MAP: Record<number, { label: string; color: string }> = {
  1: { label: '已启用', color: 'bg-green-100 text-green-700' },
  0: { label: '已禁用', color: 'bg-[var(--primary-lighter)] text-[var(--text-primary)]' },
};

const RESULT_MODES = [
  { value: 'sync', label: '同步' },
  { value: 'poll', label: '轮询' },
  { value: 'callback', label: '回调' },
];

// 渠道行组件
const ChannelRow: React.FC<{
  channel: Channel;
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
    onAddCapability: () => void;
    onEditCapability: (c: ChannelCapability) => void;
    onDeleteCapability: (id: string) => void;
    onToggleCapabilityStatus: (c: ChannelCapability) => void;
}> = ({
  channel, expanded, onToggle, onEdit, onDelete, onToggleStatus,
          accounts, capabilities, onAddAccount, onEditAccount, onDeleteAccount, onToggleAccountStatus,
          onAddCapability, onEditCapability, onDeleteCapability, onToggleCapabilityStatus
}) => {
  const status = STATUS_MAP[channel.status] || STATUS_MAP[0];
  const [copiedAccountId, setCopiedAccountId] = useState<string | null>(null);

  const getResultModeLabel = (mode: string) => {
    return RESULT_MODES.find(m => m.value === mode)?.label || mode;
  };

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
      <tr className="hover:bg-[var(--surface)] transition-colors group border-b border-[var(--border-soft)]">
        <td className="px-6 py-4">
          <button onClick={onToggle} className="p-1 hover:bg-[var(--primary-lighter)] rounded">
            {expanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
          </button>
        </td>
        <td className="px-6 py-4">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-xl bg-indigo-100 flex items-center justify-center text-[var(--primary)] font-bold uppercase text-xs">
              {channel.type.substring(0, 2)}
            </div>
            <div>
              <div className="text-sm font-bold text-[var(--text-primary)]">{channel.name}</div>
              <div className="text-xs text-[var(--text-secondary)] flex items-center gap-1">
                <Shield size={10} />
                {channel.type}
              </div>
            </div>
          </div>
        </td>
        <td className="px-6 py-4">
          <span className={`px-2 py-1 rounded-full text-[10px] font-bold uppercase tracking-wider ${status.color}`}>
            {status.label}
          </span>
        </td>
        <td className="px-6 py-4 text-center">
          <span className="text-sm font-semibold text-[var(--text-primary)]">{channel.accountsCount}</span>
        </td>
        <td className="px-6 py-4 text-right">
          <div className="flex items-center justify-end gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
            <button onClick={onToggleStatus} className={`p-2 rounded-lg ${channel.status === 1 ? 'text-yellow-600 hover:bg-yellow-50' : 'text-green-600 hover:bg-green-50'}`} title={channel.status === 1 ? '禁用' : '启用'}>
              <Power size={16} />
            </button>
            <button onClick={onEdit} className="p-2 text-[var(--text-secondary)] hover:text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg" title="编辑">
              <Edit3 size={16} />
            </button>
            <button onClick={onDelete} className="p-2 text-[var(--text-secondary)] hover:text-red-600 hover:bg-red-50 rounded-lg" title="删除">
              <Trash2 size={16} />
            </button>
          </div>
        </td>
      </tr>
      {expanded && (
        <tr>
          <td colSpan={5} className="bg-[var(--surface)]/50 px-6 py-4">
            <div className="grid grid-cols-2 gap-6">
              {/* 账号列表 */}
              <div className="bg-[var(--surface-card)] rounded-xl p-4 border border-[var(--border-soft)]">
                <div className="flex items-center justify-between mb-3">
                  <h4 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
                    <Key size={14} /> 账号列表
                  </h4>
                  <button onClick={onAddAccount} className="text-xs text-[var(--primary)] hover:text-[var(--primary)] flex items-center gap-1">
                    <Plus size={14} /> 添加
                  </button>
                </div>
                {accounts.length === 0 ? (
                  <p className="text-xs text-[var(--text-secondary)] text-center py-4">暂无账号</p>
                ) : (
                  <div className="space-y-2">
                    {accounts.map(acc => (
                      <div key={acc.id} className="flex items-center justify-between p-2 bg-[var(--surface)] rounded-lg group/acc gap-3">
                        <div className="min-w-0 flex-1">
                          <div className="text-sm font-medium text-[var(--text-primary)]">{acc.name}</div>
                          <div className="text-xs text-[var(--text-secondary)] font-mono break-all mt-1">{acc.apiKey || acc.maskedKey || '-'}</div>
                          <div className={`text-xs mt-1 ${acc.maxTasks > 0 && acc.currentTasks >= acc.maxTasks ? 'text-red-500 font-bold' : 'text-[var(--text-secondary)]'}`}>权重: {acc.weight} | 并发: {acc.currentTasks}/{acc.maxTasks || '∞'}</div>
                        </div>
                        <div className="flex items-center gap-1 opacity-0 group-hover/acc:opacity-100 shrink-0">
                          <span className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${acc.status === 1 ? 'bg-green-100 text-green-700' : 'bg-[var(--primary-lighter)] text-[var(--text-secondary)]'}`}>
                            {acc.status === 1 ? '启用' : '禁用'}
                          </span>
                          <button onClick={() => handleCopyApiKey(acc.id, acc.apiKey || acc.maskedKey || '')} className="p-1 hover:bg-gray-200 rounded" title="复制 API Key"><Copy size={12} /></button>
                          {copiedAccountId === acc.id && <span className="text-[10px] font-medium text-green-600 px-1">已复制</span>}
                          <button onClick={() => onToggleAccountStatus(acc)} className="p-1 hover:bg-gray-200 rounded"><Power size={12} /></button>
                          <button onClick={() => onEditAccount(acc)} className="p-1 hover:bg-gray-200 rounded"><Edit3 size={12} /></button>
                          <button onClick={() => onDeleteAccount(acc.id)} className="p-1 hover:bg-red-100 text-red-500 rounded"><Trash2 size={12} /></button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>

                {/* 端点列表 */}
              <div className="bg-[var(--surface-card)] rounded-xl p-4 border border-[var(--border-soft)]">
                <div className="flex items-center justify-between mb-3">
                  <h4 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
                      <Cpu size={14}/> 端点列表
                  </h4>
                    <button onClick={onAddCapability}
                            className="text-xs text-[var(--primary)] hover:text-[var(--primary)] flex items-center gap-1">
                    <Plus size={14} /> 添加
                  </button>
                </div>
                  {capabilities.length === 0 ? (
                      <p className="text-xs text-[var(--text-secondary)] text-center py-4">暂无端点配置</p>
                ) : (
                  <div className="space-y-2">
                      {capabilities.map(c => (
                          <div key={c.id}
                               className="flex items-center justify-between p-2 bg-[var(--surface)] rounded-lg group/cap">
                        <div>
                            <div className="flex items-center gap-2">
                                <span className="text-sm font-medium text-[var(--text-primary)]">{c.name || c.model || c.capabilityCode}</span>
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
                              <div className="flex items-center gap-1 opacity-0 group-hover/cap:opacity-100">
                          <span
                              className={`px-1.5 py-0.5 rounded text-[10px] font-bold ${c.status === 1 ? 'bg-green-100 text-green-700' : 'bg-[var(--primary-lighter)] text-[var(--text-secondary)]'}`}>
                            {c.status === 1 ? '启用' : '禁用'}
                          </span>
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

  // Modal states
  const [channelModal, setChannelModal] = useState<{ open: boolean; channel: Channel | null }>({ open: false, channel: null });
  const [accountModal, setAccountModal] = useState<{ open: boolean; channelId: string; account: ChannelAccount | null }>({ open: false, channelId: '', account: null });

  const loadData = async () => {
    setIsLoading(true);
    try {
      const channelsData = await fetchChannels();
      setChannels(channelsData);
    } finally {
      setIsLoading(false);
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
    await loadData();
  };

  const handleDeleteChannel = async (id: string) => {
    if (!confirm('确定删除此渠道？')) return;
    await deleteChannel(id);
    await loadData();
  };

  const handleToggleChannelStatus = async (channel: Channel) => {
    await updateChannel(channel.id, { status: channel.status === 1 ? 0 : 1 });
    await loadData();
  };

  const handleSaveAccount = async (data: any) => {
    const channelId = accountModal.channelId;
    if (accountModal.account) {
      await updateChannelAccount(accountModal.account.id, data);
    } else {
      await createChannelAccount(data);
    }
    await loadChannelDetails(channelId);
    await loadData();
  };

  const handleDeleteAccount = async (channelId: string, accountId: string) => {
    if (!confirm('确定删除此账号？')) return;
    await deleteChannelAccount(accountId);
    await loadChannelDetails(channelId);
    await loadData();
  };

  const handleToggleAccountStatus = async (channelId: string, account: ChannelAccount) => {
    await updateChannelAccount(account.id, { status: account.status === 1 ? 0 : 1 });
    await loadChannelDetails(channelId);
  };

    const handleDeleteCapability = async (channelId: string, capabilityId: string) => {
        if (!confirm('确定删除此能力配置？')) return;
        await deleteChannelCapability(capabilityId);
    await loadChannelDetails(channelId);
    await loadData();
  };

    const handleToggleCapabilityStatus = async (channelId: string, capability: ChannelCapability) => {
        await updateChannelCapability(capability.id, {status: capability.status === 1 ? 0 : 1});
    await loadChannelDetails(channelId);
  };

  const filteredChannels = channels.filter(ch =>
    ch.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
    ch.type.toLowerCase().includes(searchTerm.toLowerCase())
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-[var(--text-primary)]">渠道管理</h1>
          <p className="text-[var(--text-secondary)] mt-1">配置上游服务商、账号池及模型映射</p>
        </div>
        <button
          onClick={() => setChannelModal({ open: true, channel: null })}
          className="flex items-center gap-2 px-6 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90 transition-all shadow-sm"
        >
          <Plus size={18} />
          新建渠道
        </button>
      </div>

      <div className="bg-[var(--surface-card)] rounded-2xl shadow-sm border border-[var(--border-soft)] overflow-hidden">
        <div className="p-4 border-b border-[var(--border-soft)] flex items-center gap-4 bg-[var(--surface)]/50">
          <div className="relative flex-1 max-w-md">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" size={18} />
            <input
              type="text"
              value={searchTerm}
              onChange={e => setSearchTerm(e.target.value)}
              placeholder="搜索名称或类型..."
              className="w-full pl-10 pr-4 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)] focus:border-transparent"
            />
          </div>
          <div className="flex-1"></div>
          <button
            onClick={loadData}
            className="flex items-center gap-2 px-3 py-1.5 text-xs font-semibold text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors"
          >
            <RefreshCw size={14} className={isLoading ? 'animate-spin' : ''} />
            刷新
          </button>
        </div>

        <div className="overflow-x-auto">
          <table className="w-full text-left">
            <thead>
              <tr className="border-b border-[var(--border-soft)]">
                <th className="px-6 py-4 w-12"></th>
                <th className="px-6 py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">名称 / 类型</th>
                <th className="px-6 py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider">状态</th>
                <th className="px-6 py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-center">账号数</th>
                <th className="px-6 py-4 text-xs font-bold text-[var(--text-secondary)] uppercase tracking-wider text-right">操作</th>
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
                filteredChannels.map(channel => (
                  <ChannelRow
                    key={channel.id}
                    channel={channel}
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
                    onAddCapability={() => window.location.hash = '#/capabilities'}
                    onEditCapability={() => window.location.hash = '#/capabilities'}
                    onDeleteCapability={id => handleDeleteCapability(channel.id, id)}
                    onToggleCapabilityStatus={c => handleToggleCapabilityStatus(channel.id, c)}
                  />
                ))
              )}
            </tbody>
          </table>
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
        onClose={() => setAccountModal({ open: false, channelId: '', account: null })}
        onSave={handleSaveAccount}
      />
    </div>
  );
};

export default Channels;
