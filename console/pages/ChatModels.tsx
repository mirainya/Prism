import React, {useEffect, useState} from 'react';
import {Plus, Edit3, Trash2, X, Bot, Power, Zap, Check} from 'lucide-react';
import {
    fetchChatModels,
    createChatModel,
    updateChatModel,
    deleteChatModel,
    fetchChatModelPresets,
    quickSetupChatModels,
    fetchChannels,
} from '../services/api';
import {ChatModel, Channel, CHAT_PROVIDERS, PRICE_MODES} from '../types';

const STATUS_MAP: Record<number, { label: string; color: string }> = {
    1: {label: '已启用', color: 'bg-green-100 text-green-700'},
    0: {label: '已禁用', color: 'bg-gray-100 text-gray-700'},
};

// 新建/编辑模型弹窗
const ChatModelModal: React.FC<{
    isOpen: boolean;
    model?: ChatModel | null;
    onClose: () => void;
    onSave: (data: any) => Promise<void>;
}> = ({isOpen, model, onClose, onSave}) => {
    const [form, setForm] = useState({
        code: '',
        name: '',
        provider: 'openai',
        description: '',
    });
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        if (model) {
            setForm({
                code: model.code,
                name: model.name,
                provider: model.provider,
                description: model.description,
            });
        } else {
            setForm({
                code: '',
                name: '',
                provider: 'openai',
                description: '',
            });
        }
    }, [model, isOpen]);

    if (!isOpen) return null;

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setLoading(true);
        try {
            await onSave(form);
            onClose();
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
            <div className="bg-white rounded-2xl w-full max-w-lg p-6 shadow-xl max-h-[90vh] overflow-y-auto">
                <div className="flex items-center justify-between mb-6">
                    <h3 className="text-lg font-bold text-gray-900">{model ? '编辑模型' : '新建模型'}</h3>
                    <button onClick={onClose} className="p-2 hover:bg-gray-100 rounded-lg"><X size={20}/></button>
                </div>
                <form onSubmit={handleSubmit} className="space-y-4">
                    <div>
                        <label className="block text-sm font-medium text-gray-700 mb-1">模型标识</label>
                        <input
                            type="text"
                            value={form.code}
                            onChange={e => setForm({...form, code: e.target.value})}
                            disabled={!!model}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 disabled:bg-gray-50"
                            placeholder="如: gpt-4o, claude-3-opus"
                            required
                        />
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-gray-700 mb-1">显示名称</label>
                        <input
                            type="text"
                            value={form.name}
                            onChange={e => setForm({...form, name: e.target.value})}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            placeholder="如: GPT-4o, Claude 3 Opus"
                            required
                        />
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-gray-700 mb-1">提供商</label>
                        <select
                            value={form.provider}
                            onChange={e => setForm({...form, provider: e.target.value})}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                        >
                            {CHAT_PROVIDERS.map(p => (
                                <option key={p.value} value={p.value}>{p.label}</option>
                            ))}
                        </select>
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-gray-700 mb-1">描述</label>
                        <textarea
                            value={form.description}
                            onChange={e => setForm({...form, description: e.target.value})}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            rows={3}
                            placeholder="模型描述信息"
                        />
                    </div>
                    <div className="flex gap-3 pt-4">
                        <button type="button" onClick={onClose}
                                className="flex-1 px-4 py-2 border border-gray-200 rounded-lg text-gray-700 hover:bg-gray-50">取消
                        </button>
                        <button type="submit" disabled={loading}
                                className="flex-1 px-4 py-2 bg-indigo-600 text-white rounded-lg hover:bg-indigo-700 disabled:opacity-50">
                            {loading ? '保存中...' : '保存'}
                        </button>
                    </div>
                </form>
            </div>
        </div>
    );
};

