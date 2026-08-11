import React from 'react';
import { ChevronUp, ChevronDown, X } from 'lucide-react';
import { Select } from '../components/ui';
import { ChannelPriorityItem, CapabilityWithChannels, ChannelOption } from '../types';

export const ChannelConfigEditor: React.FC<{
    priorities: ChannelPriorityItem[];
    setPriorities: (p: ChannelPriorityItem[]) => void;
    capabilities: CapabilityWithChannels[];
    loading: boolean;
}> = ({priorities, setPriorities, capabilities, loading}) => {
    const getPriorities = (code: string) => priorities.filter(p => p.capabilityCode === code).sort((a, b) => a.priority - b.priority);

    const addChannel = (code: string, channelId: number) => {
        const existing = priorities.filter(p => p.capabilityCode === code);
        const maxP = existing.length > 0 ? Math.max(...existing.map(p => p.priority)) : 0;
        setPriorities([...priorities, {capabilityCode: code, channelId, priority: maxP + 1}]);
    };

    const removeChannel = (code: string, channelId: number) => {
        const newP = priorities.filter(p => !(p.capabilityCode === code && p.channelId === channelId));
        const capP = newP.filter(p => p.capabilityCode === code).sort((a, b) => a.priority - b.priority).map((p, i) => ({
            ...p,
            priority: i + 1
        }));
        const otherP = newP.filter(p => p.capabilityCode !== code);
        setPriorities([...otherP, ...capP]);
    };

    const moveUp = (code: string, channelId: number) => {
        const capP = priorities.filter(p => p.capabilityCode === code).sort((a, b) => a.priority - b.priority);
        const idx = capP.findIndex(p => p.channelId === channelId);
        if (idx <= 0) return;
        const temp = capP[idx].priority;
        capP[idx] = {...capP[idx], priority: capP[idx - 1].priority};
        capP[idx - 1] = {...capP[idx - 1], priority: temp};
        const otherP = priorities.filter(p => p.capabilityCode !== code);
        setPriorities([...otherP, ...capP]);
    };

    const moveDown = (code: string, channelId: number) => {
        const capP = priorities.filter(p => p.capabilityCode === code).sort((a, b) => a.priority - b.priority);
        const idx = capP.findIndex(p => p.channelId === channelId);
        if (idx < 0 || idx >= capP.length - 1) return;
        const temp = capP[idx].priority;
        capP[idx] = {...capP[idx], priority: capP[idx + 1].priority};
        capP[idx + 1] = {...capP[idx + 1], priority: temp};
        const otherP = priorities.filter(p => p.capabilityCode !== code);
        setPriorities([...otherP, ...capP]);
    };

    const getName = (code: string, channelId: number): string => {
        const cap = capabilities.find(c => c.code === code);
        if (!cap) return `渠道 ${channelId}`;
        const ch = cap.channels.find(c => c.channelId === channelId);
        return ch ? `${ch.channelName}${ch.model ? ` (${ch.model})` : ''}` : `渠道 ${channelId}`;
    };

    const getAvailable = (code: string): ChannelOption[] => {
        const cap = capabilities.find(c => c.code === code);
        if (!cap) return [];
        const used = priorities.filter(p => p.capabilityCode === code).map(p => p.channelId);
        return cap.channels.filter(ch => !used.includes(ch.channelId));
    };

    if (loading) {
        return <div className="text-sm text-[var(--text-secondary)] py-4 text-center">加载中...</div>;
    }

    if (capabilities.length === 0) {
        return <div className="text-sm text-[var(--text-secondary)] py-4 text-center">暂无可用能力</div>;
    }

    return (
        <div className="space-y-4 max-h-80 overflow-y-auto">
            {capabilities.map(cap => (
                <div key={cap.code} className="border border-[var(--border-soft)] rounded-lg p-3">
                    <div className="flex items-center justify-between mb-2">
                        <span className="font-medium text-[var(--text-primary)]">{cap.name}</span>
                        <span className="text-xs text-[var(--text-secondary)]">{cap.code}</span>
                    </div>
                    <div className="space-y-2">
                        {getPriorities(cap.code).map((p, idx) => (
                            <div key={p.channelId}
                                 className="flex items-center gap-2 bg-[var(--surface)] rounded-lg px-3 py-2">
                                <span className="text-sm text-[var(--text-secondary)] w-6">{idx + 1}.</span>
                                <span className="flex-1 text-sm">{getName(cap.code, p.channelId)}</span>
                                <button onClick={() => moveUp(cap.code, p.channelId)} disabled={idx === 0}
                                        className="p-1 hover:bg-gray-200 rounded disabled:opacity-30">
                                    <ChevronUp size={14}/>
                                </button>
                                <button onClick={() => moveDown(cap.code, p.channelId)}
                                        disabled={idx === getPriorities(cap.code).length - 1}
                                        className="p-1 hover:bg-gray-200 rounded disabled:opacity-30">
                                    <ChevronDown size={14}/>
                                </button>
                                <button onClick={() => removeChannel(cap.code, p.channelId)}
                                        className="p-1 hover:bg-red-100 text-red-500 rounded">
                                    <X size={14}/>
                                </button>
                            </div>
                        ))}
                        {getAvailable(cap.code).length > 0 && (
                            <Select
                                value=""
                                onChange={v => { if (v) addChannel(cap.code, Number(v)); }}
                                options={[{ label: '+ 添加渠道', value: '' }, ...getAvailable(cap.code).map(ch => ({ label: `${ch.channelName}${ch.model ? ` (${ch.model})` : ''} - ¥${ch.price}`, value: String(ch.channelId) }))]}
                            />
                        )}
                    </div>
                </div>
            ))}
        </div>
    );
};
