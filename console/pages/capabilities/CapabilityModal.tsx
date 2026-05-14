import React, { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import { Modal } from '../../components/ui/Modal';
import { createCapability, updateCapability } from '../../services/api';
import { Capability, CapabilityStandardParamSchema } from '../../types';
import { STANDARD_PARAMS, CAPABILITY_TYPES, PARAM_TYPES, CustomParam } from './constants';

const CapabilityModal: React.FC<{
    isOpen: boolean;
    capability: Capability | null;
    onClose: () => void;
    onSave: () => void;
}> = ({isOpen, capability, onClose, onSave}) => {
    const [form, setForm] = useState({ code: '', name: '', type: 'image', description: '', status: 1 });
    const [selectedStandardParams, setSelectedStandardParams] = useState<string[]>([]);
    const [requiredStandardParams, setRequiredStandardParams] = useState<string[]>([]);
    const [customParams, setCustomParams] = useState<CustomParam[]>([]);
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        if (capability) {
            setForm({ code: capability.code, name: capability.name, type: capability.type || 'image', description: capability.description || '', status: capability.status });
            const allParams = capability.standardParams || {};
            const presetKeys: string[] = [];
            const reqKeys: string[] = [];
            const customs: CustomParam[] = [];
            for (const [key, schema] of Object.entries(allParams) as [string, CapabilityStandardParamSchema][]) {
                if (key in STANDARD_PARAMS) {
                    presetKeys.push(key);
                    if (schema.required) reqKeys.push(key);
                } else {
                    customs.push({ key, name: schema.name || key, type: schema.type || 'string', options: (schema.options || schema.enumValues || []).join(', '), default: schema.default != null ? String(schema.default) : '', required: !!schema.required });
                }
            }
            setSelectedStandardParams(presetKeys);
            setRequiredStandardParams(reqKeys);
            setCustomParams(customs);
        } else {
            setForm({code: '', name: '', type: 'image', description: '', status: 1});
            setSelectedStandardParams([]);
            setRequiredStandardParams([]);
            setCustomParams([]);
        }
    }, [capability, isOpen]);

    if (!isOpen) return null;

    const buildStandardParams = () => {
        const result: Record<string, any> = {};
        for (const key of selectedStandardParams) {
            const def = STANDARD_PARAMS[key];
            if (!def) continue;
            result[key] = { type: def.type, name: def.name, required: requiredStandardParams.includes(key), ...(def.options ? {options: def.options} : {}) };
        }
        for (const cp of customParams) {
            if (!cp.key.trim()) continue;
            const entry: Record<string, any> = { type: cp.type, name: cp.name || cp.key, required: cp.required };
            const opts = cp.options.split(/[,，]/).map(s => s.trim()).filter(Boolean);
            if (opts.length > 0) entry.options = opts;
            if (cp.default.trim()) entry.default = cp.type === 'number' ? Number(cp.default) : cp.default;
            result[cp.key.trim()] = entry;
        }
        return result;
    };

    const toggleStandardParam = (key: string, checked: boolean) => {
        if (checked) { setSelectedStandardParams(prev => prev.includes(key) ? prev : [...prev, key]); return; }
        setSelectedStandardParams(prev => prev.filter(item => item !== key));
        setRequiredStandardParams(prev => prev.filter(item => item !== key));
    };

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setLoading(true);
        try {
            const standard_params = buildStandardParams();
            if (capability) {
                await updateCapability(capability.code, { code: form.code.trim(), name: form.name, type: form.type, description: form.description, status: form.status, standard_params });
            } else {
                await createCapability({ code: form.code, name: form.name, type: form.type, description: form.description, standard_params });
            }
            onSave();
            onClose();
        } finally {
            setLoading(false);
        }
    };

    return (
        <Modal open={true} onClose={onClose} title={capability ? '编辑能力' : '新建能力'}>
                <form onSubmit={handleSubmit} className="space-y-4 max-h-[70vh] overflow-y-auto">
                    <div>
                        <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">能力编码 <span className="text-red-500">*</span></label>
                        <input type="text" value={form.code} onChange={e => setForm({...form, code: e.target.value})}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                            placeholder="如: text2img, img2video" required />
                        <p className="text-xs text-[var(--text-secondary)] mt-1">唯一标识，修改后会同步更新相关引用数据</p>
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">能力名称 <span className="text-red-500">*</span></label>
                        <input type="text" value={form.name} onChange={e => setForm({...form, name: e.target.value})}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                            placeholder="如: 文生图, 图生视频" required />
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">能力类型 <span className="text-red-500">*</span></label>
                        <select value={form.type} onChange={e => setForm({...form, type: e.target.value})}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                            {CAPABILITY_TYPES.map(t => (<option key={t.value} value={t.value}>{t.label}</option>))}
                        </select>
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">描述</label>
                        <textarea value={form.description} onChange={e => setForm({...form, description: e.target.value})}
                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                            placeholder="能力的详细描述..." rows={3} />
                    </div>
                    <div>
                        <div className="flex items-center justify-between mb-2">
                            <label className="block text-sm font-medium text-[var(--text-primary)]">标准参数</label>
                            <span className="text-xs text-[var(--text-secondary)]">供 Playground / API 统一输入使用</span>
                        </div>
                        <div className="max-h-64 overflow-y-auto rounded-xl border border-[var(--border-soft)] divide-y divide-gray-100 bg-[var(--surface)]">
                            {Object.entries(STANDARD_PARAMS).map(([key, def]) => {
                                const checked = selectedStandardParams.includes(key);
                                const required = requiredStandardParams.includes(key);
                                return (
                                    <label key={key} className="flex items-start gap-3 px-3 py-3 hover:bg-[var(--surface-card)] transition-colors cursor-pointer">
                                        <input type="checkbox" checked={checked} onChange={e => toggleStandardParam(key, e.target.checked)}
                                            className="mt-1 rounded border-gray-300 text-[var(--primary)] focus:ring-[var(--primary)]" />
                                        <div className="flex-1 min-w-0">
                                            <div className="flex items-center gap-2 flex-wrap">
                                                <span className="text-sm font-medium text-gray-800">{def.name}</span>
                                                <code className="text-xs text-[var(--text-secondary)]">{key}</code>
                                                <span className="px-1.5 py-0.5 rounded bg-[var(--surface-card)] text-[10px] text-[var(--text-secondary)] border border-[var(--border-soft)]">{def.type}</span>
                                            </div>
                                            {def.options?.length ? (
                                                <div className="mt-1 text-xs text-[var(--text-secondary)]">可选值：{def.options.join(' / ')}</div>
                                            ) : null}
                                            <div className="mt-2 flex items-center gap-2">
                                                <input type="checkbox" checked={required} disabled={!checked}
                                                    onChange={e => {
                                                        if (e.target.checked) {
                                                            setRequiredStandardParams(prev => prev.includes(key) ? prev : [...prev, key]);
                                                        } else {
                                                            setRequiredStandardParams(prev => prev.filter(item => item !== key));
                                                        }
                                                    }}
                                                    className="rounded border-gray-300 text-[var(--primary)] focus:ring-[var(--primary)]" />
                                                <span className={`text-xs ${checked ? 'text-[var(--text-secondary)]' : 'text-gray-300'}`}>必填</span>
                                            </div>
                                        </div>
                                    </label>
                                );
                            })}
                        </div>
                        <p className="text-xs text-[var(--text-secondary)] mt-2">未配置时，Playground 会走基础 prompt 兜底输入。</p>
                    </div>
                    <div>
                        <div className="flex items-center justify-between mb-2">
                            <label className="block text-sm font-medium text-[var(--text-primary)]">自定义参数</label>
                            <button type="button" onClick={() => setCustomParams(prev => [...prev, {key: '', name: '', type: 'string', options: '', default: '', required: false}])}
                                className="text-xs text-[var(--primary)] hover:text-[var(--primary)]">+ 添加参数</button>
                        </div>
                        {customParams.length > 0 && (
                            <div className="space-y-2">
                                {customParams.map((cp, i) => (
                                    <div key={i} className="rounded-lg border border-[var(--border-soft)] bg-[var(--surface)] p-3 space-y-2">
                                        <div className="grid grid-cols-3 gap-2">
                                            <input type="text" value={cp.key} placeholder="字段名 (key)"
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, key: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-[var(--border-soft)] rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                                            <input type="text" value={cp.name} placeholder="显示名称"
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, name: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-[var(--border-soft)] rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                                            <select value={cp.type}
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, type: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-[var(--border-soft)] rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                {PARAM_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
                                            </select>
                                        </div>
                                        <div className="grid grid-cols-2 gap-2">
                                            <input type="text" value={cp.options} placeholder="可选值 (逗号分隔)"
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, options: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-[var(--border-soft)] rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                                            <input type="text" value={cp.default} placeholder="默认值"
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, default: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-[var(--border-soft)] rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                                        </div>
                                        <div className="flex items-center justify-between">
                                            <label className="flex items-center gap-1.5 text-xs text-[var(--text-secondary)]">
                                                <input type="checkbox" checked={cp.required}
                                                    onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, required: e.target.checked} : p))}
                                                    className="rounded border-gray-300 text-[var(--primary)] focus:ring-[var(--primary)]" />
                                                必填
                                            </label>
                                            <button type="button" onClick={() => setCustomParams(prev => prev.filter((_, j) => j !== i))}
                                                className="text-xs text-red-500 hover:text-red-600">删除</button>
                                        </div>
                                    </div>
                                ))}
                            </div>
                        )}
                        {customParams.length === 0 && (
                            <p className="text-xs text-[var(--text-secondary)]">预设列表没有的参数可在此添加（如 output_compression）</p>
                        )}
                    </div>
                    <div className="flex justify-end gap-3 pt-4">
                        <button type="button" onClick={onClose}
                                className="px-4 py-2 text-sm font-bold text-[var(--text-secondary)] bg-[var(--primary-lighter)] rounded-lg hover:bg-gray-200 transition-colors">取消
                        </button>
                        <button type="submit" disabled={loading}
                                className="px-4 py-2 text-sm font-bold text-white bg-[var(--primary)] rounded-lg hover:opacity-90 disabled:opacity-50 transition-colors">
                            {loading ? '保存中...' : '保存'}
                        </button>
                    </div>
                </form>
        </Modal>
    );
};

export default CapabilityModal;
