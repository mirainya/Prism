import React, {useEffect, useMemo, useState} from 'react';
import {ChevronDown, ChevronRight, X} from 'lucide-react';
import {Channel, ChatModel, ChatModelChannel, PRICE_MODES} from '../types';

export interface ChatModelChannelFormData {
    model_code: string;
    channel_id: number;
    vendor_model: string;
    priority: number;
    price_mode: string;
    input_price: number;
    output_price: number;
    request_path: string;
    timeout: number;
    supports_stream?: boolean;
    default_stream?: boolean;
    extra_headers: Record<string, string>;
    extra_config: Record<string, any>;
}

export interface ChatModelChannelModalDefaults {
    modelCode?: string;
    channelId?: number;
}

const DEFAULT_FORM: ChatModelChannelFormData = {
    model_code: '',
    channel_id: 0,
    vendor_model: '',
    priority: 0,
    price_mode: 'token',
    input_price: 0,
    output_price: 0,
    request_path: '/v1/chat/completions',
    timeout: 120,
    supports_stream: true,
    default_stream: false,
    extra_headers: {},
    extra_config: {},
};

const stringifyJson = (value: Record<string, any> | undefined) => JSON.stringify(value || {}, null, 2);

const parseJsonObject = (text: string, fieldName: string): Record<string, any> => {
    try {
        const parsed = JSON.parse(text);
        if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') {
            throw new Error(`${fieldName} 必须是 JSON 对象`);
        }
        return parsed;
    } catch (error) {
        if (error instanceof Error && error.message.includes('必须是 JSON 对象')) {
            throw error;
        }
        throw new Error(`${fieldName} JSON 格式错误`);
    }
};

const buildCreateForm = (
    chatModels: ChatModel[],
    channels: Channel[],
    defaults?: ChatModelChannelModalDefaults,
): ChatModelChannelFormData => ({
    ...DEFAULT_FORM,
    model_code: defaults?.modelCode || chatModels[0]?.code || '',
    channel_id: defaults?.channelId || Number(channels[0]?.id) || 0,
});

const buildEditForm = (channelMapping: ChatModelChannel): ChatModelChannelFormData => ({
    model_code: channelMapping.modelCode,
    channel_id: channelMapping.channelId,
    vendor_model: channelMapping.vendorModel,
    priority: channelMapping.priority,
    price_mode: channelMapping.priceMode,
    input_price: channelMapping.inputPrice,
    output_price: channelMapping.outputPrice,
    request_path: channelMapping.requestPath,
    timeout: channelMapping.timeout,
    supports_stream: channelMapping.supportsStream ?? true,
    default_stream: channelMapping.defaultStream ?? false,
    extra_headers: channelMapping.extraHeaders || {},
    extra_config: channelMapping.extraConfig || {},
});

