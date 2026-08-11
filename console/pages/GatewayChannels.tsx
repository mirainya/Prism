import React, { useEffect, useState } from 'react';
import { Plus, RefreshCw, Edit3, Trash2, ChevronDown, ChevronRight, Key, Power, Download, MessageSquare, GripVertical, Server, Search, Check, Activity, Loader2 } from 'lucide-react';
import { DndContext, PointerSensor, closestCenter, useSensor, useSensors, DragEndEvent } from '@dnd-kit/core';
import { SortableContext, arrayMove, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import {
  GwChannel, GwChannelKey, GwAbility, GwCapabilityName, GwTransportName, GW_CAPABILITY_NAMES, GW_TRANSPORTS,
  fetchGwChannels, createGwChannel, updateGwChannel, deleteGwChannel, reorderGwChannels,
  fetchGwKeys, createGwKey, updateGwKey, deleteGwKey,
  fetchGwAbilities, deleteGwAbility, updateGwAbility, fetchGwAbilityTransports, upsertGwAbilityTransport, probeGwAbilityTransport,
} from '../services/gatewayApi';
import { GwChannelModal, GwKeyModal, GwPullModal } from './gateway_channels/GwChannelModals';
import { Modal, useAppDialog } from '../components/ui';

const PROTOCOL_COLORS: Record<string, string> = {
  openai: 'bg-emerald-100 text-emerald-700',
  anthropic: 'bg-orange-100 text-orange-700',
  volcengine: 'bg-blue-100 text-blue-700',
  google: 'bg-red-100 text-red-700',
};

const CAPABILITY_LABELS: Record<GwCapabilityName, string> = {
  stream: '流式输出',
  vision: '图片',
  files: '文件',
  audio: '音频',
  video: '视频',
  tools: '工具调用',
  structured_output: '结构化输出',
  reasoning: '推理',
  background: '后台任务',
  web_search: '联网搜索',
  file_search: '文件搜索',
  code_interpreter: '代码解释器',
  computer_use: '计算机操作',
  image_generation: '图片生成',
};

const TRANSPORT_LABELS: Record<GwTransportName, string> = {
  openai_chat: 'OpenAI Chat',
  openai_responses: 'OpenAI Responses',
  anthropic_messages: 'Anthropic Messages',
  google_generate_content: 'Google GenerateContent',
  volcengine_responses_v3: '火山 Responses v3',
};

const maskApiKey = (apiKey: string) => {
  if (!apiKey) return '';
  if (apiKey.length <= 10) return '********';
  return `${apiKey.slice(0, 5)}...${apiKey.slice(-4)}`;
};

const defaultCapabilities = (protocol?: string): Record<GwCapabilityName, boolean> => {
	const enabled = new Set<GwCapabilityName>(['stream', 'vision', 'files', 'tools']);
  switch (protocol) {
    case 'openai':
    case 'custom':
      enabled.add('structured_output');
      enabled.add('reasoning');
      break;
    case 'anthropic':
      break;
    case 'google':
      enabled.add('structured_output');
      break;
    case 'volcengine':
      enabled.add('audio');
      enabled.add('video');
      enabled.add('structured_output');
      enabled.add('reasoning');
      enabled.add('web_search');
      break;
  }
  return Object.fromEntries(GW_CAPABILITY_NAMES.map(name => [name, enabled.has(name)])) as Record<GwCapabilityName, boolean>;
};

const normalizeCapabilities = (ability: GwAbility): Record<GwCapabilityName, boolean> => {
  if (Array.isArray(ability.capabilities)) {
    const enabled = new Set(ability.capabilities);
    return Object.fromEntries(GW_CAPABILITY_NAMES.map(name => [name, enabled.has(name)])) as Record<GwCapabilityName, boolean>;
  }

  const capabilities = defaultCapabilities(ability.protocol);
  if (ability.capabilities) {
    for (const name of GW_CAPABILITY_NAMES) {
      if (typeof ability.capabilities[name] === 'boolean') {
        capabilities[name] = ability.capabilities[name];
      }
    }
  }
  return capabilities;
};

// 单渠道展开区: 左 keys 列表(点选) + 右该 key 的 abilities
const ChannelDetail: React.FC<{
  channel: GwChannel;
  onAddKey: () => void;
  onEditKey: (k: GwChannelKey) => void;
  onPullKey: (k: GwChannelKey) => void;
  reloadSignal: number;
}> = ({ channel, onAddKey, onEditKey, onPullKey, reloadSignal }) => {
  const { askConfirmation } = useAppDialog();
  const [keys, setKeys] = useState<GwChannelKey[]>([]);
  const [selectedKeyId, setSelectedKeyId] = useState<number | null>(null);
  const [abilities, setAbilities] = useState<GwAbility[]>([]);
  const [abLoading, setAbLoading] = useState(false);
  const [abSearch, setAbSearch] = useState('');
  const [editingAb, setEditingAb] = useState<GwAbility | null>(null);
  const [editingAbOpen, setEditingAbOpen] = useState(false);

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
    const confirmed = await askConfirmation({
      title: '删除 Key？',
      description: '该 Key 的全部路由能力也会一并删除。',
      confirmLabel: '删除 Key',
      tone: 'danger',
    });
    if (!confirmed) return;
    await deleteGwKey(Number(id));
    if (selectedKeyId === Number(id)) { setSelectedKeyId(null); setAbilities([]); }
    await loadKeys();
  };

  const handleDeleteAbility = async (id: number) => {
    const confirmed = await askConfirmation({
      title: '移除模型能力？',
      description: '仅删除当前路由索引，后续仍可重新拉取并导入。',
      confirmLabel: '移除能力',
      tone: 'warning',
    });
    if (!confirmed) return;
    await deleteGwAbility(id);
    if (selectedKeyId) loadAbilities(selectedKeyId);
  };

  const handleSaveAbility = async (id: number, data: Record<string, any>) => {
    await updateGwAbility(id, data);
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
                    <div className="text-xs text-[var(--text-secondary)] font-mono break-all mt-1">{maskApiKey(k.api_key)}</div>
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
                      <button onClick={() => { setEditingAb(ab); setEditingAbOpen(true); }} className="p-1 text-[var(--text-secondary)] hover:text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded" title="编辑"><Edit3 size={12} /></button>
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
        <AbilityEditModal key={editingAb.id} isOpen={editingAbOpen} ability={editingAb} onClose={() => setEditingAbOpen(false)} onSave={handleSaveAbility} />
      )}
    </div>
  );
};

