import React, { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import { Modal } from '../../components/ui/Modal';
import { createChatModelChannel, updateChatModelChannel } from '../../services/api';
import { ChatModelChannel, Channel } from '../../types';

const ChatModelChannelModal: React.FC<{
    isOpen: boolean;
    modelCode: string;
    channel: ChatModelChannel | null;
    channels: Channel[];
    onClose: () => void;
    onSave: () => void;
}> = ({ isOpen, modelCode, channel, channels, onClose, onSave }) => {
    const [form, setForm] = useState({
        channel_id: 0,
        vendor_model: '',
        protocol: 'openai',
        priority: 0,
        price_mode: 'token' as 'token' | 'request',
        input_price: 0,
        output_price: 0,
        request_path: '',
        timeout: 60,
        supports_stream: true,
        default_stream: true,
        transfer_enabled: false,
    });
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        if (channel) {
            setForm({
                channel_id: Number(channel.channelId),
                vendor_model: channel.vendorModel || '',
                protocol: channel.protocol || 'openai',
                priority: channel.priority || 0,
                price_mode: channel.priceMode || 'token',
                input_price: channel.inputPrice || 0,
                output_price: channel.outputPrice || 0,
                request_path: channel.requestPath || '',
                timeout: channel.timeout || 60,
                supports_stream: channel.supportsStream !== false,
                default_stream: channel.defaultStream !== false,
                transfer_enabled: channel.extraConfig?.transfer_enabled === true,
            });
        } else {
            setForm({
                channel_id: 0,
                vendor_model: '',
                protocol: 'openai',
                priority: 0,
                price_mode: 'token',
                input_price: 0,
                output_price: 0,
                request_path: '',
                timeout: 60,
                supports_stream: true,
                default_stream: true,
                transfer_enabled: false,
            });
        }
    }, [channel, isOpen]);

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setLoading(true);
        try {
            const payload = {
                vendor_model: form.vendor_model,
                protocol: form.protocol,
                priority: form.priority,
                price_mode: form.price_mode,
                input_price: form.input_price,
                output_price: form.output_price,
                request_path: form.request_path,
                timeout: form.timeout,
                supports_stream: form.supports_stream,
                default_stream: form.default_stream,
                extra_config: { transfer_enabled: form.transfer_enabled },
            };
            if (channel) {
                await updateChatModelChannel(channel.id, payload);
            } else {
                await createChatModelChannel({ ...payload, model_code: modelCode, channel_id: form.channel_id });
            }
            onSave();
            onClose();
        } finally {
            setLoading(false);
        }
    };

    if (!isOpen) return null;

    return (
        <Modal open={true} onClose={onClose} title={`${channel ? '编辑' : '新建'}渠道映射`}>
                <form onSubmit={handleSubmit} className="space-y-4">
                    {!channel && (
                        <div>
                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">渠道 <span className="text-red-500">*</span></label>
                            <select value={form.channel_id} onChange={e => setForm({ ...form, channel_id: Number(e.target.value) })}
                                className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg" required>
                                <option value={0}>选择渠道</option>
                                {channels.map(ch => <option key={ch.id} value={ch.id}>{ch.name}</option>)}
                            </select>
                        </div>
                    )}
                    <div className="grid grid-cols-2 gap-4">
                        <div>
                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">供应商模型名</label>
                            <input type="text" value={form.vendor_model} onChange={e => setForm({ ...form, vendor_model: e.target.value })}
                                className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg" placeholder="留空则使用模型code" />
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">优先级</label>
                            <input type="number" value={form.priority} onChange={e => setForm({ ...form, priority: Number(e.target.value) })}
                                className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg" />
                        </div>
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">协议</label>
                        <select value={form.protocol} onChange={e => setForm({ ...form, protocol: e.target.value })}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg">
                            <option value="openai">OpenAI 兼容 (chat/completions)</option>
                            <option value="anthropic">Anthropic (Claude)</option>
                            <option value="volcengine">火山引擎 (豆包 Responses)</option>
                        </select>
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">请求路径</label>
                        <input type="text" value={form.request_path} onChange={e => setForm({ ...form, request_path: e.target.value })}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg" placeholder="/v1/chat/completions" />
                    </div>
                    <div className="grid grid-cols-3 gap-4">
                        <div>
                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">计价模式</label>
                            <select value={form.price_mode} onChange={e => setForm({ ...form, price_mode: e.target.value as any })}
                                className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg">
                                <option value="token">按Token</option>
                                <option value="request">按请求</option>
                            </select>
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">输入价格</label>
                            <input type="number" value={form.input_price} onChange={e => setForm({ ...form, input_price: Number(e.target.value) })}
                                className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg" step="0.0001" min="0" />
                        </div>
                        <div>
                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">输出价格</label>
                            <input type="number" value={form.output_price} onChange={e => setForm({ ...form, output_price: Number(e.target.value) })}
                                className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg" step="0.0001" min="0" />
                        </div>
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">超时时间(秒)</label>
                        <input type="number" value={form.timeout} onChange={e => setForm({ ...form, timeout: Number(e.target.value) })}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg" min="1" />
                    </div>
                    <div className="space-y-2">
                        <div className="flex items-center gap-2">
                            <input type="checkbox" id="supports_stream" checked={form.supports_stream}
                                onChange={e => setForm({ ...form, supports_stream: e.target.checked })}
                                className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded" />
                            <label htmlFor="supports_stream" className="text-sm text-[var(--text-primary)]">支持流式输出</label>
                        </div>
                        <div className="flex items-center gap-2">
                            <input type="checkbox" id="default_stream" checked={form.default_stream}
                                onChange={e => setForm({ ...form, default_stream: e.target.checked })}
                                className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded" />
                            <label htmlFor="default_stream" className="text-sm text-[var(--text-primary)]">默认使用流式</label>
                        </div>
                        <div className="flex items-center gap-2">
                            <input type="checkbox" id="mc_transfer_enabled" checked={form.transfer_enabled}
                                onChange={e => setForm({ ...form, transfer_enabled: e.target.checked })}
                                className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded" />
                            <label htmlFor="mc_transfer_enabled" className="text-sm text-[var(--text-primary)]">结果文件转存到 OSS</label>
                        </div>
                    </div>
                    <div className="flex justify-end gap-3 pt-4">
                        <button type="button" onClick={onClose} className="px-4 py-2 text-sm font-bold text-[var(--text-secondary)] bg-[var(--primary-lighter)] rounded-lg hover:bg-gray-200 transition-colors">取消</button>
                        <button type="submit" disabled={loading}
                            className="px-4 py-2 text-sm font-bold text-white bg-[var(--primary)] rounded-lg hover:opacity-90 disabled:opacity-50 transition-colors">
                            {loading ? '保存中...' : '保存'}
                        </button>
                    </div>
                </form>
        </Modal>
    );
};

export default ChatModelChannelModal;
