import React, { useEffect, useState } from 'react';
import {
    Key,
    Plus,
    Copy,
    Trash2,
    CheckCircle2,
    AlertCircle,
    PlusCircle,
    Edit2,
    HardDriveUpload,
    Unlink,
    LoaderCircle,
    ShieldCheck,
} from 'lucide-react';
import { Badge, Button, Modal, Table, type TableColumn, useAppDialog } from '../components/ui';
import { PageHeader } from '../components/shell';
import {
    fetchTokens,
    createToken,
    deleteToken,
    rechargeToken,
    updateToken,
    bindTokenFileStorage,
    unbindTokenFileStorage,
} from '../services/api';
import { ApiToken } from '../types';

const formatAmount = (value: number) => value.toLocaleString('zh-CN', {
  minimumFractionDigits: 2,
  maximumFractionDigits: 4,
});

const Tokens: React.FC = () => {
  const { askConfirmation, showAlert } = useAppDialog();
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [newTokenName, setNewTokenName] = useState('');
    const [newTokenBalance, setNewTokenBalance] = useState<string>('');
  const [newTokenKey, setNewTokenKey] = useState('');
  const [isCreating, setIsCreating] = useState(false);
  const [deletingTokenId, setDeletingTokenId] = useState<string | null>(null);

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
    const [isEditing, setIsEditing] = useState(false);

    // 每个 Prism API Key 独立选择结果转存账号。
    const [storageToken, setStorageToken] = useState<ApiToken | null>(null);
    const [storageAPIKey, setStorageAPIKey] = useState('');
    const [isSavingStorage, setIsSavingStorage] = useState(false);

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

  const handleCreate = async () => {
    if (!newTokenName.trim()) return;
    setIsCreating(true);
    try {
        const balance = parseFloat(newTokenBalance) || 0;
        const result = await createToken(newTokenName, balance);
      setNewTokenKey(result.key);
      loadTokens();
    } catch (err: any) {
      await showAlert({ title: '创建失败', description: err.message || '令牌创建失败，请稍后重试。', tone: 'danger' });
    } finally {
      setIsCreating(false);
    }
  };

  const handleDelete = async (id: string, name: string) => {
    const confirmed = await askConfirmation({
      title: '删除 API 令牌？',
      description: `删除“${name}”后，使用该令牌的请求将立即失效。`,
      confirmLabel: '删除令牌',
      tone: 'danger',
    });
    if (!confirmed) return;
    setDeletingTokenId(id);
    try {
      await deleteToken(id);
      setTokens(current => current.filter(token => token.id !== id));
    } catch (err: any) {
      await showAlert({ title: '删除失败', description: err.message || '令牌删除失败，请稍后重试。', tone: 'danger' });
    } finally {
      setDeletingTokenId(null);
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
            await showAlert({ title: '金额无效', description: '请输入大于 0 的充值金额。', tone: 'warning' });
            return;
        }
        setIsRecharging(true);
        try {
            await rechargeToken(rechargeTokenId, amount);
            loadTokens();
            setShowRechargeModal(false);
        } catch (err: any) {
            await showAlert({ title: '充值失败', description: err.message || '充值失败，请稍后重试。', tone: 'danger' });
        } finally {
            setIsRecharging(false);
        }
    };

    // 打开编辑弹窗
    const openEditModal = (token: ApiToken) => {
        setEditTokenId(token.id);
        setEditTokenName(token.name);
        setShowEditModal(true);
    };

    // 保存编辑
    const handleSaveEdit = async () => {
        setIsEditing(true);
        try {
            await updateToken(editTokenId, {
                name: editTokenName,
            });
            loadTokens();
            setShowEditModal(false);
        } catch (err: any) {
            await showAlert({ title: '保存失败', description: err.message || '令牌保存失败，请稍后重试。', tone: 'danger' });
        } finally {
            setIsEditing(false);
        }
    };

    const openStorageModal = (token: ApiToken) => {
        setStorageToken(token);
        setStorageAPIKey('');
    };

    const closeStorageModal = () => {
        if (isSavingStorage) return;
        setStorageToken(null);
        setStorageAPIKey('');
    };

    const handleBindStorage = async () => {
        if (!storageToken || !storageAPIKey.trim()) return;
        setIsSavingStorage(true);
        try {
            await bindTokenFileStorage(storageToken.id, storageAPIKey.trim());
            loadTokens();
            setStorageToken(null);
            setStorageAPIKey('');
        } catch (err: any) {
            await showAlert({
                title: '绑定失败',
                description: err.message || 'XFileStorage Key 验证失败，请检查 Key 与服务状态。',
                tone: 'danger',
            });
        } finally {
            setIsSavingStorage(false);
        }
    };

    const handleUnbindStorage = async () => {
        if (!storageToken) return;
        const confirmed = await askConfirmation({
            title: '停止新任务转存？',
            description: `解绑后，“${storageToken.name}”的图片和视频结果将直接使用上游地址。`,
            confirmLabel: '确认解绑',
            tone: 'danger',
        });
        if (!confirmed) return;
        setIsSavingStorage(true);
        try {
            await unbindTokenFileStorage(storageToken.id);
            loadTokens();
            setStorageToken(null);
            setStorageAPIKey('');
        } catch (err: any) {
            await showAlert({ title: '解绑失败', description: err.message || '暂时无法解绑，请稍后重试。', tone: 'danger' });
        } finally {
            setIsSavingStorage(false);
        }
    };

    const closeModal = () => {
        setShowCreateModal(false);
        setNewTokenName('');
        setNewTokenBalance('');
        setNewTokenKey('');
    };

  const storageCount = tokens.filter(token => token.xfsStorage.configured).length;
  const columns: TableColumn<ApiToken>[] = [
    {
      header: '令牌',
      wrap: true,
      render: token => (
        <div className="min-w-[190px]">
          <div className="font-semibold text-[var(--text-primary)]">{token.name}</div>
          <code className="mt-1 block text-xs text-[var(--text-secondary)]">{token.key}</code>
        </div>
      ),
    },
    {
      header: '结果转存',
      render: token => (
        <div className="flex items-center gap-2">
          <Badge variant={token.xfsStorage.configured ? 'success' : 'default'}>
            {token.xfsStorage.configured ? token.xfsStorage.keyHint : '未绑定'}
          </Badge>
          <button
            type="button"
            onClick={() => openStorageModal(token)}
            className="inline-flex items-center gap-1 rounded-lg px-2 py-1 text-xs font-semibold text-[var(--primary)] transition hover:bg-[var(--primary-lighter)]"
          >
            <HardDriveUpload size={14} />
            {token.xfsStorage.configured ? '管理' : '绑定'}
          </button>
        </div>
      ),
    },
    {
      header: '可用余额',
      className: 'tabular-nums',
      render: token => <span className="font-semibold text-emerald-600">¥{formatAmount(token.balance)}</span>,
    },
    {
      header: '已使用',
      className: 'tabular-nums',
      render: token => <span className="text-[var(--text-secondary)]">¥{formatAmount(token.totalUsed)}</span>,
    },
    {
      header: <span className="sr-only">操作</span>,
      className: 'w-px',
      render: token => (
        <div className="flex items-center justify-end gap-1">
          <button
            type="button"
            onClick={() => openRechargeModal(token)}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--text-secondary)] transition hover:bg-emerald-50 hover:text-emerald-600"
            title="充值"
            aria-label={`为 ${token.name} 充值`}
          >
            <PlusCircle size={16} />
          </button>
          <button
            type="button"
            onClick={() => openEditModal(token)}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--text-secondary)] transition hover:bg-[var(--primary-lighter)] hover:text-[var(--primary)]"
            title="编辑"
            aria-label={`编辑 ${token.name}`}
          >
            <Edit2 size={16} />
          </button>
          <button
            type="button"
            onClick={() => void handleDelete(token.id, token.name)}
            disabled={deletingTokenId !== null}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--text-secondary)] transition hover:bg-red-50 hover:text-red-600 disabled:opacity-40"
            title="删除"
            aria-label={`删除 ${token.name}`}
          >
            {deletingTokenId === token.id ? <LoaderCircle size={16} className="animate-spin" /> : <Trash2 size={16} />}
          </button>
        </div>
      ),
    },
  ];


  return (
    <div className="space-y-4">
      <PageHeader
        icon={Key}
        title="API 令牌"
        meta={isLoading ? '正在读取令牌' : `共 ${tokens.length} 个令牌 · ${storageCount} 个已启用结果转存`}
        actions={(
          <Button onClick={() => setShowCreateModal(true)}>
            <Plus size={16} />
            创建令牌
          </Button>
        )}
      />

      <section className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)]">
        <Table
          aria-label="API 令牌列表"
          columns={columns}
          rows={isLoading ? [] : tokens}
          rowKey={token => token.id}
          minWidth="760px"
          busy={isLoading}
          empty={isLoading ? '正在读取令牌...' : '暂无令牌'}
        />
        <div className="flex items-start gap-2 border-t border-[var(--border-soft)] bg-[var(--surface-muted)]/40 px-4 py-3 text-xs text-[var(--text-secondary)]">
          <ShieldCheck size={15} className="mt-0.5 shrink-0 text-[var(--primary)]" />
          完整密钥仅在创建时显示。删除令牌后，该令牌会立即停止授权。
        </div>
      </section>

        {/* 创建令牌弹窗 */}
        <Modal open={showCreateModal} onClose={closeModal} title="创建新令牌" width="max-w-xl">
            {newTokenKey ? (
              <div className="modal-form">
                <div className="modal-scroll-body space-y-4">
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
                      className="modal-button modal-button-primary mt-2 w-full"
                    >
                      {copiedId === 'new' ? (
                        <><CheckCircle2 size={16} /> 已复制</>
                      ) : (
                        <><Copy size={16} /> 复制完整 API Key</>
                      )}
                    </button>
                </div>
                </div>
                <div className="modal-footer">
                  <button onClick={closeModal} className="modal-button modal-button-secondary">我已保存，关闭</button>
                </div>
              </div>
            ) : (
              <div className="modal-form">
               <div className="modal-scroll-body space-y-4">
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
                </div>
                <div className="modal-footer">
                  <button type="button" onClick={closeModal} className="modal-button modal-button-secondary">取消</button>
                  <button onClick={handleCreate} disabled={isCreating || !newTokenName.trim()} className="modal-button modal-button-primary">
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
              </div>
            )}
        </Modal>

        {/* 充值弹窗 */}
            <Modal open={showRechargeModal} onClose={() => setShowRechargeModal(false)} title="充值余额" width="max-w-md">
                    <div className="modal-form">
                      <div className="modal-scroll-body space-y-4">
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
                      </div>
                      <div className="modal-footer">
                        <button type="button" onClick={() => setShowRechargeModal(false)} className="modal-button modal-button-secondary">取消</button>
                        <button onClick={handleRecharge} disabled={isRecharging || !rechargeAmount || parseFloat(rechargeAmount) <= 0} className="modal-button modal-button-primary">
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
                    </div>
            </Modal>

        <Modal open={storageToken !== null} onClose={closeStorageModal} title="结果转存设置" width="max-w-lg">
          <div className="modal-form">
            <div className="modal-scroll-body space-y-4">
              <div className="rounded-xl bg-[var(--surface)] p-4">
                <p className="text-xs text-[var(--text-secondary)]">API 令牌</p>
                <p className="mt-1 font-bold text-[var(--text-primary)]">{storageToken?.name}</p>
                <p className="mt-2 text-sm text-[var(--text-secondary)]">
                  绑定后，此令牌的图片和视频结果会转存到当前 XFileStorage。
                </p>
              </div>
              {storageToken?.xfsStorage.configured && (
                <div className="flex items-center justify-between gap-3 rounded-xl border border-green-200 bg-green-50 p-3 text-sm">
                  <span className="font-semibold text-green-700">当前已绑定 {storageToken.xfsStorage.keyHint}</span>
                  <button
                    type="button"
                    onClick={() => void handleUnbindStorage()}
                    disabled={isSavingStorage}
                    className="inline-flex items-center gap-1 font-semibold text-red-600 disabled:opacity-50"
                  >
                    <Unlink size={14} />
                    解绑
                  </button>
                </div>
              )}
              <div>
                <label className="mb-1 block text-sm font-medium text-[var(--text-primary)]">
                  {storageToken?.xfsStorage.configured ? '新的 XFileStorage Key' : 'XFileStorage Key'}
                </label>
                <input
                  type="text"
                  value={storageAPIKey}
                  onChange={event => setStorageAPIKey(event.target.value)}
                  placeholder="xfs_..."
                  autoComplete="off"
                  spellCheck={false}
                  className="w-full rounded-lg border border-[var(--border-soft)] px-4 py-3 font-mono text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                />
                <p className="mt-2 text-xs text-[var(--text-secondary)]">保存时会验证上传、读取、签名和删除权限。</p>
              </div>
            </div>
            <div className="modal-footer">
              <button type="button" onClick={closeStorageModal} disabled={isSavingStorage} className="modal-button modal-button-secondary">取消</button>
              <button
                type="button"
                onClick={() => void handleBindStorage()}
                disabled={isSavingStorage || !storageAPIKey.trim()}
                className="modal-button modal-button-primary"
              >
                <HardDriveUpload size={17} />
                {isSavingStorage ? '正在验证' : storageToken?.xfsStorage.configured ? '验证并更换' : '验证并绑定'}
              </button>
            </div>
          </div>
        </Modal>

        {/* 编辑令牌弹窗 */}
            <Modal open={showEditModal} onClose={() => setShowEditModal(false)} title="编辑令牌" width="max-w-xl">
                    <div className="modal-form">
                      <div className="modal-scroll-body space-y-4">
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
                      </div>
                      <div className="modal-footer">
                        <button type="button" onClick={() => setShowEditModal(false)} className="modal-button modal-button-secondary">取消</button>
                        <button onClick={handleSaveEdit} disabled={isEditing || !editTokenName.trim()} className="modal-button modal-button-primary">
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
                    </div>
            </Modal>
    </div>
  );
};

export default Tokens;
