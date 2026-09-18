import React, { useState, useRef, useEffect, useLayoutEffect } from 'react';
import { createPortal } from 'react-dom';
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
  const [menuStyle, setMenuStyle] = useState<React.CSSProperties | null>(null);
  const ref = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      const target = e.target as Node;
      if (!ref.current?.contains(target) && !menuRef.current?.contains(target)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  useLayoutEffect(() => {
    if (!open) {
      setMenuStyle(null);
      return;
    }
    const position = () => {
      const trigger = ref.current?.getBoundingClientRect();
      if (!trigger) return;
      const gap = 4;
      const roomBelow = window.innerHeight - trigger.bottom - gap;
      const roomAbove = trigger.top - gap;
      const openAbove = roomBelow < 160 && roomAbove > roomBelow;
      const maxHeight = Math.max(96, Math.min(240, openAbove ? roomAbove : roomBelow));
      setMenuStyle({
        position: 'fixed',
        left: trigger.left,
        width: Math.max(trigger.width, 140),
        maxHeight,
        zIndex: 1000,
        ...(openAbove
          ? { bottom: window.innerHeight - trigger.top + gap }
          : { top: trigger.bottom + gap }),
      });
    };
    position();
    window.addEventListener('resize', position);
    window.addEventListener('scroll', position, true);
    return () => {
      window.removeEventListener('resize', position);
      window.removeEventListener('scroll', position, true);
    };
  }, [open]);

  const selected = options.find(o => o.value === value);

  return (
    <div ref={ref} className={`select-control relative ${className}`}>
      <button type="button" onClick={() => !disabled && setOpen(!open)}
        aria-haspopup="listbox" aria-expanded={open}
        className={`select-trigger inline-flex items-center gap-2 w-full px-3 py-2 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface-card)] hover:bg-[var(--surface)] focus:outline-none transition-colors ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}>
        <span className={`truncate flex-1 text-left ${!selected ? 'text-[var(--text-tertiary)]' : ''}`}>{selected?.label || placeholder}</span>
        <ChevronDown size={14} className={`text-[var(--text-tertiary)] flex-shrink-0 transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && menuStyle && createPortal(
        <div ref={menuRef} role="listbox" className="select-menu min-w-[140px] bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-lg shadow-lg py-1 overflow-y-auto" style={menuStyle}>
          {options.map(opt => (
            <button key={opt.value} type="button" onClick={() => { onChange(opt.value); setOpen(false); }}
              role="option" aria-selected={opt.value === value}
              className={`select-option w-full text-left px-3 py-1.5 text-sm hover:bg-[var(--surface)] flex items-center gap-2 transition-colors ${opt.value === value ? 'text-[var(--primary)]' : 'text-[var(--text-primary)]'}`}>
              <span className="flex-1 truncate">{opt.label}</span>
              {opt.value === value && <Check size={14} className="text-[var(--primary)] flex-shrink-0" />}
            </button>
          ))}
        </div>,
        document.body,
      )}
    </div>
  );
};
