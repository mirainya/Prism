import React from 'react'
import { X } from 'lucide-react'

interface ModalProps {
  open: boolean
  onClose: () => void
  title?: string
  children: React.ReactNode
  width?: string
}

export const Modal: React.FC<ModalProps> = ({ open, onClose, title, children, width = 'max-w-lg' }) => {
  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="absolute inset-0 bg-black/60 backdrop-blur-md" onClick={onClose} />
      <div className={`relative bg-white border-2 border-[var(--primary)]/20 rounded-2xl p-6 w-full ${width} max-h-[90vh] overflow-y-auto shadow-[0_20px_60px_-10px_var(--glow-color),0_0_30px_var(--glow-color)]`}>
        {title && (
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-lg font-bold text-[var(--text-primary)]">{title}</h3>
            <button onClick={onClose} className="p-2 hover:bg-[var(--primary-lighter)] rounded-lg text-[var(--text-secondary)] transition-colors">
              <X size={18} />
            </button>
          </div>
        )}
        {children}
      </div>
    </div>
  )
}
