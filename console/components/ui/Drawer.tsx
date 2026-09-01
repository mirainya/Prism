import React, { useId } from 'react';
import { X } from 'lucide-react';
import { Dialog } from './Dialog';

interface DrawerProps {
  open: boolean;
  onClose: () => void;
  title?: string;
  subtitle?: React.ReactNode;
  children: React.ReactNode;
  width?: string;
  headerActions?: React.ReactNode;
  panelClassName?: string;
}

export const Drawer: React.FC<DrawerProps> = ({
  open,
  onClose,
  title,
  subtitle,
  children,
  width = 'max-w-4xl',
  headerActions,
  panelClassName = '',
}) => {
  const titleId = useId();

  return (
    <Dialog
      open={open}
      onClose={onClose}
      motion="right"
      ariaLabel={title ? undefined : '侧边栏'}
      ariaLabelledby={title ? titleId : undefined}
      containerClassName="items-stretch justify-end"
      panelClassName={`flex h-full w-full ${width} flex-col border-l border-[var(--border-soft)] bg-[var(--surface-elevated)] shadow-2xl ${panelClassName}`}
    >
      {title && (
        <header className="flex items-start justify-between gap-4 border-b border-[var(--border-soft)] px-5 py-4">
          <div className="min-w-0 flex-1">
            <h2 id={titleId} className="text-lg font-bold text-[var(--text-primary)]">{title}</h2>
            {subtitle && <div className="mt-1 truncate text-xs text-[var(--text-secondary)]">{subtitle}</div>}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {headerActions}
            <button type="button" title="关闭" aria-label="关闭" onClick={onClose} className="rounded-lg p-2 text-[var(--text-secondary)] hover:bg-[var(--surface)] hover:text-[var(--text-primary)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]/40">
              <X size={18} />
            </button>
          </div>
        </header>
      )}
      {children}
    </Dialog>
  );
};
