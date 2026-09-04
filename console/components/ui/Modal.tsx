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
      panelClassName={`modal-panel w-full ${width} ${panelClassName}`}
    >
        {title && (
          <div className="modal-titlebar flex items-center justify-between">
            <h3 id={titleId} className="modal-title text-lg font-bold text-[var(--text-primary)]">{title}</h3>
            <button type="button" onClick={onClose} title="关闭" className="modal-close text-[var(--text-secondary)] focus-visible:outline-none">
              <X size={18} />
            </button>
          </div>
        )}
        <div className="modal-content">{children}</div>
    </Dialog>
  )
}