const ChatModelChannelModal: React.FC<{
    isOpen: boolean;
    channelMapping?: ChatModelChannel | null;
    chatModels: ChatModel[];
    channels: Channel[];
    defaults?: ChatModelChannelModalDefaults;
    onClose: () => void;
    onSave: (data: ChatModelChannelFormData) => Promise<void>;
}> = ({isOpen, channelMapping, chatModels, channels, defaults, onClose, onSave}) => {
    const [form, setForm] = useState<ChatModelChannelFormData>(DEFAULT_FORM);
    const [extraHeadersText, setExtraHeadersText] = useState('{}');
    const [extraConfigText, setExtraConfigText] = useState('{}');
    const [jsonError, setJsonError] = useState<{extraHeaders?: string; extraConfig?: string}>({});
    const [showAdvanced, setShowAdvanced] = useState(false);
    const [loading, setLoading] = useState(false);

    const hasAdvancedDefaults = useMemo(() => {
        return extraHeadersText !== '{}' || extraConfigText !== '{}';
    }, [extraConfigText, extraHeadersText]);

    useEffect(() => {
        if (!isOpen) {
            return;
        }

        const nextForm = channelMapping
            ? buildEditForm(channelMapping)
            : buildCreateForm(chatModels, channels, defaults);

        setForm(nextForm);
        setExtraHeadersText(stringifyJson(nextForm.extra_headers));
        setExtraConfigText(stringifyJson(nextForm.extra_config));
        setJsonError({});
        setShowAdvanced(Boolean(channelMapping?.extraHeaders && Object.keys(channelMapping.extraHeaders).length > 0)
            || Boolean(channelMapping?.extraConfig && Object.keys(channelMapping.extraConfig).length > 0));
    }, [channelMapping, chatModels, channels, defaults, isOpen]);

    useEffect(() => {
        if (hasAdvancedDefaults) {
            setShowAdvanced(true);
        }
    }, [hasAdvancedDefaults]);

    if (!isOpen) return null;

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();

        let parsedHeaders: Record<string, string>;
        let parsedConfig: Record<string, any>;

        try {
            parsedHeaders = parseJsonObject(extraHeadersText, '额外请求头') as Record<string, string>;
            parsedConfig = parseJsonObject(extraConfigText, '额外配置');
            setJsonError({});
        } catch (error) {
            const message = error instanceof Error ? error.message : 'JSON 格式错误';
            setJsonError({
                extraHeaders: message.includes('请求头') ? message : undefined,
                extraConfig: message.includes('额外配置') ? message : undefined,
            });
            return;
        }

        setLoading(true);
        try {
            await onSave({
                ...form,
                extra_headers: parsedHeaders,
                extra_config: parsedConfig,
            });
            onClose();
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
            <div className="bg-white rounded-2xl w-full max-w-2xl p-6 shadow-xl max-h-[90vh] overflow-y-auto">
                <div className="flex items-center justify-between mb-6">
                    <div>
                        <h3 className="text-lg font-bold text-gray-900">{channelMapping ? '编辑渠道映射' : '新建渠道映射'}</h3>
                        <p className="text-sm text-gray-500 mt-1">配置模型与渠道之间的供应商映射关系</p>
                    </div>
                    <button onClick={onClose} className="p-2 hover:bg-gray-100 rounded-lg"><X size={20}/></button>
                </div>
                <form onSubmit={handleSubmit} className="space-y-4">
                    <div className="grid grid-cols-2 gap-4">
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">模型</label>
                            <select
                                value={form.model_code}
                                onChange={e => setForm({...form, model_code: e.target.value})}
                                disabled={!!channelMapping}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 disabled:bg-gray-50"
                                required
                            >
                                {chatModels.map(m => (
                                    <option key={m.code} value={m.code}>{m.name} ({m.code})</option>
                                ))}
                            </select>
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">渠道</label>
                            <select
                                value={form.channel_id}
                                onChange={e => setForm({...form, channel_id: Number(e.target.value)})}
                                disabled={!!channelMapping}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 disabled:bg-gray-50"
                                required
                            >
                                {channels.map(ch => (
                                    <option key={ch.id} value={ch.id}>{ch.name} ({ch.type})</option>
                                ))}
                            </select>
                        </div>
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-gray-700 mb-1">供应商模型名</label>
                        <input
                            type="text"
                            value={form.vendor_model}
                            onChange={e => setForm({...form, vendor_model: e.target.value})}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            placeholder="如: gpt-4o-2024-08-06"
                            required
                        />
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">优先级</label>
                            <input
                                type="number"
                                value={form.priority}
                                onChange={e => setForm({...form, priority: Number(e.target.value)})}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            />
                            <p className="text-xs text-gray-500 mt-1">数值越大优先级越高</p>
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">计价模式</label>
                            <select
                                value={form.price_mode}
                                onChange={e => setForm({...form, price_mode: e.target.value})}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            >
                                {PRICE_MODES.map(m => (
                                    <option key={m.value} value={m.value}>{m.label}</option>
                                ))}
                            </select>
                        </div>
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">输入价格 (￥/1M tokens)</label>
                            <input
                                type="number"
                                step="0.0001"
                                value={form.input_price}
                                onChange={e => setForm({...form, input_price: Number(e.target.value)})}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            />
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">输出价格 (￥/1M tokens)</label>
                            <input
                                type="number"
                                step="0.0001"
                                value={form.output_price}
                                onChange={e => setForm({...form, output_price: Number(e.target.value)})}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            />
                        </div>
                    </div>
                    <div className="grid grid-cols-2 gap-4">
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">请求路径</label>
                            <input
                                type="text"
                                value={form.request_path}
                                onChange={e => setForm({...form, request_path: e.target.value})}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            />
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-gray-700 mb-1">超时时间 (秒)</label>
                            <input
                                type="number"
                                value={form.timeout}
                                onChange={e => setForm({...form, timeout: Number(e.target.value)})}
                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            />
                        </div>
                    </div>

                    <div className="grid grid-cols-2 gap-4">
                        <label className="flex items-start gap-3 rounded-xl border border-gray-200 p-3 cursor-pointer hover:border-indigo-300">
                            <input
                                type="checkbox"
                                checked={form.supports_stream ?? false}
                                onChange={e => setForm({
                                    ...form,
                                    supports_stream: e.target.checked,
                                    default_stream: e.target.checked ? form.default_stream : false,
                                })}
                                className="mt-1"
                            />
                            <div>
                                <div className="text-sm font-medium text-gray-700">支持流式响应</div>
                                <p className="text-xs text-gray-500 mt-1">关闭后 Playground 会默认禁用 stream 开关。</p>
                            </div>
                        </label>
                        <label className={`flex items-start gap-3 rounded-xl border p-3 ${form.supports_stream ? 'border-gray-200 cursor-pointer hover:border-indigo-300' : 'border-gray-100 bg-gray-50 cursor-not-allowed'}`}>
                            <input
                                type="checkbox"
                                checked={Boolean(form.default_stream && form.supports_stream)}
                                disabled={!form.supports_stream}
                                onChange={e => setForm({...form, default_stream: e.target.checked})}
                                className="mt-1"
                            />
                            <div>
                                <div className="text-sm font-medium text-gray-700">默认启用流式</div>
                                <p className="text-xs text-gray-500 mt-1">仅在支持流式时生效，未显式指定 stream 时作为默认行为。</p>
                            </div>
                        </label>
                    </div>

                    <div className="border border-gray-200 rounded-xl overflow-hidden">
                        <button
                            type="button"
                            onClick={() => setShowAdvanced(prev => !prev)}
                            className="w-full flex items-center justify-between px-4 py-3 bg-gray-50 hover:bg-gray-100 transition-colors"
                        >
                            <div className="text-left">
                                <div className="text-sm font-medium text-gray-800">高级配置</div>
                                <div className="text-xs text-gray-500 mt-1">可选配置 extraHeaders、extraConfig 与 extra_body 等厂商扩展参数</div>
                            </div>
                            {showAdvanced ? <ChevronDown size={18} className="text-gray-400"/> : <ChevronRight size={18} className="text-gray-400"/>}
                        </button>
                        {showAdvanced && (
                            <div className="p-4 space-y-4 border-t border-gray-200 bg-white">
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">额外请求头 (JSON)</label>
                                    <textarea
                                        value={extraHeadersText}
                                        onChange={e => setExtraHeadersText(e.target.value)}
                                        className={`w-full px-3 py-2 border rounded-lg font-mono text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500 ${jsonError.extraHeaders ? 'border-red-300' : 'border-gray-200'}`}
                                        placeholder='{"x-foo": "bar"}'
                                        rows={5}
                                    />
                                    {jsonError.extraHeaders && <p className="text-xs text-red-500 mt-1">{jsonError.extraHeaders}</p>}
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">额外配置 (JSON)</label>
                                    <textarea
                                        value={extraConfigText}
                                        onChange={e => setExtraConfigText(e.target.value)}
                                        className={`w-full px-3 py-2 border rounded-lg font-mono text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500 ${jsonError.extraConfig ? 'border-red-300' : 'border-gray-200'}`}
                                        placeholder='{"stream_options": {"include_usage": true}}'
                                        rows={6}
                                    />
                                    {jsonError.extraConfig && <p className="text-xs text-red-500 mt-1">{jsonError.extraConfig}</p>}
                                </div>
                            </div>
                        )}
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

export default ChatModelChannelModal;
