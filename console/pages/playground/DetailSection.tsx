import React, { useState } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';

const DetailSection: React.FC<{
  title: string;
  icon: React.ReactNode;
  content: string;
}> = ({ title, icon, content }) => {
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="rounded-lg border border-[var(--border-soft)] overflow-hidden bg-[var(--surface-card)]">
      <button
        type="button"
        onClick={() => setExpanded(prev => !prev)}
        className="w-full flex items-center justify-between px-3 py-2 bg-[var(--surface)] text-xs font-semibold text-[var(--text-secondary)]"
      >
        <span className="flex items-center gap-2">{icon} {title}</span>
        {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
      </button>
      {expanded && (
        <pre className="p-3 text-xs overflow-auto max-h-72 bg-[var(--surface)] border-t border-[var(--border-soft)]">{content}</pre>
      )}
    </div>
  );
};

export default DetailSection;
