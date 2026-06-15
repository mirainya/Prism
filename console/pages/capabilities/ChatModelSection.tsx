import React, { useEffect, useMemo, useState } from 'react';
import { Plus, Edit2, Trash2, ChevronDown, ChevronRight, Power, RefreshCw, Search, Settings, MessageSquare, GripVertical } from 'lucide-react';
import { DndContext, DragOverlay, PointerSensor, closestCenter, useSensor, useSensors, DragStartEvent, DragEndEvent } from '@dnd-kit/core';
import { SortableContext, arrayMove, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { Modal } from '../../components/ui/Modal';
import {
    fetchChatModels, createChatModel, updateChatModel, deleteChatModel, reorderChatModels,
    fetchChatModelChannels, deleteChatModelChannel, updateChatModelChannel,
} from '../../services/api';
import { ChatModel, ChatModelChannel, Channel } from '../../types';
import ChatModelChannelModal from './ChatModelChannelModal';

const SortableModelRow: React.FC<{ id: string; disabled: boolean; children: React.ReactNode }> = ({ id, disabled, children }) => {
    const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id, disabled });
    const style = { transform: CSS.Transform.toString(transform), transition, opacity: isDragging ? 0.4 : 1 };
    return (
        <div ref={setNodeRef} style={style}
            className="relative rounded-2xl border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-sm overflow-hidden">
            {!disabled && (
                <span {...attributes} {...listeners}
                    className="absolute left-1 top-1/2 -translate-y-1/2 z-10 p-1 text-[var(--text-secondary)] hover:text-[var(--primary)] cursor-grab active:cursor-grabbing touch-none"
                    title="拖拽排序">
                    <GripVertical size={16} />
                </span>
            )}
            <div className={disabled ? '' : 'pl-5'}>{children}</div>
        </div>
    );
};

