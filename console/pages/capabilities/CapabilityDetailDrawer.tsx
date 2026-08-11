import React, { useMemo } from 'react';
import { CircleAlert, KeyRound, Layers3, Route } from 'lucide-react';
import { Capability, Channel, ChannelCapability } from '../../types';
import CapabilityEndpointList, { summarizeEndpointModelRoutes } from './CapabilityEndpointList';
import { Drawer } from '../../components/ui';

const CapabilityDetailDrawer: React.FC<{
    open: boolean;
    capability: Capability;
    endpoints: ChannelCapability[];
    channelMap: Map<string, Channel>;
    expandedEndpointId: string | null;
    onClose: () => void;
    onToggleExpanded: (id: string) => void;
    onAdd: () => void;
    onEdit: (endpoint: ChannelCapability) => void;
    onManageKeys: (endpoint: ChannelCapability) => void;
    onToggleStatus: (endpoint: ChannelCapability) => void;
    onDelete: (id: string) => void;
}> = ({
    open, capability, endpoints, channelMap, expandedEndpointId, onClose,
    onToggleExpanded, onAdd, onEdit, onManageKeys, onToggleStatus, onDelete,
}) => {
    const summary = useMemo(
        () => summarizeEndpointModelRoutes(endpoints, capability.status, channelMap),
        [capability.status, channelMap, endpoints],
    );

    const items = [
        {label: '模型线路', value: summary.modelRouteCount, icon: Layers3, className: 'text-blue-600'},
        {label: '操作', value: summary.operationCount, icon: Route, className: 'text-emerald-600'},
        {label: '绑定 Key', value: summary.keyCount, icon: KeyRound, className: 'text-violet-600'},
        {label: '不可用操作', value: summary.unavailableCount, icon: CircleAlert, className: summary.unavailableCount ? 'text-red-600' : 'text-[var(--text-secondary)]'},
    ];

    return (
        <Drawer open={open} title="能力详情" subtitle={<span className="font-mono">{capability.code}</span>} onClose={onClose} width="max-w-5xl">
                <header className="border-b border-[var(--border-soft)] bg-[var(--surface-card)] px-4 py-4 md:px-6">
                    <div className="flex items-start gap-3">
                        <div className="min-w-0 flex-1">
                            <div className="flex flex-wrap items-center gap-2">
                                <h2 id="capability-detail-title" className="truncate text-lg font-bold text-[var(--text-primary)]">
                                    {capability.name}
                                </h2>
                                <span className={`rounded-full px-2 py-0.5 text-xs ${capability.status === 1 ? 'bg-green-50 text-green-700' : 'bg-gray-100 text-gray-600'}`}>
                                    {capability.status === 1 ? '启用' : '禁用'}
                                </span>
                            </div>
                            <div className="mt-1 truncate text-xs text-[var(--text-secondary)]" title={capability.code}>{capability.code}</div>
                        </div>
                    </div>
                    <div className="mt-4 grid grid-cols-4 overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface)]">
                        {items.map(({label, value, icon: Icon, className}, index) => (
                            <div key={label} className={`min-w-0 px-2 py-2.5 text-center ${index ? 'border-l border-[var(--border-soft)]' : ''}`}>
                                <div className={`flex items-center justify-center gap-1 ${className}`}>
                                    <Icon size={13} />
                                    <span className="text-sm font-bold">{value}</span>
                                </div>
                                <div className="mt-0.5 truncate text-[11px] text-[var(--text-secondary)]">{label}</div>
                            </div>
                        ))}
                    </div>
                </header>
                <div className="min-h-0 flex-1 overflow-y-auto">
                    <CapabilityEndpointList
                        capabilityStatus={capability.status}
                        endpoints={endpoints}
                        channelMap={channelMap}
                        expandedEndpointId={expandedEndpointId}
                        onToggleExpanded={onToggleExpanded}
                        onAdd={onAdd}
                        onEdit={onEdit}
                        onManageKeys={onManageKeys}
                        onToggleStatus={onToggleStatus}
                        onDelete={onDelete}
                    />
                </div>
        </Drawer>
    );
};

export default CapabilityDetailDrawer;
