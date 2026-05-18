import React, { useState, useRef, useEffect, useMemo } from 'react';
import { ChevronDown, Check, Search } from 'lucide-react';

const PROVIDER_LABELS: Record<string, string> = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  google: 'Google',
  volcengine: '火山引擎',
};

const getLabel = (provider: string) => PROVIDER_LABELS[provider] || provider || '其他';

export interface ModelOption {
  id: string;
  label?: string;
  provider: string;
}

const ModelSelector: React.FC<{
  options: ModelOption[];
  value: string;
  onChange: (id: string) => void;
  placeholder?: string;
  allOption?: string;
  className?: string;
}> = ({ options, value, onChange, placeholder = '选择模型', allOption, className }) => {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const ref = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  useEffect(() => {
    if (open) { setSearch(''); setTimeout(() => inputRef.current?.focus(), 0); }
  }, [open]);

  const filtered = useMemo(() => {
    if (!search) return options;
    const q = search.toLowerCase();
    return options.filter(m => m.id.toLowerCase().includes(q) || m.label?.toLowerCase().includes(q) || m.provider.toLowerCase().includes(q));
  }, [options, search]);

  const grouped: Record<string, ModelOption[]> = {};
  for (const m of filtered) {
    const key = m.provider || '';
    (grouped[key] ||= []).push(m);
  }
  const groups = Object.entries(grouped);

  const selected = options.find(m => m.id === value);
  const displayText = value === '' && allOption ? allOption : (selected ? (selected.label || selected.id) : placeholder);

  return (
    <div ref={ref} className={`relative ${className || ''}`}>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="inline-flex items-center gap-2 px-3 py-1.5 border border-[var(--border-soft)] rounded-lg text-sm bg-[var(--surface-card)] hover:bg-[var(--surface)] focus:outline-none focus:ring-2 focus:ring-[var(--primary)] w-full"
      >
        <span className={`truncate flex-1 text-left ${!selected && !allOption ? 'text-[var(--text-tertiary)]' : ''}`}>{displayText}</span>
        <ChevronDown size={14} className="text-[var(--text-tertiary)] flex-shrink-0" />
      </button>

      {open && (
        <div className="absolute top-full left-0 mt-1 w-full min-w-[240px] bg-[var(--surface-card)] border border-[var(--border-soft)] rounded-xl shadow-lg z-50 flex flex-col" style={{ maxHeight: 'min(320px, calc(100vh - 200px))' }}>
          <div className="px-2 pt-2 pb-1 sticky top-0 bg-[var(--surface-card)]">
            <div className="relative">
              <Search size={13} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--text-tertiary)]" />
              <input
                ref={inputRef}
                value={search}
                onChange={e => setSearch(e.target.value)}
                placeholder="搜索模型..."
                className="w-full pl-8 pr-3 py-1.5 text-sm border border-[var(--border-soft)] rounded-lg bg-[var(--surface)] focus:outline-none focus:ring-1 focus:ring-[var(--primary)]"
              />
            </div>
          </div>
          <div className="overflow-y-auto flex-1 py-1">
            {allOption && (
              <button
                type="button"
                onClick={() => { onChange(''); setOpen(false); }}
                className={`w-full text-left px-3 py-1.5 text-sm hover:bg-[var(--surface)] flex items-center gap-2 ${value === '' ? 'text-[var(--primary)]' : 'text-[var(--text-primary)]'}`}
              >
                <span className="flex-1">{allOption}</span>
                {value === '' && <Check size={14} className="text-[var(--primary)] flex-shrink-0" />}
              </button>
            )}
            {groups.length === 0 && (
              <div className="px-3 py-3 text-sm text-[var(--text-tertiary)] text-center">无匹配结果</div>
            )}
            {groups.map(([provider, items], gi) => (
              <div key={provider}>
                {(gi > 0 || allOption) && <div className="mx-2 my-1 border-t border-[var(--border-soft)]" />}
                <div className="px-3 py-1 text-[11px] font-medium text-[var(--text-tertiary)]">
                  {getLabel(provider)}
                </div>
                {items.map(m => (
                  <button
                    key={m.id}
                    type="button"
                    onClick={() => { onChange(m.id); setOpen(false); }}
                    className={`w-full text-left px-3 py-1.5 text-sm hover:bg-[var(--surface)] flex items-center gap-2 ${m.id === value ? 'text-[var(--primary)]' : 'text-[var(--text-primary)]'}`}
                  >
                    <span className="truncate flex-1">{m.label || m.id}</span>
                    {m.id === value && <Check size={14} className="text-[var(--primary)] flex-shrink-0" />}
                  </button>
                ))}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
};

export default ModelSelector;
