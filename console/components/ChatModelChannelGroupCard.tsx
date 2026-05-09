import React from 'react';
import {ChevronDown, ChevronRight, Plus} from 'lucide-react';
import {ChatModelChannel} from '../types';
import ChatModelChannelRow from './ChatModelChannelRow';

export interface ChatModelChannelGroup {
    modelCode: string;
    modelName: string;
    provider?: string;
    mappings: ChatModelChannel[];
    enabledCount: number;
}

const ChatModelChannelGroupCard: React.FC<{
    group: ChatModelChannelGroup;
    expanded: boolean;
    onToggle: () => void;
    onAdd: (modelCode: string) => void;
    onEdit: (mapping: ChatModelChannel) => void;
    onDelete: (id: number) => void;
    onToggleStatus: (mapping: ChatModelChannel) => void;
}> = ({group, expanded, onToggle, onAdd, onEdit, onDelete, onToggleStatus}) => {
    return (
        <div className="bg-white rounded-2xl border border-gray-100 shadow-sm overflow-hidden">
            <div
                className="p-4 flex items-center justify-between gap-4 cursor-pointer hover:bg-gray-50"
                onClick={onToggle}
            >
                <div className="flex items-center gap-3 min-w-0">
                    <div className="w-10 h-10 rounded-xl bg-indigo-100 flex items-center justify-center text-indigo-600 font-bold uppercase text-xs">
                        {group.modelName.slice(0, 2)}
                    </div>
                    <div className="min-w-0">
                        <div className="flex items-center gap-2 flex-wrap">
                            <span className="font-semibold text-gray-900">{group.modelName}</span>
                            <code className="text-xs px-2 py-0.5 bg-gray-100 rounded text-gray-600">{group.modelCode}</code>
                            {group.provider && (
                                <span className="text-xs px-2 py-0.5 rounded bg-blue-100 text-blue-700">
                                    {group.provider}
                                </span>
                            )}
                        </div>
                        <div className="text-sm text-gray-500 mt-1">
                            共 {group.mappings.length} 条映射，已启用 {group.enabledCount} 条
                        </div>
                    </div>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                    <button
                        type="button"
                        onClick={(event) => {
                            event.stopPropagation();
                            onAdd(group.modelCode);
                        }}
                        className="flex items-center gap-1 px-3 py-1.5 text-sm text-indigo-600 hover:text-indigo-700 hover:bg-indigo-50 rounded-lg transition-colors"
                    >
                        <Plus size={16}/>
                        添加映射
                    </button>
                    {expanded ? (
                        <ChevronDown size={18} className="text-gray-400"/>
                    ) : (
                        <ChevronRight size={18} className="text-gray-400"/>
                    )}
                </div>
            </div>

            {expanded && (
                <div className="border-t border-gray-100 bg-gray-50 p-4 space-y-3">
                    {group.mappings.map(mapping => (
                        <ChatModelChannelRow
                            key={mapping.id}
                            mapping={mapping}
                            onEdit={onEdit}
                            onDelete={onDelete}
                            onToggleStatus={onToggleStatus}
                        />
                    ))}
                </div>
            )}
        </div>
    );
};

export default ChatModelChannelGroupCard;
