import React, { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import { Modal } from '../../components/ui/Modal';
import JsonEditor from '../../components/ui/JsonEditor';
import { createChannelCapability, updateChannelCapability } from '../../services/api';
import { ChannelCapability, Channel, Capability } from '../../types';
import {
    RESULT_MODES, STANDARD_PARAMS, STANDARD_RESPONSE, POLL_PARAMS,
    STANDARD_STATUS_VALUES, formatPrice,
} from './constants';
import { FieldMappingRow, ValueMappingRow, FixedParamRow } from './MappingRows';
import {
    FieldMapping, ValueMapping, FixedParam, TypeConvert, SuccessCondition,
    SUCCESS_CONDITION_OPERATORS, CONFIG_TEMPLATES,
    parseParamMapping, parseResponseMapping, buildParamMapping, buildResponseMapping,
} from './mappingUtils';


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
    const [activeTab, setActiveTab] = useState<'basic' | 'request' | 'param' | 'response' | 'poll_response' | 'callback' | 'schema'>('basic');
    const [form, setForm] = useState({
        channel_id: 0, capability_code: '', model: '', name: '', price: 0, price_unit: 'request',
        result_mode: 'poll', request_path: '', request_method: 'POST', content_type: 'application/json',
        auth_location: 'header', auth_key: 'Authorization', auth_value_prefix: 'Bearer ',
        poll_path: '', poll_method: 'GET', poll_interval: 5, poll_max_attempts: 60, transfer_enabled: false,
    });
    const [paramFieldMappings, setParamFieldMappings] = useState<FieldMapping[]>([]);
    const [paramValueMappings, setParamValueMappings] = useState<ValueMapping[]>([]);
    const [paramFixedParams, setParamFixedParams] = useState<FixedParam[]>([]);
    const [paramTypeConverts, setParamTypeConverts] = useState<TypeConvert[]>([]);
    const [respFieldMappings, setRespFieldMappings] = useState<FieldMapping[]>([]);
    const [respValueMappings, setRespValueMappings] = useState<ValueMapping[]>([]);
    const [respTypeConverts, setRespTypeConverts] = useState<TypeConvert[]>([]);
    const [respSuccessCondition, setRespSuccessCondition] = useState<SuccessCondition | null>(null);
    const [pollRespFieldMappings, setPollRespFieldMappings] = useState<FieldMapping[]>([]);
    const [pollRespValueMappings, setPollRespValueMappings] = useState<ValueMapping[]>([]);
    const [pollRespTypeConverts, setPollRespTypeConverts] = useState<TypeConvert[]>([]);
    const [pollRespSuccessCondition, setPollRespSuccessCondition] = useState<SuccessCondition | null>(null);
    const [useSeparatePollMapping, setUseSeparatePollMapping] = useState(false);
    const [pollParamFieldMappings, setPollParamFieldMappings] = useState<FieldMapping[]>([]);
    const [pollParamFixedParams, setPollParamFixedParams] = useState<FixedParam[]>([]);
    const [callbackConfig, setCallbackConfig] = useState({ task_id_path: '', status_path: '', result_path: '' });
    const [callbackStatusMappings, setCallbackStatusMappings] = useState<{stdValue: string; vendorValue: string}[]>([]);
    const [paramSchemaJson, setParamSchemaJson] = useState('');
    const [paramSchemaError, setParamSchemaError] = useState('');
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
                transfer_enabled: channelCapability.extraConfig?.transfer_enabled === true,
            });
            const paramData = parseParamMapping(channelCapability.paramMapping || {});
            setParamFieldMappings(paramData.fieldMappings);
            setParamValueMappings(paramData.valueMappings);
            setParamFixedParams(paramData.fixedParams);
            setParamTypeConverts(paramData.typeConverts);
            const respData = parseResponseMapping(channelCapability.responseMapping || {});
            setRespFieldMappings(respData.fieldMappings);
            setRespValueMappings(respData.valueMappings);
            setRespTypeConverts(respData.typeConverts);
            setRespSuccessCondition(respData.successCondition);
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
                setPollRespFieldMappings([]); setPollRespValueMappings([]); setPollRespTypeConverts([]); setPollRespSuccessCondition(null);
            }
            const pollParamMapping = channelCapability.pollParamMapping || {};
            if (Object.keys(pollParamMapping).length > 0) {
                const pollParamData = parseParamMapping(pollParamMapping);
                setPollParamFieldMappings(pollParamData.fieldMappings);
                setPollParamFixedParams(pollParamData.fixedParams);
            } else {
                setPollParamFieldMappings([]); setPollParamFixedParams([]);
            }
            const cbMapping = channelCapability.callbackMapping || {};
            setCallbackConfig({ task_id_path: cbMapping.task_id_path || '', status_path: cbMapping.status_path || '', result_path: cbMapping.result_path || '' });
            const cbStatusMappings: {stdValue: string; vendorValue: string}[] = [];
            if (cbMapping.status_mapping) {
                Object.entries(cbMapping.status_mapping).forEach(([vendor, std]) => {
                    cbStatusMappings.push({stdValue: std as string, vendorValue: vendor});
                });
            }
            setCallbackStatusMappings(cbStatusMappings);
            const ps = (channelCapability as any).paramSchema;
            setParamSchemaJson(ps && Object.keys(ps).length > 0 ? JSON.stringify(ps, null, 2) : '');
        } else {
            setForm({
                channel_id: channels[0]?.id ? Number(channels[0].id) : 0,
                capability_code: capabilityCode, model: '', name: '', price: 0, price_unit: 'request',
                result_mode: 'poll', request_path: '', request_method: 'POST', content_type: 'application/json',
                auth_location: 'header', auth_key: 'Authorization', auth_value_prefix: 'Bearer ',
                poll_path: '', poll_method: 'GET', poll_interval: 5, poll_max_attempts: 60,
            });
            setParamFieldMappings([]); setParamValueMappings([]); setParamFixedParams([]); setParamTypeConverts([]);
            setRespFieldMappings([]); setRespValueMappings([]); setRespTypeConverts([]); setRespSuccessCondition(null);
            setPollRespFieldMappings([]); setPollRespValueMappings([]); setPollRespTypeConverts([]); setPollRespSuccessCondition(null);
            setUseSeparatePollMapping(false); setPollParamFieldMappings([]); setPollParamFixedParams([]);
            setCallbackConfig({task_id_path: '', status_path: '', result_path: ''}); setCallbackStatusMappings([]);
            setParamSchemaJson('');
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
            const pollResponseMapping = useSeparatePollMapping
                ? buildResponseMapping(pollRespFieldMappings, pollRespValueMappings, pollRespTypeConverts, pollRespSuccessCondition)
                : null;
            const callbackMapping: Record<string, any> = {};
            if (callbackConfig.task_id_path) callbackMapping.task_id_path = callbackConfig.task_id_path;
            if (callbackConfig.status_path) callbackMapping.status_path = callbackConfig.status_path;
            if (callbackConfig.result_path) callbackMapping.result_path = callbackConfig.result_path;
            if (callbackStatusMappings.length > 0) {
                const statusMap: Record<string, string> = {};
                callbackStatusMappings.forEach(m => { if (m.vendorValue && m.stdValue) statusMap[m.vendorValue] = m.stdValue; });
                if (Object.keys(statusMap).length > 0) callbackMapping.status_mapping = statusMap;
            }
            const data: Record<string, any> = {
                channel_id: form.channel_id, model_code: form.capability_code, model: form.model, name: form.name,
                price: form.price, price_unit: form.price_unit, result_mode: form.result_mode,
                request_path: form.request_path, request_method: form.request_method, content_type: form.content_type,
                auth_location: form.auth_location, auth_key: form.auth_key, auth_value_prefix: form.auth_value_prefix,
                poll_path: form.poll_path, poll_method: form.poll_method, poll_interval: form.poll_interval,
                poll_max_attempts: form.poll_max_attempts, param_mapping: paramMapping, callback_mapping: callbackMapping,
                extra_config: {transfer_enabled: form.transfer_enabled},
            };
            if (paramSchemaJson.trim()) {
                try { data.param_schema = JSON.parse(paramSchemaJson); } catch { setParamSchemaError('JSON 格式错误'); setLoading(false); return; }
            } else {
                data.param_schema = null;
            }
            if (form.result_mode === 'sync' || form.result_mode === 'poll') data.response_mapping = responseMapping;
            if (form.result_mode === 'poll') {
                if (pollResponseMapping) data.poll_response_mapping = pollResponseMapping;
                if (Object.keys(buildParamMapping(pollParamFieldMappings, [], pollParamFixedParams)).length > 0) {
                    data.poll_param_mapping = buildParamMapping(pollParamFieldMappings, [], pollParamFixedParams);
                }
            }
            if (channelCapability) {
                await updateChannelCapability(channelCapability.id, data as Parameters<typeof updateChannelCapability>[1]);
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
        {key: 'schema', label: '参数Schema'},
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
        return [...baseTabs, {key: 'callback', label: '回调映射'}];
    })();

    const addParamFieldMapping = (stdField: string) => {
        if (!paramFieldMappings.find(m => m.stdField === stdField))
            setParamFieldMappings([...paramFieldMappings, {stdField, vendorField: ''}]);
    };
    const addRespFieldMapping = (stdField: string) => {
        if (!respFieldMappings.find(m => m.stdField === stdField))
            setRespFieldMappings([...respFieldMappings, {stdField, vendorField: ''}]);
    };
    const addPollRespFieldMapping = (stdField: string) => {
        if (!pollRespFieldMappings.find(m => m.stdField === stdField))
            setPollRespFieldMappings([...pollRespFieldMappings, {stdField, vendorField: ''}]);
    };
    const addPollParamFieldMapping = (stdField: string) => {
        if (!pollParamFieldMappings.find(m => m.stdField === stdField))
            setPollParamFieldMappings([...pollParamFieldMappings, {stdField, vendorField: ''}]);
    };

    return (
        <Modal open={true} onClose={onClose} title={channelCapability ? '编辑渠道能力配置' : '新建渠道能力配置'} width="max-w-3xl">
                {!templateSelected && !channelCapability ? (
                    <div className="max-h-[70vh] overflow-y-auto">
                        <p className="text-sm text-[var(--text-secondary)] mb-4">选择一个模板快速开始，或从空白自定义配置</p>
                        <div className="grid grid-cols-2 gap-3">
                            {Object.entries(CONFIG_TEMPLATES).map(([key, tpl]) => (
                                <button key={key} type="button"
                                    onClick={() => {
                                        setForm(prev => ({...prev, ...tpl.form}));
                                        if (tpl.paramFieldMappings) setParamFieldMappings(tpl.paramFieldMappings);
                                        if (tpl.respFieldMappings) setRespFieldMappings(tpl.respFieldMappings);
                                        if (tpl.respSuccessCondition !== undefined) setRespSuccessCondition(tpl.respSuccessCondition);
                                        if (tpl.paramFixedParams) setParamFixedParams(tpl.paramFixedParams);
                                        setTemplateSelected(true);
                                    }}
                                    className="text-left p-4 rounded-xl border border-[var(--border-soft)] hover:border-indigo-300 hover:bg-[var(--primary-lighter)] transition-colors"
                                >
                                    <div className="font-medium text-sm text-[var(--text-primary)]">{tpl.label}</div>
                                    <div className="text-xs text-[var(--text-secondary)] mt-1">{tpl.description}</div>
                                </button>
                            ))}
                        </div>
                    </div>
                ) : (
                <>
                <div className="flex border-b border-[var(--border-soft)] mb-4 overflow-x-auto">
                    {tabs.map(tab => (
                        <button key={tab.key} type="button" onClick={() => setActiveTab(tab.key as any)}
                            className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors whitespace-nowrap ${activeTab === tab.key ? 'border-indigo-600 text-[var(--primary)]' : 'border-transparent text-[var(--text-secondary)] hover:text-[var(--text-primary)]'}`}>
                            {tab.label}
                        </button>
                    ))}
                </div>

                <form onSubmit={handleSubmit} className="max-h-[70vh] overflow-y-auto">
                    {/* 基本信息 */}
                    {activeTab === 'basic' && (
                        <div className="space-y-4">
                            <div className="grid grid-cols-2 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">渠道 <span className="text-red-500">*</span></label>
                                    <select value={form.channel_id} onChange={e => setForm({...form, channel_id: Number(e.target.value)})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" required>
                                        <option value={0}>选择渠道</option>
                                        {channels.map(ch => (<option key={ch.id} value={ch.id}>{ch.name} ({ch.type})</option>))}
                                    </select>
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">能力编码 <span className="text-red-500">*</span></label>
                                    <select value={form.capability_code} onChange={e => setForm({...form, capability_code: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" required>
                                        <option value="">选择能力</option>
                                        {capabilities.map(cap => (<option key={cap.code} value={cap.code}>{cap.name} ({cap.code})</option>))}
                                    </select>
                                </div>
                            </div>
                            <div className="grid grid-cols-2 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">配置名称</label>
                                    <input type="text" value={form.name} onChange={e => setForm({...form, name: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                        placeholder="如: 官方渠道-高质量" />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">模型标识</label>
                                    <input type="text" value={form.model} onChange={e => setForm({...form, model: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                        placeholder="如: midjourney-v6" />
                                </div>
                            </div>
                            <div className="grid grid-cols-3 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">结果模式</label>
                                    <select value={form.result_mode} onChange={e => setForm({...form, result_mode: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                        {RESULT_MODES.map(m => (<option key={m.value} value={m.value}>{m.label}</option>))}
                                    </select>
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">单价</label>
                                    <input type="number" value={form.price} onChange={e => setForm({...form, price: Number(e.target.value)})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                        step="0.0001" min="0" />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">计价单位</label>
                                    <select value={form.price_unit} onChange={e => setForm({...form, price_unit: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                        <option value="request">按请求</option>
                                        <option value="second">按秒</option>
                                        <option value="image">按图片</option>
                                    </select>
                                </div>
                            </div>
                            <div className="flex items-center gap-2 mt-2">
                                <input type="checkbox" id="transfer_enabled" checked={form.transfer_enabled}
                                    onChange={e => setForm({...form, transfer_enabled: e.target.checked})}
                                    className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded focus:ring-[var(--primary)]" />
                                <label htmlFor="transfer_enabled" className="text-sm text-[var(--text-primary)]">结果文件转存到 OSS</label>
                            </div>
                        </div>
                    )}

                    {/* 请求配置 */}
                    {activeTab === 'request' && (
                        <div className="space-y-4">
                            <div>
                                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">请求路径 <span className="text-red-500">*</span></label>
                                <input type="text" value={form.request_path} onChange={e => setForm({...form, request_path: e.target.value})}
                                    className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                    placeholder="/api/v1/images/generate" required />
                            </div>
                            <div className="grid grid-cols-2 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">请求方法</label>
                                    <select value={form.request_method} onChange={e => setForm({...form, request_method: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                        <option value="POST">POST</option>
                                        <option value="GET">GET</option>
                                    </select>
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">Content-Type</label>
                                    <select value={form.content_type} onChange={e => setForm({...form, content_type: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                        <option value="application/json">application/json</option>
                                        <option value="application/x-www-form-urlencoded">application/x-www-form-urlencoded</option>
                                        <option value="multipart/form-data">multipart/form-data</option>
                                    </select>
                                </div>
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4 mt-4">
                                <h4 className="text-sm font-medium text-[var(--text-primary)] mb-3">认证配置</h4>
                            </div>
                            <div className="grid grid-cols-3 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">认证位置</label>
                                    <select value={form.auth_location} onChange={e => setForm({...form, auth_location: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                        <option value="header">请求头 (Header)</option>
                                        <option value="body">请求体 (Body)</option>
                                        <option value="query">URL参数 (Query)</option>
                                    </select>
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">参数名</label>
                                    <input type="text" value={form.auth_key} onChange={e => setForm({...form, auth_key: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                        placeholder="Authorization" />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">值前缀</label>
                                    <input type="text" value={form.auth_value_prefix} onChange={e => setForm({...form, auth_value_prefix: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                        placeholder="Bearer " />
                                </div>
                            </div>
                            {(form.result_mode === 'poll' || form.result_mode === 'callback') && (
                                <div className="border-t border-[var(--border-soft)] pt-4 mt-4 space-y-4">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">轮询配置{form.result_mode === 'callback' && '（回调兜底，选填）'}</h4>
                                    <div>
                                        <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">轮询路径</label>
                                        <input type="text" value={form.poll_path} onChange={e => setForm({...form, poll_path: e.target.value})}
                                            className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                            placeholder="/api/v1/tasks/{task_id}" />
                                        <p className="text-xs text-[var(--text-secondary)] mt-1">支持 {'{task_id}'} 占位符</p>
                                    </div>
                                    <div className="grid grid-cols-2 gap-4">
                                        <div>
                                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">轮询方法</label>
                                            <select value={form.poll_method} onChange={e => setForm({...form, poll_method: e.target.value})}
                                                className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="GET">GET</option>
                                                <option value="POST">POST</option>
                                            </select>
                                        </div>
                                        <div>
                                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">独立轮询响应映射</label>
                                            <div className="flex items-center gap-2 mt-2">
                                                <input type="checkbox" checked={useSeparatePollMapping} onChange={e => setUseSeparatePollMapping(e.target.checked)}
                                                    className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded focus:ring-[var(--primary)]" />
                                                <span className="text-sm text-[var(--text-secondary)]">启用独立轮询响应映射</span>
                                            </div>
                                        </div>
                                    </div>
                                    <div className="grid grid-cols-2 gap-4">
                                        <div>
                                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">轮询间隔 (秒)</label>
                                            <input type="number" value={form.poll_interval} onChange={e => setForm({...form, poll_interval: Number(e.target.value)})}
                                                className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                min={1} max={60} />
                                        </div>
                                        <div>
                                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">最大轮询次数</label>
                                            <input type="number" value={form.poll_max_attempts} onChange={e => setForm({...form, poll_max_attempts: Number(e.target.value)})}
                                                className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                min={1} max={1000} />
                                        </div>
                                    </div>
                                    {form.poll_method === 'POST' && (
                                        <div className="border-t border-[var(--border-soft)] pt-4 mt-4">
                                            <h4 className="text-sm font-medium text-[var(--text-primary)] mb-3">轮询请求参数</h4>
                                            <p className="text-xs text-[var(--text-secondary)] mb-3">配置 POST 轮询请求的参数映射</p>
                                            <div className="mb-4">
                                                <div className="flex items-center justify-between mb-2">
                                                    <span className="text-sm text-[var(--text-primary)]">字段映射</span>
                                                    <select onChange={e => { if (e.target.value) addPollParamFieldMapping(e.target.value); e.target.value = ''; }}
                                                        className="px-3 py-1.5 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                        <option value="">+ 添加字段</option>
                                                        {Object.entries(POLL_PARAMS).filter(([key]) => !pollParamFieldMappings.find(m => m.stdField === key)).map(([key, def]) => (
                                                            <option key={key} value={key}>{def.name} ({key})</option>
                                                        ))}
                                                    </select>
                                                </div>
                                                {pollParamFieldMappings.length === 0 ? (
                                                    <div className="text-sm text-[var(--text-secondary)] text-center py-3 bg-[var(--surface)] rounded-lg">暂无字段映射</div>
                                                ) : (
                                                    pollParamFieldMappings.map((m, i) => (
                                                        <FieldMappingRow key={m.stdField} stdField={m.stdField} stdName={POLL_PARAMS[m.stdField]?.name || m.stdField}
                                                            vendorField={m.vendorField}
                                                            onChange={val => { const newList = [...pollParamFieldMappings]; newList[i].vendorField = val; setPollParamFieldMappings(newList); }}
                                                            onRemove={() => setPollParamFieldMappings(pollParamFieldMappings.filter((_, idx) => idx !== i))} />
                                                    ))
                                                )}
                                            </div>
                                            <div>
                                                <div className="flex items-center justify-between mb-2">
                                                    <span className="text-sm text-[var(--text-primary)]">固定参数</span>
                                                    <button type="button" onClick={() => setPollParamFixedParams([...pollParamFixedParams, {name: '', value: ''}])}
                                                        className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加参数</button>
                                                </div>
                                                {pollParamFixedParams.length === 0 ? (
                                                    <div className="text-sm text-[var(--text-secondary)] text-center py-3 bg-[var(--surface)] rounded-lg">暂无固定参数</div>
                                                ) : (
                                                    pollParamFixedParams.map((p, i) => (
                                                        <div key={i} className="flex items-center gap-2 mb-2">
                                                            <input type="text" value={p.name} onChange={e => { const newList = [...pollParamFixedParams]; newList[i].name = e.target.value; setPollParamFixedParams(newList); }}
                                                                placeholder="参数名" className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                                                            <span className="text-[var(--text-secondary)]">=</span>
                                                            <input type="text" value={p.value} onChange={e => { const newList = [...pollParamFixedParams]; newList[i].value = e.target.value; setPollParamFixedParams(newList); }}
                                                                placeholder="固定值" className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" />
                                                            <button type="button" onClick={() => setPollParamFixedParams(pollParamFixedParams.filter((_, idx) => idx !== i))}
                                                                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"><X size={14}/></button>
                                                        </div>
                                                    ))
                                                )}
                                            </div>
                                        </div>
                                    )}
                                </div>
                            )}
                        </div>
                    )}

                    {/* 参数Schema */}
                    {activeTab === 'schema' && (
                        <div className="space-y-3">
                            <p className="text-xs text-[var(--text-secondary)]">覆盖能力级别的参数定义。留空则使用能力默认 schema。Playground 会根据此 schema 渲染输入表单。</p>
                            {paramSchemaError && <p className="text-xs text-red-500">{paramSchemaError}</p>}
                            <JsonEditor value={paramSchemaJson} onChange={v => { setParamSchemaJson(v); setParamSchemaError(''); }}
                                height="300px" placeholder='{"prompt": {"type": "string", "name": "描述文本", "required": true}}' />
                            <div className="flex items-center gap-2">
                                <button type="button" onClick={() => { try { setParamSchemaJson(JSON.stringify(JSON.parse(paramSchemaJson), null, 2)); setParamSchemaError(''); } catch (e: any) { setParamSchemaError(e.message); } }}
                                    className="px-3 py-1.5 text-xs border border-[var(--border-soft)] rounded-lg hover:bg-[var(--surface)] transition-colors" disabled={!paramSchemaJson.trim()}>格式化</button>
                                <button type="button" onClick={() => { const cap = capabilities.find(c => c.code === form.capability_code); if (cap?.standardParams && Object.keys(cap.standardParams).length > 0) { setParamSchemaJson(JSON.stringify(cap.standardParams, null, 2)); setParamSchemaError(''); } }}
                                    className="px-3 py-1.5 text-xs border border-[var(--border-soft)] rounded-lg hover:bg-[var(--surface)] transition-colors">从能力复制</button>
                            </div>
                        </div>
                    )}

                    {/* 参数映射 */}
                    {activeTab === 'param' && (
                        <div className="space-y-6">
                            {paramFieldMappings.length === 0 && paramValueMappings.length === 0 && paramFixedParams.length === 0 && paramTypeConverts.length === 0 && (
                                <div className="rounded-lg border border-blue-200 bg-blue-50 px-4 py-3 text-sm text-blue-700">
                                    当前为透传模式：请求参数将原样转发给上游 API，无需配置映射。如需自定义参数转换，请在下方添加。
                                </div>
                            )}
                            <div>
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">字段映射</h4>
                                    <select onChange={e => { if (e.target.value) addParamFieldMapping(e.target.value); e.target.value = ''; }}
                                        className="px-3 py-1.5 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                        <option value="">+ 添加字段</option>
                                        {Object.entries(STANDARD_PARAMS).filter(([key]) => !paramFieldMappings.find(m => m.stdField === key)).map(([key, def]) => (
                                            <option key={key} value={key}>{def.name} ({key})</option>
                                        ))}
                                    </select>
                                </div>
                                <p className="text-xs text-[var(--text-secondary)] mb-3">配置系统标准参数字段到三方接口字段的映射</p>
                                {paramFieldMappings.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无字段映射，请从上方添加</div>
                                ) : (
                                    paramFieldMappings.map((m, i) => (
                                        <FieldMappingRow key={m.stdField} stdField={m.stdField} stdName={STANDARD_PARAMS[m.stdField]?.name || m.stdField}
                                            vendorField={m.vendorField}
                                            onChange={val => { const newList = [...paramFieldMappings]; newList[i].vendorField = val; setParamFieldMappings(newList); }}
                                            onRemove={() => setParamFieldMappings(paramFieldMappings.filter((_, idx) => idx !== i))} />
                                    ))
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">值映射</h4>
                                    <button type="button" onClick={() => setParamValueMappings([...paramValueMappings, {field: '', stdValue: '', vendorValue: ''}])}
                                        className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加映射</button>
                                </div>
                                <p className="text-xs text-[var(--text-secondary)] mb-3">配置枚举值的映射（如将标准值 realistic 映射为三方的 photo）</p>
                                {paramValueMappings.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无值映射</div>
                                ) : (
                                    paramValueMappings.map((m, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <select value={m.field} onChange={e => { const newList = [...paramValueMappings]; newList[i].field = e.target.value; setParamValueMappings(newList); }}
                                                className="w-36 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="">选择字段</option>
                                                {paramFieldMappings.map(fm => (<option key={fm.stdField} value={fm.stdField}>{STANDARD_PARAMS[fm.stdField]?.name || fm.stdField}</option>))}
                                            </select>
                                            <ValueMappingRow stdValue={m.stdValue} vendorValue={m.vendorValue}
                                                onChange={val => { const newList = [...paramValueMappings]; newList[i].vendorValue = val; setParamValueMappings(newList); }}
                                                onRemove={() => setParamValueMappings(paramValueMappings.filter((_, idx) => idx !== i))} />
                                        </div>
                                    ))
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">固定参数</h4>
                                    <button type="button" onClick={() => setParamFixedParams([...paramFixedParams, {name: '', value: ''}])}
                                        className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加参数</button>
                                </div>
                                <p className="text-xs text-[var(--text-secondary)] mb-3">配置每次请求都会附加的固定参数</p>
                                {paramFixedParams.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无固定参数</div>
                                ) : (
                                    paramFixedParams.map((p, i) => (
                                        <FixedParamRow key={i} paramName={p.name} paramValue={p.value}
                                            onNameChange={val => { const newList = [...paramFixedParams]; newList[i].name = val; setParamFixedParams(newList); }}
                                            onValueChange={val => { const newList = [...paramFixedParams]; newList[i].value = val; setParamFixedParams(newList); }}
                                            onRemove={() => setParamFixedParams(paramFixedParams.filter((_, idx) => idx !== i))} />
                                    ))
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">类型转换</h4>
                                    <button type="button" onClick={() => setParamTypeConverts([...paramTypeConverts, {field: '', type: 'array_to_string', separator: ','}])}
                                        className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加转换</button>
                                </div>
                                <p className="text-xs text-[var(--text-secondary)] mb-3">配置参数类型转换（如将数组转为逗号分隔的字符串）</p>
                                {paramTypeConverts.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无类型转换</div>
                                ) : (
                                    paramTypeConverts.map((tc, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <select value={tc.field} onChange={e => { const newList = [...paramTypeConverts]; newList[i].field = e.target.value; setParamTypeConverts(newList); }}
                                                className="w-40 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="">选择字段</option>
                                                {paramFieldMappings.map(m => (<option key={m.stdField} value={m.stdField}>{STANDARD_PARAMS[m.stdField]?.name || m.stdField}</option>))}
                                            </select>
                                            <select value={tc.type} onChange={e => { const newList = [...paramTypeConverts]; newList[i].type = e.target.value as 'string_to_array' | 'array_to_string'; setParamTypeConverts(newList); }}
                                                className="w-44 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="array_to_string">数组→字符串</option>
                                                <option value="string_to_array">字符串→数组</option>
                                            </select>
                                            <input type="text" value={tc.separator} onChange={e => { const newList = [...paramTypeConverts]; newList[i].separator = e.target.value; setParamTypeConverts(newList); }}
                                                className="w-24 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" placeholder="分隔符" />
                                            <span className="text-xs text-[var(--text-secondary)] whitespace-nowrap">用 \n 表示换行</span>
                                            <button type="button" onClick={() => setParamTypeConverts(paramTypeConverts.filter((_, idx) => idx !== i))}
                                                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"><X size={14}/></button>
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
                            <div>
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">字段映射</h4>
                                    <select onChange={e => { if (e.target.value) addRespFieldMapping(e.target.value); e.target.value = ''; }}
                                        className="px-3 py-1.5 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                        <option value="">+ 添加字段</option>
                                        {Object.entries(STANDARD_RESPONSE).filter(([key]) => !respFieldMappings.find(m => m.stdField === key)).map(([key, def]) => (
                                            <option key={key} value={key}>{def.name} ({key})</option>
                                        ))}
                                    </select>
                                </div>
                                <p className="text-xs text-[var(--text-secondary)] mb-3">配置三方接口响应字段路径到系统标准字段的映射（支持路径如 data.output.images[0]）</p>
                                {respFieldMappings.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无字段映射，请从上方添加</div>
                                ) : (
                                    respFieldMappings.map((m, i) => (
                                        <div key={m.stdField} className="flex items-center gap-2 mb-2">
                                            <div className="flex-1 px-3 py-2 bg-[var(--surface)] rounded-lg text-sm">
                                                <span className="text-[var(--text-secondary)]">{STANDARD_RESPONSE[m.stdField]?.name || m.stdField}</span>
                                                <code className="ml-2 text-xs text-[var(--text-secondary)]">{m.stdField}</code>
                                            </div>
                                            <span className="text-[var(--text-secondary)]">←</span>
                                            <input type="text" value={m.vendorField}
                                                onChange={e => { const newList = [...respFieldMappings]; newList[i].vendorField = e.target.value; setRespFieldMappings(newList); }}
                                                className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                placeholder="三方响应字段路径，如 data.task_id" />
                                            <button type="button" onClick={() => setRespFieldMappings(respFieldMappings.filter((_, idx) => idx !== i))}
                                                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"><X size={14}/></button>
                                        </div>
                                    ))
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">状态值映射</h4>
                                    <button type="button" onClick={() => setRespValueMappings([...respValueMappings, {field: 'status', stdValue: '', vendorValue: ''}])}
                                        className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加映射</button>
                                </div>
                                <p className="text-xs text-[var(--text-secondary)] mb-3">配置三方状态值到系统标准状态值的映射</p>
                                {respValueMappings.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无状态值映射</div>
                                ) : (
                                    respValueMappings.map((m, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <input type="text" value={m.vendorValue}
                                                onChange={e => { const newList = [...respValueMappings]; newList[i].vendorValue = e.target.value; setRespValueMappings(newList); }}
                                                className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                placeholder="三方状态值" />
                                            <span className="text-[var(--text-secondary)]">→</span>
                                            <select value={m.stdValue}
                                                onChange={e => { const newList = [...respValueMappings]; newList[i].stdValue = e.target.value; setRespValueMappings(newList); }}
                                                className="w-36 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="">系统状态</option>
                                                {STANDARD_STATUS_VALUES.map(v => (<option key={v} value={v}>{v}</option>))}
                                            </select>
                                            <button type="button" onClick={() => setRespValueMappings(respValueMappings.filter((_, idx) => idx !== i))}
                                                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"><X size={14}/></button>
                                        </div>
                                    ))
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">类型转换</h4>
                                    <button type="button" onClick={() => setRespTypeConverts([...respTypeConverts, {field: '', type: 'string_to_array', separator: ','}])}
                                        className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加转换</button>
                                </div>
                                {respTypeConverts.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无类型转换</div>
                                ) : (
                                    respTypeConverts.map((tc, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <select value={tc.field} onChange={e => { const newList = [...respTypeConverts]; newList[i].field = e.target.value; setRespTypeConverts(newList); }}
                                                className="w-40 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="">选择字段</option>
                                                {respFieldMappings.map(m => (<option key={m.stdField} value={m.stdField}>{STANDARD_RESPONSE[m.stdField]?.name || m.stdField}</option>))}
                                            </select>
                                            <select value={tc.type} onChange={e => { const newList = [...respTypeConverts]; newList[i].type = e.target.value as 'string_to_array' | 'array_to_string'; setRespTypeConverts(newList); }}
                                                className="w-44 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="string_to_array">字符串→数组</option>
                                                <option value="array_to_string">数组→字符串</option>
                                            </select>
                                            <input type="text" value={tc.separator} onChange={e => { const newList = [...respTypeConverts]; newList[i].separator = e.target.value; setRespTypeConverts(newList); }}
                                                className="w-24 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" placeholder="分隔符" />
                                            <span className="text-xs text-[var(--text-secondary)] whitespace-nowrap">用 \n 表示换行</span>
                                            <button type="button" onClick={() => setRespTypeConverts(respTypeConverts.filter((_, idx) => idx !== i))}
                                                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"><X size={14}/></button>
                                        </div>
                                    ))
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">成功条件</h4>
                                    {!respSuccessCondition ? (
                                        <button type="button" onClick={() => setRespSuccessCondition({field: '', operator: 'eq', value: ''})}
                                            className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加条件</button>
                                    ) : (
                                        <button type="button" onClick={() => setRespSuccessCondition(null)}
                                            className="px-3 py-1.5 text-sm text-red-500 hover:bg-red-50 rounded-lg">移除条件</button>
                                    )}
                                </div>
                                <p className="text-xs text-[var(--text-secondary)] mb-3">配置响应成功的判断条件（如 code 等于 0 表示成功）。不配置时使用默认的 status 字段判断</p>
                                {respSuccessCondition && (
                                    <div className="flex items-center gap-2 mb-2 flex-wrap">
                                        <input type="text" value={respSuccessCondition.field}
                                            onChange={e => setRespSuccessCondition({...respSuccessCondition, field: e.target.value})}
                                            className="w-40 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                            placeholder="字段路径，如 code" />
                                        <select value={respSuccessCondition.operator}
                                            onChange={e => setRespSuccessCondition({...respSuccessCondition, operator: e.target.value as SuccessCondition['operator']})}
                                            className="w-32 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                            {SUCCESS_CONDITION_OPERATORS.map(op => (<option key={op.value} value={op.value}>{op.label}</option>))}
                                        </select>
                                        {SUCCESS_CONDITION_OPERATORS.find(o => o.value === respSuccessCondition.operator)?.needValue && (
                                            <input type="text" value={respSuccessCondition.value !== undefined ? String(respSuccessCondition.value) : ''}
                                                onChange={e => setRespSuccessCondition({...respSuccessCondition, value: e.target.value})}
                                                className="w-32 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                placeholder="比较值" />
                                        )}
                                        {SUCCESS_CONDITION_OPERATORS.find(o => o.value === respSuccessCondition.operator)?.needValues && (
                                            <input type="text" value={respSuccessCondition.values?.join(',') || ''}
                                                onChange={e => setRespSuccessCondition({...respSuccessCondition, values: e.target.value.split(',').map(v => v.trim()).filter(v => v)})}
                                                className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                placeholder="值列表，逗号分隔" />
                                        )}
                                    </div>
                                )}
                            </div>
                            {form.result_mode === 'poll' && (
                                <div className="border-t border-[var(--border-soft)] pt-4">
                                    <div className="flex items-center justify-between mb-3">
                                        <h4 className="text-sm font-medium text-[var(--text-primary)]">独立轮询响应映射</h4>
                                        <label className="flex items-center gap-2 cursor-pointer">
                                            <input type="checkbox" checked={useSeparatePollMapping} onChange={e => setUseSeparatePollMapping(e.target.checked)}
                                                className="rounded border-gray-300 text-[var(--primary)] focus:ring-[var(--primary)]" />
                                            <span className="text-sm text-[var(--text-secondary)]">启用独立轮询响应映射</span>
                                        </label>
                                    </div>
                                    <p className="text-xs text-[var(--text-secondary)]">当轮询接口的响应格式与提交接口不同时，启用此选项并在"轮询响应"标签页中单独配置</p>
                                </div>
                            )}
                        </div>
                    )}

                    {/* 轮询响应映射 */}
                    {activeTab === 'poll_response' && useSeparatePollMapping && (
                        <div className="space-y-6">
                            <p className="text-xs text-[var(--text-secondary)]">配置轮询接口响应的字段映射（当轮询响应格式与提交响应不同时使用）</p>
                            <div>
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">字段映射</h4>
                                    <select onChange={e => { if (e.target.value) addPollRespFieldMapping(e.target.value); e.target.value = ''; }}
                                        className="px-3 py-1.5 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                        <option value="">+ 添加字段</option>
                                        {Object.entries(STANDARD_RESPONSE).filter(([key]) => !pollRespFieldMappings.find(m => m.stdField === key)).map(([key, def]) => (
                                            <option key={key} value={key}>{def.name} ({key})</option>
                                        ))}
                                    </select>
                                </div>
                                {pollRespFieldMappings.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无字段映射，请从上方添加</div>
                                ) : (
                                    pollRespFieldMappings.map((m, i) => (
                                        <div key={m.stdField} className="flex items-center gap-2 mb-2">
                                            <div className="flex-1 px-3 py-2 bg-[var(--surface)] rounded-lg text-sm">
                                                <span className="text-[var(--text-secondary)]">{STANDARD_RESPONSE[m.stdField]?.name || m.stdField}</span>
                                                <code className="ml-2 text-xs text-[var(--text-secondary)]">{m.stdField}</code>
                                            </div>
                                            <span className="text-[var(--text-secondary)]">←</span>
                                            <input type="text" value={m.vendorField}
                                                onChange={e => { const newList = [...pollRespFieldMappings]; newList[i].vendorField = e.target.value; setPollRespFieldMappings(newList); }}
                                                className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                placeholder="三方响应字段路径" />
                                            <button type="button" onClick={() => setPollRespFieldMappings(pollRespFieldMappings.filter((_, idx) => idx !== i))}
                                                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"><X size={14}/></button>
                                        </div>
                                    ))
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">状态值映射</h4>
                                    <button type="button" onClick={() => setPollRespValueMappings([...pollRespValueMappings, {field: 'status', stdValue: '', vendorValue: ''}])}
                                        className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加映射</button>
                                </div>
                                {pollRespValueMappings.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无状态值映射</div>
                                ) : (
                                    pollRespValueMappings.map((m, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <input type="text" value={m.vendorValue}
                                                onChange={e => { const newList = [...pollRespValueMappings]; newList[i].vendorValue = e.target.value; setPollRespValueMappings(newList); }}
                                                className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                placeholder="三方状态值" />
                                            <span className="text-[var(--text-secondary)]">→</span>
                                            <select value={m.stdValue}
                                                onChange={e => { const newList = [...pollRespValueMappings]; newList[i].stdValue = e.target.value; setPollRespValueMappings(newList); }}
                                                className="w-36 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="">系统状态</option>
                                                {STANDARD_STATUS_VALUES.map(v => (<option key={v} value={v}>{v}</option>))}
                                            </select>
                                            <button type="button" onClick={() => setPollRespValueMappings(pollRespValueMappings.filter((_, idx) => idx !== i))}
                                                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"><X size={14}/></button>
                                        </div>
                                    ))
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">类型转换</h4>
                                    <button type="button" onClick={() => setPollRespTypeConverts([...pollRespTypeConverts, {field: '', type: 'string_to_array', separator: ','}])}
                                        className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加转换</button>
                                </div>
                                {pollRespTypeConverts.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无类型转换</div>
                                ) : (
                                    pollRespTypeConverts.map((tc, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <select value={tc.field} onChange={e => { const newList = [...pollRespTypeConverts]; newList[i].field = e.target.value; setPollRespTypeConverts(newList); }}
                                                className="w-40 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="">选择字段</option>
                                                {pollRespFieldMappings.map(m => (<option key={m.stdField} value={m.stdField}>{STANDARD_RESPONSE[m.stdField]?.name || m.stdField}</option>))}
                                            </select>
                                            <select value={tc.type} onChange={e => { const newList = [...pollRespTypeConverts]; newList[i].type = e.target.value as 'string_to_array' | 'array_to_string'; setPollRespTypeConverts(newList); }}
                                                className="w-40 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="string_to_array">字符串→数组</option>
                                                <option value="array_to_string">数组→字符串</option>
                                            </select>
                                            <input type="text" value={tc.separator} onChange={e => { const newList = [...pollRespTypeConverts]; newList[i].separator = e.target.value; setPollRespTypeConverts(newList); }}
                                                className="w-20 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]" placeholder="分隔符" />
                                            <button type="button" onClick={() => setPollRespTypeConverts(pollRespTypeConverts.filter((_, idx) => idx !== i))}
                                                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"><X size={14}/></button>
                                        </div>
                                    ))
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">成功条件</h4>
                                    {!pollRespSuccessCondition ? (
                                        <button type="button" onClick={() => setPollRespSuccessCondition({field: '', operator: 'eq', value: ''})}
                                            className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加条件</button>
                                    ) : (
                                        <button type="button" onClick={() => setPollRespSuccessCondition(null)}
                                            className="px-3 py-1.5 text-sm text-red-500 hover:bg-red-50 rounded-lg">移除条件</button>
                                    )}
                                </div>
                                <p className="text-xs text-[var(--text-secondary)] mb-3">配置轮询响应成功的判断条件。不配置时使用默认的 status 字段判断</p>
                                {pollRespSuccessCondition && (
                                    <div className="flex items-center gap-2 mb-2 flex-wrap">
                                        <input type="text" value={pollRespSuccessCondition.field}
                                            onChange={e => setPollRespSuccessCondition({...pollRespSuccessCondition, field: e.target.value})}
                                            className="w-40 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                            placeholder="字段路径，如 code" />
                                        <select value={pollRespSuccessCondition.operator}
                                            onChange={e => setPollRespSuccessCondition({...pollRespSuccessCondition, operator: e.target.value as SuccessCondition['operator']})}
                                            className="w-32 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                            {SUCCESS_CONDITION_OPERATORS.map(op => (<option key={op.value} value={op.value}>{op.label}</option>))}
                                        </select>
                                        {SUCCESS_CONDITION_OPERATORS.find(o => o.value === pollRespSuccessCondition.operator)?.needValue && (
                                            <input type="text" value={pollRespSuccessCondition.value !== undefined ? String(pollRespSuccessCondition.value) : ''}
                                                onChange={e => setPollRespSuccessCondition({...pollRespSuccessCondition, value: e.target.value})}
                                                className="w-32 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                placeholder="比较值" />
                                        )}
                                        {SUCCESS_CONDITION_OPERATORS.find(o => o.value === pollRespSuccessCondition.operator)?.needValues && (
                                            <input type="text" value={pollRespSuccessCondition.values?.join(',') || ''}
                                                onChange={e => setPollRespSuccessCondition({...pollRespSuccessCondition, values: e.target.value.split(',').map(v => v.trim()).filter(v => v)})}
                                                className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                placeholder="值列表，逗号分隔" />
                                        )}
                                    </div>
                                )}
                            </div>
                        </div>
                    )}

                    {/* 回调映射 */}
                    {activeTab === 'callback' && (
                        <div className="space-y-6">
                            <div>
                                <h4 className="text-sm font-medium text-[var(--text-primary)] mb-3">路径配置</h4>
                                <div className="space-y-3">
                                    <div className="flex items-center gap-2">
                                        <label className="w-24 text-sm text-[var(--text-secondary)]">任务ID路径</label>
                                        <input type="text" value={callbackConfig.task_id_path}
                                            onChange={e => setCallbackConfig({...callbackConfig, task_id_path: e.target.value})}
                                            className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                            placeholder="如 data.taskId" />
                                    </div>
                                    <div className="flex items-center gap-2">
                                        <label className="w-24 text-sm text-[var(--text-secondary)]">状态路径</label>
                                        <input type="text" value={callbackConfig.status_path}
                                            onChange={e => setCallbackConfig({...callbackConfig, status_path: e.target.value})}
                                            className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                            placeholder="如 data.state" />
                                    </div>
                                    <div className="flex items-center gap-2">
                                        <label className="w-24 text-sm text-[var(--text-secondary)]">结果路径</label>
                                        <input type="text" value={callbackConfig.result_path}
                                            onChange={e => setCallbackConfig({...callbackConfig, result_path: e.target.value})}
                                            className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                            placeholder="如 data.output" />
                                    </div>
                                </div>
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4">
                                <div className="flex items-center justify-between mb-3">
                                    <h4 className="text-sm font-medium text-[var(--text-primary)]">状态值映射</h4>
                                    <button type="button" onClick={() => setCallbackStatusMappings([...callbackStatusMappings, {stdValue: '', vendorValue: ''}])}
                                        className="px-3 py-1.5 text-sm text-[var(--primary)] hover:bg-[var(--primary-lighter)] rounded-lg">+ 添加映射</button>
                                </div>
                                {callbackStatusMappings.length === 0 ? (
                                    <div className="text-sm text-[var(--text-secondary)] text-center py-4 bg-[var(--surface)] rounded-lg">暂无状态值映射</div>
                                ) : (
                                    callbackStatusMappings.map((m, i) => (
                                        <div key={i} className="flex items-center gap-2 mb-2">
                                            <input type="text" value={m.vendorValue}
                                                onChange={e => { const newList = [...callbackStatusMappings]; newList[i].vendorValue = e.target.value; setCallbackStatusMappings(newList); }}
                                                className="flex-1 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                placeholder="三方状态值，如 COMPLETED" />
                                            <span className="text-[var(--text-secondary)]">→</span>
                                            <select value={m.stdValue}
                                                onChange={e => { const newList = [...callbackStatusMappings]; newList[i].stdValue = e.target.value; setCallbackStatusMappings(newList); }}
                                                className="w-36 px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-[var(--primary)]">
                                                <option value="">系统状态</option>
                                                {STANDARD_STATUS_VALUES.map(v => (<option key={v} value={v}>{v}</option>))}
                                            </select>
                                            <button type="button" onClick={() => setCallbackStatusMappings(callbackStatusMappings.filter((_, idx) => idx !== i))}
                                                className="p-2 text-[var(--text-secondary)] hover:text-red-500 hover:bg-red-50 rounded-lg"><X size={14}/></button>
                                        </div>
                                    ))
                                )}
                            </div>
                        </div>
                    )}

                    <div className="flex justify-end gap-3 pt-6 mt-4 border-t border-[var(--border-soft)]">
                        <button type="button" onClick={onClose}
                            className="px-4 py-2 text-sm font-bold text-[var(--text-secondary)] bg-[var(--primary-lighter)] rounded-lg hover:bg-gray-200 transition-colors">取消</button>
                        <button type="submit" disabled={loading}
                            className="px-4 py-2 text-sm font-bold text-white bg-[var(--primary)] rounded-lg hover:opacity-90 disabled:opacity-50 transition-colors">
                            {loading ? '保存中...' : '保存'}
                        </button>
                    </div>
                </form>
                </>
                )}
        </Modal>
    );
};

export default ChannelCapabilityModal;
