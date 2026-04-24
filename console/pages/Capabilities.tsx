import React, { useEffect, useMemo, useState } from 'react';
import {Plus, Settings, Cpu, Edit2, Trash2, ChevronDown, ChevronRight, X, Power, RefreshCw, Search} from 'lucide-react';
import {
    fetchCapabilities, fetchChannelCapabilities, fetchChannels,
    createCapability, updateCapability, deleteCapability,
    createChannelCapability, updateChannelCapability, deleteChannelCapability
} from '../services/api';
import { Capability, ChannelCapability, Channel, CapabilityStandardParamSchema } from '../types';

const RESULT_MODES = [
    {value: 'sync', label: '同步'},
    {value: 'poll', label: '轮询'},
    {value: 'callback', label: '回调'},
];

// 系统标准参数字段定义
const STANDARD_PARAMS: Record<string, { name: string; type: string; group?: string; options?: string[] }> = {
    // 通用
    prompt: {name: '提示词', type: 'string', group: '通用'},
    negative_prompt: {name: '负向提示词', type: 'string', group: '通用'},
    callback_url: {name: '回调地址', type: 'string', group: '通用'},
    // 图片尺寸
    aspect_ratio: {name: '宽高比', type: 'enum', group: '尺寸', options: ['1:1', '16:9', '9:16', '4:3', '3:4', '3:2', '2:3']},
    width: {name: '宽度', type: 'number', group: '尺寸'},
    height: {name: '高度', type: 'number', group: '尺寸'},
    size: {name: '尺寸', type: 'enum', group: '尺寸', options: ['1024x1024', '1536x1024', '1024x1536', '1792x1024', '1024x1792', 'auto']},
    image_size: {name: '分辨率', type: 'enum', group: '尺寸', options: ['1K', '2K', '4K']},
    // 生成控制
    seed: {name: '随机种子', type: 'number', group: '生成控制'},
    steps: {name: '生成步数', type: 'number', group: '生成控制'},
    cfg_scale: {name: 'CFG强度', type: 'number', group: '生成控制'},
    strength: {name: '变化强度', type: 'number', group: '生成控制'},
    n: {name: '生成数量', type: 'number', group: '生成控制'},
    quality: {name: '图片质量', type: 'enum', group: '生成控制', options: ['auto', 'high', 'medium', 'low']},
    style: {name: '风格', type: 'enum', group: '生成控制', options: ['realistic', 'anime', 'cartoon']},
    // 输出格式
    response_format: {name: '响应格式', type: 'enum', group: '输出', options: ['url', 'b64_json']},
    output_format: {name: '输出格式', type: 'enum', group: '输出', options: ['png', 'jpeg', 'webp']},
    background: {name: '背景', type: 'enum', group: '输出', options: ['auto', 'transparent', 'opaque']},
    // 输入
    image_urls: {name: '图片URL列表', type: 'array', group: '输入'},
    // 视频
    seconds: {name: '时长(秒)', type: 'number', group: '视频'},
    fps: {name: '帧率', type: 'enum', group: '视频', options: ['24', '30', '60']},
};

// 系统标准响应字段定义
const STANDARD_RESPONSE: Record<string, { name: string; type: string; enumValues?: string[] }> = {
    task_id: {name: '任务ID', type: 'string'},
    status: {name: '状态', type: 'enum', enumValues: ['pending', 'processing', 'success', 'failed', 'cancelled']},
    progress: {name: '进度', type: 'number'},
    url: {name: '结果URL', type: 'string'},
    urls: {name: '结果URL列表', type: 'array'},
    data: {name: '结果数据', type: 'string'},
    error: {name: '错误信息', type: 'string'},
};

// 轮询请求参数字段定义
const POLL_PARAMS: Record<string, { name: string; type: string }> = {
    task_id: {name: '任务ID', type: 'string'},
};

// 系统标准状态值
const STANDARD_STATUS_VALUES = ['pending', 'processing', 'success', 'failed', 'cancelled'];

// 字段映射行组件
const FieldMappingRow: React.FC<{
    stdField: string;
    stdName: string;
    vendorField: string;
    onChange: (value: string) => void;
    onRemove: () => void;
}> = ({stdField, stdName, vendorField, onChange, onRemove}) => (
    <div className="flex items-center gap-2 mb-2">
        <div className="flex-1 px-3 py-2 bg-gray-50 rounded-lg text-sm">
            <span className="text-gray-600">{stdName}</span>
            <code className="ml-2 text-xs text-gray-400">{stdField}</code>
        </div>
        <span className="text-gray-400">→</span>
        <input
            type="text"
            value={vendorField}
            onChange={e => onChange(e.target.value)}
            className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            placeholder="三方字段名或路径"
        />
        <button type="button" onClick={onRemove}
                className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
            <X size={14}/>
        </button>
    </div>
);

// 值映射行组件
const ValueMappingRow: React.FC<{
    stdValue: string;
    vendorValue: string;
    onChange: (value: string) => void;
    onRemove: () => void;
}> = ({stdValue, vendorValue, onChange, onRemove}) => (
    <div className="flex items-center gap-2 mb-2">
        <div className="w-32 px-3 py-2 bg-gray-50 rounded-lg text-sm text-gray-600">{stdValue}</div>
        <span className="text-gray-400">→</span>
        <input
            type="text"
            value={vendorValue}
            onChange={e => onChange(e.target.value)}
            className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            placeholder="三方对应值"
        />
        <button type="button" onClick={onRemove}
                className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
            <X size={14}/>
        </button>
    </div>
);

// 固定参数行组件
const FixedParamRow: React.FC<{
    paramName: string;
    paramValue: string;
    onNameChange: (value: string) => void;
    onValueChange: (value: string) => void;
    onRemove: () => void;
}> = ({paramName, paramValue, onNameChange, onValueChange, onRemove}) => (
    <div className="flex items-center gap-2 mb-2">
        <input
            type="text"
            value={paramName}
            onChange={e => onNameChange(e.target.value)}
            className="w-40 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            placeholder="参数名"
        />
        <span className="text-gray-400">=</span>
        <input
            type="text"
            value={paramValue}
            onChange={e => onValueChange(e.target.value)}
            className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
            placeholder="固定值"
        />
        <button type="button" onClick={onRemove}
                className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
            <X size={14}/>
        </button>
    </div>
);

// 能力编辑弹窗
const CAPABILITY_TYPES = [
    {value: 'image', label: '图片'},
    {value: 'video', label: '视频'},
    {value: 'chat', label: '对话'},
    {value: 'other', label: '其他'},
];

const CAPABILITY_TYPE_ORDER = ['image', 'video', 'chat', 'other'] as const;

const normalizeText = (value: string | undefined | null) => (value || '').toLowerCase();

const getCapabilityTypeLabel = (type?: string) => {
    switch (type) {
        case 'image':
            return '图片';
        case 'video':
            return '视频';
        case 'chat':
            return '对话';
        default:
            return '其他';
    }
};

const getCapabilityTypeBadgeClass = (type?: string) => {
    switch (type) {
        case 'image':
            return 'bg-pink-100 text-pink-700';
        case 'video':
            return 'bg-violet-100 text-violet-700';
        case 'chat':
            return 'bg-sky-100 text-sky-700';
        default:
            return 'bg-gray-100 text-gray-700';
    }
};

const formatPrice = (price?: number) => {
    const numeric = Number(price);
    return Number.isFinite(numeric) ? numeric.toString() : '0';
};

interface CustomParam {
    key: string;
    name: string;
    type: string;
    options: string;
    default: string;
    required: boolean;
}

const PARAM_TYPES = ['string', 'number', 'enum', 'array'];

