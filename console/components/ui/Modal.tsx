import React, { useId } from 'react'
import { X } from 'lucide-react'
import { Dialog, DialogMotion } from './Dialog'

interface ModalProps {
  open: boolean
  onClose: () => void
  title?: string
  children: React.ReactNode
  width?: string
  motion?: DialogMotion
  panelClassName?: string
}

export const Modal: React.FC<ModalProps> = ({ open, onClose, title, children, width = 'max-w-lg', motion = 'center', panelClassName = '' }) => {
  const titleId = useId()

  return (
    <Dialog
      open={open}
      onClose={onClose}
      motion={motion}
      ariaLabel={title ? undefined : '弹窗'}
      ariaLabelledby={title ? titleId : undefined}
      panelClassName={`w-full ${width} max-h-[90vh] overflow-y-auto rounded-lg border border-[var(--border-soft)] bg-[var(--surface-card)] p-6 shadow-2xl ${panelClassName}`}
    >
        {title && (
          <div className="flex items-center justify-between mb-4">
            <h3 id={titleId} className="text-lg font-bold text-[var(--text-primary)]">{title}</h3>
            <button type="button" onClick={onClose} title="关闭" className="p-2 hover:bg-[var(--primary-lighter)] rounded-lg text-[var(--text-secondary)] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--primary)]/40">
              <X size={18} />
            </button>
          </div>
        )}
        {children}
    </Dialog>
  )
}