// 快速配置弹窗
const QuickSetupModal: React.FC<{
    isOpen: boolean;
    onClose: () => void;
    onDone: () => void;
}> = ({isOpen, onClose, onDone}) => {
    const [step, setStep] = useState(1);
    const [channels, setChannels] = useState<Channel[]>([]);
    const [channelId, setChannelId] = useState<number>(0);
    const [provider, setProvider] = useState('openai');
    const [presets, setPresets] = useState<{ code: string; name: string }[]>([]);
    const [selected, setSelected] = useState<Set<string>>(new Set());
    const [priceMode, setPriceMode] = useState('token');
    const [inputPrice, setInputPrice] = useState(0);
    const [outputPrice, setOutputPrice] = useState(0);
    const [requestPath, setRequestPath] = useState('/v1/chat/completions');
    const [loading, setLoading] = useState(false);
    const [result, setResult] = useState<{ created: number; skipped: number; mapped: number } | null>(null);

    useEffect(() => {
        if (isOpen) {
            setStep(1);
            setSelected(new Set());
            setResult(null);
            fetchChannels().then(setChannels);
        }
    }, [isOpen]);

    const loadPresets = async () => {
        setLoading(true);
        try {
            const data = await fetchChatModelPresets(provider);
            setPresets(data);
            setSelected(new Set(data.map(p => p.code)));
        } finally {
            setLoading(false);
        }
    };

    const toggleSelect = (code: string) => {
        const next = new Set(selected);
        if (next.has(code)) next.delete(code); else next.add(code);
        setSelected(next);
    };

    const selectAll = () => {
        if (selected.size === presets.length) setSelected(new Set());
        else setSelected(new Set(presets.map(p => p.code)));
    };

    const handleSubmit = async () => {
        setLoading(true);
        try {
            const models = presets.filter(p => selected.has(p.code)).map(p => ({
                code: p.code,
                name: p.name,
            }));
            const res = await quickSetupChatModels({
                channel_id: channelId,
                provider,
                models,
                price_mode: priceMode,
                input_price: inputPrice,
                output_price: outputPrice,
                request_path: requestPath,
            });
            setResult(res);
            setStep(3);
        } finally {
            setLoading(false);
        }
    };

    if (!isOpen) return null;

    return (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
            <div className="bg-white rounded-2xl w-full max-w-2xl p-6 shadow-xl max-h-[90vh] overflow-y-auto">
                <div className="flex items-center justify-between mb-6">
                    <h3 className="text-lg font-bold text-gray-900 flex items-center gap-2">
                        <Zap size={20} className="text-amber-500"/>快速配置模型
                    </h3>
                    <button onClick={onClose} className="p-2 hover:bg-gray-100 rounded-lg"><X size={20}/></button>
                </div>

                {/* 步骤指示器 */}
                <div className="flex items-center gap-2 mb-6">
                    {[1, 2, 3].map(s => (
                        <div key={s} className="flex items-center gap-2">
                            <div className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-medium ${
                                step >= s ? 'bg-indigo-600 text-white' : 'bg-gray-200 text-gray-500'
                            }`}>{step > s ? <Check size={14}/> : s}</div>
                            {s < 3 && <div className={`w-12 h-0.5 ${step > s ? 'bg-indigo-600' : 'bg-gray-200'}`}/>}
                        </div>
                    ))}
                    <span className="ml-2 text-sm text-gray-500">
                        {step === 1 && '选择渠道和提供商'}
                        {step === 2 && '选择模型和配置'}
                        {step === 3 && '完成'}
                    </span>
                </div>

                {step === 1 && (
                    <div className="space-y-4">
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">关联渠道</label>
                            <select
                                value={channelId}
                                onChange={e => setChannelId(Number(e.target.value))}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            >
                                <option value={0}>请选择渠道</option>
                                {channels.filter(ch => ch.status === 1).map(ch => (
                                    <option key={ch.id} value={ch.id}>{ch.name} ({ch.type})</option>
                                ))}
                            </select>
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">提供商</label>
                            <select
                                value={provider}
                                onChange={e => setProvider(e.target.value)}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            >
                                {CHAT_PROVIDERS.map(p => (
                                    <option key={p.value} value={p.value}>{p.label}</option>
                                ))}
                            </select>
                        </div>
                        <div className="flex justify-end pt-4">
                            <button
                                onClick={() => { loadPresets(); setStep(2); }}
                                disabled={!channelId}
                                className="px-4 py-2 bg-indigo-600 text-white rounded-lg hover:bg-indigo-700 disabled:opacity-50"
                            >下一步</button>
                        </div>
                    </div>
                )}

                {step === 2 && (
                    <div className="space-y-4">
                        <div>
                            <div className="flex items-center justify-between mb-2">
                                <label className="text-sm font-medium text-gray-700">选择模型 ({selected.size}/{presets.length})</label>
                                <button onClick={selectAll} className="text-xs text-indigo-600 hover:underline">
                                    {selected.size === presets.length ? '取消全选' : '全选'}
                                </button>
                            </div>
                            {loading ? (
                                <div className="py-8 text-center text-gray-400">加载中...</div>
                            ) : (
                                <div className="border border-gray-200 rounded-lg max-h-48 overflow-y-auto divide-y divide-gray-100">
                                    {presets.map(p => (
                                        <label key={p.code} className="flex items-center gap-3 px-3 py-2 hover:bg-gray-50 cursor-pointer">
                                            <input
                                                type="checkbox"
                                                checked={selected.has(p.code)}
                                                onChange={() => toggleSelect(p.code)}
                                                className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500"
                                            />
                                            <span className="font-mono text-sm text-gray-900">{p.code}</span>
                                            <span className="text-sm text-gray-500">{p.name}</span>
                                        </label>
                                    ))}
                                </div>
                            )}
                        </div>
                        <div className="grid grid-cols-2 gap-4">
                            <div>
                                <label className="block text-sm font-medium text-gray-700 mb-1">计价模式</label>
                                <select
                                    value={priceMode}
                                    onChange={e => setPriceMode(e.target.value)}
                                    className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                >
                                    {PRICE_MODES.map(m => (
                                        <option key={m.value} value={m.value}>{m.label}</option>
                                    ))}
                                </select>
                            </div>
                            <div>
                                <label className="block text-sm font-medium text-gray-700 mb-1">请求路径</label>
                                <input
                                    type="text"
                                    value={requestPath}
                                    onChange={e => setRequestPath(e.target.value)}
                                    className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                />
                            </div>
                        </div>
                        <div className="grid grid-cols-2 gap-4">
                            <div>
                                <label className="block text-sm font-medium text-gray-700 mb-1">输入价格 (每百万token)</label>
                                <input
                                    type="number"
                                    value={inputPrice}
                                    onChange={e => setInputPrice(Number(e.target.value))}
                                    className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    min={0}
                                    step={0.01}
                                />
                            </div>
                            <div>
                                <label className="block text-sm font-medium text-gray-700 mb-1">输出价格 (每百万token)</label>
                                <input
                                    type="number"
                                    value={outputPrice}
                                    onChange={e => setOutputPrice(Number(e.target.value))}
                                    className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    min={0}
                                    step={0.01}
                                />
                            </div>
                        </div>
                        <div className="flex gap-3 pt-4">
                            <button onClick={() => setStep(1)}
                                    className="px-4 py-2 border border-gray-200 rounded-lg text-gray-700 hover:bg-gray-50">上一步</button>
                            <button
                                onClick={handleSubmit}
                                disabled={loading || selected.size === 0}
                                className="flex-1 px-4 py-2 bg-indigo-600 text-white rounded-lg hover:bg-indigo-700 disabled:opacity-50"
                            >{loading ? '配置中...' : `确认配置 (${selected.size} 个模型)`}</button>
                        </div>
                    </div>
                )}

                {step === 3 && result && (
                    <div className="text-center py-8">
                        <div className="w-16 h-16 bg-green-100 rounded-full flex items-center justify-center mx-auto mb-4">
                            <Check size={32} className="text-green-600"/>
                        </div>
                        <h4 className="text-lg font-semibold text-gray-900 mb-2">配置完成</h4>
                        <div className="flex justify-center gap-6 text-sm text-gray-600 mb-6">
                            <span>新建模型: <strong className="text-indigo-600">{result.created}</strong></span>
                            <span>已跳过: <strong className="text-gray-500">{result.skipped}</strong></span>
                            <span>渠道映射: <strong className="text-green-600">{result.mapped}</strong></span>
                        </div>
                        <button
                            onClick={() => { onDone(); onClose(); }}
                            className="px-6 py-2 bg-indigo-600 text-white rounded-lg hover:bg-indigo-700"
                        >完成</button>
                    </div>
                )}
            </div>
        </div>
    );
};

const ChatModels: React.FC = () => {
    const [models, setModels] = useState<ChatModel[]>([]);
    const [loading, setLoading] = useState(true);
    const [modalOpen, setModalOpen] = useState(false);
    const [quickSetupOpen, setQuickSetupOpen] = useState(false);
    const [editingModel, setEditingModel] = useState<ChatModel | null>(null);

    const loadModels = async () => {
        setLoading(true);
        try {
            const data = await fetchChatModels();
            setModels(data);
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        loadModels();
    }, []);

    const handleCreate = () => {
        setEditingModel(null);
        setModalOpen(true);
    };

    const handleEdit = (model: ChatModel) => {
        setEditingModel(model);
        setModalOpen(true);
    };

    const handleSave = async (data: any) => {
        if (editingModel) {
            await updateChatModel(editingModel.code, data);
        } else {
            await createChatModel(data);
        }
        loadModels();
    };

    const handleDelete = async (code: string) => {
        if (confirm('确定删除该模型?')) {
            await deleteChatModel(code);
            loadModels();
        }
    };

    const handleToggleStatus = async (model: ChatModel) => {
        const newStatus = model.status === 1 ? 0 : 1;
        await updateChatModel(model.code, {status: newStatus});
        loadModels();
    };

    const getProviderLabel = (provider: string) => {
        return CHAT_PROVIDERS.find(p => p.value === provider)?.label || provider;
    };

    if (loading) {
        return (
            <div className="flex items-center justify-center h-64">
                <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-indigo-600"></div>
            </div>
        );
    }

    return (
        <div className="p-6 max-w-7xl mx-auto">
            <div className="flex items-center justify-between mb-6">
                <div className="flex items-center gap-3">
                    <div className="p-2 bg-indigo-100 rounded-lg">
                        <Bot className="text-indigo-600" size={24}/>
                    </div>
                    <div>
                        <h1 className="text-xl font-bold text-gray-900">语言模型管理</h1>
                        <p className="text-sm text-gray-500">管理 Chat 模型定义</p>
                    </div>
                </div>
                <div className="flex items-center gap-2">
                    <button
                        onClick={() => setQuickSetupOpen(true)}
                        className="flex items-center gap-2 px-4 py-2 border border-amber-300 text-amber-700 bg-amber-50 rounded-lg hover:bg-amber-100 transition-colors"
                    >
                        <Zap size={20}/>
                        快速配置
                    </button>
                    <button
                        onClick={handleCreate}
                        className="flex items-center gap-2 px-4 py-2 bg-indigo-600 text-white rounded-lg hover:bg-indigo-700 transition-colors"
                    >
                        <Plus size={20}/>
                        添加模型
                    </button>
                </div>
            </div>

            <div className="bg-white rounded-xl shadow-sm border border-gray-100 overflow-hidden">
                <table className="min-w-full">
                    <thead className="bg-gray-50">
                    <tr>
                        <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">模型标识</th>
                        <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">名称</th>
                        <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">提供商</th>
                        <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">描述</th>
                        <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">状态</th>
                        <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">操作</th>
                    </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-100">
                    {models.map(model => (
                        <tr key={model.code} className="hover:bg-gray-50">
                            <td className="px-6 py-4 font-mono text-sm text-gray-900">{model.code}</td>
                            <td className="px-6 py-4 text-sm text-gray-900">{model.name}</td>
                            <td className="px-6 py-4">
                                    <span className="px-2 py-1 bg-blue-100 text-blue-700 rounded text-xs font-medium">
                                        {getProviderLabel(model.provider)}
                                    </span>
                            </td>
                            <td className="px-6 py-4 text-sm text-gray-500 max-w-xs truncate">{model.description}</td>
                            <td className="px-6 py-4">
                                    <span
                                        className={`px-2 py-1 rounded text-xs font-medium ${STATUS_MAP[model.status]?.color}`}>
                                        {STATUS_MAP[model.status]?.label}
                                    </span>
                            </td>
                            <td className="px-6 py-4">
                                <div className="flex items-center gap-1">
                                    <button
                                        onClick={() => handleToggleStatus(model)}
                                        className={`p-1.5 rounded-lg transition-colors ${model.status === 1 ? 'text-green-600 hover:bg-green-50' : 'text-gray-400 hover:bg-gray-100'}`}
                                        title={model.status === 1 ? '禁用' : '启用'}
                                    >
                                        <Power size={16}/>
                                    </button>
                                    <button
                                        onClick={() => handleEdit(model)}
                                        className="p-1.5 text-gray-400 hover:text-indigo-600 hover:bg-indigo-50 rounded-lg transition-colors"
                                        title="编辑"
                                    >
                                        <Edit3 size={16}/>
                                    </button>
                                    <button
                                        onClick={() => handleDelete(model.code)}
                                        className="p-1.5 text-gray-400 hover:text-red-600 hover:bg-red-50 rounded-lg transition-colors"
                                        title="删除"
                                    >
                                        <Trash2 size={16}/>
                                    </button>
                                </div>
                            </td>
                        </tr>
                    ))}
                    {models.length === 0 && (
                        <tr>
                            <td colSpan={6} className="px-6 py-12 text-center text-gray-500">
                                暂无模型数据
                            </td>
                        </tr>
                    )}
                    </tbody>
                </table>
            </div>

            <ChatModelModal
                isOpen={modalOpen}
                model={editingModel}
                onClose={() => setModalOpen(false)}
                onSave={handleSave}
            />

            <QuickSetupModal
                isOpen={quickSetupOpen}
                onClose={() => setQuickSetupOpen(false)}
                onDone={loadModels}
            />
        </div>
    );
};

export default ChatModels;
