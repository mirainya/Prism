import React, { useEffect, useState } from 'react';
import { Modal } from '../../components/ui/Modal';
import { Select } from '../../components/ui';
import JsonEditor from '../../components/ui/JsonEditor';
import { createCapability, updateCapability } from '../../services/api';
import { Capability } from '../../types';
import { STANDARD_PARAMS, CAPABILITY_TYPES } from './constants';

const CapabilityModal: React.FC<{
    isOpen: boolean;
    capability: Capability | null;
    onClose: () => void;
    onSave: () => void;
}> = ({isOpen, capability, onClose, onSave}) => {
    const [form, setForm] = useState({ code: '', name: '', type: 'image', description: '', status: 1 });
    const [aliasesText, setAliasesText] = useState('');
    const [paramJson, setParamJson] = useState('{}');
    const [jsonError, setJsonError] = useState('');
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        if (capability) {
            setForm({ code: capability.code, name: capability.name, type: capability.type || 'image', description: capability.description || '', status: capability.status });
            setAliasesText((capability.aliases || []).join('\n'));
            setParamJson(Object.keys(capability.standardParams || {}).length > 0
                ? JSON.stringify(capability.standardParams, null, 2) : '{}');
        } else {
            setForm({code: '', name: '', type: 'image', description: '', status: 1});
            setAliasesText('');
            setParamJson('{}');
        }
        setJsonError('');
    }, [capability, isOpen]);

    const validateJson = (text: string) => {
        try { JSON.parse(text); setJsonError(''); return true; }
        catch (e: any) { setJsonError(e.message); return false; }
    };

    const insertParam = (key: string) => {
        const def = STANDARD_PARAMS[key];
        if (!def) return;
        try {
            const current = JSON.parse(paramJson);
            if (current[key]) return;
            const entry: Record<string, any> = { type: def.type, name: def.name };
            if (def.options?.length) entry.options = def.options;
            current[key] = entry;
            const newJson = JSON.stringify(current, null, 2);
            setParamJson(newJson);
            setJsonError('');
        } catch {}
    };

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!validateJson(paramJson)) return;
        setLoading(true);
        try {
            const standard_params = JSON.parse(paramJson);
            const aliases = Array.from(new Set(aliasesText.split(/[\n,]/).map(alias => alias.trim()).filter(Boolean)));
            if (capability) {
                await updateCapability(capability.code, { code: form.code.trim(), name: form.name, type: form.type, description: form.description, status: form.status, aliases, standard_params });
            } else {
                await createCapability({ code: form.code, name: form.name, type: form.type, description: form.description, aliases, standard_params });
            }
            onSave();
            onClose();
        } finally {
            setLoading(false);
        }
    };

    const paramGroups = Object.entries(STANDARD_PARAMS).reduce<Record<string, [string, typeof STANDARD_PARAMS[string]][]>>((acc, [key, def]) => {
        const group = def.group || '其他';
        (acc[group] ||= []).push([key, def]);
        return acc;
    }, {});

    return (
        <Modal open={isOpen} onClose={onClose} title={capability ? '编辑能力' : '新建能力'} width="max-w-2xl" panelClassName="capability-modal-panel">
                <form onSubmit={handleSubmit} className="capability-modal-form">
                  <div className="capability-modal-body space-y-4">
                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                        <div>
                            <label className="capability-field-label block text-sm font-medium mb-1">能力编码 *</label>
                            <input type="text" value={form.code} onChange={e => setForm({...form, code: e.target.value})}
                                className="capability-field w-full px-3 py-2 rounded-lg"
                                placeholder="如: gpt_image2" required />
                        </div>
                        <div>
                            <label className="capability-field-label block text-sm font-medium mb-1">能力名称 *</label>
                            <input type="text" value={form.name} onChange={e => setForm({...form, name: e.target.value})}
                                className="capability-field w-full px-3 py-2 rounded-lg"
                                placeholder="如: ChatGPT生图" required />
                        </div>
                    </div>
                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                        <div>
                            <label className="capability-field-label block text-sm font-medium mb-1">类型 *</label>
                            <Select value={form.type} onChange={v => setForm({...form, type: v})}
                                className="capability-select"
                                options={CAPABILITY_TYPES.filter(t => t.value !== 'video').map(t => ({ label: t.label, value: t.value }))} />
                        </div>
                        <div>
                            <label className="capability-field-label block text-sm font-medium mb-1">描述</label>
                            <input type="text" value={form.description} onChange={e => setForm({...form, description: e.target.value})}
                                className="capability-field w-full px-3 py-2 rounded-lg"
                                placeholder="能力描述" />
                        </div>
                    </div>

                    <div>
                        <label className="capability-field-label block text-sm font-medium mb-1">模型别名</label>
                        <textarea value={aliasesText} onChange={e => setAliasesText(e.target.value)} rows={3}
                            className="capability-field capability-textarea w-full px-3 py-2 rounded-lg"
                            placeholder="每行一个上游模型名" />
                        <p className="capability-field-help mt-1 text-xs">请求中的模型名会按精确匹配后，再查找这里配置的别名。</p>
                    </div>

                    <div>
                        <div className="flex items-center justify-between mb-2">
                            <label className="capability-field-label block text-sm font-medium">参数 Schema (JSON)</label>
                            {jsonError && <span className="text-xs text-red-500">{jsonError}</span>}
                        </div>
                        <JsonEditor value={paramJson} onChange={v => { setParamJson(v); validateJson(v); }}
                            height="240px" className="capability-json-editor" placeholder='{"prompt": {"type": "string", "name": "描述", "required": true}}' />
                        <div className="mt-2">
                            <p className="capability-field-help mb-1.5 text-xs">快速插入预设参数：</p>
                            <div className="space-y-1.5">
                                {Object.entries(paramGroups).map(([group, items]) => (
                                    <div key={group} className="flex items-center gap-1.5 flex-wrap">
                                        <span className="capability-chip-group text-[10px] w-12 shrink-0">{group}</span>
                                        {items.map(([key, def]) => (
                                            <button key={key} type="button" onClick={() => insertParam(key)}
                                                className="capability-param-chip rounded px-2 py-0.5 text-[11px] transition-colors">
                                                {def.name}
                                            </button>
                                        ))}
                                    </div>
                                ))}
                            </div>
                        </div>
                    </div>
                  </div>

                    <div className="capability-modal-footer flex justify-end gap-3 pt-4">
                        <button type="button" onClick={onClose}
                                className="capability-secondary-button rounded-lg px-4 py-2 text-sm font-bold transition-colors">取消</button>
                        <button type="submit" disabled={loading || !!jsonError}
                                className="capability-primary-button rounded-lg px-4 py-2 text-sm font-bold text-white transition-colors disabled:opacity-50">
                            {loading ? '保存中...' : '保存'}
                        </button>
                    </div>
                </form>
        </Modal>
    );
};

export default CapabilityModal;
