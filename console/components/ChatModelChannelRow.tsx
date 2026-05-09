import React from 'react';
import {Edit3, Power, Settings2, Trash2} from 'lucide-react';
import {ChatModelChannel, PRICE_MODES} from '../types';

const STATUS_MAP: Record<number, { label: string; color: string }> = {
    1: {label: '已启用', color: 'bg-green-100 text-green-700'},
    0: {label: '已禁用', color: 'bg-gray-100 text-gray-700'},
};

const getPriceModeLabel = (mode: string) => {
    return PRICE_MODES.find(item => item.value === mode)?.label || mode;
};

const hasAdvancedConfig = (mapping: ChatModelChannel) => {
    return Object.keys(mapping.extraHeaders || {}).length > 0 || Object.keys(mapping.extraConfig || {}).length > 0;
};

const getStreamSummary = (mapping: ChatModelChannel) => {
    if (mapping.supportsStream === false) {
        return '不支持流式';
    }
    if (mapping.defaultStream) {
        return '默认流式';
    }
    if (mapping.supportsStream) {
        return '支持流式';
    }
    return '流式未声明';
};

const ChatModelChannelRow: React.FC<{
    mapping: ChatModelChannel;
    onEdit: (mapping: ChatModelChannel) => void;
    onDelete: (id: number) => void;
    onToggleStatus: (mapping: ChatModelChannel) => void;
}> = ({mapping, onEdit, onDelete, onToggleStatus}) => {
    return (
        <div className="flex items-center justify-between gap-4 rounded-xl border border-gray-100 bg-white px-4 py-4 hover:border-gray-200 transition-colors">
            <div className="min-w-0 flex-1 grid grid-cols-1 md:grid-cols-5 gap-4">
                <div className="min-w-0">
                    <div className="text-sm font-medium text-gray-900 truncate">{mapping.channel?.name || '-'}</div>
                    <div className="text-xs text-gray-500 mt-1">{mapping.channel?.type || '-'}</div>
                </div>
                <div className="min-w-0">
                    <div className="text-sm font-mono text-gray-700 truncate">{mapping.vendorModel}</div>
                    <div className="text-xs text-gray-500 mt-1 truncate">{mapping.requestPath || '-'}</div>
                </div>
                <div>
                    <div className="text-sm text-gray-700">优先级 {mapping.priority}</div>
                    <div className="text-xs text-gray-500 mt-1">{getPriceModeLabel(mapping.priceMode)}</div>
                    <div className="text-xs text-gray-500 mt-1">{getStreamSummary(mapping)}</div>
                </div>
                <div className="text-xs text-gray-600">
                    {mapping.priceMode === 'token' ? (
                        <>
                            <div>输入: ￥{mapping.inputPrice}/1M</div>
                            <div className="mt-1">输出: ￥{mapping.outputPrice}/1M</div>
                        </>
                    ) : (
                        <div>￥{mapping.inputPrice}/次</div>
                    )}
                </div>
                <div className="flex items-center gap-2 flex-wrap">
                    <span className={`px-2 py-1 rounded text-xs font-medium ${STATUS_MAP[mapping.status]?.color || STATUS_MAP[0].color}`}>
                        {STATUS_MAP[mapping.status]?.label || '未知状态'}
                    </span>
                    {mapping.supportsStream === false && (
                        <span className="px-2 py-1 rounded text-xs font-medium bg-rose-100 text-rose-700">
                            非流式
                        </span>
                    )}
                    {mapping.defaultStream && mapping.supportsStream !== false && (
                        <span className="px-2 py-1 rounded text-xs font-medium bg-blue-100 text-blue-700">
                            默认流式
                        </span>
                    )}
                    {Object.keys(mapping.extraHeaders || {}).length > 0 && (
                        <span className="px-2 py-1 rounded text-xs font-medium bg-violet-100 text-violet-700">
                            Headers
                        </span>
                    )}
                    {hasAdvancedConfig(mapping) && (
                        <span className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium bg-amber-100 text-amber-700">
                            <Settings2 size={12}/>
                            Extra Body
                        </span>
                    )}
                </div>
            </div>
            <div className="flex items-center gap-1 shrink-0">
                <button
                    onClick={() => onToggleStatus(mapping)}
                    className={`p-2 rounded-lg transition-colors ${mapping.status === 1 ? 'text-green-600 hover:bg-green-50' : 'text-gray-400 hover:bg-gray-100'}`}
                    title={mapping.status === 1 ? '禁用' : '启用'}
                >
                    <Power size={16}/>
                </button>
                <button
                    onClick={() => onEdit(mapping)}
                    className="p-2 text-gray-400 hover:text-indigo-600 hover:bg-indigo-50 rounded-lg transition-colors"
                    title="编辑"
                >
                    <Edit3 size={16}/>
                </button>
                <button
                    onClick={() => onDelete(mapping.id)}
                    className="p-2 text-gray-400 hover:text-red-600 hover:bg-red-50 rounded-lg transition-colors"
                    title="删除"
                >
                    <Trash2 size={16}/>
                </button>
            </div>
        </div>
    );
};

export default ChatModelChannelRow;
