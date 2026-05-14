import React from 'react'

type Variant = 'primary' | 'secondary' | 'danger' | 'ghost'
type Size = 'sm' | 'md' | 'lg'

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  loading?: boolean
}

const variantStyles: Record<Variant, string> = {
  primary: 'bg-gradient-to-r from-[var(--primary)] to-[var(--primary-light)] text-white shadow-md hover:shadow-lg hover:-translate-y-0.5',
  secondary: 'bg-[var(--primary-lighter)] text-[var(--primary)] hover:bg-[var(--border-soft)]',
  danger: 'bg-gradient-to-r from-red-500 to-red-400 text-white shadow-md hover:shadow-lg hover:-translate-y-0.5',
  ghost: 'text-[var(--text-secondary)] hover:bg-[var(--primary-lighter)] hover:text-[var(--primary)]',
}

const sizeStyles: Record<Size, string> = {
  sm: 'px-3 py-1.5 text-xs',
  md: 'px-4 py-2 text-sm',
  lg: 'px-6 py-2.5 text-base',
}

export const Button: React.FC<ButtonProps> = ({
  variant = 'primary',
  size = 'md',
  loading,
  disabled,
  className = '',
  children,
  ...props
}) => (
  <button
    className={`inline-flex items-center justify-center gap-2 rounded-xl font-medium transition-all duration-200 disabled:opacity-50 disabled:pointer-events-none ${variantStyles[variant]} ${sizeStyles[size]} ${className}`}
    disabled={disabled || loading}
    {...props}
  >
    {loading && <span className="w-4 h-4 border-2 border-current border-t-transparent rounded-full animate-spin" />}
    {children}
  </button>
)
