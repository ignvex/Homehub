import { useCallback, useEffect, useState } from 'react'

const STORAGE_KEY = 'homehub-theme'
export type ThemePreference = 'light' | 'dark' | 'system'

function getInitialTheme(): ThemePreference {
  const stored = localStorage.getItem(STORAGE_KEY)
  return stored === 'dark' || stored === 'light' || stored === 'system' ? stored : 'system'
}

function resolveTheme(preference: ThemePreference): 'light' | 'dark' {
  return preference === 'system' ? (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : preference
}

export function useTheme() {
  const [theme, setTheme] = useState<ThemePreference>(getInitialTheme)

  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const apply = () => document.documentElement.setAttribute('data-theme', resolveTheme(theme))
    apply()
    localStorage.setItem(STORAGE_KEY, theme)
    if (theme === 'system') media.addEventListener('change', apply)
    return () => media.removeEventListener('change', apply)
  }, [theme])

  const toggleTheme = useCallback(() => setTheme(previous => resolveTheme(previous) === 'light' ? 'dark' : 'light'), [])
  return { theme, setTheme, toggleTheme, resolvedTheme: resolveTheme(theme) }
}
