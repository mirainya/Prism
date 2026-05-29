import React, { useState, useRef, useEffect } from 'react';
import { ChevronDown, Check } from 'lucide-react';

const EnumSelect: React.FC<{
  options: string[];
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  disabled?: boolean;
  description?: string;
}> = ({ options, value, onChange, placeholder = '请选择', disabled, description }) => {
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

  return (
    <div ref={ref} className="relative">
      <button type="button" onClick={() => !disabled && setOpen(!open)}
        className={`inline-flex items-center gap-2 w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface-card)] hover:bg-[var(--surface)] focus:outline-none focus:ring-2 focus:ring-[var(--primary)] transition-colors ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}>
        <span className={`truncate flex-1 text-left ${!value ? 'text-[var(--text-tertiary)]' : ''}`}>{value || placeholder}</span>
        <ChevronDown size={14} className={`text-[var(--text-tertiary)] flex-shrink-0 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && (
        <div className="absolute top-full left-0 mt-1 w-full min-w-[140px] bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-xl shadow-lg z-50 py-1 overflow-y-auto" style={{ maxHeight: '200px' }}>
          {description && <div className="px-3 py-1 text-[10px] text-[var(--text-tertiary)] border-b border-[var(--border-soft)] mb-1">{description}</div>}
          {options.map(opt => (
            <button key={opt} type="button" onClick={() => { onChange(opt); setOpen(false); }}
              className={`w-full text-left px-3 py-1.5 text-sm hover:bg-[var(--surface)] flex items-center gap-2 transition-colors ${opt === value ? 'text-[var(--primary)]' : 'text-[var(--text-primary)]'}`}>
              <span className="flex-1">{opt}</span>
              {opt === value && <Check size={14} className="text-[var(--primary)] flex-shrink-0" />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
};

export default EnumSelect;