const ChatModelSection: React.FC<{ channels: Channel[] }> = ({ channels }) => {
    const [models, setModels] = useState<ChatModel[]>([]);
    const [modelChannels, setModelChannels] = useState<ChatModelChannel[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [expandedModel, setExpandedModel] = useState<string | null>(null);
    const [searchTerm, setSearchTerm] = useState('');

    // Model form
    const [modelModal, setModelModal] = useState<{ open: boolean; model: ChatModel | null }>({ open: false, model: null });
    const [modelForm, setModelForm] = useState({ code: '', name: '', provider: '', description: '', features: [] as string[], max_tokens: 0 });
    const [modelFormLoading, setModelFormLoading] = useState(false);

    // Channel modal
    const [mcModal, setMcModal] = useState<{ open: boolean; modelCode: string; mc: ChatModelChannel | null }>({ open: false, modelCode: '', mc: null });

    const channelNameMap = useMemo(() => {
        const map = new Map<string, string>();
        channels.forEach(ch => map.set(ch.id, ch.name));
        return map;
    }, [channels]);

    const modelChannelsByCode = useMemo(() => {
        const map = new Map<string, ChatModelChannel[]>();
        modelChannels.forEach(mc => {
            const list = map.get(mc.modelCode) || [];
            list.push(mc);
            map.set(mc.modelCode, list);
        });
        return map;
    }, [modelChannels]);

    const loadData = async () => {
        setIsLoading(true);
        try {
            const [ms, mcs] = await Promise.all([fetchChatModels(), fetchChatModelChannels()]);
            setModels(ms);
            setModelChannels(mcs);
        } finally {
            setIsLoading(false);
        }
    };

    useEffect(() => { loadData(); }, []);

    const [activeId, setActiveId] = useState<string | null>(null);
    const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));
    const canDrag = !searchTerm.trim();

    const handleDragEnd = async (e: DragEndEvent) => {
        setActiveId(null);
        const { active, over } = e;
        if (!over || active.id === over.id) return;
        const oldIndex = models.findIndex(m => m.code === active.id);
        const newIndex = models.findIndex(m => m.code === over.id);
        if (oldIndex < 0 || newIndex < 0) return;
        const reordered = arrayMove(models, oldIndex, newIndex);
        setModels(reordered); // 乐观更新
        try {
            await reorderChatModels(reordered.map(m => m.code));
        } catch {
            loadData(); // 失败回滚
        }
    };

    const filteredModels = useMemo(() => {
        const kw = searchTerm.trim().toLowerCase();
        if (!kw) return models;
        return models.filter(m =>
            m.code.toLowerCase().includes(kw) || m.name.toLowerCase().includes(kw) || m.provider.toLowerCase().includes(kw)
        );
    }, [models, searchTerm]);

    const handleDeleteModel = async (code: string) => {
        if (!confirm('确定删除该模型? 相关的渠道映射也会被删除。')) return;
        await deleteChatModel(code);
        loadData();
    };

    const handleToggleModelStatus = async (m: ChatModel) => {
        await updateChatModel(m.code, { status: m.status === 1 ? 0 : 1 });
        loadData();
    };

    const handleDeleteMc = async (id: number) => {
        if (!confirm('确定删除该渠道映射?')) return;
        await deleteChatModelChannel(id);
        loadData();
    };

    const handleToggleMcStatus = async (mc: ChatModelChannel) => {
        await updateChatModelChannel(mc.id, { status: mc.status === 1 ? 0 : 1 });
        loadData();
    };

    const handleSaveModel = async (e: React.FormEvent) => {
        e.preventDefault();
        setModelFormLoading(true);
        try {
            if (modelModal.model) {
                await updateChatModel(modelModal.model.code, { name: modelForm.name, provider: modelForm.provider, description: modelForm.description, features: modelForm.features, max_tokens: modelForm.max_tokens });
            } else {
                await createChatModel({ ...modelForm, max_tokens: modelForm.max_tokens });
            }
            setModelModal({ open: false, model: null });
            loadData();
        } finally {
            setModelFormLoading(false);
        }
    };

    const openModelModal = (model: ChatModel | null) => {
        if (model) {
            setModelForm({ code: model.code, name: model.name, provider: model.provider, description: model.description, features: model.features || [], max_tokens: model.maxTokens || 0 });
        } else {
            setModelForm({ code: '', name: '', provider: '', description: '', features: [], max_tokens: 0 });
        }
        setModelModal({ open: true, model });
    };

    return (
        <div className="space-y-4 md:space-y-6">
            <div className="flex flex-col gap-3 md:gap-4 lg:flex-row lg:items-center lg:justify-between">
                <div>
                    <h1 className="text-xl md:text-2xl font-bold text-[var(--text-primary)]">
                        对话模型
                        <span className="ml-2 md:ml-3 text-xs md:text-base font-normal text-[var(--text-secondary)]">
                            {models.length} 模型 / {models.filter(m => m.status === 1).length} 启用
                        </span>
                    </h1>
                    <p className="text-[var(--text-secondary)] mt-1 text-sm md:text-base">管理对话模型及渠道映射</p>
                </div>
                <div className="flex flex-wrap gap-2">
                    <button onClick={loadData}
                        className="flex items-center gap-2 px-3 md:px-4 py-2 border border-[var(--border-soft)] rounded-lg text-[var(--text-secondary)] hover:bg-[var(--surface)]">
                        <RefreshCw size={16} className={isLoading ? 'animate-spin' : ''} />
                        <span className="hidden md:inline">刷新</span>
                    </button>
                    <button onClick={() => openModelModal(null)}
                        className="flex items-center gap-2 px-4 md:px-6 py-2 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90 shadow-sm">
                        <Plus size={18} />
                        <span className="hidden md:inline">新建模型</span>
                        <span className="md:hidden">新建</span>
                    </button>
                </div>
            </div>

            {/* Search */}
            <div className="flex items-center gap-3">
                <div className="relative flex-1 md:max-w-md">
                    <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-secondary)]" />
                    <input type="text" value={searchTerm} onChange={e => setSearchTerm(e.target.value)}
                        className="w-full pl-9 pr-4 py-2 border border-[var(--border-soft)] rounded-lg text-sm"
                        placeholder="搜索模型名称..." />
                </div>
            </div>

            {/* Model list */}
            {isLoading ? (
                <div className="animate-pulse space-y-4">
                    {[1, 2, 3].map(i => <div key={i} className="bg-[var(--surface-card)] p-6 rounded-2xl border border-[var(--border-soft)] h-20" />)}
                </div>
            ) : (
                <DndContext sensors={sensors} collisionDetection={closestCenter}
                    onDragStart={(e: DragStartEvent) => setActiveId(String(e.active.id))}
                    onDragEnd={handleDragEnd} onDragCancel={() => setActiveId(null)}>
                    <SortableContext items={filteredModels.map(m => m.code)} strategy={verticalListSortingStrategy}>
                        <div className="space-y-3">
                            {filteredModels.map(model => {
                                const isExpanded = expandedModel === model.code;
                                const mcs = modelChannelsByCode.get(model.code) || [];
                                return (
                                    <SortableModelRow key={model.code} id={model.code} disabled={!canDrag}>
                                        <div className="p-4 flex items-center justify-between cursor-pointer hover:bg-[var(--surface)]"
                                    onClick={() => setExpandedModel(isExpanded ? null : model.code)}>
                                    <div className="flex items-center gap-3 min-w-0 flex-1">
                                        <span className="text-[var(--text-secondary)]">
                                            {isExpanded ? <ChevronDown size={18} /> : <ChevronRight size={18} />}
                                        </span>
                                        <div className="p-2 bg-[var(--primary-lighter)] rounded-xl"><MessageSquare size={18} className="text-[var(--primary)]" /></div>
                                        <div className="min-w-0 flex-1">
                                            <div className="flex items-center gap-2 flex-wrap">
                                                <span className="font-bold text-[var(--text-primary)]">{model.name}</span>
                                                <code className="text-xs px-2 py-0.5 bg-[var(--primary-lighter)] rounded text-[var(--text-secondary)]">{model.code}</code>
                                                <span className="text-xs px-2 py-0.5 rounded bg-blue-50 text-blue-600">{model.provider}</span>
                                                <span className={`text-xs px-2 py-0.5 rounded-full ${model.status === 1 ? 'bg-green-50 text-green-600' : 'bg-red-50 text-red-600'}`}>
                                                    {model.status === 1 ? '启用' : '禁用'}
                                                </span>
                                                <span className="text-xs text-[var(--text-secondary)]">渠道 {mcs.length}</span>
                                            </div>
                                            {model.description && <p className="text-sm text-[var(--text-secondary)] mt-1 truncate">{model.description}</p>}
                                        </div>
                                    </div>
                                    <div className="flex items-center gap-2 shrink-0">
                                        <button onClick={e => { e.stopPropagation(); handleToggleModelStatus(model); }}
                                            className={`p-2 rounded-lg text-sm ${model.status === 1 ? 'text-yellow-700 hover:bg-yellow-50' : 'text-green-700 hover:bg-green-50'}`}>
                                            <Power size={16} />
                                        </button>
                                        <button onClick={e => { e.stopPropagation(); openModelModal(model); }}
                                            className="p-2 text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">
                                            <Edit2 size={16} />
                                        </button>
                                        <button onClick={e => { e.stopPropagation(); handleDeleteModel(model.code); }}
                                            className="p-2 text-red-600 hover:bg-red-50 rounded-lg">
                                            <Trash2 size={16} />
                                        </button>
                                    </div>
                                </div>

                                {isExpanded && (
                                    <div className="border-t border-[var(--border-soft)] bg-[var(--surface)]/70 p-4">
                                        <div className="flex items-center justify-between mb-3">
                                            <h4 className="text-sm font-bold text-[var(--text-primary)]">渠道映射</h4>
                                            <button onClick={() => setMcModal({ open: true, modelCode: model.code, mc: null })}
                                                className="inline-flex items-center gap-1 px-3 py-1.5 bg-[var(--primary)] text-white rounded-lg text-sm font-bold hover:opacity-90">
                                                <Plus size={14} /> 添加
                                            </button>
                                        </div>
                                        {mcs.length === 0 ? (
                                            <p className="text-sm text-[var(--text-secondary)] text-center py-6">暂无渠道映射</p>
                                        ) : (
                                            <div className="space-y-2">
                                                {mcs.map(mc => (
                                                    <div key={mc.id} className="bg-[var(--surface-card)] p-3 rounded-xl border border-[var(--border-soft)] flex items-center justify-between gap-3">
                                                        <div className="min-w-0 flex-1">
                                                            <div className="flex items-center gap-2 flex-wrap">
                                                                <span className="text-sm font-medium text-[var(--text-primary)]">
                                                                    {channelNameMap.get(String(mc.channelId)) || `渠道#${mc.channelId}`}
                                                                </span>
                                                                {mc.vendorModel && <code className="text-xs px-1.5 py-0.5 bg-gray-100 rounded">{mc.vendorModel}</code>}
                                                                <span className={`text-xs px-1.5 py-0.5 rounded-full ${mc.status === 1 ? 'bg-green-50 text-green-600' : 'bg-red-50 text-red-600'}`}>
                                                                    {mc.status === 1 ? '启用' : '禁用'}
                                                                </span>
                                                            </div>
                                                            <div className="flex flex-wrap gap-3 text-xs text-[var(--text-secondary)] mt-1">
                                                                <span>优先级 {mc.priority}</span>
                                                                <span>{mc.priceMode === 'token' ? `输入¥${mc.inputPrice}/输出¥${mc.outputPrice}` : `¥${mc.inputPrice}/次`}</span>
                                                                <span>{mc.supportsStream !== false ? '✓流式' : '✗流式'}</span>
                                                                {mc.defaultStream === false && <span>默认非流式</span>}
                                                                {mc.extraConfig?.transfer_enabled && <span>✓OSS转存</span>}
                                                            </div>
                                                        </div>
                                                        <div className="flex items-center gap-1 shrink-0">
                                                            <button onClick={() => handleToggleMcStatus(mc)} className="p-1.5 hover:bg-gray-100 rounded-lg"><Power size={14} /></button>
                                                            <button onClick={() => setMcModal({ open: true, modelCode: model.code, mc })} className="p-1.5 hover:bg-gray-100 rounded-lg"><Edit2 size={14} /></button>
                                                            <button onClick={() => handleDeleteMc(mc.id)} className="p-1.5 text-red-500 hover:bg-red-50 rounded-lg"><Trash2 size={14} /></button>
                                                        </div>
                                                    </div>
                                                ))}
                                            </div>
                                        )}
                                    </div>
                                )}
                                    </SortableModelRow>
                                );
                            })}
                        </div>
                    </SortableContext>
                    <DragOverlay>
                        {activeId ? (() => {
                            const m = models.find(x => x.code === activeId);
                            if (!m) return null;
                            return (
                                <div className="rounded-2xl border border-[var(--primary)] bg-[var(--surface-card)] shadow-lg p-4 flex items-center gap-3 opacity-95">
                                    <GripVertical size={18} className="text-[var(--text-secondary)]" />
                                    <div className="p-2 bg-[var(--primary-lighter)] rounded-xl"><MessageSquare size={18} className="text-[var(--primary)]" /></div>
                                    <span className="font-bold text-[var(--text-primary)]">{m.name}</span>
                                    <code className="text-xs px-2 py-0.5 bg-[var(--primary-lighter)] rounded text-[var(--text-secondary)]">{m.code}</code>
                                </div>
                            );
                        })() : null}
                    </DragOverlay>
                </DndContext>
            )}

            {/* Model Modal */}
            {modelModal.open && (
                <Modal open={true} onClose={() => setModelModal({ open: false, model: null })} title={`${modelModal.model ? '编辑' : '新建'}模型`} width="max-w-md">
                        <form onSubmit={handleSaveModel} className="space-y-4">
                            <div>
                                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">模型编码 <span className="text-red-500">*</span></label>
                                <input type="text" value={modelForm.code} onChange={e => setModelForm({ ...modelForm, code: e.target.value })}
                                    className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] text-[var(--text-primary)]" required disabled={!!modelModal.model}
                                    placeholder="如: gpt-4o" />
                            </div>
                            <div>
                                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">模型名称 <span className="text-red-500">*</span></label>
                                <input type="text" value={modelForm.name} onChange={e => setModelForm({ ...modelForm, name: e.target.value })}
                                    className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] text-[var(--text-primary)]" required placeholder="如: GPT-4o" />
                            </div>
                            <div>
                                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">供应商</label>
                                <div className="relative">
                                    <input type="text" list="provider-options" value={modelForm.provider} onChange={e => setModelForm({ ...modelForm, provider: e.target.value })}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] text-[var(--text-primary)]" placeholder="选择或输入供应商" />
                                    <datalist id="provider-options">
                                        <option value="openai">OpenAI 兼容</option>
                                        <option value="anthropic">Anthropic (Claude)</option>
                                        <option value="google">Google (Gemini)</option>
                                        <option value="volcengine">火山引擎 (豆包)</option>
                                    </datalist>
                                </div>
                            </div>
                            <div>
                                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">描述</label>
                                <input type="text" value={modelForm.description} onChange={e => setModelForm({ ...modelForm, description: e.target.value })}
                                    className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] text-[var(--text-primary)]" />
                            </div>
                            <div>
                                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">能力特性</label>
                                <div className="flex flex-wrap gap-3">
                                    {['tools', 'vision', 'json_mode', 'reasoning'].map(f => (
                                        <label key={f} className="inline-flex items-center gap-1.5 text-sm text-[var(--text-primary)] cursor-pointer">
                                            <input type="checkbox" checked={modelForm.features.includes(f)}
                                                onChange={e => setModelForm({ ...modelForm, features: e.target.checked ? [...modelForm.features, f] : modelForm.features.filter(x => x !== f) })}
                                                className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded" />
                                            {f}
                                        </label>
                                    ))}
                                </div>
                            </div>
                            <div>
                                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">最大 Tokens</label>
                                <input type="number" value={modelForm.max_tokens || ''} onChange={e => setModelForm({ ...modelForm, max_tokens: parseInt(e.target.value) || 0 })}
                                    className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] text-[var(--text-primary)]" placeholder="如: 128000" />
                            </div>
                            <div className="flex justify-end gap-3 pt-4">
                                <button type="button" onClick={() => setModelModal({ open: false, model: null })}
                                    className="px-4 py-2 text-sm font-bold text-[var(--text-secondary)] bg-[var(--primary-lighter)] rounded-lg hover:bg-gray-200 transition-colors">取消</button>
                                <button type="submit" disabled={modelFormLoading}
                                    className="px-4 py-2 text-sm font-bold text-white bg-[var(--primary)] rounded-lg hover:opacity-90 disabled:opacity-50 transition-colors">
                                    {modelFormLoading ? '保存中...' : '保存'}
                                </button>
                            </div>
                        </form>
                </Modal>
            )}

            <ChatModelChannelModal
                isOpen={mcModal.open}
                modelCode={mcModal.modelCode}
                channel={mcModal.mc}
                channels={channels}
                onClose={() => setMcModal({ open: false, modelCode: '', mc: null })}
                onSave={loadData}
            />
        </div>
    );
};

export default ChatModelSection;
