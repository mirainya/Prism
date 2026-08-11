import React from 'react';
import {
    ChevronDown, ChevronRight, Edit2, KeyRound, Layers3, Power, Plus, Trash2,
} from 'lucide-react';
import { Channel, ChannelCapability, EndpointAccountBinding } from '../../types';
import { formatPrice } from './constants';

export type EndpointRouteState = {
    available: boolean;
    activeBindings: EndpointAccountBinding[];
    label: string;
    badgeClass: string;
};

export type EndpointModelRouteGroup = {
    id: string;
    channelId: string;
    model: string;
    endpoints: ChannelCapability[];
};

export type EndpointOriginPresentation = {
    label: string;
    detail: string;
    badgeClass: string;
};

export const getEndpointOriginPresentation = (endpoint: ChannelCapability): EndpointOriginPresentation => {
    const snapshot = endpoint.originSnapshot || {};
    const accountId = snapshot.accountId || Number(endpoint.originAccountId || 0);
    const account = snapshot.accountName || (accountId ? `Key #${accountId}` : '');
    switch (endpoint.originType) {
    case 'manual':
        return {label: '手动创建', detail: snapshot.channelName || '', badgeClass: 'bg-blue-50 text-blue-700'};
    case 'key_discovery':
        return {label: 'Key 发现', detail: account, badgeClass: 'bg-emerald-50 text-emerald-700'};
    case 'endpoint_import':
        return {
            label: '端点导入',
            detail: [account, snapshot.sourceEndpointId ? `来源端点 #${snapshot.sourceEndpointId}` : ''].filter(Boolean).join(' · '),
            badgeClass: 'bg-violet-50 text-violet-700',
        };
    case 'legacy_inferred':
        return {label: '历史推断', detail: account, badgeClass: 'bg-amber-50 text-amber-700'};
    default:
        return {label: '历史未知', detail: '', badgeClass: 'bg-gray-100 text-gray-600'};
    }
};

export const getEndpointRouteState = (
    endpoint: ChannelCapability,
    capabilityStatus: number,
    channelStatus: number,
): EndpointRouteState => {
    const activeBindings = endpoint.accountBindings.filter(binding =>
        binding.status === 1 && binding.accountStatus === 1
    );

    if (capabilityStatus !== 1) {
        return {available: false, activeBindings, label: '能力已禁用', badgeClass: 'bg-gray-100 text-gray-600'};
    }
    if (endpoint.status !== 1) {
        return {available: false, activeBindings, label: '端点已禁用', badgeClass: 'bg-gray-100 text-gray-600'};
    }
    if (channelStatus !== 1) {
        return {available: false, activeBindings, label: '渠道已禁用', badgeClass: 'bg-amber-50 text-amber-700'};
    }
    if (endpoint.accountBindings.length === 0) {
        return {available: false, activeBindings, label: '未绑定 Key', badgeClass: 'bg-red-50 text-red-600'};
    }
    if (activeBindings.length === 0) {
        return {available: false, activeBindings, label: '无可用 Key', badgeClass: 'bg-red-50 text-red-600'};
    }
    return {available: true, activeBindings, label: '可用', badgeClass: 'bg-green-50 text-green-700'};
};

export const getEndpointOperationLabel = (endpoint: ChannelCapability): string => {
    const labels: Record<string, string> = {
        'images.generate': '生成',
        'images.edit': '编辑',
        'videos.generate': '视频生成',
    };
    const operations = endpoint.supportedOperations?.length
        ? endpoint.supportedOperations
        : endpoint.routeOperation ? [endpoint.routeOperation] : [];
    return operations.map(operation => labels[operation] || operation).join(' / ') || '未指定操作';
};

const getEndpointOperations = (endpoint: ChannelCapability): string[] =>
    endpoint.supportedOperations?.length
        ? endpoint.supportedOperations
        : endpoint.routeOperation ? [endpoint.routeOperation] : [];

