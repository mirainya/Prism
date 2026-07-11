import React from 'react'
import { themes } from './themes'
import { useTheme } from './ThemeProvider'

export const ThemeSwitch: React.FC = () => {
  const { theme, setTheme } = useTheme()

  return (
    <div className="flex items-center gap-1.5">
      {themes.map(t => (
        <button
          key={t.key}
          onClick={() => setTheme(t.key)}
          title={t.name}
          className={`w-5 h-5 rounded-full transition-transform hover:scale-125 ${
            theme.key === t.key ? 'ring-2 ring-offset-2 scale-110' : ''
          }`}
          style={{ backgroundColor: t.color, '--tw-ring-color': t.color } as React.CSSProperties}
        />
      ))}
    </div>
  )
}
