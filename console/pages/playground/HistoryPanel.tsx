import React from 'react';
import { History, Plus, Loader2 } from 'lucide-react';
import { PlaygroundConversation } from '../../types';
import StatusBadge from './StatusBadge';
import { formatTime } from './utils';

const HistoryPanel: React.FC<{
  items: PlaygroundConversation[];
  selectedConversationId?: number;
  loadingConversationId?: number;
  currentModel?: string;
  onSelect: (conversation: PlaygroundConversation) => void;
  onCreateNew: () => void;
  loading: boolean;
}> = ({ items, selectedConversationId, loadingConversationId, currentModel, onSelect, onCreateNew, loading }) => {
  return (
    <div className="w-72 flex-shrink-0 bg-[var(--surface-card)] rounded-xl border border-[var(--border-soft)] overflow-hidden flex flex-col">
      <div className="px-4 py-3 border-b border-[var(--border-soft)] flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)]">
          <History size={16} /> 历史会话
        </div>
        <button
          type="button"
          onClick={onCreateNew}
          className="inline-flex items-center gap-1 rounded-lg border border-indigo-200 bg-[var(--primary-lighter)] px-2 py-1 text-xs font-medium text-[var(--primary)] hover:bg-indigo-100"
        >
          <Plus size={12} /> 新会话
        </button>
      </div>
      <div className="flex-1 overflow-y-auto">
        {loading ? (
          <div className="p-4 text-sm text-[var(--text-secondary)] flex items-center gap-2"><Loader2 size={14} className="animate-spin" /> 加载中...</div>
        ) : items.length === 0 ? (
          <div className="p-4 text-sm text-[var(--text-secondary)]">还没有历史会话</div>
        ) : items.map(item => {
          const modelMatched = currentModel && item.model === currentModel;
          return (
            <button
              key={item.id}
              onClick={() => onSelect(item)}
              className={`w-full text-left px-4 py-3 border-b border-[var(--border-soft)] hover:bg-[var(--surface)] transition-colors ${selectedConversationId === item.id ? 'bg-[var(--primary-lighter)]' : ''}`}
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="text-sm font-medium text-gray-800 truncate">{item.title || `会话 #${item.id}`}</div>
                  <div className="mt-1 flex items-center gap-2 flex-wrap">
                    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-[11px] ${modelMatched ? 'bg-indigo-100 text-[var(--primary)]' : 'bg-[var(--primary-lighter)] text-[var(--text-secondary)]'}`}>{item.model}</span>
                    <span className="text-[11px] text-[var(--text-secondary)]">{item.messageCount} 条消息</span>
                  </div>
                </div>
                {loadingConversationId === item.id
                  ? <Loader2 size={14} className="animate-spin text-[var(--primary)]"/>
                  : <StatusBadge status={item.lastStatus || 'pending'} />}
              </div>
              <div className="mt-2 text-[11px] text-[var(--text-secondary)] flex items-center justify-between gap-2">
                <span className="truncate">会话 #{item.id}</span>
                <span>{formatTime(item.updatedAt)}</span>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
};

export default HistoryPanel;
