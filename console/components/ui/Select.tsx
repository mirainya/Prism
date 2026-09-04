import React, { useState, useRef, useEffect } from 'react';
import { ChevronDown, Check } from 'lucide-react';

export interface SelectOption {
  label: string;
  value: string;
}

export const Select: React.FC<{
  options: SelectOption[];
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
}> = ({ options, value, onChange, placeholder = '请选择', disabled, className = '' }) => {
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

  const selected = options.find(o => o.value === value);

  return (
    <div ref={ref} className={`select-control relative ${className}`}>
      <button type="button" onClick={() => !disabled && setOpen(!open)}
        className={`select-trigger inline-flex items-center gap-2 w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface-card)] hover:bg-[var(--surface)] focus:outline-none transition-colors ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}>
        <span className={`truncate flex-1 text-left ${!selected ? 'text-[var(--text-tertiary)]' : ''}`}>{selected?.label || placeholder}</span>
        <ChevronDown size={14} className={`text-[var(--text-tertiary)] flex-shrink-0 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && (
        <div className="select-menu absolute top-full left-0 mt-1 w-full min-w-[140px] bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg shadow-lg z-50 py-1 overflow-y-auto" style={{ maxHeight: '240px' }}>
          {options.map(opt => (
            <button key={opt.value} type="button" onClick={() => { onChange(opt.value); setOpen(false); }}
              className={`select-option w-full text-left px-3 py-1.5 text-sm hover:bg-[var(--surface)] flex items-center gap-2 transition-colors ${opt.value === value ? 'text-[var(--primary)]' : 'text-[var(--text-primary)]'}`}>
              <span className="flex-1 truncate">{opt.label}</span>
              {opt.value === value && <Check size={14} className="text-[var(--primary)] flex-shrink-0" />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
};
