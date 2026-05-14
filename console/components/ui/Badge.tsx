import React from 'react'

type BadgeVariant = 'success' | 'warning' | 'error' | 'info' | 'default'

interface BadgeProps {
  children: React.ReactNode
  variant?: BadgeVariant
  glow?: boolean
}

const variantStyles: Record<BadgeVariant, string> = {
  success: 'bg-emerald-100 text-emerald-700',
  warning: 'bg-amber-100 text-amber-700',
  error: 'bg-red-100 text-red-700',
  info: 'bg-[var(--primary-lighter)] text-[var(--primary)]',
  default: 'bg-gray-100 text-gray-600',
}

export const Badge: React.FC<BadgeProps> = ({ children, variant = 'default', glow }) => (
  <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${variantStyles[variant]} ${glow ? 'shadow-sm' : ''}`}>
    {children}
  </span>
)