// 能力编辑弹窗: 编辑 vendor_model/priority/status/price/capabilities
const AbilityEditModal: React.FC<{
  isOpen: boolean;
  ability: GwAbility;
  onClose: () => void;
  onSave: (id: number, data: Record<string, any>) => Promise<void>;
}> = ({ isOpen, ability, onClose, onSave }) => {
  const [modelName, setModelName] = useState(ability.model_name || '');
  const [vendorModel, setVendorModel] = useState(ability.vendor_model || '');
  const [priority, setPriority] = useState(String(ability.priority ?? 0));
  const [status, setStatus] = useState(ability.status);
  const [priceMode, setPriceMode] = useState(ability.price_mode || 'token');
  const [inputPrice, setInputPrice] = useState(ability.input_price || '0');
  const [outputPrice, setOutputPrice] = useState(ability.output_price || '0');
  const [capabilities, setCapabilities] = useState(() => normalizeCapabilities(ability));
	const [transports, setTransports] = useState<Record<GwTransportName, boolean>>(
		() => Object.fromEntries(GW_TRANSPORTS.map(name => [name, false])) as Record<GwTransportName, boolean>,
	);
	const [transportLoading, setTransportLoading] = useState(true);
	const [transportChecks, setTransportChecks] = useState<Partial<Record<GwTransportName, { ok: boolean; error?: string }>>>({});
	const [checkingTransport, setCheckingTransport] = useState<GwTransportName | null>(null);
	const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

	useEffect(() => {
		let active = true;
		setTransportLoading(true);
		fetchGwAbilityTransports(ability.id)
			.then(rows => {
				if (!active) return;
				const enabled = new Set(rows.filter(row => row.status === 1).map(row => row.transport));
				setTransports(Object.fromEntries(GW_TRANSPORTS.map(name => [name, enabled.has(name)])) as Record<GwTransportName, boolean>);
				setTransportChecks(Object.fromEntries(rows.filter(row => row.checked_at).map(row => [row.transport, { ok: !row.last_error, error: row.last_error || undefined }])));
			})
			.catch(err => active && setError(err.message || '加载 Transport 失败'))
			.finally(() => active && setTransportLoading(false));
		return () => { active = false; };
	}, [ability.id]);

	const handleProbe = async (name: GwTransportName) => {
		setError('');
		setCheckingTransport(name);
		try {
			await upsertGwAbilityTransport(ability.id, name, transports[name] ? 1 : 0);
			const result = await probeGwAbilityTransport(ability.id, name);
			setTransportChecks(current => ({ ...current, [name]: { ok: result.ok, error: result.error } }));
		} catch (err: any) {
			setError(err.message || '检测失败');
		} finally {
			setCheckingTransport(null);
		}
	};

  const handleSubmit = async () => {
		setError('');
		if (!GW_TRANSPORTS.some(name => transports[name])) {
			setError('至少启用一个上游 Transport');
			return;
		}
		if (!modelName.trim()) {
			setError('对外模型名不能为空');
			return;
		}
    setSaving(true);
    try {
      await onSave(ability.id, {
        model_name: modelName.trim(),
        vendor_model: vendorModel.trim(),
        priority: Number(priority) || 0,
        status,
        price_mode: priceMode,
        input_price: inputPrice,
        output_price: outputPrice,
        capabilities,
      });
			await Promise.all(GW_TRANSPORTS.map(name => upsertGwAbilityTransport(ability.id, name, transports[name] ? 1 : 0)));
			onClose();
		} catch (err: any) {
			setError(err.message || '保存失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={isOpen} onClose={onClose} title="编辑模型能力" width="max-w-md">
        <div className="space-y-4">
          <div>
            <label className="block text-xs font-bold text-[var(--text-secondary)] mb-1">对外模型名(model_name,路由标识)</label>
            <input value={modelName} onChange={e => setModelName(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg text-sm font-mono focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
            <p className="text-[10px] text-[var(--text-secondary)] mt-1">调用方请求时填的模型名。同 key 下想区分同一上游模型(如 super/free)时,改成不同的名并保留 vendor_model 指向上游真名。</p>
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
			<label className="block text-xs font-bold text-[var(--text-secondary)] mb-2">上游 Transport</label>
			{transportLoading ? (
				<div className="text-xs text-[var(--text-secondary)] py-2">加载中...</div>
			) : (
				<div className="space-y-2">
					{GW_TRANSPORTS.map(name => {
						const check = transportChecks[name];
						return (
							<div key={name} className="flex items-center gap-2 rounded-lg border border-[var(--border-soft)] px-3 py-2 text-xs">
								<label className="flex min-w-0 flex-1 items-center gap-2 cursor-pointer">
									<input type="checkbox" checked={transports[name]}
										onChange={event => setTransports(current => ({ ...current, [name]: event.target.checked }))}
										className="h-4 w-4 shrink-0 accent-[var(--primary)]" />
									<span className="font-medium text-[var(--text-primary)] break-words">{TRANSPORT_LABELS[name]}</span>
								</label>
								{check && <span className={`text-[10px] ${check.ok ? 'text-emerald-600' : 'text-red-600'}`} title={check.error}>{check.ok ? '可用' : '失败'}</span>}
								<button type="button" onClick={() => handleProbe(name)} disabled={checkingTransport !== null}
									className="p-1.5 rounded text-[var(--text-secondary)] hover:text-[var(--primary)] hover:bg-[var(--primary-lighter)] disabled:opacity-40"
									title="检测此 Transport">
									{checkingTransport === name ? <Loader2 size={13} className="animate-spin" /> : <Activity size={13} />}
								</button>
							</div>
						);
					})}
				</div>
			)}
		  </div>
		  <div>
			<label className="block text-xs font-bold text-[var(--text-secondary)] mb-2">语义能力</label>
            <div className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3">
              {GW_CAPABILITY_NAMES.map(name => (
                <label key={name} className="flex min-w-0 items-center gap-2 text-xs text-[var(--text-primary)] cursor-pointer">
                  <input
                    type="checkbox"
                    checked={capabilities[name]}
                    onChange={e => setCapabilities(current => ({ ...current, [name]: e.target.checked }))}
                    className="h-4 w-4 shrink-0 accent-[var(--primary)]"
                  />
                  <span className="min-w-0 break-words">{CAPABILITY_LABELS[name]}</span>
                </label>
              ))}
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
		{error && <div className="pt-2 text-xs text-red-600">{error}</div>}
        <div className="flex justify-end gap-2 border-t border-[var(--border-soft)] pt-4">
          <button onClick={onClose} className="px-4 py-2 text-sm font-semibold text-[var(--text-secondary)] hover:text-[var(--text-primary)]">取消</button>
		  <button onClick={handleSubmit} disabled={saving || transportLoading}
            className="flex items-center gap-1.5 px-5 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90 disabled:opacity-50">
            <Check size={16} /> {saving ? '保存中...' : '保存'}
          </button>
        </div>
    </Modal>
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
  const { askConfirmation } = useAppDialog();
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
    const confirmed = await askConfirmation({
      title: '删除网关渠道？',
      description: '该渠道下的全部 Key 与路由能力也会一并删除。',
      confirmLabel: '删除渠道',
      tone: 'danger',
    });
    if (!confirmed) return;
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
