import React, { useEffect, useState } from 'react';
import {
    Key,
    Plus,
    Copy,
    Trash2,
    CheckCircle2,
    AlertCircle,
    Wallet,
    PlusCircle,
    Edit2,
    ChevronUp,
    ChevronDown,
} from 'lucide-react';
import { Modal } from '../components/ui/Modal';
import {
    fetchTokens,
    createToken,
    deleteToken,
    rechargeToken,
    updateToken,
    fetchAllCapabilityChannels
} from '../services/api';
import {ApiToken, ChannelPriorityItem, CapabilityWithChannels} from '../types';
import { ChannelConfigEditor } from './ChannelConfigEditor';
import { STATUS_COLORS, STATUS_LABELS } from '../constants';

const Tokens: React.FC = () => {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [newTokenName, setNewTokenName] = useState('');
    const [newTokenBalance, setNewTokenBalance] = useState<string>('');
  const [newTokenKey, setNewTokenKey] = useState('');
  const [isCreating, setIsCreating] = useState(false);

    // 充值相关状态
    const [showRechargeModal, setShowRechargeModal] = useState(false);
    const [rechargeTokenId, setRechargeTokenId] = useState<string>('');
    const [rechargeTokenName, setRechargeTokenName] = useState<string>('');
    const [rechargeAmount, setRechargeAmount] = useState<string>('');
    const [isRecharging, setIsRecharging] = useState(false);

    // 编辑相关状态
    const [showEditModal, setShowEditModal] = useState(false);
    const [editTokenId, setEditTokenId] = useState<string>('');
    const [editTokenName, setEditTokenName] = useState<string>('');
    const [editChannelPriorities, setEditChannelPriorities] = useState<ChannelPriorityItem[]>([]);
    const [isEditing, setIsEditing] = useState(false);
    const [capabilityChannels, setCapabilityChannels] = useState<CapabilityWithChannels[]>([]);
    const [isLoadingCapabilities, setIsLoadingCapabilities] = useState(false);

    // 创建时的渠道配置
    const [createChannelPriorities, setCreateChannelPriorities] = useState<ChannelPriorityItem[]>([]);
    const [showChannelConfig, setShowChannelConfig] = useState(false);

  const loadTokens = () => {
    setIsLoading(true);
    fetchTokens()
      .then(data => setTokens(data))
      .finally(() => setIsLoading(false));
  };

  useEffect(() => {
    loadTokens();
  }, []);

  const copyText = (text: string) => {
    if (navigator.clipboard?.writeText) {
      return navigator.clipboard.writeText(text);
    }
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    document.body.removeChild(ta);
    return Promise.resolve();
  };

  const handleCopy = (id: string, key: string) => {
    copyText(key);
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 2000);
  };

  const handleCreate = async () => {
    if (!newTokenName.trim()) return;
    setIsCreating(true);
    try {
        const balance = parseFloat(newTokenBalance) || 0;
        const result = await createToken(newTokenName, balance, createChannelPriorities.length > 0 ? createChannelPriorities : undefined);
      setNewTokenKey(result.key);
      loadTokens();
    } catch (err: any) {
      alert(err.message || '创建失败');
    } finally {
      setIsCreating(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`确定要删除令牌 "${name}" 吗? 此操作不可恢复。`)) return;
    try {
      await deleteToken(id);
      loadTokens();
    } catch (err: any) {
      alert(err.message || '删除失败');
    }
  };

    const openRechargeModal = (token: ApiToken) => {
        setRechargeTokenId(token.id);
        setRechargeTokenName(token.name);
        setRechargeAmount('');
        setShowRechargeModal(true);
    };

    const handleRecharge = async () => {
        const amount = parseFloat(rechargeAmount);
        if (!amount || amount <= 0) {
            alert('请输入有效的充值金额');
            return;
        }
        setIsRecharging(true);
        try {
            await rechargeToken(rechargeTokenId, amount);
            loadTokens();
            setShowRechargeModal(false);
        } catch (err: any) {
            alert(err.message || '充值失败');
        } finally {
            setIsRecharging(false);
        }
    };

    // 打开编辑弹窗
    const openEditModal = async (token: ApiToken) => {
        setEditTokenId(token.id);
        setEditTokenName(token.name);
        setEditChannelPriorities(token.channelPriorities || []);
        setShowEditModal(true);

        // 加载能力渠道列表
        setIsLoadingCapabilities(true);
        try {
            const caps = await fetchAllCapabilityChannels();
            setCapabilityChannels(caps);
        } catch (err: any) {
            console.error('加载能力渠道列表失败:', err);
        } finally {
            setIsLoadingCapabilities(false);
        }
    };

    // 保存编辑
    const handleSaveEdit = async () => {
        setIsEditing(true);
        try {
            await updateToken(editTokenId, {
                name: editTokenName,
                channelPriorities: editChannelPriorities,
            });
            loadTokens();
            setShowEditModal(false);
        } catch (err: any) {
            alert(err.message || '保存失败');
        } finally {
            setIsEditing(false);
        }
    };

    const closeModal = () => {
        setShowCreateModal(false);
        setNewTokenName('');
        setNewTokenBalance('');
        setNewTokenKey('');
        setCreateChannelPriorities([]);
        setShowChannelConfig(false);
    };


  return (
    <div className="space-y-4 md:space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">API 令牌管理</h1>
          <p className="text-[var(--text-secondary)] mt-1 text-sm md:text-base">创建和管理用于调用 Prism API 的密钥</p>
        </div>
        <button
          onClick={() => setShowCreateModal(true)}
          className="flex items-center gap-2 px-4 md:px-6 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90 transition-all shadow-sm"
        >
          <Plus size={18} />
          <span className="hidden md:inline">创建新令牌</span>
          <span className="md:hidden">创建</span>
        </button>
      </div>

      <div className="grid grid-cols-1 gap-4">
        {isLoading ? (
          Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="bg-[var(--surface-card)] p-8 rounded-2xl border border-[var(--border-soft)] animate-pulse h-32"></div>
          ))
        ) : tokens.length === 0 ? (
          <div className="bg-[var(--surface-card)] p-12 rounded-2xl border border-[var(--border-soft)] text-center">
            <Key className="mx-auto text-gray-300 mb-4" size={48} />
            <p className="text-[var(--text-secondary)]">暂无令牌，点击上方按钮创建</p>
          </div>
        ) : tokens.map(token => (
          <div key={token.id} className="bg-[var(--surface-card)] p-4 md:p-6 rounded-2xl border border-[var(--border-soft)] shadow-sm flex flex-col md:flex-row items-start md:items-center gap-4 md:gap-6 group">
            <div className="p-3 md:p-4 bg-[var(--surface)] rounded-2xl text-[var(--primary)] hidden md:block">
              <Key size={24} />
            </div>

            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-3 mb-1">
                <h3 className="font-bold text-[var(--text-primary)] truncate">{token.name}</h3>
                <span className={`px-2 py-0.5 rounded-full text-[10px] font-bold uppercase ${STATUS_COLORS[token.status]}`}>
                  {STATUS_LABELS[token.status]}
                </span>
                  {token.channelPriorities && token.channelPriorities.length > 0 && (
                      <span className="px-2 py-0.5 rounded-full text-[10px] font-bold bg-blue-100 text-blue-700">
                          已配置渠道
                      </span>
                  )}
              </div>
              <div className="flex items-center gap-2 text-xs md:text-sm text-[var(--text-secondary)] font-mono overflow-hidden">
                <code className="truncate max-w-[180px] md:max-w-none">{token.key}</code>
                <button
                  onClick={() => handleCopy(token.id, token.key)}
                  className={`p-1 rounded hover:bg-[var(--primary-lighter)] transition-colors ${copiedId === token.id ? 'text-green-500' : 'text-[var(--text-secondary)]'}`}
                >
                  {copiedId === token.id ? <CheckCircle2 size={16} /> : <Copy size={16} />}
                </button>
              </div>
            </div>

              <div className="flex items-center gap-4 md:gap-6">
                  <div className="text-center">
                      <div className="flex items-center gap-1 text-[10px] font-bold text-[var(--text-secondary)] uppercase mb-1">
                          <Wallet size={12}/>
                          <span>余额</span>
                      </div>
                      <p className="text-base md:text-lg font-bold text-green-600">¥{token.balance.toFixed(4)}</p>
              </div>
                  <div className="text-center">
                      <div className="text-[10px] font-bold text-[var(--text-secondary)] uppercase mb-1">已使用</div>
                      <p className="text-base md:text-lg font-bold text-[var(--text-secondary)]">¥{token.totalUsed.toFixed(4)}</p>
              </div>
            </div>

            <div className="flex gap-2 md:opacity-0 md:group-hover:opacity-100 transition-opacity">
                <button
                    onClick={() => openEditModal(token)}
                    className="p-2 text-[var(--text-secondary)] hover:text-indigo-500 hover:bg-[var(--primary-lighter)] rounded-lg"
                    title="编辑"
                >
                    <Edit2 size={18}/>
                </button>
                <button
                  onClick={() => openRechargeModal(token)}
                  className="p-2 text-[var(--text-secondary)] hover:text-green-500 hover:bg-green-50 rounded-lg"
                  title="充值"
              >
                  <PlusCircle size={18}/>
              </button>
                <button
                onClick={() => handleDelete(token.id, token.name)}
                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"
                title="删除"
              >
                <Trash2 size={18} />
              </button>
            </div>
          </div>
        ))}
      </div>

      <div className="bg-amber-50 border border-amber-100 rounded-2xl p-6 flex gap-4 items-start">
        <AlertCircle className="text-amber-500 mt-1 flex-shrink-0" />
        <div>
          <h4 className="font-bold text-amber-900">安全提示</h4>
          <p className="text-sm text-amber-700 mt-1">
            API 令牌是您访问 Prism 服务的唯一凭证。请不要在前端代码中硬编码令牌，也不要在公开场合分享您的密钥。
          </p>
        </div>
      </div>

        {/* 创建令牌弹窗 */}
      {showCreateModal && (
        <Modal open={true} onClose={closeModal} title="创建新令牌">
            {newTokenKey ? (
              <div className="space-y-4">
                <div className="p-4 bg-amber-50 border border-amber-300 rounded-xl">
                    <div className="flex items-center gap-2 mb-2">
                        <AlertCircle size={16} className="text-amber-600" />
                        <p className="text-sm font-bold text-amber-700">请立即复制并妥善保存，关闭后将无法再次查看！</p>
                    </div>
                </div>
                <div className="p-4 bg-green-50 border border-green-200 rounded-xl">
                    <p className="text-sm text-green-700 mb-2 font-medium">令牌创建成功!</p>
                    <textarea
                      readOnly
                      value={newTokenKey}
                      rows={3}
                      onClick={e => (e.target as HTMLTextAreaElement).select()}
                      className="w-full text-sm font-mono bg-[var(--surface-card)] p-3 rounded-lg border border-green-200 resize-none focus:outline-none focus:ring-2 focus:ring-green-400"
                    />
                    <button
                      onClick={() => {
                        copyText(newTokenKey).then(() => {
                          setCopiedId('new');
                          setTimeout(() => setCopiedId(null), 2000);
                        });
                      }}
                      className="mt-2 w-full py-2 flex items-center justify-center gap-2 bg-green-600 text-white rounded-lg hover:bg-green-700 text-sm font-medium"
                    >
                      {copiedId === 'new' ? (
                        <><CheckCircle2 size={16} /> 已复制</>
                      ) : (
                        <><Copy size={16} /> 复制完整 API Key</>
                      )}
                    </button>
                </div>
                <button
                  onClick={closeModal}
                  className="w-full py-3 bg-[var(--primary)] text-white rounded-lg font-bold hover:opacity-90"
                >
                  我已保存，关闭
                </button>
              </div>
            ) : (
              <div className="space-y-4">
                <div>
                  <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">令牌名称</label>
                  <input
                    type="text"
                    value={newTokenName}
                    onChange={e => setNewTokenName(e.target.value)}
                    placeholder="如: 生产环境、测试项目"
                    className="w-full px-4 py-3 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                  />
                </div>
                  <div>
                      <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">初始余额 (元)</label>
                      <input
                          type="number"
                          step="0.01"
                          min="0"
                          value={newTokenBalance}
                          onChange={e => setNewTokenBalance(e.target.value)}
                          placeholder="0.00"
                          className="w-full px-4 py-3 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                  />
                </div>

                  {/* 渠道配置区域 */}
                  <div className="border-t pt-4">
                      <button
                          type="button"
                          onClick={async () => {
                              if (!showChannelConfig && capabilityChannels.length === 0) {
                                  setIsLoadingCapabilities(true);
                                  try {
                                      const caps = await fetchAllCapabilityChannels();
                                      setCapabilityChannels(caps);
                                  } catch (err) {
                                      console.error('加载能力渠道列表失败:', err);
                                  } finally {
                                      setIsLoadingCapabilities(false);
                                  }
                              }
                              setShowChannelConfig(!showChannelConfig);
                          }}
                          className="flex items-center gap-2 text-sm text-[var(--primary)] hover:text-[var(--primary)]"
                      >
                          {showChannelConfig ? <ChevronUp size={16}/> : <ChevronDown size={16}/>}
                          {showChannelConfig ? '收起渠道配置' : '配置渠道优先级 (可选)'}
                      </button>
                      {showChannelConfig && (
                          <div className="mt-3">
                              <p className="text-xs text-[var(--text-secondary)] mb-2">为每个能力配置渠道调用顺序，调用时将按优先级选择可用渠道</p>
                              <ChannelConfigEditor
                                  priorities={createChannelPriorities}
                                  setPriorities={setCreateChannelPriorities}
                                  capabilities={capabilityChannels}
                                  loading={isLoadingCapabilities}
                              />
                          </div>
                      )}
                  </div>

                <button
                  onClick={handleCreate}
                  disabled={isCreating || !newTokenName.trim()}
                  className="w-full py-3 bg-[var(--primary)] text-white rounded-lg font-bold hover:opacity-90 disabled:opacity-50 flex items-center justify-center gap-2"
                >
                  {isCreating ? (
                    <div className="animate-spin rounded-full h-5 w-5 border-b-2 border-white"></div>
                  ) : (
                    <>
                      <Plus size={18} />
                      创建令牌
                    </>
                  )}
                </button>
              </div>
            )}
        </Modal>
      )}

        {/* 充值弹窗 */}
        {showRechargeModal && (
            <Modal open={true} onClose={() => setShowRechargeModal(false)} title="充值余额" width="max-w-md">
                    <div className="space-y-4">
                        <div className="p-4 bg-[var(--surface)] rounded-xl">
                            <p className="text-sm text-[var(--text-secondary)]">为令牌充值</p>
                            <p className="text-lg font-bold text-[var(--text-primary)] mt-1">{rechargeTokenName}</p>
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">充值金额 (元)</label>
                            <input
                                type="number"
                                step="0.01"
                                min="0.01"
                                value={rechargeAmount}
                                onChange={e => setRechargeAmount(e.target.value)}
                                placeholder="请输入充值金额"
                                className="w-full px-4 py-3 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-green-500"
                                autoFocus
                            />
                        </div>
                        <button
                            onClick={handleRecharge}
                            disabled={isRecharging || !rechargeAmount || parseFloat(rechargeAmount) <= 0}
                            className="w-full py-3 bg-green-600 text-white rounded-lg font-bold hover:bg-green-700 disabled:opacity-50 flex items-center justify-center gap-2"
                        >
                            {isRecharging ? (
                                <div className="animate-spin rounded-full h-5 w-5 border-b-2 border-white"></div>
                            ) : (
                                <>
                                    <PlusCircle size={18}/>
                                    确认充值
                                </>
                            )}
                        </button>
                    </div>
            </Modal>
      )}

        {/* 编辑令牌弹窗 */}
        {showEditModal && (
            <Modal open={true} onClose={() => setShowEditModal(false)} title="编辑令牌">
                    <div className="space-y-4">
                        <div>
                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">令牌名称</label>
                            <input
                                type="text"
                                value={editTokenName}
                                onChange={e => setEditTokenName(e.target.value)}
                                placeholder="令牌名称"
                                className="w-full px-4 py-3 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                            />
                        </div>

                        <div className="border-t pt-4">
                            <h4 className="font-medium text-[var(--text-primary)] mb-2">渠道优先级配置</h4>
                            <p className="text-xs text-[var(--text-secondary)] mb-3">为每个能力配置渠道调用顺序，调用时将按优先级选择可用渠道</p>
                            <ChannelConfigEditor
                                priorities={editChannelPriorities}
                                setPriorities={setEditChannelPriorities}
                                capabilities={capabilityChannels}
                                loading={isLoadingCapabilities}
                            />
                        </div>

                        <button
                            onClick={handleSaveEdit}
                            disabled={isEditing || !editTokenName.trim()}
                            className="w-full py-3 bg-[var(--primary)] text-white rounded-lg font-bold hover:opacity-90 disabled:opacity-50 flex items-center justify-center gap-2"
                        >
                            {isEditing ? (
                                <div className="animate-spin rounded-full h-5 w-5 border-b-2 border-white"></div>
                            ) : (
                                <>
                                    <CheckCircle2 size={18}/>
                                    保存
                                </>
                            )}
                        </button>
                    </div>
            </Modal>
        )}
    </div>
  );
};

export default Tokens;
