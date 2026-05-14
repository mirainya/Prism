import React, { useState } from 'react';
import { Brain, ChevronDown, ChevronRight } from 'lucide-react';

const ThinkingBlock: React.FC<{ content: string }> = ({ content }) => {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="border-b border-[var(--border-soft)]">
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center gap-1.5 px-4 py-2 text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)] transition-colors"
      >
        <Brain size={12} />
        <span>思考过程</span>
        {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
      </button>
      {expanded && (
        <div className="px-4 pb-3 text-xs text-[var(--text-secondary)] whitespace-pre-wrap bg-[var(--surface)] max-h-60 overflow-y-auto">
          {content}
        </div>
      )}
    </div>
  );
};

export default ThinkingBlock;
