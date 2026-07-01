import React, { useState } from 'react';
import { ThinkingConfig, ThinkingOption } from '../../types';

// 各 provider 预设模板:body 已按协议标准写法填好,一键套用后可再改
const PRESETS: Record<string, ThinkingConfig> = {
    volcengine: {
        locked: false,
        default: 'default',
        options: [
            { label: '厂商默认', value: 'default', body: {} },
            { label: '关闭', value: 'off', body: { reasoning: { effort: 'minimal' } } },
            { label: '轻量', value: 'low', body: { reasoning: { effort: 'low' } } },
            { label: '均衡', value: 'medium', body: { reasoning: { effort: 'medium' } } },
            { label: '深度', value: 'high', body: { reasoning: { effort: 'high' } } },
            { label: '最高', value: 'max', body: { reasoning: { effort: 'max' } } },
        ],
    },
    openai: {
        locked: false,
        default: 'default',
        options: [
            { label: '厂商默认', value: 'default', body: {} },
            { label: '关闭', value: 'off', body: { reasoning_effort: 'minimal' } },
            { label: '轻量', value: 'low', body: { reasoning_effort: 'low' } },
            { label: '均衡', value: 'medium', body: { reasoning_effort: 'medium' } },
            { label: '深度', value: 'high', body: { reasoning_effort: 'high' } },
        ],
    },
    anthropic: {
        locked: false,
        default: 'default',
        options: [
            { label: '厂商默认', value: 'default', body: {} },
            { label: '轻量', value: 'low', body: { thinking: { type: 'enabled', budget_tokens: 2048 } } },
            { label: '均衡', value: 'medium', body: { thinking: { type: 'enabled', budget_tokens: 8192 } } },
            { label: '深度', value: 'high', body: { thinking: { type: 'enabled', budget_tokens: 16384 } } },
        ],
    },
    google: {
        locked: false,
        default: 'default',
        options: [
            { label: '厂商默认', value: 'default', body: {} },
            { label: '关闭', value: 'off', body: { reasoning_effort: 'none' } },
            { label: '开启', value: 'high', body: { reasoning_effort: 'high' } },
        ],
    },
};

interface Props {
    value: ThinkingConfig | null;
    onChange: (cfg: ThinkingConfig | null) => void;
    provider: string;
}

const ThinkingConfigEditor: React.FC<Props> = ({ value, onChange, provider }) => {
    // 每档 body 的 JSON 文本缓存(允许编辑中的非法中间态)
    const [bodyText, setBodyText] = useState<Record<number, string>>({});
    const [bodyErr, setBodyErr] = useState<Record<number, boolean>>({});

    const enabled = value != null;

    const toggle = (on: boolean) => {
        if (on) {
            onChange(PRESETS[provider] || { locked: false, default: 'default', options: [{ label: '厂商默认', value: 'default', body: {} }] });
        } else {
            onChange(null);
        }
    };

    const applyPreset = (p: string) => {
        if (PRESETS[p]) {
            onChange(JSON.parse(JSON.stringify(PRESETS[p])));
            setBodyText({});
            setBodyErr({});
        }
    };

    const patch = (partial: Partial<ThinkingConfig>) => {
        if (value) onChange({ ...value, ...partial });
    };

    const updateOption = (i: number, patch: Partial<ThinkingOption>) => {
        if (!value) return;
        const opts = value.options.map((o, idx) => (idx === i ? { ...o, ...patch } : o));
        onChange({ ...value, options: opts });
    };

    const removeOption = (i: number) => {
        if (!value) return;
        onChange({ ...value, options: value.options.filter((_, idx) => idx !== i) });
    };

    const addOption = () => {
        if (!value) return;
        onChange({ ...value, options: [...value.options, { label: '', value: '', body: {} }] });
    };

    return (
        <div className="space-y-2">
            <label className="inline-flex items-center gap-1.5 text-sm text-[var(--text-primary)] cursor-pointer">
                <input type="checkbox" checked={enabled} onChange={e => toggle(e.target.checked)}
                    className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded" />
                启用思考模式配置
            </label>

            {enabled && value && (
                <div className="space-y-3 border border-[var(--border-soft)] rounded-lg p-3 bg-[var(--surface)]">
                    <div className="flex items-center gap-3 flex-wrap">
                        <select onChange={e => e.target.value && applyPreset(e.target.value)} value=""
                            className="px-2 py-1 text-sm border border-[var(--border-soft)] rounded bg-[var(--surface)]">
                            <option value="">套用预设模板...</option>
                            {Object.keys(PRESETS).map(p => <option key={p} value={p}>{p}</option>)}
                        </select>
                        <label className="inline-flex items-center gap-1.5 text-sm cursor-pointer">
                            <input type="checkbox" checked={!!value.locked}
                                onChange={e => patch({ locked: e.target.checked })}
                                className="h-4 w-4 rounded" />
                            锁定(禁止会话/请求覆盖)
                        </label>
                        <label className="inline-flex items-center gap-1.5 text-sm">
                            默认档位
                            <select value={value.default || ''} onChange={e => patch({ default: e.target.value })}
                                className="px-2 py-1 text-sm border border-[var(--border-soft)] rounded bg-[var(--surface)]">
                                {value.options.map((o, i) => <option key={i} value={o.value}>{o.label || o.value}</option>)}
                            </select>
                        </label>
                    </div>

                    <div className="space-y-2">
                        {value.options.map((opt, i) => (
                            <div key={i} className="flex items-start gap-2 border-t border-[var(--border-soft)] pt-2">
                                <div className="flex flex-col gap-1 w-28 shrink-0">
                                    <input value={opt.label} placeholder="名称" onChange={e => updateOption(i, { label: e.target.value })}
                                        className="px-2 py-1 text-sm border border-[var(--border-soft)] rounded bg-[var(--surface)]" />
                                    <input value={opt.value} placeholder="value" onChange={e => updateOption(i, { value: e.target.value })}
                                        className="px-2 py-1 text-xs border border-[var(--border-soft)] rounded bg-[var(--surface)]" />
                                </div>
                                <div className="flex-1">
                                    <textarea rows={2}
                                        value={bodyText[i] ?? JSON.stringify(opt.body || {})}
                                        onChange={e => {
                                            setBodyText({ ...bodyText, [i]: e.target.value });
                                            try {
                                                const parsed = e.target.value.trim() ? JSON.parse(e.target.value) : {};
                                                updateOption(i, { body: parsed });
                                                setBodyErr({ ...bodyErr, [i]: false });
                                            } catch {
                                                setBodyErr({ ...bodyErr, [i]: true });
                                            }
                                        }}
                                        className={`w-full px-2 py-1 text-xs font-mono border rounded bg-[var(--surface)] ${bodyErr[i] ? 'border-red-400' : 'border-[var(--border-soft)]'}`}
                                        placeholder='body JSON,空 {} =不注入' />
                                    {bodyErr[i] && <span className="text-xs text-red-500">JSON 格式错误</span>}
                                </div>
                                <button type="button" onClick={() => removeOption(i)}
                                    className="text-xs text-red-500 hover:underline shrink-0 pt-1">删除</button>
                            </div>
                        ))}
                        <button type="button" onClick={addOption}
                            className="text-sm text-[var(--primary)] hover:underline">+ 添加档位</button>
                    </div>
                </div>
            )}
        </div>
    );
};

export default ThinkingConfigEditor;
export { PRESETS };
