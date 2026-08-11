import React, { useEffect, useState } from 'react';
import { X } from 'lucide-react';
import { Modal } from '../../components/ui/Modal';
import { Select } from '../../components/ui';
import JsonEditor from '../../components/ui/JsonEditor';
import { createChannelCapability, fetchChannelAccounts, updateChannelCapability } from '../../services/api';
import { ChannelCapability, Channel, ChannelAccount, Capability } from '../../types';
import {
    RESULT_MODES, STANDARD_PARAMS, STANDARD_RESPONSE, POLL_PARAMS,
    STANDARD_STATUS_VALUES,
} from './constants';
import { FieldMappingRow, ValueMappingRow, FixedParamRow } from './MappingRows';
import {
    FieldMapping, ValueMapping, FixedParam, TypeConvert, SuccessCondition,
    SUCCESS_CONDITION_OPERATORS,
    parseParamMapping, parseResponseMapping, buildParamMapping, buildResponseMapping,
} from './mappingUtils';

type AccountBindingDraft = {
    account_id: number;
    status: number;
    priority: number;
    weight: number;
};

const ChannelCapabilityModal: React.FC<{
    isOpen: boolean;
    capabilityCode: string;
    channelCapability: ChannelCapability | null;
    channels: Channel[];
    capabilities: Capability[];
    defaultChannelId?: number;
    defaultAccountId?: number;
    initialTab?: 'basic' | 'accounts';
    onClose: () => void;
    onSave: () => void;
}> = ({isOpen, capabilityCode, channelCapability, channels, capabilities, defaultChannelId, defaultAccountId, initialTab = 'basic', onClose, onSave}) => {
    const [activeTab, setActiveTab] = useState<'basic' | 'accounts' | 'request' | 'param' | 'response' | 'poll_response' | 'callback' | 'schema'>('basic');
    const [form, setForm] = useState({
        channel_id: 0, capability_code: '', route_operation: '', supported_operations: [] as string[], model: '', price: 0, price_unit: 'request',
        result_mode: 'poll', request_path: '', request_method: 'POST', content_type: 'application/json',
        auth_location: 'header', auth_key: 'Authorization', auth_value_prefix: 'Bearer ',
        poll_path: '', poll_method: 'GET', poll_interval: 5, poll_max_attempts: 60, transfer_enabled: false,
        image_edit_enabled: false, image_input_mode: 'multipart', image_edit_path: '/v1/images/edits', image_file_field: 'image',
    });
    const [accountBindings, setAccountBindings] = useState<AccountBindingDraft[]>([]);
    const [availableAccounts, setAvailableAccounts] = useState<ChannelAccount[]>([]);
    const [accountsLoading, setAccountsLoading] = useState(false);
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
    const [basicError, setBasicError] = useState('');
    const [loading, setLoading] = useState(false);

    useEffect(() => {
        // 后端保存嵌套映射对象，编辑器使用可增删的行数组；打开时一次拆解全部子配置。
        if (channelCapability) {
            const imageEdit = channelCapability.extraConfig?.image_edit || {};
            const imageInputMode = imageEdit.input_mode === 'url' ? 'url' : 'multipart';
            setForm({
                channel_id: Number(channelCapability.channelId),
                capability_code: channelCapability.capabilityCode,
                route_operation: channelCapability.routeOperation || '',
                supported_operations: channelCapability.supportedOperations?.length
                    ? channelCapability.supportedOperations
                    : channelCapability.routeOperation ? [channelCapability.routeOperation] : [],
                model: channelCapability.model || '',
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
                image_edit_enabled: imageEdit.enabled === true,
                image_input_mode: imageInputMode,
                image_edit_path: imageEdit.edit_path || '/v1/images/edits',
                image_file_field: imageEdit.file_field || (imageInputMode === 'url' ? 'image_urls' : 'image'),
            });
            setAccountBindings((channelCapability.accountBindings || []).map(binding => ({
                account_id: Number(binding.accountId),
                status: binding.status,
                priority: binding.priority,
                weight: binding.weight,
            })));
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
                channel_id: defaultChannelId || (channels[0]?.id ? Number(channels[0].id) : 0),
                capability_code: capabilityCode, route_operation: '', supported_operations: [], model: '', price: 0, price_unit: 'request',
                result_mode: 'poll', request_path: '', request_method: 'POST', content_type: 'application/json',
                auth_location: 'header', auth_key: 'Authorization', auth_value_prefix: 'Bearer ',
                poll_path: '', poll_method: 'GET', poll_interval: 5, poll_max_attempts: 60, transfer_enabled: false,
                image_edit_enabled: false, image_input_mode: 'multipart', image_edit_path: '/v1/images/edits', image_file_field: 'image',
            });
            setAccountBindings(defaultAccountId ? [{account_id: defaultAccountId, status: 1, priority: 0, weight: 10}] : []);
            setParamFieldMappings([]); setParamValueMappings([]); setParamFixedParams([]); setParamTypeConverts([]);
            setRespFieldMappings([]); setRespValueMappings([]); setRespTypeConverts([]); setRespSuccessCondition(null);
            setPollRespFieldMappings([]); setPollRespValueMappings([]); setPollRespTypeConverts([]); setPollRespSuccessCondition(null);
            setUseSeparatePollMapping(false); setPollParamFieldMappings([]); setPollParamFixedParams([]);
            setCallbackConfig({task_id_path: '', status_path: '', result_path: ''}); setCallbackStatusMappings([]);
            setParamSchemaJson('');
        }
        setBasicError('');
        setActiveTab(initialTab);
    }, [channelCapability, capabilityCode, channels, isOpen, defaultChannelId, defaultAccountId, initialTab]);

    useEffect(() => {
        if (!isOpen || !form.channel_id) {
            setAvailableAccounts([]);
            return;
        }
        let cancelled = false;
        setAccountsLoading(true);
        fetchChannelAccounts(String(form.channel_id))
            .then(accounts => {
                if (!cancelled) setAvailableAccounts(accounts);
            })
            .catch(() => {
                if (!cancelled) setAvailableAccounts([]);
            })
            .finally(() => {
                if (!cancelled) setAccountsLoading(false);
            });
        return () => { cancelled = true; };
    }, [isOpen, form.channel_id]);

    const baseTabs = [
        {key: 'basic', label: '基本信息'},
        {key: 'accounts', label: `绑定 Key (${accountBindings.length})`},
        {key: 'request', label: '请求配置'},
        {key: 'schema', label: '参数Schema'},
        {key: 'param', label: '参数映射'},
        {key: 'response', label: '响应映射'},
    ];
    const tabs = (() => {
        // 流式响应经 SSE 直接抠图,响应映射不生效,隐藏该 tab
        if (form.result_mode === 'stream') return baseTabs.filter(t => t.key !== 'response');
        if (form.result_mode === 'sync') return baseTabs;
        if (form.result_mode === 'poll') {
            const t = [...baseTabs];
            if (useSeparatePollMapping) t.push({key: 'poll_response', label: '轮询响应'});
            t.push({key: 'callback', label: '回调映射'});
            return t;
        }
        return [...baseTabs, {key: 'callback', label: '回调映射'}];
    })();

    // 切换模式后当前 tab 若被隐藏(如流式隐去响应映射),回退到基本信息避免空白
    // 注意: 必须在 early-return 之前,否则 isOpen 切换导致 hooks 数量变化(React #310)
    useEffect(() => {
        if (!tabs.some(t => t.key === activeTab)) setActiveTab('basic');
    }, [tabs, activeTab]);

    const selectedCapabilityType = capabilities.find(capability => capability.code === form.capability_code)?.type || 'other';

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!form.channel_id || !form.capability_code.trim() || (selectedCapabilityType === 'image' && form.supported_operations.length === 0)) {
            setBasicError(!form.channel_id
                ? '请选择渠道'
                : !form.capability_code.trim()
                    ? '请选择平台能力'
                    : '请选择生成或编辑操作');
            setActiveTab('basic');
            return;
        }
        setBasicError('');
        setLoading(true);
        try {
            // 提交前把各 Tab 的行状态重新组装成 Provider 执行器使用的映射对象。
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
                channel_id: form.channel_id, model_code: form.capability_code, route_operation: form.route_operation,
                supported_operations: form.supported_operations,
                vendor_model: form.model,
                account_bindings: accountBindings,
                price_mode: form.price_unit, input_price: form.price, output_price: 0, interaction_mode: form.result_mode,
                request_path: form.request_path, request_method: form.request_method, content_type: form.content_type,
                auth_location: form.auth_location, auth_key: form.auth_key, auth_value_prefix: form.auth_value_prefix,
                poll_path: form.poll_path, poll_method: form.poll_method, poll_interval: form.poll_interval,
                poll_max_attempts: form.poll_max_attempts, param_mapping: paramMapping, callback_mapping: callbackMapping,
                extra_config: {
                    ...(channelCapability?.extraConfig || {}),
                    transfer_enabled: form.transfer_enabled,
                    image_edit: form.image_edit_enabled ? {
                        enabled: true,
                        input_mode: form.image_input_mode,
                        file_field: form.image_file_field.trim() || (form.image_input_mode === 'url' ? 'image_urls' : 'image'),
                        ...(form.image_input_mode === 'multipart' ? {edit_path: form.image_edit_path.trim() || '/v1/images/edits'} : {}),
                    } : {enabled: false},
                },
            };
            if (paramSchemaJson.trim()) {
                try { data.param_schema = JSON.parse(paramSchemaJson); } catch { setParamSchemaError('JSON 格式错误'); setLoading(false); return; }
            } else {
                data.param_schema = null;
            }
            // 流式(stream)走 SSE 直抠 b64,不经 ResponseMapping,故不下发响应映射
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
        <Modal open={isOpen} onClose={onClose} title={channelCapability ? '编辑渠道能力配置' : '新建渠道能力配置'} width="max-w-3xl">
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
                            {basicError && (
                                <div className="px-3 py-2 border border-red-200 bg-red-50 text-red-700 text-sm rounded-lg">
                                    {basicError}
                                </div>
                            )}
                            <div className="grid grid-cols-2 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">渠道 <span className="text-red-500">*</span></label>
                                    <Select value={String(form.channel_id)} onChange={v => {
                                        setForm({...form, channel_id: Number(v)});
                                        setAccountBindings([]);
                                        setBasicError('');
                                    }}
                                        options={[{ label: '选择渠道', value: '0' }, ...channels.map(ch => ({ label: `${ch.name} (${ch.type})`, value: String(ch.id) }))]} />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">平台能力 <span className="text-red-500">*</span></label>
                                    <Select value={form.capability_code} onChange={v => {
                                        const type = capabilities.find(capability => capability.code === v)?.type;
                                        const operations = type === 'video' ? ['videos.generate'] : [];
                                        setForm({...form, capability_code: v, route_operation: operations[0] || '', supported_operations: operations});
                                        setBasicError('');
                                    }}
                                        options={[{ label: '选择能力', value: '' }, ...capabilities.map(cap => ({ label: `${cap.name} (${cap.code})`, value: cap.code }))]} />
                                </div>
                            </div>
                            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">上游模型标识</label>
                                    <input type="text" value={form.model} onChange={e => setForm({...form, model: e.target.value})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                        placeholder="如: midjourney-v6" />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">
                                        支持操作 {selectedCapabilityType === 'image' && <span className="text-red-500">*</span>}
                                    </label>
                                    {selectedCapabilityType === 'image' ? (
                                        <div className="flex h-10 items-center gap-4 rounded-lg border border-[var(--border-soft)] px-3">
                                            {[
                                                {value: 'images.generate', label: '图片生成'},
                                                {value: 'images.edit', label: '图片编辑'},
                                            ].map(operation => (
                                                <label key={operation.value} className="inline-flex items-center gap-1.5 text-sm text-[var(--text-primary)]">
                                                    <input type="checkbox" checked={form.supported_operations.includes(operation.value)}
                                                        onChange={event => {
                                                            const operations = event.target.checked
                                                                ? [...form.supported_operations, operation.value]
                                                                : form.supported_operations.filter(item => item !== operation.value);
                                                            const routeOperation = operations.includes(form.route_operation)
                                                                ? form.route_operation
                                                                : operations[0] || '';
                                                            setForm({...form, supported_operations: operations, route_operation: routeOperation});
                                                            setBasicError('');
                                                        }}
                                                        className="h-4 w-4 rounded border-gray-300 text-[var(--primary)] focus:ring-[var(--primary)]" />
                                                    {operation.label}
                                                </label>
                                            ))}
                                        </div>
                                    ) : (
                                        <Select value={form.route_operation} onChange={v => {
                                            setForm({...form, route_operation: v, supported_operations: v ? [v] : []});
                                            setBasicError('');
                                        }} options={[{ label: '未指定', value: '' }, ...(selectedCapabilityType === 'video' ? [{ label: '视频生成', value: 'videos.generate' }] : [])]} />
                                    )}
                                </div>
                            </div>
                            <div className="grid grid-cols-3 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">结果模式</label>
                                    <Select value={form.result_mode} onChange={v => setForm({...form, result_mode: v})}
                                        options={RESULT_MODES.map(m => ({ label: m.label, value: m.value }))} />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">单价</label>
                                    <input type="number" value={form.price} onChange={e => setForm({...form, price: Number(e.target.value)})}
                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                        step="0.0001" min="0" />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">计价单位</label>
                                    <Select value={form.price_unit} onChange={v => setForm({...form, price_unit: v})}
                                        options={[{ label: '按请求', value: 'request' }, { label: '按秒', value: 'second' }, { label: '按图片', value: 'image' }]} />
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

                    {activeTab === 'accounts' && (
                        <div className="space-y-3">
                            {!form.channel_id ? (
                                <div className="py-10 text-center text-sm text-[var(--text-secondary)]">未选择渠道</div>
                            ) : accountsLoading ? (
                                <div className="py-10 text-center text-sm text-[var(--text-secondary)]">加载中...</div>
                            ) : availableAccounts.length === 0 ? (
                                <div className="py-10 text-center text-sm text-[var(--text-secondary)]">当前渠道没有 Key</div>
                            ) : (
                                <div className="border border-[var(--border-soft)] rounded-lg overflow-hidden">
                                    <div className="grid grid-cols-[minmax(0,1fr)_5rem_5rem_4rem] gap-3 px-3 py-2 bg-[var(--surface)] text-xs font-medium text-[var(--text-secondary)]">
                                        <span>Key</span>
                                        <span>优先级</span>
                                        <span>权重</span>
                                        <span>状态</span>
                                    </div>
                                    {availableAccounts.map(account => {
                                        const accountId = Number(account.id);
                                        const binding = accountBindings.find(item => item.account_id === accountId);
                                        return (
                                            <div key={account.id} className="grid grid-cols-[minmax(0,1fr)_5rem_5rem_4rem] gap-3 items-center px-3 py-3 border-t border-[var(--border-soft)]">
                                                <label className="flex items-center gap-2 min-w-0">
                                                    <input type="checkbox" checked={Boolean(binding)}
                                                        onChange={event => {
                                                            if (event.target.checked) {
                                                                setAccountBindings([...accountBindings, {account_id: accountId, status: 1, priority: 0, weight: 10}]);
                                                            } else {
                                                                setAccountBindings(accountBindings.filter(item => item.account_id !== accountId));
                                                            }
                                                        }}
                                                        className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded focus:ring-[var(--primary)]" />
                                                    <span className="truncate text-sm text-[var(--text-primary)]">{account.name || `Key #${account.id}`}</span>
                                                    {account.status !== 1 && <span className="text-xs text-red-500">已停用</span>}
                                                </label>
                                                <input type="number" value={binding?.priority ?? 0} disabled={!binding}
                                                    onChange={event => setAccountBindings(accountBindings.map(item => item.account_id === accountId ? {...item, priority: Number(event.target.value)} : item))}
                                                    className="w-full px-2 py-1.5 border border-[var(--border-soft)] rounded-md text-sm disabled:opacity-40" />
                                                <input type="number" value={binding?.weight ?? 10} disabled={!binding} min="1"
                                                    onChange={event => setAccountBindings(accountBindings.map(item => item.account_id === accountId ? {...item, weight: Math.max(1, Number(event.target.value) || 1)} : item))}
                                                    className="w-full px-2 py-1.5 border border-[var(--border-soft)] rounded-md text-sm disabled:opacity-40" />
                                                <label className="flex items-center gap-1.5 text-xs text-[var(--text-secondary)]">
                                                    <input type="checkbox" checked={binding?.status === 1} disabled={!binding}
                                                        onChange={event => setAccountBindings(accountBindings.map(item => item.account_id === accountId ? {...item, status: event.target.checked ? 1 : 0} : item))}
                                                        className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded focus:ring-[var(--primary)]" />
                                                    启用
                                                </label>
                                            </div>
                                        );
                                    })}
                                </div>
                            )}
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
                                    <Select value={form.request_method} onChange={v => setForm({...form, request_method: v})}
                                        options={[{ label: 'POST', value: 'POST' }, { label: 'GET', value: 'GET' }]} />
                                </div>
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">Content-Type</label>
                                    <Select value={form.content_type} onChange={v => setForm({...form, content_type: v})}
                                        options={[{ label: 'application/json', value: 'application/json' }, { label: 'application/x-www-form-urlencoded', value: 'application/x-www-form-urlencoded' }, { label: 'multipart/form-data', value: 'multipart/form-data' }]} />
                                </div>
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4 space-y-4">
                                <div className="flex items-center gap-2">
                                    <input type="checkbox" id="image_edit_enabled" checked={form.image_edit_enabled}
                                        onChange={e => setForm({...form, image_edit_enabled: e.target.checked})}
                                        className="h-4 w-4 text-[var(--primary)] border-gray-300 rounded focus:ring-[var(--primary)]" />
                                    <label htmlFor="image_edit_enabled" className="text-sm font-medium text-[var(--text-primary)]">参考图输入适配</label>
                                </div>
                                {form.image_edit_enabled && (
                                    <div className="space-y-4">
                                        <div>
                                            <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">图片输入模式</label>
                                            <div className="grid grid-cols-2 rounded-lg border border-[var(--border-soft)] overflow-hidden">
                                                {[
                                                    {value: 'multipart', label: 'Multipart 文件'},
                                                    {value: 'url', label: '图片 URL'},
                                                ].map(option => (
                                                    <button key={option.value} type="button"
                                                        onClick={() => setForm({
                                                            ...form,
                                                            image_input_mode: option.value,
                                                            image_file_field: option.value === 'url' ? 'image_urls' : 'image',
                                                        })}
                                                        className={`px-3 py-2 text-sm transition-colors ${form.image_input_mode === option.value ? 'bg-[var(--primary)] text-white' : 'bg-[var(--surface-card)] text-[var(--text-secondary)] hover:bg-[var(--surface)]'}`}>
                                                        {option.label}
                                                    </button>
                                                ))}
                                            </div>
                                        </div>
                                        <div className={`grid gap-4 ${form.image_input_mode === 'multipart' ? 'grid-cols-2' : 'grid-cols-1'}`}>
                                            <div>
                                                <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">上游图片字段</label>
                                                <input type="text" value={form.image_file_field}
                                                    onChange={e => setForm({...form, image_file_field: e.target.value})}
                                                    className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                    placeholder={form.image_input_mode === 'url' ? 'image_urls' : 'image'} />
                                            </div>
                                            {form.image_input_mode === 'multipart' && (
                                                <div>
                                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">图片编辑路径</label>
                                                    <input type="text" value={form.image_edit_path}
                                                        onChange={e => setForm({...form, image_edit_path: e.target.value})}
                                                        className="w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--primary)]"
                                                        placeholder="/v1/images/edits" />
                                                </div>
                                            )}
                                        </div>
                                    </div>
                                )}
                            </div>
                            <div className="border-t border-[var(--border-soft)] pt-4 mt-4">
                                <h4 className="text-sm font-medium text-[var(--text-primary)] mb-3">认证配置</h4>
                            </div>
                            <div className="grid grid-cols-3 gap-4">
                                <div>
                                    <label className="block text-sm font-medium text-[var(--text-primary)] mb-1">认证位置</label>
                                    <Select value={form.auth_location} onChange={v => setForm({...form, auth_location: v})}
                                        options={[{ label: '请求头 (Header)', value: 'header' }, { label: '请求体 (Body)', value: 'body' }, { label: 'URL参数 (Query)', value: 'query' }]} />
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
                                            <Select value={form.poll_method} onChange={v => setForm({...form, poll_method: v})}
                                                options={[{ label: 'GET', value: 'GET' }, { label: 'POST', value: 'POST' }]} />
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
                                                    <Select value="" onChange={v => { if (v) addPollParamFieldMapping(v); }}
                                                        options={[{ label: '+ 添加字段', value: '' }, ...Object.entries(POLL_PARAMS).filter(([key]) => !pollParamFieldMappings.find(m => m.stdField === key)).map(([key, def]) => ({ label: `${def.name} (${key})`, value: key }))]} />
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
                                    <Select value="" onChange={v => { if (v) addParamFieldMapping(v); }}
                                        options={[{ label: '+ 添加字段', value: '' }, ...Object.entries(STANDARD_PARAMS).filter(([key]) => !paramFieldMappings.find(m => m.stdField === key)).map(([key, def]) => ({ label: `${def.name} (${key})`, value: key }))]} />
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
                                            <Select value={m.field} onChange={v => { const newList = [...paramValueMappings]; newList[i].field = v; setParamValueMappings(newList); }}
                                                className="w-36"
                                                options={[{ label: '选择字段', value: '' }, ...paramFieldMappings.map(fm => ({ label: STANDARD_PARAMS[fm.stdField]?.name || fm.stdField, value: fm.stdField }))]} />
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
                                            <Select value={tc.field} onChange={v => { const newList = [...paramTypeConverts]; newList[i].field = v; setParamTypeConverts(newList); }}
                                                className="w-40"
                                                options={[{ label: '选择字段', value: '' }, ...paramFieldMappings.map(m => ({ label: STANDARD_PARAMS[m.stdField]?.name || m.stdField, value: m.stdField }))]} />
                                            <Select value={tc.type} onChange={v => { const newList = [...paramTypeConverts]; newList[i].type = v as 'string_to_array' | 'array_to_string'; setParamTypeConverts(newList); }}
                                                className="w-44"
                                                options={[{ label: '数组→字符串', value: 'array_to_string' }, { label: '字符串→数组', value: 'string_to_array' }]} />
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
                                    <Select value="" onChange={v => { if (v) addRespFieldMapping(v); }}
                                        options={[{ label: '+ 添加字段', value: '' }, ...Object.entries(STANDARD_RESPONSE).filter(([key]) => !respFieldMappings.find(m => m.stdField === key)).map(([key, def]) => ({ label: `${def.name} (${key})`, value: key }))]} />
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
                                            <Select value={m.stdValue}
                                                onChange={v => { const newList = [...respValueMappings]; newList[i].stdValue = v; setRespValueMappings(newList); }}
                                                className="w-36"
                                                options={[{ label: '系统状态', value: '' }, ...STANDARD_STATUS_VALUES.map(v => ({ label: v, value: v }))]} />
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
                                            <Select value={tc.field} onChange={v => { const newList = [...respTypeConverts]; newList[i].field = v; setRespTypeConverts(newList); }}
                                                className="w-40"
                                                options={[{ label: '选择字段', value: '' }, ...respFieldMappings.map(m => ({ label: STANDARD_RESPONSE[m.stdField]?.name || m.stdField, value: m.stdField }))]} />
                                            <Select value={tc.type} onChange={v => { const newList = [...respTypeConverts]; newList[i].type = v as 'string_to_array' | 'array_to_string'; setRespTypeConverts(newList); }}
                                                className="w-44"
                                                options={[{ label: '字符串→数组', value: 'string_to_array' }, { label: '数组→字符串', value: 'array_to_string' }]} />
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
                                        <Select value={respSuccessCondition.operator}
                                            onChange={v => setRespSuccessCondition({...respSuccessCondition, operator: v as SuccessCondition['operator']})}
                                            className="w-32"
                                            options={SUCCESS_CONDITION_OPERATORS.map(op => ({ label: op.label, value: op.value }))} />
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
                                    <Select value="" onChange={v => { if (v) addPollRespFieldMapping(v); }}
                                        options={[{ label: '+ 添加字段', value: '' }, ...Object.entries(STANDARD_RESPONSE).filter(([key]) => !pollRespFieldMappings.find(m => m.stdField === key)).map(([key, def]) => ({ label: `${def.name} (${key})`, value: key }))]} />
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
                                            <Select value={m.stdValue}
                                                onChange={v => { const newList = [...pollRespValueMappings]; newList[i].stdValue = v; setPollRespValueMappings(newList); }}
                                                className="w-36"
                                                options={[{ label: '系统状态', value: '' }, ...STANDARD_STATUS_VALUES.map(v => ({ label: v, value: v }))]} />
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
                                            <Select value={tc.field} onChange={v => { const newList = [...pollRespTypeConverts]; newList[i].field = v; setPollRespTypeConverts(newList); }}
                                                className="w-40"
                                                options={[{ label: '选择字段', value: '' }, ...pollRespFieldMappings.map(m => ({ label: STANDARD_RESPONSE[m.stdField]?.name || m.stdField, value: m.stdField }))]} />
                                            <Select value={tc.type} onChange={v => { const newList = [...pollRespTypeConverts]; newList[i].type = v as 'string_to_array' | 'array_to_string'; setPollRespTypeConverts(newList); }}
                                                className="w-40"
                                                options={[{ label: '字符串→数组', value: 'string_to_array' }, { label: '数组→字符串', value: 'array_to_string' }]} />
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
                                        <Select value={pollRespSuccessCondition.operator}
                                            onChange={v => setPollRespSuccessCondition({...pollRespSuccessCondition, operator: v as SuccessCondition['operator']})}
                                            className="w-32"
                                            options={SUCCESS_CONDITION_OPERATORS.map(op => ({ label: op.label, value: op.value }))} />
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
                                            <Select value={m.stdValue}
                                                onChange={v => { const newList = [...callbackStatusMappings]; newList[i].stdValue = v; setCallbackStatusMappings(newList); }}
                                                className="w-36"
                                                options={[{ label: '系统状态', value: '' }, ...STANDARD_STATUS_VALUES.map(v => ({ label: v, value: v }))]} />
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
        </Modal>
    );
};

export default ChannelCapabilityModal;
