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
        <div className="channel-priority-editor">
            {capabilities.map(cap => (
                <section key={cap.code} className="channel-priority-group">
                    <div className="flex items-center justify-between mb-2">
                        <span className="text-sm font-bold text-[var(--text-primary)]">{cap.name}</span>
                        <code className="text-[11px] text-[var(--text-tertiary)]">{cap.code}</code>
                    </div>
                    <div>
                        {getPriorities(cap.code).map((p, idx) => (
                            <div key={p.channelId}
                                 className="channel-priority-row">
                                <span className="channel-priority-rank">{idx + 1}</span>
                                <span className="min-w-0 flex-1 truncate text-sm text-[var(--text-primary)]">{getName(cap.code, p.channelId)}</span>
                                <button type="button" title="上移" onClick={() => moveUp(cap.code, p.channelId)} disabled={idx === 0}
                                        className="modal-icon-button disabled:opacity-25">
                                    <ChevronUp size={14}/>
                                </button>
                                <button type="button" title="下移" onClick={() => moveDown(cap.code, p.channelId)}
                                        disabled={idx === getPriorities(cap.code).length - 1}
                                        className="modal-icon-button disabled:opacity-25">
                                    <ChevronDown size={14}/>
                                </button>
                                <button type="button" title="移除" onClick={() => removeChannel(cap.code, p.channelId)}
                                        className="modal-icon-button modal-icon-button-danger">
                                    <X size={14}/>
                                </button>
                            </div>
                        ))}
                        {getAvailable(cap.code).length > 0 && (
                            <Select
                                className="channel-priority-select"
                                value=""
                                onChange={v => { if (v) addChannel(cap.code, Number(v)); }}
                                options={[{ label: '+ 添加渠道', value: '' }, ...getAvailable(cap.code).map(ch => ({ label: `${ch.channelName}${ch.model ? ` (${ch.model})` : ''} - ¥${ch.price}`, value: String(ch.channelId) }))]}
                            />
                        )}
                    </div>
                </section>
            ))}
        </div>
    );
};
