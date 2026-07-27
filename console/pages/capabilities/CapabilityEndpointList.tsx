import React from 'react';
import {
    ChevronDown, ChevronRight, Edit2, KeyRound, Power, Plus, Trash2,
} from 'lucide-react';
import { Channel, ChannelCapability, EndpointAccountBinding } from '../../types';
import { formatPrice } from './constants';

export type EndpointRouteState = {
    available: boolean;
    activeBindings: EndpointAccountBinding[];
    label: string;
    badgeClass: string;
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

const BindingStatus: React.FC<{binding: EndpointAccountBinding}> = ({binding}) => {
    if (binding.accountStatus !== 1) {
        return <span className="text-xs text-red-600">账号禁用</span>;
    }
    if (binding.status !== 1) {
        return <span className="text-xs text-amber-700">绑定禁用</span>;
    }
    return <span className="text-xs text-green-700">可用</span>;
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
}) => (
    <div className="border-t border-[var(--border-soft)] bg-[var(--surface)]/70 p-3 md:p-4">
        <div className="mb-3 flex items-center justify-between gap-3">
            <div>
                <h4 className="text-sm font-bold text-[var(--text-primary)]">能力端点</h4>
                <p className="mt-0.5 text-xs text-[var(--text-secondary)]">展开端点可查看绑定 Key 与调度参数</p>
            </div>
            <button onClick={onAdd}
                className="inline-flex items-center gap-2 rounded-lg bg-[var(--primary)] px-3 py-2 text-sm font-bold text-white hover:opacity-90">
                <Plus size={14} />
                添加端点
            </button>
        </div>

        {endpoints.length === 0 ? (
            <div className="border border-dashed border-[var(--border-soft)] px-5 py-8 text-center">
                <div className="text-sm font-medium text-[var(--text-primary)]">当前能力还没有端点</div>
                <button onClick={onAdd}
                    className="mt-3 inline-flex items-center gap-2 rounded-lg bg-[var(--primary)] px-3 py-2 text-sm font-bold text-white hover:opacity-90">
                    <Plus size={14} />
                    添加端点
                </button>
            </div>
        ) : (
            <div className="overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)]">
                <div className="hidden grid-cols-[minmax(13rem,1.4fr)_minmax(10rem,1fr)_minmax(8rem,.8fr)_7rem_7rem_9rem] gap-3 border-b border-[var(--border-soft)] bg-[var(--surface)] px-3 py-2 text-xs font-medium text-[var(--text-secondary)] lg:grid">
                    <span>端点</span>
                    <span>渠道 / 模式</span>
                    <span>请求</span>
                    <span>Key</span>
                    <span>状态</span>
                    <span className="text-right">操作</span>
                </div>
                {endpoints.map(endpoint => {
                    const channel = channelMap.get(endpoint.channelId);
                    const routeState = getEndpointRouteState(endpoint, capabilityStatus, channel?.status ?? 0);
                    const isExpanded = expandedEndpointId === endpoint.id;
                    return (
                        <div key={endpoint.id} className="border-b border-[var(--border-soft)] last:border-b-0">
                            <div className="grid gap-3 px-3 py-3 lg:grid-cols-[minmax(13rem,1.4fr)_minmax(10rem,1fr)_minmax(8rem,.8fr)_7rem_7rem_9rem] lg:items-center">
                                <button type="button" onClick={() => onToggleExpanded(endpoint.id)}
                                    className="flex min-w-0 items-center gap-2 text-left">
                                    <span className="text-[var(--text-secondary)]">
                                        {isExpanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                                    </span>
                                    <span className="min-w-0">
                                        <span className="block truncate text-sm font-semibold text-[var(--text-primary)]" title={endpoint.name || endpoint.model || '未命名端点'}>
                                            {endpoint.name || endpoint.model || '未命名端点'}
                                        </span>
                                        <span className="block truncate text-xs text-[var(--text-secondary)]" title={endpoint.model}>
                                            {endpoint.model || '未配置上游模型'}
                                        </span>
                                    </span>
                                </button>
                                <div className="flex min-w-0 items-center gap-2 text-xs">
                                    <span className="truncate rounded bg-blue-50 px-2 py-1 text-blue-700" title={channel?.name || endpoint.channelId}>
                                        {channel?.name || endpoint.channelId}
                                    </span>
                                    <span className="rounded bg-violet-50 px-2 py-1 text-violet-700">{endpoint.resultMode}</span>
                                </div>
                                <div className="min-w-0 text-xs text-[var(--text-secondary)]">
                                    <div className="truncate" title={`${endpoint.requestMethod} ${endpoint.requestPath || ''}`}>
                                        <span className="mr-1 font-semibold text-[var(--text-primary)]">{endpoint.requestMethod}</span>
                                        {endpoint.requestPath || '未配置路径'}
                                    </div>
                                    <div className="mt-1">{formatPrice(endpoint.price)}/{endpoint.priceUnit || 'request'}</div>
                                </div>
                                <button type="button" onClick={() => onToggleExpanded(endpoint.id)}
                                    className="flex items-center gap-1.5 text-left text-sm text-[var(--text-primary)]"
                                    title="查看绑定 Key">
                                    <KeyRound size={14} className="text-[var(--text-secondary)]" />
                                    <span className="font-semibold">{routeState.activeBindings.length}</span>
                                    <span className="text-[var(--text-secondary)]">/ {endpoint.accountBindings.length}</span>
                                </button>
                                <div>
                                    <span className={`inline-flex rounded-full px-2 py-1 text-xs font-medium ${routeState.badgeClass}`}>
                                        {routeState.label}
                                    </span>
                                </div>
                                <div className="flex items-center justify-start gap-1 lg:justify-end">
                                    <button type="button" onClick={() => onToggleStatus(endpoint)}
                                        className={`rounded-md p-2 ${endpoint.status === 1 ? 'text-amber-700 hover:bg-amber-50' : 'text-green-700 hover:bg-green-50'}`}
                                        title={endpoint.status === 1 ? '禁用端点' : '启用端点'}>
                                        <Power size={15} />
                                    </button>
                                    <button type="button" onClick={() => onManageKeys(endpoint)}
                                        className="rounded-md p-2 text-[var(--primary)] hover:bg-[var(--primary-lighter)]" title="管理 Key">
                                        <KeyRound size={15} />
                                    </button>
                                    <button type="button" onClick={() => onEdit(endpoint)}
                                        className="rounded-md p-2 text-[var(--primary)] hover:bg-[var(--primary-lighter)]" title="编辑端点">
                                        <Edit2 size={15} />
                                    </button>
                                    <button type="button" onClick={() => onDelete(endpoint.id)}
                                        className="rounded-md p-2 text-red-600 hover:bg-red-50" title="删除端点">
                                        <Trash2 size={15} />
                                    </button>
                                </div>
                            </div>

                            {isExpanded && (
                                <div className="border-t border-[var(--border-soft)] bg-[var(--surface)] px-3 py-3 md:pl-11">
                                    {endpoint.accountBindings.length === 0 ? (
                                        <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-[var(--text-secondary)]">
                                            <span>该端点尚未绑定 Key</span>
                                            <button type="button" onClick={() => onManageKeys(endpoint)}
                                                className="inline-flex items-center gap-1.5 rounded-lg border border-[var(--border-soft)] px-3 py-1.5 text-sm font-medium text-[var(--primary)] hover:bg-[var(--primary-lighter)]">
                                                <KeyRound size={14} /> 管理 Key
                                            </button>
                                        </div>
                                    ) : (
                                        <div className="overflow-x-auto">
                                            <div className="grid min-w-[38rem] grid-cols-[minmax(11rem,1fr)_7rem_6rem_6rem_7rem] gap-3 px-2 pb-2 text-xs font-medium text-[var(--text-secondary)]">
                                                <span>Key</span><span>状态</span><span>优先级</span><span>权重</span><span></span>
                                            </div>
                                            {endpoint.accountBindings.map(binding => (
                                                <div key={binding.id || binding.accountId}
                                                    className="grid min-w-[38rem] grid-cols-[minmax(11rem,1fr)_7rem_6rem_6rem_7rem] items-center gap-3 border-t border-[var(--border-soft)] px-2 py-2 text-sm">
                                                    <span className="truncate font-medium text-[var(--text-primary)]" title={binding.accountName || `Key #${binding.accountId}`}>
                                                        {binding.accountName || `Key #${binding.accountId}`}
                                                    </span>
                                                    <BindingStatus binding={binding} />
                                                    <span>{binding.priority}</span>
                                                    <span>{binding.weight}</span>
                                                    <button type="button" onClick={() => onManageKeys(endpoint)}
                                                        className="inline-flex items-center justify-center gap-1 rounded-md px-2 py-1.5 text-xs font-medium text-[var(--primary)] hover:bg-[var(--primary-lighter)]">
                                                        <Edit2 size={13} /> 编辑
                                                    </button>
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
        )}
    </div>
);

export default CapabilityEndpointList;
