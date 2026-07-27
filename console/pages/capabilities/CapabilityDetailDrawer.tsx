import React, { useEffect, useMemo } from 'react';
import { CircleAlert, CircleCheck, KeyRound, Route, X } from 'lucide-react';
import { Capability, Channel, ChannelCapability } from '../../types';
import CapabilityEndpointList, { getEndpointRouteState } from './CapabilityEndpointList';

const CapabilityDetailDrawer: React.FC<{
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
    capability, endpoints, channelMap, expandedEndpointId, onClose,
    onToggleExpanded, onAdd, onEdit, onManageKeys, onToggleStatus, onDelete,
}) => {
    useEffect(() => {
        const previousOverflow = document.body.style.overflow;
        const handleKeyDown = (event: KeyboardEvent) => {
            if (event.key === 'Escape') onClose();
        };
        document.body.style.overflow = 'hidden';
        window.addEventListener('keydown', handleKeyDown);
        return () => {
            document.body.style.overflow = previousOverflow;
            window.removeEventListener('keydown', handleKeyDown);
        };
    }, [onClose]);

    const summary = useMemo(() => {
        const states = endpoints.map(endpoint => getEndpointRouteState(
            endpoint,
            capability.status,
            channelMap.get(endpoint.channelId)?.status ?? 0,
        ));
        return {
            endpointCount: endpoints.length,
            keyCount: new Set(endpoints.flatMap(endpoint => endpoint.accountBindings.map(binding => binding.accountId))).size,
            activeRouteCount: states.reduce((total, state) => total + (state.available ? state.activeBindings.length : 0), 0),
            unavailableCount: states.filter(state => !state.available).length,
        };
    }, [capability.status, channelMap, endpoints]);

    const items = [
        {label: '端点', value: summary.endpointCount, icon: Route, className: 'text-blue-600'},
        {label: '绑定 Key', value: summary.keyCount, icon: KeyRound, className: 'text-violet-600'},
        {label: '有效线路', value: summary.activeRouteCount, icon: CircleCheck, className: 'text-green-600'},
        {label: '不可用', value: summary.unavailableCount, icon: CircleAlert, className: summary.unavailableCount ? 'text-red-600' : 'text-[var(--text-secondary)]'},
    ];

    return (
        <div className="fixed inset-0 z-40 bg-black/30" onClick={onClose}>
            <aside role="dialog" aria-modal="true" aria-labelledby="capability-detail-title"
                className="ml-auto flex h-full w-full max-w-5xl flex-col border-l border-[var(--border-soft)] bg-[var(--surface-card)] shadow-2xl animate-slide-in-right"
                onClick={event => event.stopPropagation()}>
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
                        <button type="button" onClick={onClose}
                            className="rounded-lg p-2 text-[var(--text-secondary)] hover:bg-[var(--surface)] hover:text-[var(--text-primary)]"
                            title="关闭">
                            <X size={18} />
                        </button>
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
            </aside>
        </div>
    );
};

export default CapabilityDetailDrawer;
