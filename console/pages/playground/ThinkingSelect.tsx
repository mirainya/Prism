import React, { useState, useRef, useEffect } from 'react';
import { ChevronDown, Check } from 'lucide-react';

interface Option { label: string; value: string; }

// 思考档位下拉,视觉对齐 EnumSelect,工具栏紧凑尺寸,支持 label/value 与 locked
const ThinkingSelect: React.FC<{
  options: Option[];
  value: string;
  onChange: (v: string) => void;
  locked?: boolean;
}> = ({ options, value, onChange, locked }) => {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  const current = options.find(o => o.value === value);

  return (
    <div ref={ref} className="relative">
      <button type="button" onClick={() => !locked && setOpen(!open)}
        className={`inline-flex items-center gap-1.5 px-2 py-1 border border-[var(--border-soft)] rounded-lg text-[11px] bg-[var(--surface-card)] hover:bg-[var(--surface)] focus:outline-none focus:ring-2 focus:ring-[var(--primary)] transition-colors ${locked ? 'opacity-50 cursor-not-allowed' : ''}`}>
        <span className="text-[var(--text-secondary)]">{current?.label || current?.value || '默认'}</span>
        <ChevronDown size={12} className={`text-[var(--text-tertiary)] transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && (
        <div className="absolute top-full left-0 mt-1 min-w-[120px] bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-xl shadow-lg z-50 py-1 overflow-y-auto" style={{ maxHeight: '220px' }}>
          {options.map(opt => (
            <button key={opt.value} type="button" onClick={() => { onChange(opt.value); setOpen(false); }}
              className={`w-full text-left px-3 py-1.5 text-xs hover:bg-[var(--surface)] flex items-center gap-2 transition-colors ${opt.value === value ? 'text-[var(--primary)]' : 'text-[var(--text-primary)]'}`}>
              <span className="flex-1">{opt.label || opt.value}</span>
              {opt.value === value && <Check size={13} className="text-[var(--primary)] flex-shrink-0" />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
};

export default ThinkingSelect;
