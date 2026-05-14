import React from 'react'

interface CardProps {
  children: React.ReactNode
  className?: string
  hover?: boolean
}

export const Card: React.FC<CardProps> = ({ children, className = '', hover = true }) => (
  <div className={`glass-card p-6 ${hover ? 'hover-lift' : ''} ${className}`}>
    {children}
  </div>
)
