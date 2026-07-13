import { useTheme } from '@/hooks/useTheme'

export function ThemeToggle() {
  const { theme, toggleTheme } = useTheme()
  const isDark = theme === 'dark'

  return (
    <button className="theme-rail" type="button" onClick={toggleTheme} aria-label={isDark ? '切换到浅色主题' : '切换到深色主题'}>
      <span aria-hidden="true">{isDark ? '☀' : '☾'}</span>
      <span>{isDark ? '浅色' : '深色'}</span>
    </button>
  )
}