export const groupEndpointModelRoutes = (endpoints: ChannelCapability[]): EndpointModelRouteGroup[] => {
    const groups = new Map<string, EndpointModelRouteGroup>();
    endpoints.forEach(endpoint => {
        const model = endpoint.model.trim();
        // 没有上游模型标识的旧端点不能确认属于同一线路，保持独立展示。
        const identity = model ? model.toLowerCase() : `endpoint:${endpoint.id}`;
        const id = `model-route:${endpoint.channelId}:${identity}`;
        const current = groups.get(id);
        if (current) {
            current.endpoints.push(endpoint);
            return;
        }
        groups.set(id, {
            id,
            channelId: endpoint.channelId,
            model: model || endpoint.name || `端点 #${endpoint.id}`,
            endpoints: [endpoint],
        });
    });
    const operationOrder: Record<string, number> = {
        'images.generate': 1,
        'images.edit': 2,
        'videos.generate': 3,
    };
    const operationRank = (endpoint: ChannelCapability) => {
        const ranks = getEndpointOperations(endpoint).map(operation => operationOrder[operation] ?? 99);
        return ranks.length > 0 ? Math.min(...ranks) : 99;
    };
    return Array.from(groups.values())
        .map(group => ({
            ...group,
            endpoints: [...group.endpoints].sort((left, right) =>
                operationRank(left) - operationRank(right)
                || left.id.localeCompare(right.id)
            ),
        }))
        .sort((left, right) => left.model.localeCompare(right.model) || left.channelId.localeCompare(right.channelId));
};

export const summarizeEndpointModelRoutes = (
    endpoints: ChannelCapability[],
    capabilityStatus: number,
    channelMap: Map<string, Channel>,
) => {
    const groups = groupEndpointModelRoutes(endpoints);
    const states = endpoints.map(endpoint => getEndpointRouteState(
        endpoint,
        capabilityStatus,
        channelMap.get(endpoint.channelId)?.status ?? 0,
    ));
    const operationCounts = endpoints.map(endpoint => Math.max(1, getEndpointOperations(endpoint).length));
    return {
        modelRouteCount: groups.length,
        operationCount: operationCounts.reduce((total, count) => total + count, 0),
        keyCount: new Set(endpoints.flatMap(endpoint => endpoint.accountBindings.map(binding => binding.accountId))).size,
        unavailableCount: states.reduce((total, state, index) => total + (state.available ? 0 : operationCounts[index]), 0),
    };
};

const BindingStatus: React.FC<{binding: EndpointAccountBinding}> = ({binding}) => {
    if (binding.accountStatus !== 1) return <span className="text-xs text-red-600">账号禁用</span>;
    if (binding.status !== 1) return <span className="text-xs text-amber-700">绑定禁用</span>;
    return <span className="text-xs text-green-700">可用</span>;
};

const OperationBadge: React.FC<{endpoint: ChannelCapability}> = ({endpoint}) => {
    const specified = getEndpointOperations(endpoint).length > 0;
    return (
        <span className={`inline-flex rounded px-2 py-1 text-xs font-medium ${specified ? 'bg-emerald-50 text-emerald-700' : 'bg-amber-50 text-amber-700'}`}>
            {getEndpointOperationLabel(endpoint)}
        </span>
    );
};

