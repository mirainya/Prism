import React, { useId, useRef } from 'react';
import { AlertCircle, AlertTriangle, LoaderCircle, X } from 'lucide-react';
import { Dialog } from './Dialog';

export type ConfirmDialogTone = 'info' | 'warning' | 'danger';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: ConfirmDialogTone;
  busy?: boolean;
  showCancel?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export const ConfirmDialog: React.FC<ConfirmDialogProps> = ({
  open,
  title,
  description,
  confirmLabel = '确认',
  cancelLabel = '取消',
  tone = 'warning',
  busy = false,
  showCancel = true,
  onConfirm,
  onCancel,
}) => {
  const titleId = useId();
  const descriptionId = useId();
  const cancelRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);

  const confirmClass = tone === 'danger'
    ? 'modal-button-danger focus-visible:ring-red-500'
    : 'modal-button-primary focus-visible:ring-[var(--primary)]';
  const iconClass = tone === 'danger'
    ? 'bg-red-50 text-red-600 border-red-100'
    : tone === 'warning'
      ? 'bg-amber-50 text-amber-600 border-amber-100'
      : 'bg-[var(--primary-lighter)] text-[var(--primary)] border-[var(--border-soft)]';

  return (
    <Dialog
      open={open}
      onClose={onCancel}
      role="alertdialog"
      ariaLabelledby={titleId}
      ariaDescribedby={descriptionId}
      dismissible={!busy}
      initialFocusRef={showCancel ? cancelRef : confirmRef}
      panelClassName="modal-panel w-full max-w-md"
    >
      <div>
        <div className="flex items-start gap-4 px-5 py-5 sm:px-6">
          <div className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border ${iconClass}`}>
            {tone === 'info' ? <AlertCircle size={20} /> : <AlertTriangle size={20} />}
          </div>
          <div className="min-w-0 flex-1 pt-0.5">
            <h2 id={titleId} className="text-base font-bold text-[var(--text-primary)]">{title}</h2>
            <p id={descriptionId} className="mt-1.5 text-sm leading-6 text-[var(--text-secondary)]">{description}</p>
          </div>
          <button
            type="button"
            title="关闭"
            disabled={busy}
            onClick={onCancel}
            className="modal-close shrink-0 text-[var(--text-secondary)] focus-visible:outline-none disabled:opacity-50"
          >
            <X size={17} />
          </button>
        </div>
        <div className="flex flex-col-reverse gap-2 border-t border-[var(--border-soft)] bg-[var(--surface-tint)] px-5 py-4 sm:flex-row sm:justify-end sm:px-6">
          {showCancel && (
            <button
              ref={cancelRef}
              type="button"
              disabled={busy}
              onClick={onCancel}
              className="modal-button modal-button-secondary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)] focus-visible:ring-offset-2"
            >
              {cancelLabel}
            </button>
          )}
          <button
            ref={confirmRef}
            type="button"
            disabled={busy}
            onClick={onConfirm}
            className={`modal-button text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 ${confirmClass}`}
          >
            {busy && <LoaderCircle size={16} className="animate-spin" />}
            {busy ? '处理中...' : confirmLabel}
          </button>
        </div>
      </div>
    </Dialog>
  );
};