const CapabilityModal: React.FC<{
    isOpen: boolean;
    capability: Capability | null;
    onClose: () => void;
    onSave: () => void;
}> = ({isOpen, capability, onClose, onSave}) => {
    const [form, setForm] = useState({
        code: '',
        name: '',
        type: 'image',
        description: '',
        status: 1,
    });
    const [selectedStandardParams, setSelectedStandardParams] = useState<string[]>([]);
    const [requiredStandardParams, setRequiredStandardParams] = useState<string[]>([]);
    const [customParams, setCustomParams] = useState<CustomParam[]>([]);
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        if (capability) {
            setForm({
                code: capability.code,
                name: capability.name,
                type: capability.type || 'image',
                description: capability.description || '',
                status: capability.status,
            });
            const allParams = capability.standardParams || {};
            const presetKeys: string[] = [];
            const reqKeys: string[] = [];
            const customs: CustomParam[] = [];
            for (const [key, schema] of Object.entries(allParams) as [string, CapabilityStandardParamSchema][]) {
                if (key in STANDARD_PARAMS) {
                    presetKeys.push(key);
                    if (schema.required) reqKeys.push(key);
                } else {
                    customs.push({
                        key,
                        name: schema.name || key,
                        type: schema.type || 'string',
                        options: (schema.options || schema.enumValues || []).join(', '),
                        default: schema.default != null ? String(schema.default) : '',
                        required: !!schema.required,
                    });
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
            result[key] = {
                type: def.type,
                name: def.name,
                required: requiredStandardParams.includes(key),
                ...(def.options ? {options: def.options} : {}),
            };
        }
        for (const cp of customParams) {
            if (!cp.key.trim()) continue;
            const entry: Record<string, any> = {
                type: cp.type,
                name: cp.name || cp.key,
                required: cp.required,
            };
            const opts = cp.options.split(/[,，]/).map(s => s.trim()).filter(Boolean);
            if (opts.length > 0) entry.options = opts;
            if (cp.default.trim()) {
                entry.default = cp.type === 'number' ? Number(cp.default) : cp.default;
            }
            result[cp.key.trim()] = entry;
        }
        return result;
    };

    const toggleStandardParam = (key: string, checked: boolean) => {
        if (checked) {
            setSelectedStandardParams(prev => prev.includes(key) ? prev : [...prev, key]);
            return;
        }
        setSelectedStandardParams(prev => prev.filter(item => item !== key));
        setRequiredStandardParams(prev => prev.filter(item => item !== key));
    };

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setLoading(true);
        try {
            const standard_params = buildStandardParams();
            if (capability) {
                await updateCapability(capability.code, {
                    code: form.code.trim(),
                    name: form.name,
                    type: form.type,
                    description: form.description,
                    status: form.status,
                    standard_params,
                });
            } else {
                await createCapability({
                    code: form.code,
                    name: form.name,
                    type: form.type,
                    description: form.description,
                    standard_params,
                });
            }
            onSave();
            onClose();
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
            <div className="bg-white rounded-2xl w-full max-w-lg p-6 shadow-xl max-h-[90vh] overflow-hidden flex flex-col">
                <div className="flex items-center justify-between mb-6">
                    <h3 className="text-lg font-bold text-gray-900">{capability ? '编辑能力' : '新建能力'}</h3>
                    <button onClick={onClose} className="p-2 hover:bg-gray-100 rounded-lg"><X size={20}/></button>
                </div>
                <form onSubmit={handleSubmit} className="space-y-4 flex-1 overflow-y-auto">
                    <div>
                        <label className="block text-sm font-medium text-gray-700 mb-1">能力编码 <span
                            className="text-red-500">*</span></label>
                        <input
                            type="text"
                            value={form.code}
                            onChange={e => setForm({...form, code: e.target.value})}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            placeholder="如: text2img, img2video"
                            required
                        />
                        <p className="text-xs text-gray-500 mt-1">唯一标识，修改后会同步更新相关引用数据</p>
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-gray-700 mb-1">能力名称 <span
                            className="text-red-500">*</span></label>
                        <input
                            type="text"
                            value={form.name}
                            onChange={e => setForm({...form, name: e.target.value})}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            placeholder="如: 文生图, 图生视频"
                            required
                        />
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-gray-700 mb-1">能力类型 <span
                            className="text-red-500">*</span></label>
                        <select
                            value={form.type}
                            onChange={e => setForm({...form, type: e.target.value})}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                        >
                            {CAPABILITY_TYPES.map(t => (
                                <option key={t.value} value={t.value}>{t.label}</option>
                            ))}
                        </select>
                    </div>
                    <div>
                        <label className="block text-sm font-medium text-gray-700 mb-1">描述</label>
                        <textarea
                            value={form.description}
                            onChange={e => setForm({...form, description: e.target.value})}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                            placeholder="能力的详细描述..."
                            rows={3}
                        />
                    </div>
                    <div>
                        <div className="flex items-center justify-between mb-2">
                            <label className="block text-sm font-medium text-gray-700">标准参数</label>
                            <span className="text-xs text-gray-400">供 Playground / API 统一输入使用</span>
                        </div>
                        <div className="max-h-64 overflow-y-auto rounded-xl border border-gray-200 divide-y divide-gray-100 bg-gray-50">
                            {Object.entries(STANDARD_PARAMS).map(([key, def]) => {
                                const checked = selectedStandardParams.includes(key);
                                const required = requiredStandardParams.includes(key);
                                return (
                                    <label key={key} className="flex items-start gap-3 px-3 py-3 hover:bg-white transition-colors cursor-pointer">
                                        <input
                                            type="checkbox"
                                            checked={checked}
                                            onChange={e => toggleStandardParam(key, e.target.checked)}
                                            className="mt-1 rounded border-gray-300 text-indigo-600 focus:ring-indigo-500"
                                        />
                                        <div className="flex-1 min-w-0">
                                            <div className="flex items-center gap-2 flex-wrap">
                                                <span className="text-sm font-medium text-gray-800">{def.name}</span>
                                                <code className="text-xs text-gray-400">{key}</code>
                                                <span className="px-1.5 py-0.5 rounded bg-white text-[10px] text-gray-500 border border-gray-200">{def.type}</span>
                                            </div>
                                            {def.options?.length ? (
                                                <div className="mt-1 text-xs text-gray-500">可选值：{def.options.join(' / ')}</div>
                                            ) : null}
                                            <div className="mt-2 flex items-center gap-2">
                                                <input
                                                    type="checkbox"
                                                    checked={required}
                                                    disabled={!checked}
                                                    onChange={e => {
                                                        if (e.target.checked) {
                                                            setRequiredStandardParams(prev => prev.includes(key) ? prev : [...prev, key]);
                                                        } else {
                                                            setRequiredStandardParams(prev => prev.filter(item => item !== key));
                                                        }
                                                    }}
                                                    className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500"
                                                />
                                                <span className={`text-xs ${checked ? 'text-gray-600' : 'text-gray-300'}`}>必填</span>
                                            </div>
                                        </div>
                                    </label>
                                );
                            })}
                        </div>
                        <p className="text-xs text-gray-500 mt-2">未配置时，Playground 会走基础 prompt 兜底输入。</p>
                    </div>
                    <div>
                        <div className="flex items-center justify-between mb-2">
                            <label className="block text-sm font-medium text-gray-700">自定义参数</label>
                            <button type="button" onClick={() => setCustomParams(prev => [...prev, {key: '', name: '', type: 'string', options: '', default: '', required: false}])}
                                className="text-xs text-indigo-600 hover:text-indigo-700">+ 添加参数</button>
                        </div>
                        {customParams.length > 0 && (
                            <div className="space-y-2">
                                {customParams.map((cp, i) => (
                                    <div key={i} className="rounded-lg border border-gray-200 bg-gray-50 p-3 space-y-2">
                                        <div className="grid grid-cols-3 gap-2">
                                            <input type="text" value={cp.key} placeholder="字段名 (key)"
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, key: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-gray-200 rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                                            <input type="text" value={cp.name} placeholder="显示名称"
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, name: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-gray-200 rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                                            <select value={cp.type}
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, type: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-gray-200 rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500">
                                                {PARAM_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
                                            </select>
                                        </div>
                                        <div className="grid grid-cols-2 gap-2">
                                            <input type="text" value={cp.options} placeholder="可选值 (逗号分隔)"
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, options: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-gray-200 rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                                            <input type="text" value={cp.default} placeholder="默认值"
                                                onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, default: e.target.value} : p))}
                                                className="px-2 py-1.5 border border-gray-200 rounded-lg text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500" />
                                        </div>
                                        <div className="flex items-center justify-between">
                                            <label className="flex items-center gap-1.5 text-xs text-gray-600">
                                                <input type="checkbox" checked={cp.required}
                                                    onChange={e => setCustomParams(prev => prev.map((p, j) => j === i ? {...p, required: e.target.checked} : p))}
                                                    className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500" />
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
                            <p className="text-xs text-gray-400">预设列表没有的参数可在此添加（如 output_compression）</p>
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

// 映射配置类型
interface FieldMapping {
    stdField: string;
    vendorField: string
}

interface ValueMapping {
    field: string;
    stdValue: string;
    vendorValue: string
}

interface FixedParam {
    name: string;
    value: string
}

interface TypeConvert {
    field: string;
    type: 'string_to_array' | 'array_to_string';
    separator: string;
}

// 成功条件配置
interface SuccessCondition {
    field: string;
    operator: 'eq' | 'ne' | 'exists' | 'not_exists' | 'in' | 'not_in' | 'gt' | 'gte' | 'lt' | 'lte';
    value?: string | number | boolean;
    values?: (string | number)[];
}

// 成功条件操作符选项
const SUCCESS_CONDITION_OPERATORS = [
    {value: 'eq', label: '等于', needValue: true, needValues: false},
    {value: 'ne', label: '不等于', needValue: true, needValues: false},
    {value: 'exists', label: '存在', needValue: false, needValues: false},
    {value: 'not_exists', label: '不存在', needValue: false, needValues: false},
    {value: 'in', label: '在列表中', needValue: false, needValues: true},
    {value: 'not_in', label: '不在列表中', needValue: false, needValues: true},
    {value: 'gt', label: '大于', needValue: true, needValues: false},
    {value: 'gte', label: '大于等于', needValue: true, needValues: false},
    {value: 'lt', label: '小于', needValue: true, needValues: false},
    {value: 'lte', label: '小于等于', needValue: true, needValues: false},
];

// 解析 JSON 映射为表单数据
const parseParamMapping = (mapping: Record<string, any>) => {
    const fieldMappings: FieldMapping[] = [];
    const valueMappings: ValueMapping[] = [];
    const fixedParams: FixedParam[] = [];
    const typeConverts: TypeConvert[] = [];

    if (mapping.field_mapping) {
        Object.entries(mapping.field_mapping).forEach(([std, vendor]) => {
            fieldMappings.push({stdField: std, vendorField: vendor as string});
        });
    }
    if (mapping.value_mapping) {
        Object.entries(mapping.value_mapping).forEach(([field, values]) => {
            Object.entries(values as Record<string, string>).forEach(([stdVal, vendorVal]) => {
                valueMappings.push({field, stdValue: stdVal, vendorValue: vendorVal});
            });
        });
    }
    if (mapping.fixed_params) {
        Object.entries(mapping.fixed_params).forEach(([name, value]) => {
            fixedParams.push({name, value: String(value)});
        });
    }
    if (mapping.type_convert) {
        Object.entries(mapping.type_convert).forEach(([field, config]) => {
            const c = config as { type: string; separator: string };
            typeConverts.push({
                field,
                type: c.type as 'string_to_array' | 'array_to_string',
                separator: c.separator || ','
            });
        });
    }

    return {fieldMappings, valueMappings, fixedParams, typeConverts};
};

const parseResponseMapping = (mapping: Record<string, any>) => {
    const fieldMappings: FieldMapping[] = [];
    const valueMappings: ValueMapping[] = [];
    const typeConverts: TypeConvert[] = [];
    let successCondition: SuccessCondition | null = null;

    if (mapping.field_mapping) {
        Object.entries(mapping.field_mapping).forEach(([std, vendor]) => {
            fieldMappings.push({stdField: std, vendorField: vendor as string});
        });
    }
    if (mapping.value_mapping) {
        Object.entries(mapping.value_mapping).forEach(([field, values]) => {
            Object.entries(values as Record<string, string>).forEach(([vendorVal, stdVal]) => {
                valueMappings.push({field, stdValue: stdVal, vendorValue: vendorVal});
            });
        });
    }
    if (mapping.type_convert) {
        Object.entries(mapping.type_convert).forEach(([field, config]) => {
            const c = config as { type: string; separator: string };
            typeConverts.push({
                field,
                type: c.type as 'string_to_array' | 'array_to_string',
                separator: c.separator || ','
            });
        });
    }
    if (mapping.success_condition) {
        successCondition = mapping.success_condition as SuccessCondition;
    }

    return {fieldMappings, valueMappings, typeConverts, successCondition};
};

// 构建 JSON 映射
const buildParamMapping = (fieldMappings: FieldMapping[], valueMappings: ValueMapping[], fixedParams: FixedParam[], typeConverts: TypeConvert[] = []) => {
    const result: Record<string, any> = {};

    const fieldMap: Record<string, string> = {};
    fieldMappings.forEach(m => {
        if (m.stdField && m.vendorField) fieldMap[m.stdField] = m.vendorField;
    });
    if (Object.keys(fieldMap).length > 0) result.field_mapping = fieldMap;

    const valueMap: Record<string, Record<string, string>> = {};
    valueMappings.forEach(m => {
        if (m.field && m.stdValue && m.vendorValue) {
            if (!valueMap[m.field]) valueMap[m.field] = {};
            valueMap[m.field][m.stdValue] = m.vendorValue;
        }
    });
    if (Object.keys(valueMap).length > 0) result.value_mapping = valueMap;

    // 类型转换
    const typeConvertMap: Record<string, { type: string; separator: string }> = {};
    typeConverts.forEach(tc => {
        if (tc.field && tc.type) {
            typeConvertMap[tc.field] = {type: tc.type, separator: tc.separator || ','};
        }
    });
    if (Object.keys(typeConvertMap).length > 0) result.type_convert = typeConvertMap;

    const fixed: Record<string, any> = {};
    fixedParams.forEach(p => {
        if (p.name && p.value) {
            // 尝试解析为数字或布尔值
            if (p.value === 'true') fixed[p.name] = true;
            else if (p.value === 'false') fixed[p.name] = false;
            else if (!isNaN(Number(p.value))) fixed[p.name] = Number(p.value);
            else fixed[p.name] = p.value;
        }
    });
    if (Object.keys(fixed).length > 0) result.fixed_params = fixed;

    return result;
};

const buildResponseMapping = (fieldMappings: FieldMapping[], valueMappings: ValueMapping[], typeConverts: TypeConvert[] = [], successCondition: SuccessCondition | null = null) => {
    const result: Record<string, any> = {};

    const fieldMap: Record<string, string> = {};
    fieldMappings.forEach(m => {
        if (m.stdField && m.vendorField) fieldMap[m.stdField] = m.vendorField;
    });
    if (Object.keys(fieldMap).length > 0) result.field_mapping = fieldMap;

    // 响应映射的 value_mapping 是 { 三方值: 标准值 }
    const valueMap: Record<string, Record<string, string>> = {};
    valueMappings.forEach(m => {
        if (m.field && m.stdValue && m.vendorValue) {
            if (!valueMap[m.field]) valueMap[m.field] = {};
            valueMap[m.field][m.vendorValue] = m.stdValue;
        }
    });
    if (Object.keys(valueMap).length > 0) result.value_mapping = valueMap;

    // 类型转换
    const typeConvertMap: Record<string, { type: string; separator: string }> = {};
    typeConverts.forEach(tc => {
        if (tc.field && tc.type) {
            typeConvertMap[tc.field] = {type: tc.type, separator: tc.separator || ','};
        }
    });
    if (Object.keys(typeConvertMap).length > 0) result.type_convert = typeConvertMap;

    // 成功条件
    if (successCondition && successCondition.field && successCondition.operator) {
        const cond: Record<string, any> = {
            field: successCondition.field,
            operator: successCondition.operator,
        };
        const opConfig = SUCCESS_CONDITION_OPERATORS.find(o => o.value === successCondition.operator);
        if (opConfig?.needValue && successCondition.value !== undefined && successCondition.value !== '') {
            // 尝试转换为数字
            const numVal = Number(successCondition.value);
            cond.value = isNaN(numVal) ? successCondition.value : numVal;
        }
        if (opConfig?.needValues && successCondition.values && successCondition.values.length > 0) {
            cond.values = successCondition.values.map(v => {
                const numVal = Number(v);
                return isNaN(numVal) ? v : numVal;
            });
        }
        result.success_condition = cond;
    }

    return result;
};

// 配置模板
const CONFIG_TEMPLATES: Record<string, {
    label: string;
    description: string;
    form: Record<string, any>;
    paramFieldMappings?: FieldMapping[];
    paramFixedParams?: FixedParam[];
    respFieldMappings?: FieldMapping[];
    respSuccessCondition?: SuccessCondition | null;
}> = {
    openai_image: {
        label: 'OpenAI 图片生成',
        description: 'gpt-image-1 / dall-e-3 透传',
        form: {
            result_mode: 'sync',
            request_path: '/v1/images/generations',
            content_type: 'application/json',
            auth_location: 'header',
            auth_key: 'Authorization',
            auth_value_prefix: 'Bearer ',
        },
        paramFieldMappings: [],
        respFieldMappings: [
            {stdField: 'url', vendorField: 'data[0].b64_json'},
            {stdField: 'urls', vendorField: 'data[].b64_json'},
        ],
        respSuccessCondition: {field: 'data[0].b64_json', operator: 'exists'},
    },
    midjourney_poll: {
        label: 'Midjourney (轮询)',
        description: '提交 + 轮询模式',
        form: {
            result_mode: 'poll',
            request_path: '/mj/submit/imagine',
            content_type: 'application/json',
            auth_location: 'header',
            auth_key: 'mj-api-secret',
            auth_value_prefix: '',
            poll_path: '/mj/task/{task_id}/fetch',
            poll_method: 'GET',
            poll_interval: 10,
            poll_max_attempts: 60,
        },
        paramFieldMappings: [{stdField: 'prompt', vendorField: 'prompt'}],
        respFieldMappings: [{stdField: 'task_id', vendorField: 'result'}],
        respSuccessCondition: {field: 'code', operator: 'eq', value: 1},
    },
    flux_sync: {
        label: 'Flux 图片生成',
        description: '同步返回模式',
        form: {
            result_mode: 'sync',
            request_path: '/v1/images/generations',
            content_type: 'application/json',
            auth_location: 'header',
            auth_key: 'Authorization',
            auth_value_prefix: 'Bearer ',
        },
        paramFieldMappings: [{stdField: 'prompt', vendorField: 'prompt'}],
        respFieldMappings: [{stdField: 'url', vendorField: 'data[0].url'}],
    },
    custom: {
        label: '自定义',
        description: '从空白开始配置',
        form: {},
        paramFieldMappings: [],
        respFieldMappings: [],
    },
};

// 渠道能力配置编辑弹窗
const ChannelCapabilityModal: React.FC<{
    isOpen: boolean;
    capabilityCode: string;
    channelCapability: ChannelCapability | null;
    channels: Channel[];
    capabilities: Capability[];
    onClose: () => void;
    onSave: () => void;
}> = ({isOpen, capabilityCode, channelCapability, channels, capabilities, onClose, onSave}) => {
    const [templateSelected, setTemplateSelected] = useState(!!channelCapability);
    const [activeTab, setActiveTab] = useState<'basic' | 'request' | 'param' | 'response' | 'poll_response' | 'callback'>('basic');
    const [form, setForm] = useState({
        channel_id: 0,
        capability_code: '',
        model: '',
        name: '',
        price: 0,
        price_unit: 'request',
        result_mode: 'poll',
        request_path: '',
        request_method: 'POST',
        content_type: 'application/json',
        auth_location: 'header',
        auth_key: 'Authorization',
        auth_value_prefix: 'Bearer ',
        poll_path: '',
        poll_method: 'GET',
        poll_interval: 5,
        poll_max_attempts: 60,
        transfer_enabled: true,
    });

    // 参数映射表单状态
    const [paramFieldMappings, setParamFieldMappings] = useState<FieldMapping[]>([]);
    const [paramValueMappings, setParamValueMappings] = useState<ValueMapping[]>([]);
    const [paramFixedParams, setParamFixedParams] = useState<FixedParam[]>([]);
    const [paramTypeConverts, setParamTypeConverts] = useState<TypeConvert[]>([]);

    // 响应映射表单状态
    const [respFieldMappings, setRespFieldMappings] = useState<FieldMapping[]>([]);
    const [respValueMappings, setRespValueMappings] = useState<ValueMapping[]>([]);
    const [respTypeConverts, setRespTypeConverts] = useState<TypeConvert[]>([]);
    const [respSuccessCondition, setRespSuccessCondition] = useState<SuccessCondition | null>(null);

    // 轮询响应映射表单状态
    const [pollRespFieldMappings, setPollRespFieldMappings] = useState<FieldMapping[]>([]);
    const [pollRespValueMappings, setPollRespValueMappings] = useState<ValueMapping[]>([]);
    const [pollRespTypeConverts, setPollRespTypeConverts] = useState<TypeConvert[]>([]);
    const [pollRespSuccessCondition, setPollRespSuccessCondition] = useState<SuccessCondition | null>(null);
    const [useSeparatePollMapping, setUseSeparatePollMapping] = useState(false);

    // 轮询参数映射表单状态（POST请求时使用）
    const [pollParamFieldMappings, setPollParamFieldMappings] = useState<FieldMapping[]>([]);
    const [pollParamFixedParams, setPollParamFixedParams] = useState<FixedParam[]>([]);

    // 回调映射
    const [callbackConfig, setCallbackConfig] = useState({
        task_id_path: '',
        status_path: '',
        result_path: '',
    });
    const [callbackStatusMappings, setCallbackStatusMappings] = useState<{
        stdValue: string;
        vendorValue: string
    }[]>([]);

    const [loading, setLoading] = useState(false);

    useEffect(() => {
        if (channelCapability) {
            setForm({
                channel_id: Number(channelCapability.channelId),
                capability_code: channelCapability.capabilityCode,
                model: channelCapability.model || '',
                name: channelCapability.name || '',
                price: channelCapability.price || 0,
                price_unit: channelCapability.priceUnit || 'request',
                result_mode: channelCapability.resultMode || 'poll',
                request_path: channelCapability.requestPath || '',
                request_method: channelCapability.requestMethod || 'POST',
                content_type: channelCapability.contentType || 'application/json',
                auth_location: channelCapability.authLocation || 'header',
                auth_key: channelCapability.authKey || 'Authorization',
                auth_value_prefix: channelCapability.authValuePrefix ?? '',
                poll_path: channelCapability.pollPath || '',
                poll_method: channelCapability.pollMethod || 'GET',
                poll_interval: channelCapability.pollInterval || 5,
                poll_max_attempts: channelCapability.pollMaxAttempts || 60,
                transfer_enabled: channelCapability.extraConfig?.transfer_enabled !== false,
            });

            // 解析参数映射
            const paramData = parseParamMapping(channelCapability.paramMapping || {});
            setParamFieldMappings(paramData.fieldMappings);
            setParamValueMappings(paramData.valueMappings);
            setParamFixedParams(paramData.fixedParams);
            setParamTypeConverts(paramData.typeConverts);

            // 解析响应映射
            const respData = parseResponseMapping(channelCapability.responseMapping || {});
            setRespFieldMappings(respData.fieldMappings);
            setRespValueMappings(respData.valueMappings);
            setRespTypeConverts(respData.typeConverts);
            setRespSuccessCondition(respData.successCondition);

            // 解析轮询响应映射
            const pollRespMapping = channelCapability.pollResponseMapping || {};
            if (Object.keys(pollRespMapping).length > 0) {
                setUseSeparatePollMapping(true);
                const pollRespData = parseResponseMapping(pollRespMapping);
                setPollRespFieldMappings(pollRespData.fieldMappings);
                setPollRespValueMappings(pollRespData.valueMappings);
                setPollRespTypeConverts(pollRespData.typeConverts);
                setPollRespSuccessCondition(pollRespData.successCondition);
            } else {
                setUseSeparatePollMapping(false);
                setPollRespFieldMappings([]);
                setPollRespValueMappings([]);
                setPollRespTypeConverts([]);
                setPollRespSuccessCondition(null);
            }

            // 解析轮询参数映射
            const pollParamMapping = channelCapability.pollParamMapping || {};
            if (Object.keys(pollParamMapping).length > 0) {
                const pollParamData = parseParamMapping(pollParamMapping);
                setPollParamFieldMappings(pollParamData.fieldMappings);
                setPollParamFixedParams(pollParamData.fixedParams);
            } else {
                setPollParamFieldMappings([]);
                setPollParamFixedParams([]);
            }

            // 解析回调映射
            const cbMapping = channelCapability.callbackMapping || {};
            setCallbackConfig({
                task_id_path: cbMapping.task_id_path || '',
                status_path: cbMapping.status_path || '',
                result_path: cbMapping.result_path || '',
            });
            const cbStatusMappings: { stdValue: string; vendorValue: string }[] = [];
            if (cbMapping.status_mapping) {
                Object.entries(cbMapping.status_mapping).forEach(([vendor, std]) => {
                    cbStatusMappings.push({stdValue: std as string, vendorValue: vendor});
                });
            }
            setCallbackStatusMappings(cbStatusMappings);
        } else {
            setForm({
                channel_id: channels[0]?.id ? Number(channels[0].id) : 0,
                capability_code: capabilityCode,
                model: '',
                name: '',
                price: 0,
                price_unit: 'request',
                result_mode: 'poll',
                request_path: '',
                request_method: 'POST',
                content_type: 'application/json',
                auth_location: 'header',
                auth_key: 'Authorization',
                auth_value_prefix: 'Bearer ',
                poll_path: '',
                poll_method: 'GET',
                poll_interval: 5,
                poll_max_attempts: 60,
            });
            setParamFieldMappings([]);
            setParamValueMappings([]);
            setParamFixedParams([]);
            setParamTypeConverts([]);
            setRespFieldMappings([]);
            setRespValueMappings([]);
            setRespTypeConverts([]);
            setRespSuccessCondition(null);
            setPollRespFieldMappings([]);
            setPollRespValueMappings([]);
            setPollRespTypeConverts([]);
            setPollRespSuccessCondition(null);
            setUseSeparatePollMapping(false);
            setPollParamFieldMappings([]);
            setPollParamFixedParams([]);
            setCallbackConfig({task_id_path: '', status_path: '', result_path: ''});
            setCallbackStatusMappings([]);
        }
        setActiveTab('basic');
        setTemplateSelected(!!channelCapability);
    }, [channelCapability, capabilityCode, channels, isOpen]);

    if (!isOpen) return null;

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setLoading(true);
        try {
            const paramMapping = buildParamMapping(paramFieldMappings, paramValueMappings, paramFixedParams, paramTypeConverts);
            const responseMapping = buildResponseMapping(respFieldMappings, respValueMappings, respTypeConverts, respSuccessCondition);

            // 轮询响应映射（如果启用单独配置）
            const pollResponseMapping = useSeparatePollMapping
                ? buildResponseMapping(pollRespFieldMappings, pollRespValueMappings, pollRespTypeConverts, pollRespSuccessCondition)
                : null;

            const callbackMapping: Record<string, any> = {};
            if (callbackConfig.task_id_path) callbackMapping.task_id_path = callbackConfig.task_id_path;
            if (callbackConfig.status_path) callbackMapping.status_path = callbackConfig.status_path;
            if (callbackConfig.result_path) callbackMapping.result_path = callbackConfig.result_path;
            if (callbackStatusMappings.length > 0) {
                const statusMap: Record<string, string> = {};
                callbackStatusMappings.forEach(m => {
                    if (m.vendorValue && m.stdValue) statusMap[m.vendorValue] = m.stdValue;
                });
                if (Object.keys(statusMap).length > 0) callbackMapping.status_mapping = statusMap;
            }

            const data: Record<string, any> = {
                channel_id: form.channel_id,
                capability_code: form.capability_code,
                model: form.model,
                name: form.name,
                price: form.price,
                price_unit: form.price_unit,
                result_mode: form.result_mode,
                request_path: form.request_path,
                request_method: form.request_method,
                content_type: form.content_type,
                auth_location: form.auth_location,
                auth_key: form.auth_key,
                auth_value_prefix: form.auth_value_prefix,
                poll_path: form.poll_path,
                poll_method: form.poll_method,
                poll_interval: form.poll_interval,
                poll_max_attempts: form.poll_max_attempts,
                param_mapping: paramMapping,
                callback_mapping: callbackMapping,
                extra_config: {transfer_enabled: form.transfer_enabled},
            };

            // 仅在有映射配置时才提交，避免覆盖旧格式配置
            if (Object.keys(responseMapping).length > 0) {
                data.response_mapping = responseMapping;
            }

            // 轮询响应映射
            if (pollResponseMapping && Object.keys(pollResponseMapping).length > 0) {
                data.poll_response_mapping = pollResponseMapping;
            }

            // 轮询参数映射
            if (form.result_mode === 'poll' && form.poll_method === 'POST') {
                data.poll_param_mapping = buildParamMapping(pollParamFieldMappings, [], pollParamFixedParams, []);
            } else {
                data.poll_param_mapping = {};
            }

            if (channelCapability) {
                await updateChannelCapability(channelCapability.id, data);
            } else {
                await createChannelCapability(data as Parameters<typeof createChannelCapability>[0]);
            }
            onSave();
            onClose();
        } finally {
            setLoading(false);
        }
    };

    const baseTabs = [
        {key: 'basic', label: '基本信息'},
        {key: 'request', label: '请求配置'},
        {key: 'param', label: '参数映射'},
        {key: 'response', label: '响应映射'},
    ];
    const tabs = (() => {
        if (form.result_mode === 'sync') return baseTabs;
        if (form.result_mode === 'poll') {
            const t = [...baseTabs];
            if (useSeparatePollMapping) t.push({key: 'poll_response', label: '轮询响应'});
            t.push({key: 'callback', label: '回调映射'});
            return t;
        }
        // callback
        return [...baseTabs, {key: 'callback', label: '回调映射'}];
    })();

    // 添加参数字段映射
    const addParamFieldMapping = (stdField: string) => {
        if (!paramFieldMappings.find(m => m.stdField === stdField)) {
            setParamFieldMappings([...paramFieldMappings, {stdField, vendorField: ''}]);
        }
    };

    // 添加响应字段映射
    const addRespFieldMapping = (stdField: string) => {
        if (!respFieldMappings.find(m => m.stdField === stdField)) {
            setRespFieldMappings([...respFieldMappings, {stdField, vendorField: ''}]);
        }
    };

    // 添加轮询响应字段映射
    const addPollRespFieldMapping = (stdField: string) => {
        if (!pollRespFieldMappings.find(m => m.stdField === stdField)) {
            setPollRespFieldMappings([...pollRespFieldMappings, {stdField, vendorField: ''}]);
        }
    };

    // 添加轮询参数字段映射（POST请求时）
    const addPollParamFieldMapping = (stdField: string) => {
        if (!pollParamFieldMappings.find(m => m.stdField === stdField)) {
            setPollParamFieldMappings([...pollParamFieldMappings, {stdField, vendorField: ''}]);
        }
    };

    return (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
            <div
                className="bg-white rounded-2xl w-full max-w-3xl p-6 shadow-xl max-h-[90vh] overflow-hidden flex flex-col">
                <div className="flex items-center justify-between mb-4">
                    <h3 className="text-lg font-bold text-gray-900">{channelCapability ? '编辑渠道能力配置' : '新建渠道能力配置'}</h3>
                    <button onClick={onClose} className="p-2 hover:bg-gray-100 rounded-lg"><X size={20}/></button>
                </div>

                {!templateSelected && !channelCapability ? (
                    <div className="flex-1 overflow-y-auto">
                        <p className="text-sm text-gray-500 mb-4">选择一个模板快速开始，或从空白自定义配置</p>
                        <div className="grid grid-cols-2 gap-3">
                            {Object.entries(CONFIG_TEMPLATES).map(([key, tpl]) => (
                                <button
                                    key={key}
                                    type="button"
                                    onClick={() => {
                                        setForm(prev => ({...prev, ...tpl.form}));
                                        if (tpl.paramFieldMappings) setParamFieldMappings(tpl.paramFieldMappings);
                                        if (tpl.respFieldMappings) setRespFieldMappings(tpl.respFieldMappings);
                                        if (tpl.respSuccessCondition !== undefined) setRespSuccessCondition(tpl.respSuccessCondition);
                                        if (tpl.paramFixedParams) setParamFixedParams(tpl.paramFixedParams);
                                        setTemplateSelected(true);
                                    }}
                                    className="text-left p-4 rounded-xl border border-gray-200 hover:border-indigo-300 hover:bg-indigo-50 transition-colors"
                                >
                                    <div className="font-medium text-sm text-gray-900">{tpl.label}</div>
                                    <div className="text-xs text-gray-500 mt-1">{tpl.description}</div>
                                </button>
                            ))}
                        </div>
                    </div>
                ) : (
                <>
                <div className="flex border-b border-gray-200 mb-4 overflow-x-auto">
                    {tabs.map(tab => (
                        <button
                            key={tab.key}
                            type="button"
                            onClick={() => setActiveTab(tab.key as any)}
                            className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors whitespace-nowrap ${
                                activeTab === tab.key
                                    ? 'border-indigo-600 text-indigo-600'
                                    : 'border-transparent text-gray-500 hover:text-gray-700'
                            }`}
                        >
                            {tab.label}
                        </button>
                    ))}
                </div>

                <form onSubmit={handleSubmit} className="flex-1 overflow-y-auto">
                    {/* 基本信息 */}
                    {activeTab === 'basic' && (
                        <div className="space-y-4">
                            <div className="grid grid-cols-2 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">渠道 <span
                                        className="text-red-500">*</span></label>
                                    <select
                                        value={form.channel_id}
                                        onChange={e => setForm({...form, channel_id: Number(e.target.value)})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                        required
                                    >
                                        <option value={0}>选择渠道</option>
                                        {channels.map(ch => (
                                            <option key={ch.id} value={ch.id}>{ch.name} ({ch.type})</option>
                                        ))}
                                    </select>
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">能力编码 <span
                                        className="text-red-500">*</span></label>
                                    <select
                                        value={form.capability_code}
                                        onChange={e => setForm({...form, capability_code: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                        required
                                    >
                                        <option value="">选择能力</option>
                                        {capabilities.map(cap => (
                                            <option key={cap.code} value={cap.code}>{cap.name} ({cap.code})</option>
                                        ))}
                                    </select>
                                </div>
                            </div>
                            <div className="grid grid-cols-2 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">配置名称</label>
                                    <input
                                        type="text"
                                        value={form.name}
                                        onChange={e => setForm({...form, name: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                        placeholder="如: Midjourney文生图"
                                    />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">模型标识</label>
                                    <input
                                        type="text"
                                        value={form.model}
                                        onChange={e => setForm({...form, model: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                        placeholder="如: midjourney-v6"
                                    />
                                </div>
                            </div>
                            <div className="grid grid-cols-3 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">结果模式</label>
                                    <select
                                        value={form.result_mode}
                                        onChange={e => setForm({...form, result_mode: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    >
                                        {RESULT_MODES.map(m => (
                                            <option key={m.value} value={m.value}>{m.label}</option>
                                        ))}
                                    </select>
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">单价</label>
                                    <input
                                        type="number"
                                        value={form.price}
                                        onChange={e => setForm({...form, price: Number(e.target.value)})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                        step="0.0001"
                                        min="0"
                                    />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">计价单位</label>
                                    <select
                                        value={form.price_unit}
                                        onChange={e => setForm({...form, price_unit: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    >
                                        <option value="request">按请求</option>
                                        <option value="second">按秒</option>
                                        <option value="image">按图片</option>
                                    </select>
                                </div>
                            </div>
                            <div className="flex items-center gap-2 mt-2">
                                <input
                                    type="checkbox"
                                    id="transfer_enabled"
                                    checked={form.transfer_enabled}
                                    onChange={e => setForm({...form, transfer_enabled: e.target.checked})}
                                    className="h-4 w-4 text-indigo-600 border-gray-300 rounded focus:ring-indigo-500"
                                />
                                <label htmlFor="transfer_enabled" className="text-sm text-gray-700">结果文件转存到 OSS</label>
                            </div>
                        </div>
                    )}

                    {/* 请求配置 */}
                    {activeTab === 'request' && (
                        <div className="space-y-4">
                            <div>
                                <label className="block text-sm font-medium text-gray-700 mb-1">请求路径 <span
                                    className="text-red-500">*</span></label>
                                <input
                                    type="text"
                                    value={form.request_path}
                                    onChange={e => setForm({...form, request_path: e.target.value})}
                                    className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    placeholder="/api/v1/images/generate"
                                    required
                                />
                            </div>
                            <div className="grid grid-cols-2 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">请求方法</label>
                                    <select
                                        value={form.request_method}
                                        onChange={e => setForm({...form, request_method: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    >
                                        <option value="POST">POST</option>
                                        <option value="GET">GET</option>
                                    </select>
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">Content-Type</label>
                                    <select
                                        value={form.content_type}
                                        onChange={e => setForm({...form, content_type: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    >
                                        <option value="application/json">application/json</option>
                                        <option
                                            value="application/x-www-form-urlencoded">application/x-www-form-urlencoded
                                        </option>
                                        <option value="multipart/form-data">multipart/form-data</option>
                                    </select>
                                </div>
                            </div>

                            {/* 认证配置 */}
                            <div className="border-t border-gray-200 pt-4 mt-4">
                                <h4 className="text-sm font-medium text-gray-900 mb-3">认证配置</h4>
                            </div>
                            <div className="grid grid-cols-3 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">认证位置</label>
                                    <select
                                        value={form.auth_location}
                                        onChange={e => setForm({...form, auth_location: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    >
                                        <option value="header">请求头 (Header)</option>
                                        <option value="body">请求体 (Body)</option>
                                        <option value="query">URL参数 (Query)</option>
                                    </select>
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">参数名</label>
                                    <input
                                        type="text"
                                        value={form.auth_key}
                                        onChange={e => setForm({...form, auth_key: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                        placeholder="Authorization"
                                    />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-gray-700 mb-1">值前缀</label>
                                    <input
                                        type="text"
                                        value={form.auth_value_prefix}
                                        onChange={e => setForm({...form, auth_value_prefix: e.target.value})}
                                        className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                        placeholder="Bearer "
                                    />
                                </div>
                            </div>
                            <p className="text-xs text-gray-500">认证值 = 前缀 + API Key，如: Bearer sk-xxx</p>

                            {form.result_mode === 'poll' && (
                                <>
                                    <div className="border-t border-gray-200 pt-4 mt-4">
                                        <h4 className="text-sm font-medium text-gray-900 mb-3">轮询配置</h4>
                                    </div>
                                    <div className="grid grid-cols-2 gap-4">
                                        <div>
                                            <label
                                                className="block text-sm font-medium text-gray-700 mb-1">轮询路径</label>
                                            <input
                                                type="text"
                                                value={form.poll_path}
                                                onChange={e => setForm({...form, poll_path: e.target.value})}
                                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="/api/v1/tasks/{task_id}"
                                            />
                                            <p className="text-xs text-gray-500 mt-1">支持 {'{task_id}'} 占位符</p>
                                        </div>
                                        <div>
                                            <label
                                                className="block text-sm font-medium text-gray-700 mb-1">轮询方法</label>
                                            <select
                                                value={form.poll_method}
                                                onChange={e => setForm({...form, poll_method: e.target.value})}
                                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="GET">GET</option>
                                                <option value="POST">POST</option>
                                            </select>
                                        </div>
                                    </div>
                                    <div className="grid grid-cols-2 gap-4">
                                        <div>
                                            <label className="block text-sm font-medium text-gray-700 mb-1">轮询间隔
                                                (秒)</label>
                                            <input
                                                type="number"
                                                value={form.poll_interval}
                                                onChange={e => setForm({
                                                    ...form,
                                                    poll_interval: Number(e.target.value)
                                                })}
                                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                min={1}
                                                max={60}
                                            />
                                        </div>
                                        <div>
                                            <label
                                                className="block text-sm font-medium text-gray-700 mb-1">最大轮询次数</label>
                                            <input
                                                type="number"
                                                value={form.poll_max_attempts}
                                                onChange={e => setForm({
                                                    ...form,
                                                    poll_max_attempts: Number(e.target.value)
                                                })}
                                                className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                min={1}
                                                max={1000}
                                            />
                                        </div>
                                    </div>
                                    {form.poll_method === 'POST' && (
                                        <div className="border-t border-gray-200 pt-4 mt-4">
                                            <h4 className="text-sm font-medium text-gray-900 mb-3">轮询请求参数</h4>
                                            <p className="text-xs text-gray-500 mb-3">配置 POST 轮询请求的参数映射</p>
                                            <div className="mb-4">
                                                <div className="flex items-center justify-between mb-2">
                                                    <span className="text-sm text-gray-700">字段映射</span>
                                                    <select
                                                        onChange={e => {
                                                            if (e.target.value) addPollParamFieldMapping(e.target.value);
                                                            e.target.value = '';
                                                        }}
                                                        className="px-3 py-1.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                    >
                                                        <option value="">+ 添加字段</option>
                                                        {Object.entries(POLL_PARAMS).filter(([key]) => !pollParamFieldMappings.find(m => m.stdField === key)).map(([key, def]) => (
                                                            <option key={key} value={key}>{def.name} ({key})</option>
                                                        ))}
                                                    </select>
                                                </div>
                                                {pollParamFieldMappings.length === 0 ? (
                                                    <div
                                                        className="text-sm text-gray-400 text-center py-3 bg-gray-50 rounded-lg">暂无字段映射</div>
                                                ) : (
                                                    pollParamFieldMappings.map((m, i) => (
                                                        <FieldMappingRow
                                                            key={m.stdField}
                                                            stdField={m.stdField}
                                                            stdName={POLL_PARAMS[m.stdField]?.name || m.stdField}
                                                            vendorField={m.vendorField}
                                                            onChange={val => {
                                                                const newList = [...pollParamFieldMappings];
                                                                newList[i].vendorField = val;
                                                                setPollParamFieldMappings(newList);
                                                            }}
                                                            onRemove={() => setPollParamFieldMappings(pollParamFieldMappings.filter((_, idx) => idx !== i))}
                                                        />
                                                    ))
                                                )}
                                            </div>
                                            <div>
                                                <div className="flex items-center justify-between mb-2">
                                                    <span className="text-sm text-gray-700">固定参数</span>
                                                    <button
                                                        type="button"
                                                        onClick={() => setPollParamFixedParams([...pollParamFixedParams, {
                                                            name: '',
                                                            value: ''
                                                        }])}
                                                        className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                                    >
                                                        + 添加参数
                                                    </button>
                                                </div>
                                                {pollParamFixedParams.length === 0 ? (
                                                    <div
                                                        className="text-sm text-gray-400 text-center py-3 bg-gray-50 rounded-lg">暂无固定参数</div>
                                                ) : (
                                                    pollParamFixedParams.map((p, i) => (
                                                        <div key={i} className="flex items-center gap-2 mb-2">
                                                            <input
                                                                type="text"
                                                                value={p.name}
                                                                onChange={e => {
                                                                    const newList = [...pollParamFixedParams];
                                                                    newList[i].name = e.target.value;
                                                                    setPollParamFixedParams(newList);
                                                                }}
                                                                placeholder="参数名"
                                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                            />
                                                            <span className="text-gray-400">=</span>
                                                            <input
                                                                type="text"
                                                                value={p.value}
                                                                onChange={e => {
                                                                    const newList = [...pollParamFixedParams];
                                                                    newList[i].value = e.target.value;
                                                                    setPollParamFixedParams(newList);
                                                                }}
                                                                placeholder="参数值，支持 {task_id} 占位符"
                                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                            />
                                                            <button
                                                                type="button"
                                                                onClick={() => setPollParamFixedParams(pollParamFixedParams.filter((_, idx) => idx !== i))}
                                                                className="p-2 text-red-500 hover:bg-red-50 rounded-lg"
                                                            >
                                                                <Trash2 size={16}/>
                                                            </button>
                                                        </div>
                                                    ))
                                                )}
                                            </div>
                                        </div>
                                    )}
                                    <div className="flex items-center gap-2 mt-4">
                                        <input
                                            type="checkbox"
                                            id="useSeparatePollMapping"
                                            checked={useSeparatePollMapping}
                                            onChange={e => setUseSeparatePollMapping(e.target.checked)}
                                            className="rounded border-gray-300 text-indigo-600 focus:ring-indigo-500"
                                        />
                                        <label htmlFor="useSeparatePollMapping" className="text-sm text-gray-700">
                                            使用独立的轮询响应映射（提交响应与轮询响应格式不同时启用）
                                        </label>
                                    </div>
                                </>
                            )}
                        </div>
                    )}

                    {/* 参数映射 */}
                    {activeTab === 'param' && (
                        <div className="space-y-6">
                            {paramFieldMappings.length === 0 && paramFixedParams.length === 0 && paramValueMappings.length === 0 && paramTypeConverts.length === 0 && (
                                <div className="rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 text-sm text-blue-700">
                                    当前为透传模式：请求参数将原样转发给上游 API，无需配置映射。如需自定义参数转换，请在下方添加。
                                </div>
                            )}
                            {/* 字段映射 */}
                            <div>
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">字段映射</h4>
                                    <select
                                        onChange={e => {
                                            if (e.target.value) addParamFieldMapping(e.target.value);
                                            e.target.value = '';
                                        }}
                                        className="px-3 py-1.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    >
                                        <option value="">+ 添加字段</option>
                                        {Object.entries(STANDARD_PARAMS).filter(([key]) => !paramFieldMappings.find(m => m.stdField === key)).map(([key, def]) => (
                                            <option key={key} value={key}>{def.name} ({key})</option>
                                        ))}
                                    </select>
                                </div>
                                <p className="text-xs text-gray-500 mb-3">配置系统标准参数到三方接口参数的字段名对应关系</p>
                                {paramFieldMappings.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无字段映射，请从上方添加</div>
                                ) : (
                                    paramFieldMappings.map((m, i) => (
                                        <FieldMappingRow
                                            key={m.stdField}
                                            stdField={m.stdField}
                                            stdName={STANDARD_PARAMS[m.stdField]?.name || m.stdField}
                                            vendorField={m.vendorField}
                                            onChange={val => {
                                                const newList = [...paramFieldMappings];
                                                newList[i].vendorField = val;
                                                setParamFieldMappings(newList);
                                            }}
                                            onRemove={() => setParamFieldMappings(paramFieldMappings.filter((_, idx) => idx !== i))}
                                        />
                                    ))
                                )}
                            </div>

                            {/* 枚举值映射 */}
                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">枚举值映射</h4>
                                    <button
                                        type="button"
                                        onClick={() => setParamValueMappings([...paramValueMappings, {
                                            field: '',
                                            stdValue: '',
                                            vendorValue: ''
                                        }])}
                                        className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                    >
                                        + 添加映射
                                    </button>
                                </div>
                                <p className="text-xs text-gray-500 mb-3">配置枚举类型字段的值对应关系（如 aspect_ratio:
                                    "16:9" → "landscape"）</p>
                                {paramValueMappings.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无枚举值映射</div>
                                ) : (
                                    paramValueMappings.map((m, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <select
                                                value={m.field}
                                                onChange={e => {
                                                    const newList = [...paramValueMappings];
                                                    newList[i].field = e.target.value;
                                                    newList[i].stdValue = '';
                                                    setParamValueMappings(newList);
                                                }}
                                                className="w-32 px-2 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="">选择字段</option>
                                                {Object.entries(STANDARD_PARAMS).filter(([, def]) => def.type === 'enum').map(([key, def]) => (
                                                    <option key={key} value={key}>{def.name}</option>
                                                ))}
                                            </select>
                                            <select
                                                value={m.stdValue}
                                                onChange={e => {
                                                    const newList = [...paramValueMappings];
                                                    newList[i].stdValue = e.target.value;
                                                    setParamValueMappings(newList);
                                                }}
                                                className="w-28 px-2 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                disabled={!m.field}
                                            >
                                                <option value="">系统值</option>
                                                {m.field && STANDARD_PARAMS[m.field]?.options?.map(v => (
                                                    <option key={v} value={v}>{v}</option>
                                                ))}
                                            </select>
                                            <span className="text-gray-400">→</span>
                                            <input
                                                type="text"
                                                value={m.vendorValue}
                                                onChange={e => {
                                                    const newList = [...paramValueMappings];
                                                    newList[i].vendorValue = e.target.value;
                                                    setParamValueMappings(newList);
                                                }}
                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="三方对应值"
                                            />
                                            <button type="button"
                                                    onClick={() => setParamValueMappings(paramValueMappings.filter((_, idx) => idx !== i))}
                                                    className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
                                                <X size={14}/>
                                            </button>
                                        </div>
                                    ))
                                )}
                            </div>

                            {/* 固定参数 */}
                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">固定参数</h4>
                                    <button
                                        type="button"
                                        onClick={() => setParamFixedParams([...paramFixedParams, {
                                            name: '',
                                            value: ''
                                        }])}
                                        className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                    >
                                        + 添加参数
                                    </button>
                                </div>
                                <p className="text-xs text-gray-500 mb-3">每次请求都会附带的固定参数</p>
                                {paramFixedParams.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无固定参数</div>
                                ) : (
                                    paramFixedParams.map((p, i) => (
                                        <FixedParamRow
                                            key={i}
                                            paramName={p.name}
                                            paramValue={p.value}
                                            onNameChange={val => {
                                                const newList = [...paramFixedParams];
                                                newList[i].name = val;
                                                setParamFixedParams(newList);
                                            }}
                                            onValueChange={val => {
                                                const newList = [...paramFixedParams];
                                                newList[i].value = val;
                                                setParamFixedParams(newList);
                                            }}
                                            onRemove={() => setParamFixedParams(paramFixedParams.filter((_, idx) => idx !== i))}
                                        />
                                    ))
                                )}
                            </div>

                            {/* 类型转换 */}
                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">类型转换</h4>
                                    <button
                                        type="button"
                                        onClick={() => setParamTypeConverts([...paramTypeConverts, {
                                            field: '',
                                            type: 'array_to_string',
                                            separator: ','
                                        }])}
                                        className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                    >
                                        + 添加转换
                                    </button>
                                </div>
                                <p className="text-xs text-gray-500 mb-3">配置参数类型转换（如将数组转为逗号分隔的字符串）</p>
                                {paramTypeConverts.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无类型转换</div>
                                ) : (
                                    paramTypeConverts.map((tc, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <select
                                                value={tc.field}
                                                onChange={e => {
                                                    const newList = [...paramTypeConverts];
                                                    newList[i].field = e.target.value;
                                                    setParamTypeConverts(newList);
                                                }}
                                                className="w-40 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="">选择字段</option>
                                                {paramFieldMappings.map(m => (
                                                    <option key={m.stdField} value={m.stdField}>
                                                        {STANDARD_PARAMS[m.stdField]?.name || m.stdField}
                                                    </option>
                                                ))}
                                            </select>
                                            <select
                                                value={tc.type}
                                                onChange={e => {
                                                    const newList = [...paramTypeConverts];
                                                    newList[i].type = e.target.value as 'string_to_array' | 'array_to_string';
                                                    setParamTypeConverts(newList);
                                                }}
                                                className="w-44 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="array_to_string">数组→字符串</option>
                                                <option value="string_to_array">字符串→数组</option>
                                            </select>
                                            <input
                                                type="text"
                                                value={tc.separator}
                                                onChange={e => {
                                                    const newList = [...paramTypeConverts];
                                                    newList[i].separator = e.target.value;
                                                    setParamTypeConverts(newList);
                                                }}
                                                className="w-24 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="分隔符"
                                            />
                                            <span
                                                className="text-xs text-gray-400 whitespace-nowrap">用 \n 表示换行</span>
                                            <button type="button"
                                                    onClick={() => setParamTypeConverts(paramTypeConverts.filter((_, idx) => idx !== i))}
                                                    className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
                                                <X size={14}/>
                                            </button>
                                        </div>
                                    ))
                                )}
                            </div>
                        </div>
                    )}

                    {/* 响应映射 */}
                    {activeTab === 'response' && (
                        <div className="space-y-6">
                            {respFieldMappings.length === 0 && respValueMappings.length === 0 && respTypeConverts.length === 0 && !respSuccessCondition && (
                                <div className="rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 text-sm text-blue-700">
                                    当前为透传模式：上游 API 的响应将原样返回，无需配置映射。如需自定义响应转换，请在下方添加。
                                </div>
                            )}
                            {/* 字段映射 */}
                            <div>
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">字段映射</h4>
                                    <select
                                        onChange={e => {
                                            if (e.target.value) addRespFieldMapping(e.target.value);
                                            e.target.value = '';
                                        }}
                                        className="px-3 py-1.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    >
                                        <option value="">+ 添加字段</option>
                                        {Object.entries(STANDARD_RESPONSE).filter(([key]) => !respFieldMappings.find(m => m.stdField === key)).map(([key, def]) => (
                                            <option key={key} value={key}>{def.name} ({key})</option>
                                        ))}
                                    </select>
                                </div>
                                <p className="text-xs text-gray-500 mb-3">配置三方接口响应字段路径到系统标准字段的映射（支持路径如
                                    data.output.images[0]）</p>
                                {respFieldMappings.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无字段映射，请从上方添加</div>
                                ) : (
                                    respFieldMappings.map((m, i) => (
                                        <div key={m.stdField} className="flex items-center gap-2 mb-2">
                                            <div className="flex-1 px-3 py-2 bg-gray-50 rounded-lg text-sm">
                                                <span
                                                    className="text-gray-600">{STANDARD_RESPONSE[m.stdField]?.name || m.stdField}</span>
                                                <code className="ml-2 text-xs text-gray-400">{m.stdField}</code>
                                            </div>
                                            <span className="text-gray-400">←</span>
                                            <input
                                                type="text"
                                                value={m.vendorField}
                                                onChange={e => {
                                                    const newList = [...respFieldMappings];
                                                    newList[i].vendorField = e.target.value;
                                                    setRespFieldMappings(newList);
                                                }}
                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="三方响应字段路径，如 data.task_id"
                                            />
                                            <button type="button"
                                                    onClick={() => setRespFieldMappings(respFieldMappings.filter((_, idx) => idx !== i))}
                                                    className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
                                                <X size={14}/>
                                            </button>
                                        </div>
                                    ))
                                )}
                            </div>

                            {/* 状态值映射 */}
                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">状态值映射</h4>
                                    <button
                                        type="button"
                                        onClick={() => setRespValueMappings([...respValueMappings, {
                                            field: 'status',
                                            stdValue: '',
                                            vendorValue: ''
                                        }])}
                                        className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                    >
                                        + 添加映射
                                    </button>
                                </div>
                                <p className="text-xs text-gray-500 mb-3">配置三方接口状态值到系统标准状态的映射（如
                                    "completed" → "success"）</p>
                                {respValueMappings.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无状态值映射</div>
                                ) : (
                                    respValueMappings.map((m, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <input
                                                type="text"
                                                value={m.vendorValue}
                                                onChange={e => {
                                                    const newList = [...respValueMappings];
                                                    newList[i].vendorValue = e.target.value;
                                                    setRespValueMappings(newList);
                                                }}
                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="三方状态值，如 completed"
                                            />
                                            <span className="text-gray-400">→</span>
                                            <select
                                                value={m.stdValue}
                                                onChange={e => {
                                                    const newList = [...respValueMappings];
                                                    newList[i].stdValue = e.target.value;
                                                    setRespValueMappings(newList);
                                                }}
                                                className="w-36 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="">系统状态</option>
                                                {STANDARD_STATUS_VALUES.map(v => (
                                                    <option key={v} value={v}>{v}</option>
                                                ))}
                                            </select>
                                            <button type="button"
                                                    onClick={() => setRespValueMappings(respValueMappings.filter((_, idx) => idx !== i))}
                                                    className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
                                                <X size={14}/>
                                            </button>
                                        </div>
                                    ))
                                )}
                            </div>

                            {/* 类型转换 */}
                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">类型转换</h4>
                                    <button
                                        type="button"
                                        onClick={() => setRespTypeConverts([...respTypeConverts, {
                                            field: '',
                                            type: 'string_to_array',
                                            separator: ','
                                        }])}
                                        className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                    >
                                        + 添加转换
                                    </button>
                                </div>
                                <p className="text-xs text-gray-500 mb-3">配置字段数据类型转换（如将逗号分隔的字符串转为数组）</p>
                                {respTypeConverts.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无类型转换</div>
                                ) : (
                                    respTypeConverts.map((tc, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <select
                                                value={tc.field}
                                                onChange={e => {
                                                    const newList = [...respTypeConverts];
                                                    newList[i].field = e.target.value;
                                                    setRespTypeConverts(newList);
                                                }}
                                                className="w-40 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="">选择字段</option>
                                                {respFieldMappings.map(m => (
                                                    <option key={m.stdField} value={m.stdField}>
                                                        {STANDARD_RESPONSE[m.stdField]?.name || m.stdField}
                                                    </option>
                                                ))}
                                            </select>
                                            <select
                                                value={tc.type}
                                                onChange={e => {
                                                    const newList = [...respTypeConverts];
                                                    newList[i].type = e.target.value as 'string_to_array' | 'array_to_string';
                                                    setRespTypeConverts(newList);
                                                }}
                                                className="w-44 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="string_to_array">字符串→数组</option>
                                                <option value="array_to_string">数组→字符串</option>
                                            </select>
                                            <input
                                                type="text"
                                                value={tc.separator}
                                                onChange={e => {
                                                    const newList = [...respTypeConverts];
                                                    newList[i].separator = e.target.value;
                                                    setRespTypeConverts(newList);
                                                }}
                                                className="w-24 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="分隔符"
                                            />
                                            <span
                                                className="text-xs text-gray-400 whitespace-nowrap">用 \n 表示换行</span>
                                            <button type="button"
                                                    onClick={() => setRespTypeConverts(respTypeConverts.filter((_, idx) => idx !== i))}
                                                    className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
                                                <X size={14}/>
                                            </button>
                                        </div>
                                    ))
                                )}
                            </div>

                            {/* 成功条件 */}
                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">成功条件</h4>
                                    {!respSuccessCondition ? (
                                        <button
                                            type="button"
                                            onClick={() => setRespSuccessCondition({
                                                field: '',
                                                operator: 'eq',
                                                value: ''
                                            })}
                                            className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                        >
                                            + 添加条件
                                        </button>
                                    ) : (
                                        <button
                                            type="button"
                                            onClick={() => setRespSuccessCondition(null)}
                                            className="px-3 py-1.5 text-sm text-red-500 hover:bg-red-50 rounded-lg"
                                        >
                                            移除条件
                                        </button>
                                    )}
                                </div>
                                <p className="text-xs text-gray-500 mb-3">配置响应成功的判断条件（如 code 等于 0
                                    表示成功）。不配置时使用默认的 status 字段判断</p>
                                {respSuccessCondition && (
                                    <div className="flex items-center gap-2 mb-2 flex-wrap">
                                        <input
                                            type="text"
                                            value={respSuccessCondition.field}
                                            onChange={e => setRespSuccessCondition({
                                                ...respSuccessCondition,
                                                field: e.target.value
                                            })}
                                            className="w-40 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            placeholder="字段路径，如 code"
                                        />
                                        <select
                                            value={respSuccessCondition.operator}
                                            onChange={e => setRespSuccessCondition({
                                                ...respSuccessCondition,
                                                operator: e.target.value as SuccessCondition['operator']
                                            })}
                                            className="w-32 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                        >
                                            {SUCCESS_CONDITION_OPERATORS.map(op => (
                                                <option key={op.value} value={op.value}>{op.label}</option>
                                            ))}
                                        </select>
                                        {SUCCESS_CONDITION_OPERATORS.find(o => o.value === respSuccessCondition.operator)?.needValue && (
                                            <input
                                                type="text"
                                                value={respSuccessCondition.value !== undefined ? String(respSuccessCondition.value) : ''}
                                                onChange={e => setRespSuccessCondition({
                                                    ...respSuccessCondition,
                                                    value: e.target.value
                                                })}
                                                className="w-32 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="比较值"
                                            />
                                        )}
                                        {SUCCESS_CONDITION_OPERATORS.find(o => o.value === respSuccessCondition.operator)?.needValues && (
                                            <input
                                                type="text"
                                                value={respSuccessCondition.values?.join(',') || ''}
                                                onChange={e => setRespSuccessCondition({
                                                    ...respSuccessCondition,
                                                    values: e.target.value.split(',').map(v => v.trim()).filter(v => v)
                                                })}
                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="值列表，逗号分隔"
                                            />
                                        )}
                                    </div>
                                )}
                            </div>
                        </div>
                    )}

                    {/* 轮询响应映射 */}
                    {activeTab === 'poll_response' && useSeparatePollMapping && (
                        <div className="space-y-6">
                            <p className="text-xs text-gray-500">配置轮询接口响应的字段映射（当轮询响应格式与提交响应不同时使用）</p>

                            {/* 字段映射 */}
                            <div>
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">字段映射</h4>
                                    <select
                                        onChange={e => {
                                            if (e.target.value) addPollRespFieldMapping(e.target.value);
                                            e.target.value = '';
                                        }}
                                        className="px-3 py-1.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                    >
                                        <option value="">+ 添加字段</option>
                                        {Object.entries(STANDARD_RESPONSE).filter(([key]) => !pollRespFieldMappings.find(m => m.stdField === key)).map(([key, def]) => (
                                            <option key={key} value={key}>{def.name} ({key})</option>
                                        ))}
                                    </select>
                                </div>
                                {pollRespFieldMappings.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无字段映射，请从上方添加</div>
                                ) : (
                                    pollRespFieldMappings.map((m, i) => (
                                        <div key={m.stdField} className="flex items-center gap-2 mb-2">
                                            <div className="flex-1 px-3 py-2 bg-gray-50 rounded-lg text-sm">
                                                <span
                                                    className="text-gray-600">{STANDARD_RESPONSE[m.stdField]?.name || m.stdField}</span>
                                                <code className="ml-2 text-xs text-gray-400">{m.stdField}</code>
                                            </div>
                                            <span className="text-gray-400">←</span>
                                            <input
                                                type="text"
                                                value={m.vendorField}
                                                onChange={e => {
                                                    const newList = [...pollRespFieldMappings];
                                                    newList[i].vendorField = e.target.value;
                                                    setPollRespFieldMappings(newList);
                                                }}
                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="三方响应字段路径"
                                            />
                                            <button type="button"
                                                    onClick={() => setPollRespFieldMappings(pollRespFieldMappings.filter((_, idx) => idx !== i))}
                                                    className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
                                                <X size={14}/>
                                            </button>
                                        </div>
                                    ))
                                )}
                            </div>

                            {/* 状态值映射 */}
                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">状态值映射</h4>
                                    <button
                                        type="button"
                                        onClick={() => setPollRespValueMappings([...pollRespValueMappings, {
                                            field: 'status',
                                            stdValue: '',
                                            vendorValue: ''
                                        }])}
                                        className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                    >
                                        + 添加映射
                                    </button>
                                </div>
                                {pollRespValueMappings.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无状态值映射</div>
                                ) : (
                                    pollRespValueMappings.map((m, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <input
                                                type="text"
                                                value={m.vendorValue}
                                                onChange={e => {
                                                    const newList = [...pollRespValueMappings];
                                                    newList[i].vendorValue = e.target.value;
                                                    setPollRespValueMappings(newList);
                                                }}
                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="三方状态值"
                                            />
                                            <span className="text-gray-400">→</span>
                                            <select
                                                value={m.stdValue}
                                                onChange={e => {
                                                    const newList = [...pollRespValueMappings];
                                                    newList[i].stdValue = e.target.value;
                                                    setPollRespValueMappings(newList);
                                                }}
                                                className="w-36 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="">系统状态</option>
                                                {STANDARD_STATUS_VALUES.map(v => (
                                                    <option key={v} value={v}>{v}</option>
                                                ))}
                                            </select>
                                            <button type="button"
                                                    onClick={() => setPollRespValueMappings(pollRespValueMappings.filter((_, idx) => idx !== i))}
                                                    className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
                                                <X size={14}/>
                                            </button>
                                        </div>
                                    ))
                                )}
                            </div>

                            {/* 类型转换 */}
                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">类型转换</h4>
                                    <button
                                        type="button"
                                        onClick={() => setPollRespTypeConverts([...pollRespTypeConverts, {
                                            field: '',
                                            type: 'string_to_array',
                                            separator: ','
                                        }])}
                                        className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                    >
                                        + 添加转换
                                    </button>
                                </div>
                                {pollRespTypeConverts.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无类型转换</div>
                                ) : (
                                    pollRespTypeConverts.map((tc, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <select
                                                value={tc.field}
                                                onChange={e => {
                                                    const newList = [...pollRespTypeConverts];
                                                    newList[i].field = e.target.value;
                                                    setPollRespTypeConverts(newList);
                                                }}
                                                className="w-40 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="">选择字段</option>
                                                {pollRespFieldMappings.map(m => (
                                                    <option key={m.stdField}
                                                            value={m.stdField}>{STANDARD_RESPONSE[m.stdField]?.name || m.stdField}</option>
                                                ))}
                                            </select>
                                            <select
                                                value={tc.type}
                                                onChange={e => {
                                                    const newList = [...pollRespTypeConverts];
                                                    newList[i].type = e.target.value as 'string_to_array' | 'array_to_string';
                                                    setPollRespTypeConverts(newList);
                                                }}
                                                className="w-40 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="string_to_array">字符串→数组</option>
                                                <option value="array_to_string">数组→字符串</option>
                                            </select>
                                            <input
                                                type="text"
                                                value={tc.separator}
                                                onChange={e => {
                                                    const newList = [...pollRespTypeConverts];
                                                    newList[i].separator = e.target.value;
                                                    setPollRespTypeConverts(newList);
                                                }}
                                                className="w-20 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="分隔符"
                                            />
                                            <button type="button"
                                                    onClick={() => setPollRespTypeConverts(pollRespTypeConverts.filter((_, idx) => idx !== i))}
                                                    className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
                                                <X size={14}/>
                                            </button>
                                        </div>
                                    ))
                                )}
                            </div>

                            {/* 成功条件 */}
                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">成功条件</h4>
                                    {!pollRespSuccessCondition ? (
                                        <button
                                            type="button"
                                            onClick={() => setPollRespSuccessCondition({
                                                field: '',
                                                operator: 'eq',
                                                value: ''
                                            })}
                                            className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                        >
                                            + 添加条件
                                        </button>
                                    ) : (
                                        <button
                                            type="button"
                                            onClick={() => setPollRespSuccessCondition(null)}
                                            className="px-3 py-1.5 text-sm text-red-500 hover:bg-red-50 rounded-lg"
                                        >
                                            移除条件
                                        </button>
                                    )}
                                </div>
                                <p className="text-xs text-gray-500 mb-3">配置轮询响应成功的判断条件。不配置时使用默认的
                                    status 字段判断</p>
                                {pollRespSuccessCondition && (
                                    <div className="flex items-center gap-2 mb-2 flex-wrap">
                                        <input
                                            type="text"
                                            value={pollRespSuccessCondition.field}
                                            onChange={e => setPollRespSuccessCondition({
                                                ...pollRespSuccessCondition,
                                                field: e.target.value
                                            })}
                                            className="w-40 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            placeholder="字段路径，如 code"
                                        />
                                        <select
                                            value={pollRespSuccessCondition.operator}
                                            onChange={e => setPollRespSuccessCondition({
                                                ...pollRespSuccessCondition,
                                                operator: e.target.value as SuccessCondition['operator']
                                            })}
                                            className="w-32 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                        >
                                            {SUCCESS_CONDITION_OPERATORS.map(op => (
                                                <option key={op.value} value={op.value}>{op.label}</option>
                                            ))}
                                        </select>
                                        {SUCCESS_CONDITION_OPERATORS.find(o => o.value === pollRespSuccessCondition.operator)?.needValue && (
                                            <input
                                                type="text"
                                                value={pollRespSuccessCondition.value !== undefined ? String(pollRespSuccessCondition.value) : ''}
                                                onChange={e => setPollRespSuccessCondition({
                                                    ...pollRespSuccessCondition,
                                                    value: e.target.value
                                                })}
                                                className="w-32 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="比较值"
                                            />
                                        )}
                                        {SUCCESS_CONDITION_OPERATORS.find(o => o.value === pollRespSuccessCondition.operator)?.needValues && (
                                            <input
                                                type="text"
                                                value={pollRespSuccessCondition.values?.join(',') || ''}
                                                onChange={e => setPollRespSuccessCondition({
                                                    ...pollRespSuccessCondition,
                                                    values: e.target.value.split(',').map(v => v.trim()).filter(v => v)
                                                })}
                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="值列表，逗号分隔"
                                            />
                                        )}
                                    </div>
                                )}
                            </div>
                        </div>
                    )}

                    {/* 回调映射 */}
                    {activeTab === 'callback' && (
                        <div className="space-y-6">
                            <p className="text-xs text-gray-500">仅在结果模式为"回调"时使用，配置三方回调数据的解析规则</p>

                            <div>
                                <h4 className="text-sm font-medium text-gray-900 mb-3">路径配置</h4>
                                <div className="space-y-3">
                                    <div className="flex items-center gap-2">
                                        <label className="w-24 text-sm text-gray-600">任务ID路径</label>
                                        <input
                                            type="text"
                                            value={callbackConfig.task_id_path}
                                            onChange={e => setCallbackConfig({
                                                ...callbackConfig,
                                                task_id_path: e.target.value
                                            })}
                                            className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            placeholder="如 data.taskId"
                                        />
                                    </div>
                                    <div className="flex items-center gap-2">
                                        <label className="w-24 text-sm text-gray-600">状态路径</label>
                                        <input
                                            type="text"
                                            value={callbackConfig.status_path}
                                            onChange={e => setCallbackConfig({
                                                ...callbackConfig,
                                                status_path: e.target.value
                                            })}
                                            className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            placeholder="如 data.state"
                                        />
                                    </div>
                                    <div className="flex items-center gap-2">
                                        <label className="w-24 text-sm text-gray-600">结果路径</label>
                                        <input
                                            type="text"
                                            value={callbackConfig.result_path}
                                            onChange={e => setCallbackConfig({
                                                ...callbackConfig,
                                                result_path: e.target.value
                                            })}
                                            className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            placeholder="如 data.output"
                                        />
                                    </div>
                                </div>
                            </div>

                            <div className="border-t border-gray-200 pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-gray-900">状态值映射</h4>
                                    <button
                                        type="button"
                                        onClick={() => setCallbackStatusMappings([...callbackStatusMappings, {
                                            stdValue: '',
                                            vendorValue: ''
                                        }])}
                                        className="px-3 py-1.5 text-sm text-indigo-600 hover:bg-indigo-50 rounded-lg"
                                    >
                                        + 添加映射
                                    </button>
                                </div>
                                {callbackStatusMappings.length === 0 ? (
                                    <div
                                        className="text-sm text-gray-400 text-center py-4 bg-gray-50 rounded-lg">暂无状态值映射</div>
                                ) : (
                                    callbackStatusMappings.map((m, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <input
                                                type="text"
                                                value={m.vendorValue}
                                                onChange={e => {
                                                    const newList = [...callbackStatusMappings];
                                                    newList[i].vendorValue = e.target.value;
                                                    setCallbackStatusMappings(newList);
                                                }}
                                                className="flex-1 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                                placeholder="三方状态值，如 COMPLETED"
                                            />
                                            <span className="text-gray-400">→</span>
                                            <select
                                                value={m.stdValue}
                                                onChange={e => {
                                                    const newList = [...callbackStatusMappings];
                                                    newList[i].stdValue = e.target.value;
                                                    setCallbackStatusMappings(newList);
                                                }}
                                                className="w-36 px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                                            >
                                                <option value="">系统状态</option>
                                                {STANDARD_STATUS_VALUES.map(v => (
                                                    <option key={v} value={v}>{v}</option>
                                                ))}
                                            </select>
                                            <button type="button"
                                                    onClick={() => setCallbackStatusMappings(callbackStatusMappings.filter((_, idx) => idx !== i))}
                                                    className="p-2 text-gray-400 hover:text-red-500 hover:bg-red-50 rounded-lg">
                                                <X size={14}/>
                                            </button>
                                        </div>
                                    ))
                                )}
                            </div>
                        </div>
                    )}

                    <div className="flex gap-3 pt-6 mt-4 border-t border-gray-200">
                        <button type="button" onClick={onClose}
                                className="flex-1 px-4 py-2 border border-gray-200 rounded-lg text-gray-700 hover:bg-gray-50">取消
                        </button>
                        <button type="submit" disabled={loading}
                                className="flex-1 px-4 py-2 bg-indigo-600 text-white rounded-lg hover:bg-indigo-700 disabled:opacity-50">
                            {loading ? '保存中...' : '保存'}
                        </button>
                    </div>
                </form>
                </>
                )}
            </div>
        </div>
    );
};

const Capabilities: React.FC = () => {
  const [capabilities, setCapabilities] = useState<Capability[]>([]);
  const [channelCapabilities, setChannelCapabilities] = useState<ChannelCapability[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [expandedCapability, setExpandedCapability] = useState<string | null>(null);
  const [searchTerm, setSearchTerm] = useState('');
  const [filterType, setFilterType] = useState('');
  const [filterStatus, setFilterStatus] = useState('all');

    const [capabilityModal, setCapabilityModal] = useState<{
        open: boolean;
        capability: Capability | null
    }>({open: false, capability: null});
    const [ccModal, setCcModal] = useState<{
        open: boolean;
        capabilityCode: string;
        cc: ChannelCapability | null
    }>({open: false, capabilityCode: '', cc: null});

    const channelNameMap = useMemo(() => {
        const map = new Map<string, string>();
        channels.forEach(channel => {
            map.set(channel.id, channel.name);
        });
        return map;
    }, [channels]);

    const channelCapabilitiesByCodeMap = useMemo(() => {
        const map = new Map<string, ChannelCapability[]>();
        channelCapabilities.forEach(cc => {
            const list = map.get(cc.capabilityCode) || [];
            list.push(cc);
            map.set(cc.capabilityCode, list);
        });
        return map;
    }, [channelCapabilities]);

    const stats = useMemo(() => ({
        totalCapabilities: capabilities.length,
        enabledCapabilities: capabilities.filter(cap => cap.status === 1).length,
        totalChannelCapabilities: channelCapabilities.length,
        enabledChannelCapabilities: channelCapabilities.filter(cc => cc.status === 1).length,
    }), [capabilities, channelCapabilities]);

    const filteredCapabilities = useMemo(() => {
        const keyword = searchTerm.trim().toLowerCase();

        return capabilities.filter(cap => {
            const normalizedType = cap.type || 'other';
            const relatedCCs = channelCapabilitiesByCodeMap.get(cap.code) || [];
            const matchesType = !filterType || normalizedType === filterType;
            const matchesStatus = filterStatus === 'all'
                || (filterStatus === 'enabled' && cap.status === 1)
                || (filterStatus === 'disabled' && cap.status !== 1);
            const matchesKeyword = !keyword || [
                cap.name,
                cap.code,
                cap.description,
                normalizedType,
                ...relatedCCs.flatMap(cc => [
                    cc.name,
                    cc.model,
                    cc.requestPath,
                    cc.resultMode,
                    channelNameMap.get(cc.channelId),
                ]),
            ].some(field => normalizeText(field).includes(keyword));

            return matchesType && matchesStatus && matchesKeyword;
        });
    }, [capabilities, channelCapabilitiesByCodeMap, channelNameMap, filterStatus, filterType, searchTerm]);

    const groupedCapabilities = useMemo(() => {
        const groups = new Map<string, Capability[]>();

        filteredCapabilities.forEach(cap => {
            const type = CAPABILITY_TYPE_ORDER.includes((cap.type || 'other') as typeof CAPABILITY_TYPE_ORDER[number])
                ? (cap.type || 'other')
                : 'other';
            const list = groups.get(type) || [];
            list.push(cap);
            groups.set(type, list);
        });

        return CAPABILITY_TYPE_ORDER
            .filter(type => groups.has(type))
            .map(type => {
                const items = (groups.get(type) || []).slice().sort((a, b) => a.name.localeCompare(b.name));
                return {
                    type,
                    label: getCapabilityTypeLabel(type),
                    items,
                    capabilityCount: items.length,
                    enabledCount: items.filter(cap => cap.status === 1).length,
                    channelCapabilityCount: items.reduce((sum, cap) => sum + (channelCapabilitiesByCodeMap.get(cap.code)?.length || 0), 0),
                };
            });
    }, [filteredCapabilities, channelCapabilitiesByCodeMap]);

    const resetFilters = () => {
        setSearchTerm('');
        setFilterType('');
        setFilterStatus('all');
    };

    useEffect(() => {
        loadData();
    }, []);

  const loadData = async () => {
    setIsLoading(true);
    try {
      const [caps, ccs, chs] = await Promise.all([
        fetchCapabilities(),
        fetchChannelCapabilities(),
        fetchChannels(),
      ]);
      setCapabilities(caps);
      setChannelCapabilities(ccs);
      setChannels(chs);
    } finally {
      setIsLoading(false);
    }
  };

  const handleDeleteCapability = async (code: string) => {
      if (!confirm('确定删除该能力定义? 相关的渠道配置也会被删除。')) return;
    await deleteCapability(code);
    loadData();
  };

  const handleDeleteChannelCapability = async (id: string) => {
    if (!confirm('确定删除该渠道能力配置?')) return;
    await deleteChannelCapability(id);
    loadData();
  };

    const handleToggleCapabilityStatus = async (cap: Capability) => {
        await updateCapability(cap.code, {status: cap.status === 1 ? 0 : 1});
        loadData();
    };

    const handleToggleCcStatus = async (cc: ChannelCapability) => {
        await updateChannelCapability(cc.id, {status: cc.status === 1 ? 0 : 1});
        loadData();
    };

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold text-gray-900">能力配置</h1>
            <p className="text-gray-500 mt-1">管理平台能力定义和渠道能力映射</p>
          </div>
        </div>
        <div className="animate-pulse space-y-4">
          {[1, 2, 3].map(i => (
            <div key={i} className="bg-white p-6 rounded-2xl border border-gray-100 h-24"></div>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">能力配置</h1>
          <p className="text-gray-500 mt-1">管理平台能力定义和渠道能力映射</p>
        </div>
          <div className="flex flex-wrap gap-2">
              <button onClick={loadData}
                      className="flex items-center gap-2 px-4 py-2 border border-gray-200 rounded-lg text-gray-600 hover:bg-gray-50">
                  <RefreshCw size={16} className={isLoading ? 'animate-spin' : ''}/>
                  刷新
              </button>
              <button
                  onClick={() => setCapabilityModal({open: true, capability: null})}
                  className="flex items-center gap-2 px-6 py-2 bg-indigo-600 text-white rounded-lg text-sm font-bold hover:bg-indigo-700 transition-all shadow-sm"
              >
                  <Plus size={18}/>
                  新建能力
              </button>
          </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
        <div className="bg-white p-5 rounded-2xl shadow-sm border border-gray-100">
          <p className="text-sm text-gray-500 font-medium">能力总数</p>
          <div className="mt-2 flex items-end justify-between">
            <h3 className="text-2xl font-bold text-gray-900">{stats.totalCapabilities}</h3>
            <div className="p-2 bg-indigo-50 rounded-xl text-indigo-600">
              <Cpu size={18} />
            </div>
          </div>
        </div>
        <div className="bg-white p-5 rounded-2xl shadow-sm border border-gray-100">
          <p className="text-sm text-gray-500 font-medium">已启用能力</p>
          <div className="mt-2 flex items-end justify-between">
            <h3 className="text-2xl font-bold text-gray-900">{stats.enabledCapabilities}</h3>
            <span className="text-xs px-2 py-1 rounded-full bg-green-50 text-green-600">启用中</span>
          </div>
        </div>
        <div className="bg-white p-5 rounded-2xl shadow-sm border border-gray-100">
          <p className="text-sm text-gray-500 font-medium">渠道配置总数</p>
          <div className="mt-2 flex items-end justify-between">
            <h3 className="text-2xl font-bold text-gray-900">{stats.totalChannelCapabilities}</h3>
            <div className="p-2 bg-violet-50 rounded-xl text-violet-600">
              <Settings size={18} />
            </div>
          </div>
        </div>
        <div className="bg-white p-5 rounded-2xl shadow-sm border border-gray-100">
          <p className="text-sm text-gray-500 font-medium">已启用渠道配置</p>
          <div className="mt-2 flex items-end justify-between">
            <h3 className="text-2xl font-bold text-gray-900">{stats.enabledChannelCapabilities}</h3>
            <span className="text-xs px-2 py-1 rounded-full bg-emerald-50 text-emerald-600">运行中</span>
          </div>
        </div>
      </div>

      <div className="bg-white rounded-2xl shadow-sm border border-gray-100 p-4 space-y-4">
        <div className="flex flex-col lg:flex-row gap-4">
          <div className="relative flex-1">
            <Search size={18} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input
              type="text"
              value={searchTerm}
              onChange={e => setSearchTerm(e.target.value)}
              placeholder="搜索能力名、编码、描述、渠道名或渠道配置..."
              className="w-full pl-10 pr-4 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 text-sm"
            />
          </div>
          <div className="lg:w-52">
            <select
              value={filterType}
              onChange={e => setFilterType(e.target.value)}
              className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 text-sm"
            >
              <option value="">全部类型</option>
              {CAPABILITY_TYPES.map(type => (
                <option key={type.value} value={type.value}>{type.label}</option>
              ))}
            </select>
          </div>
          <div className="lg:w-52">
            <select
              value={filterStatus}
              onChange={e => setFilterStatus(e.target.value)}
              className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 text-sm"
            >
              <option value="all">全部状态</option>
              <option value="enabled">仅启用</option>
              <option value="disabled">仅禁用</option>
            </select>
          </div>
          <button
            type="button"
            onClick={resetFilters}
            className="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 transition-colors"
          >
            重置筛选
          </button>
        </div>
        <div className="text-sm text-gray-500">
          共 {stats.totalCapabilities} 个能力，当前显示 {filteredCapabilities.length} 个
        </div>
      </div>

      <div className="space-y-4">
        {capabilities.length === 0 ? (
          <div className="bg-white rounded-2xl border border-gray-100 p-12 text-center">
            <Cpu size={48} className="mx-auto text-gray-300 mb-4" />
            <h3 className="text-lg font-bold text-gray-900 mb-2">暂无能力定义</h3>
            <p className="text-gray-500 mb-4">点击上方按钮创建第一个能力</p>
            <button
              onClick={() => setCapabilityModal({open: true, capability: null})}
              className="inline-flex items-center gap-2 px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-bold hover:bg-indigo-700"
            >
              <Plus size={16} />
              新建能力
            </button>
          </div>
        ) : groupedCapabilities.length === 0 ? (
          <div className="bg-white rounded-2xl border border-gray-100 p-12 text-center">
            <Search size={40} className="mx-auto text-gray-300 mb-4" />
            <h3 className="text-lg font-bold text-gray-900 mb-2">无匹配能力</h3>
            <p className="text-gray-500 mb-4">请调整搜索词或筛选条件后重试</p>
            <button
              type="button"
              onClick={resetFilters}
              className="inline-flex items-center gap-2 px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50"
            >
              重置筛选
            </button>
          </div>
        ) : (
          groupedCapabilities.map(group => (
            <div key={group.type} className="bg-white rounded-2xl border border-gray-100 shadow-sm overflow-hidden">
              <div className="p-5 border-b border-gray-100 bg-gray-50/70">
                <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                  <div className="flex items-center gap-3">
                    <div className={`px-3 py-1.5 rounded-xl text-sm font-semibold ${getCapabilityTypeBadgeClass(group.type)}`}>
                      {group.label}
                    </div>
                    <div>
                      <div className="text-base font-bold text-gray-900">{group.label}能力</div>
                      <div className="text-sm text-gray-500 mt-1">
                        共 {group.capabilityCount} 个能力，已启用 {group.enabledCount} 个，关联 {group.channelCapabilityCount} 个渠道配置
                      </div>
                    </div>
                  </div>
                </div>
              </div>

              <div className="p-4 space-y-4">
                {group.items.map(cap => {
                  const isExpanded = expandedCapability === cap.code;
                  const relatedCCs = channelCapabilitiesByCodeMap.get(cap.code) || [];
                  const enabledCCCount = relatedCCs.filter(cc => cc.status === 1).length;
                  const standardParamCount = Object.keys(cap.standardParams || {}).length;

                  return (
                    <div key={cap.code} className="rounded-2xl border border-gray-100 bg-white shadow-sm overflow-hidden">
                      <div
                        className="p-5 flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between cursor-pointer hover:bg-gray-50"
                        onClick={() => setExpandedCapability(isExpanded ? null : cap.code)}
                      >
                        <div className="flex items-start gap-4 min-w-0 flex-1">
                          <div className="pt-1 text-gray-400 shrink-0">
                            {isExpanded ? <ChevronDown size={20} /> : <ChevronRight size={20} />}
                          </div>
                          <div className="p-2.5 bg-indigo-50 text-indigo-600 rounded-xl shrink-0">
                            <Cpu size={20} />
                          </div>
                          <div className="min-w-0 flex-1 space-y-3">
                            <div className="flex flex-wrap items-center gap-2">
                              <h3 className="text-lg font-bold text-gray-900">{cap.name}</h3>
                              <span className={`text-xs px-2 py-1 rounded-full ${cap.status === 1 ? 'bg-green-50 text-green-600' : 'bg-red-50 text-red-600'}`}>
                                {cap.status === 1 ? '启用' : '禁用'}
                              </span>
                            </div>
                            <div className="flex flex-wrap items-center gap-2 text-sm">
                              <code className="px-2 py-1 bg-gray-100 rounded text-gray-600">{cap.code}</code>
                              <span className={`px-2 py-1 rounded ${getCapabilityTypeBadgeClass(cap.type)}`}>
                                {getCapabilityTypeLabel(cap.type)}
                              </span>
                              <span className="px-2 py-1 rounded bg-gray-100 text-gray-600">渠道配置 {relatedCCs.length}</span>
                              <span className="px-2 py-1 rounded bg-gray-100 text-gray-600">标准参数 {standardParamCount}</span>
                            </div>
                            <p className="text-sm text-gray-500 leading-6">{cap.description || '暂无描述'}</p>
                          </div>
                        </div>
                        <div className="flex items-center gap-2 shrink-0 self-end xl:self-start">
                          <button
                            onClick={e => {
                              e.stopPropagation();
                              handleToggleCapabilityStatus(cap);
                            }}
                            className={`inline-flex items-center gap-1 px-3 py-2 rounded-lg text-sm ${cap.status === 1 ? 'text-yellow-700 bg-yellow-50 hover:bg-yellow-100' : 'text-green-700 bg-green-50 hover:bg-green-100'}`}
                            title={cap.status === 1 ? '禁用' : '启用'}
                          >
                            <Power size={16} />
                            {cap.status === 1 ? '禁用' : '启用'}
                          </button>
                          <button
                            onClick={e => {
                              e.stopPropagation();
                              setCapabilityModal({open: true, capability: cap});
                            }}
                            className="inline-flex items-center gap-1 px-3 py-2 text-sm text-indigo-600 bg-indigo-50 hover:bg-indigo-100 rounded-lg"
                          >
                            <Edit2 size={16} />
                            编辑
                          </button>
                          <button
                            onClick={e => {
                              e.stopPropagation();
                              handleDeleteCapability(cap.code);
                            }}
                            className="inline-flex items-center gap-1 px-3 py-2 text-sm text-red-600 bg-red-50 hover:bg-red-100 rounded-lg"
                          >
                            <Trash2 size={16} />
                            删除
                          </button>
                        </div>
                      </div>

                      {isExpanded && (
                        <div className="border-t border-gray-100 bg-gray-50/70 p-4">
                          <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between mb-4">
                            <div>
                              <h4 className="text-sm font-bold text-gray-800">渠道能力配置</h4>
                              <p className="text-sm text-gray-500 mt-1">
                                共 {relatedCCs.length} 个配置，已启用 {enabledCCCount} 个
                              </p>
                            </div>
                            <button
                              onClick={() => setCcModal({open: true, capabilityCode: cap.code, cc: null})}
                              className="inline-flex items-center gap-2 px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-bold hover:bg-indigo-700"
                            >
                              <Plus size={14} />
                              添加渠道配置
                            </button>
                          </div>

                          {relatedCCs.length === 0 ? (
                            <div className="bg-white border border-dashed border-gray-200 rounded-2xl px-6 py-10 text-center">
                              <Settings size={32} className="mx-auto text-gray-300 mb-3" />
                              <div className="text-sm font-medium text-gray-700">当前能力还没有渠道配置</div>
                              <div className="text-sm text-gray-500 mt-1 mb-4">可以立即添加一个渠道配置来接入具体渠道能力</div>
                              <button
                                onClick={() => setCcModal({open: true, capabilityCode: cap.code, cc: null})}
                                className="inline-flex items-center gap-2 px-4 py-2 bg-indigo-600 text-white rounded-lg text-sm font-bold hover:bg-indigo-700"
                              >
                                <Plus size={14} />
                                添加渠道配置
                              </button>
                            </div>
                          ) : (
                            <div className="space-y-3">
                              {relatedCCs.map(cc => (
                                <div key={cc.id} className="bg-white p-4 rounded-2xl border border-gray-200 shadow-sm flex flex-col gap-4 xl:flex-row xl:items-center xl:justify-between">
                                  <div className="flex items-start gap-4 min-w-0 flex-1">
                                    <div className="p-2 bg-gray-100 rounded-xl shrink-0">
                                      <Settings size={16} className="text-gray-600" />
                                    </div>
                                    <div className="min-w-0 flex-1 space-y-2">
                                      <div className="flex flex-wrap items-center gap-2">
                                        <span className="font-semibold text-gray-900">{cc.name || cc.model || '未命名'}</span>
                                        <span className="text-xs px-2 py-1 rounded-full bg-blue-50 text-blue-600">
                                          {channelNameMap.get(cc.channelId) || cc.channelId}
                                        </span>
                                        <span className="text-xs px-2 py-1 rounded-full bg-purple-50 text-purple-600">
                                          {cc.resultMode}
                                        </span>
                                        <span className={`text-xs px-2 py-1 rounded-full ${cc.status === 1 ? 'bg-green-50 text-green-600' : 'bg-red-50 text-red-600'}`}>
                                          {cc.status === 1 ? '启用' : '禁用'}
                                        </span>
                                      </div>
                                      <div className="flex flex-wrap gap-3 text-sm text-gray-500">
                                        <span>{cc.requestMethod} {cc.requestPath || '未配置路径'}</span>
                                        <span>价格 {formatPrice(cc.price)}/{cc.priceUnit || 'request'}</span>
                                        {cc.model ? <span>模型 {cc.model}</span> : null}
                                      </div>
                                    </div>
                                  </div>
                                  <div className="flex flex-wrap items-center gap-2 shrink-0">
                                    <button
                                      onClick={() => handleToggleCcStatus(cc)}
                                      className={`inline-flex items-center gap-1 px-3 py-2 rounded-lg text-sm ${cc.status === 1 ? 'text-yellow-700 bg-yellow-50 hover:bg-yellow-100' : 'text-green-700 bg-green-50 hover:bg-green-100'}`}
                                      title={cc.status === 1 ? '禁用' : '启用'}
                                    >
                                      <Power size={14} />
                                      {cc.status === 1 ? '禁用' : '启用'}
                                    </button>
                                    <button
                                      onClick={() => setCcModal({open: true, capabilityCode: cap.code, cc})}
                                      className="inline-flex items-center gap-1 px-3 py-2 text-sm text-indigo-600 bg-indigo-50 hover:bg-indigo-100 rounded-lg"
                                    >
                                      <Edit2 size={14} />
                                      编辑
                                    </button>
                                    <button
                                      onClick={() => handleDeleteChannelCapability(cc.id)}
                                      className="inline-flex items-center gap-1 px-3 py-2 text-sm text-red-600 bg-red-50 hover:bg-red-100 rounded-lg"
                                    >
                                      <Trash2 size={14} />
                                      删除
                                    </button>
                                  </div>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          ))
        )}
      </div>

        <CapabilityModal
            isOpen={capabilityModal.open}
            capability={capabilityModal.capability}
            onClose={() => setCapabilityModal({open: false, capability: null})}
            onSave={loadData}
        />
        <ChannelCapabilityModal
            isOpen={ccModal.open}
            capabilityCode={ccModal.capabilityCode}
            channelCapability={ccModal.cc}
            channels={channels}
            capabilities={capabilities}
            onClose={() => setCcModal({open: false, capabilityCode: '', cc: null})}
            onSave={loadData}
        />
    </div>
  );
};

export default Capabilities;