const CapabilityEndpointList: React.FC<{
    capabilityStatus: number;
    endpoints: ChannelCapability[];
    channelMap: Map<string, Channel>;
    expandedEndpointId: string | null;
    onToggleExpanded: (id: string) => void;
    onAdd: () => void;
    onEdit: (endpoint: ChannelCapability) => void;
    onManageKeys: (endpoint: ChannelCapability) => void;
    onToggleStatus: (endpoint: ChannelCapability) => void;
    onDelete: (id: string) => void;
}> = ({
    capabilityStatus, endpoints, channelMap, expandedEndpointId,
    onToggleExpanded, onAdd, onEdit, onManageKeys, onToggleStatus, onDelete,
}) => {
    const groups = groupEndpointModelRoutes(endpoints);

    return (
        <div className="border-t border-[var(--border-soft)] bg-[var(--surface)]/70 p-3 md:p-4">
            <div className="mb-3 flex items-center justify-between gap-3">
                <div>
                    <h4 className="text-sm font-bold text-[var(--text-primary)]">模型线路</h4>
                </div>
                <button onClick={onAdd}
                    className="inline-flex items-center gap-2 rounded-lg bg-[var(--primary)] px-3 py-2 text-sm font-bold text-white hover:opacity-90">
                    <Plus size={14} /> 添加操作端点
                </button>
            </div>

            {groups.length === 0 ? (
                <div className="border border-dashed border-[var(--border-soft)] px-5 py-8 text-center">
                    <div className="text-sm font-medium text-[var(--text-primary)]">当前能力还没有模型线路</div>
                    <button onClick={onAdd}
                        className="mt-3 inline-flex items-center gap-2 rounded-lg bg-[var(--primary)] px-3 py-2 text-sm font-bold text-white hover:opacity-90">
                        <Plus size={14} /> 添加操作端点
                    </button>
                </div>
            ) : (
                <div className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)]">
                    <div className="hidden grid-cols-[minmax(13rem,1.4fr)_minmax(9rem,1fr)_minmax(11rem,1.1fr)_7rem_8rem] gap-3 border-b border-[var(--border-soft)] bg-[var(--surface)] px-3 py-2 text-xs font-medium text-[var(--text-secondary)] lg:grid">
                        <span>上游模型</span><span>渠道</span><span>操作</span><span>Key</span><span>线路状态</span>
                    </div>
                    {groups.map(group => {
                        const channel = channelMap.get(group.channelId);
                        const states = group.endpoints.map(endpoint => getEndpointRouteState(endpoint, capabilityStatus, channel?.status ?? 0));
                        const availableCount = states.filter(state => state.available).length;
                        const allBindings = group.endpoints.flatMap(endpoint => endpoint.accountBindings);
                        const totalKeys = new Set(allBindings.map(binding => binding.accountId)).size;
                        const activeKeys = new Set(allBindings.filter(binding => binding.status === 1 && binding.accountStatus === 1).map(binding => binding.accountId)).size;
                        const groupOrigins = Array.from(new Set(group.endpoints.map(endpoint => {
                            const origin = getEndpointOriginPresentation(endpoint);
                            return [origin.label, origin.detail].filter(Boolean).join(' · ');
                        }))).join(' / ');
                        const isExpanded = expandedEndpointId === group.id;
                        const groupStatus = availableCount === group.endpoints.length
                            ? {label: '全部可用', className: 'bg-green-50 text-green-700'}
                            : availableCount > 0
                                ? {label: '部分不可用', className: 'bg-amber-50 text-amber-700'}
                                : {label: '不可用', className: 'bg-red-50 text-red-600'};
                        return (
                            <div key={group.id} className="border-b border-[var(--border-soft)] last:border-b-0">
                                <button type="button" onClick={() => onToggleExpanded(group.id)}
                                    className="grid w-full gap-3 px-3 py-3 text-left hover:bg-[var(--surface)]/70 lg:grid-cols-[minmax(13rem,1.4fr)_minmax(9rem,1fr)_minmax(11rem,1.1fr)_7rem_8rem] lg:items-center">
                                    <span className="flex min-w-0 items-center gap-2">
                                        <span className="text-[var(--text-secondary)]">{isExpanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}</span>
                                        <Layers3 size={16} className="shrink-0 text-[var(--primary)]" />
                                        <span className="min-w-0">
                                            <span className="block truncate text-sm font-semibold text-[var(--text-primary)]" title={group.model}>{group.model}</span>
                                            <span className="block text-xs text-[var(--text-secondary)]">
                                                {group.endpoints.length} 个端点 · {group.endpoints.reduce((total, endpoint) => total + Math.max(1, getEndpointOperations(endpoint).length), 0)} 个操作
                                            </span>
                                            <span className="block truncate text-xs text-[var(--text-tertiary)]" title={groupOrigins}>{groupOrigins}</span>
                                        </span>
                                    </span>
                                    <span className="truncate text-xs text-blue-700" title={channel?.name || group.channelId}>{channel?.name || group.channelId}</span>
                                    <span className="flex flex-wrap gap-1.5">{group.endpoints.map(endpoint => <OperationBadge key={endpoint.id} endpoint={endpoint} />)}</span>
                                    <span className="flex items-center gap-1.5 text-sm text-[var(--text-primary)]"><KeyRound size={14} className="text-[var(--text-secondary)]" /><strong>{activeKeys}</strong><span className="text-[var(--text-secondary)]">/ {totalKeys}</span></span>
                                    <span><span className={`inline-flex rounded-full px-2 py-1 text-xs font-medium ${groupStatus.className}`}>{groupStatus.label}</span></span>
                                </button>

                                {isExpanded && (
                                    <div className="border-t border-[var(--border-soft)] bg-[var(--surface)] px-3 py-3 md:pl-10">
                                        <div className="space-y-3">
                                            {group.endpoints.map(endpoint => {
                                                const routeState = getEndpointRouteState(endpoint, capabilityStatus, channel?.status ?? 0);
                                                const origin = getEndpointOriginPresentation(endpoint);
                                                return (
                                                    <div key={endpoint.id} className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)]">
                                                        <div className="grid gap-3 px-3 py-3 lg:grid-cols-[8rem_minmax(12rem,1fr)_7rem_8rem_9rem] lg:items-center">
                                                            <OperationBadge endpoint={endpoint} />
                                                            <div className="min-w-0 text-xs text-[var(--text-secondary)]">
                                                                <div className="truncate text-sm font-medium text-[var(--text-primary)]" title={endpoint.name || endpoint.model}>{endpoint.name || endpoint.model}</div>
                                                                <div className="mt-1 truncate" title={`${endpoint.requestMethod} ${endpoint.requestPath || ''}`}><strong>{endpoint.requestMethod}</strong> {endpoint.requestPath || '未配置路径'}</div>
                                                                <div className="mt-1">{endpoint.resultMode} · {formatPrice(endpoint.price)}/{endpoint.priceUnit || 'request'}</div>
                                                                <div className="mt-1 flex min-w-0 items-center gap-1.5">
                                                                    <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${origin.badgeClass}`}>{origin.label}</span>
                                                                    {origin.detail && <span className="truncate" title={origin.detail}>{origin.detail}</span>}
                                                                </div>
                                                            </div>
                                                            <button type="button" onClick={() => onManageKeys(endpoint)} className="flex items-center gap-1.5 text-left text-sm" title="管理 Key">
                                                                <KeyRound size={14} className="text-[var(--text-secondary)]" /><strong>{routeState.activeBindings.length}</strong><span className="text-[var(--text-secondary)]">/ {endpoint.accountBindings.length}</span>
                                                            </button>
                                                            <span><span className={`inline-flex rounded-full px-2 py-1 text-xs font-medium ${routeState.badgeClass}`}>{routeState.label}</span></span>
                                                            <div className="flex items-center gap-1 lg:justify-end">
                                                                <button type="button" onClick={() => onToggleStatus(endpoint)} className={`rounded-md p-2 ${endpoint.status === 1 ? 'text-amber-700 hover:bg-amber-50' : 'text-green-700 hover:bg-green-50'}`} title={endpoint.status === 1 ? '禁用端点' : '启用端点'}><Power size={15} /></button>
                                                                <button type="button" onClick={() => onManageKeys(endpoint)} className="rounded-md p-2 text-[var(--primary)] hover:bg-[var(--primary-lighter)]" title="管理 Key"><KeyRound size={15} /></button>
                                                                <button type="button" onClick={() => onEdit(endpoint)} className="rounded-md p-2 text-[var(--primary)] hover:bg-[var(--primary-lighter)]" title="编辑端点"><Edit2 size={15} /></button>
                                                                <button type="button" onClick={() => onDelete(endpoint.id)} className="rounded-md p-2 text-red-600 hover:bg-red-50" title="删除端点"><Trash2 size={15} /></button>
                                                            </div>
                                                        </div>
                                                        {endpoint.accountBindings.length > 0 && (
                                                            <div className="overflow-x-auto border-t border-[var(--border-soft)] bg-[var(--surface)] px-3 py-2">
                                                                <div className="grid min-w-[35rem] grid-cols-[minmax(11rem,1fr)_7rem_6rem_6rem_7rem] gap-3 px-2 pb-2 text-xs font-medium text-[var(--text-secondary)]">
                                                                    <span>Key</span><span>状态</span><span>优先级</span><span>权重</span><span></span>
                                                                </div>
                                                                {endpoint.accountBindings.map(binding => (
                                                                    <div key={binding.id || binding.accountId} className="grid min-w-[35rem] grid-cols-[minmax(11rem,1fr)_7rem_6rem_6rem_7rem] items-center gap-3 border-t border-[var(--border-soft)] px-2 py-2 text-sm">
                                                                        <span className="truncate font-medium" title={binding.accountName || `Key #${binding.accountId}`}>{binding.accountName || `Key #${binding.accountId}`}</span>
                                                                        <BindingStatus binding={binding} /><span>{binding.priority}</span><span>{binding.weight}</span>
                                                                        <button type="button" onClick={() => onManageKeys(endpoint)} className="inline-flex items-center justify-center gap-1 rounded-md px-2 py-1.5 text-xs font-medium text-[var(--primary)] hover:bg-[var(--primary-lighter)]"><Edit2 size={13} /> 编辑</button>
                                                                    </div>
                                                                ))}
                                                            </div>
                                                        )}
                                                    </div>
                                                );
                                            })}
                                        </div>
                                    </div>
                                )}
                            </div>
                        );
                    })}
                </div>
            )}
        </div>
    );
};

export default CapabilityEndpointList;
