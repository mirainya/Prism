import React from 'react';
import type { LucideIcon } from 'lucide-react';

interface PageHeaderProps {
  icon: LucideIcon;
  title: string;
  meta?: React.ReactNode;
  actions?: React.ReactNode;
}

export const PageHeader: React.FC<PageHeaderProps> = ({ icon: Icon, title, meta, actions }) => (
  <header className="flex min-h-11 flex-wrap items-center justify-between gap-3">
    <div className="flex min-w-0 items-center gap-3">
      <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg [background:var(--brand-gradient)] text-white shadow-[0_6px_16px_var(--glow-color)]">
        <Icon size={20} strokeWidth={2.2} />
      </span>
      <div className="min-w-0">
        <h1 className="truncate text-xl font-bold text-[var(--text-primary)]">{title}</h1>
        {meta && <div className="mt-0.5 text-xs text-[var(--text-secondary)]">{meta}</div>}
      </div>
    </div>
    {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
  </header>
);
