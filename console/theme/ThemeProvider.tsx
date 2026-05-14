import React, { createContext, useContext, useEffect, useState } from 'react'
import { themes, Theme } from './themes'

interface ThemeContextValue {
  theme: Theme
  setTheme: (key: string) => void
}

const ThemeContext = createContext<ThemeContextValue>({
  theme: themes[0],
  setTheme: () => {},
})

export const useTheme = () => useContext(ThemeContext)

export const ThemeProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [theme, setThemeState] = useState<Theme>(() => {
    const saved = localStorage.getItem('prism_theme')
    return themes.find(t => t.key === saved) || themes[0]
  })

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme.key === 'lavender' ? '' : theme.key)
    localStorage.setItem('prism_theme', theme.key)
  }, [theme])

  const setTheme = (key: string) => {
    const t = themes.find(th => th.key === key)
    if (t) setThemeState(t)
  }

  return (
    <ThemeContext.Provider value={{ theme, setTheme }}>
      {children}
    </ThemeContext.Provider>
  )
}
