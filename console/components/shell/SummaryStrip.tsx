import React from 'react';
import type { LucideIcon } from 'lucide-react';

export interface SummaryStripItem {
  label: string;
  value: React.ReactNode;
  icon: LucideIcon;
  color: string;
  note?: string;
}

interface SummaryStripProps {
  items: SummaryStripItem[];
}

export const SummaryStrip: React.FC<SummaryStripProps> = ({ items }) => (
  <section className="grid grid-cols-2 overflow-hidden rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] shadow-[var(--shadow-soft)] xl:grid-cols-4">
    {items.map(({ label, value, icon: Icon, color, note }, index) => (
      <article
        key={label}
        className={`flex min-w-0 items-center gap-3 px-3 py-3.5 sm:px-4 ${index % 2 === 0 ? 'border-r border-[var(--border-soft)]' : ''} ${index < items.length - 2 ? 'border-b border-[var(--border-soft)]' : ''} ${index < items.length - 1 ? 'xl:border-r xl:border-[var(--border-soft)]' : 'xl:border-r-0'} xl:border-b-0`}
      >
        <span
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg"
          style={{ color, backgroundColor: `color-mix(in srgb, ${color} 13%, white)` }}
        >
          <Icon size={17} />
        </span>
        <div className="min-w-0">
          <div className="text-[11px] font-semibold text-[var(--text-secondary)]">{label}</div>
          <div className="mt-0.5 flex min-w-0 items-baseline gap-2">
            <span className="truncate text-lg font-extrabold text-[var(--text-primary)]">{value}</span>
            {note && <span className="truncate text-[11px] text-[var(--text-tertiary)]">{note}</span>}
          </div>
        </div>
      </article>
    ))}
  </section>
);
