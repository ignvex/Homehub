import { useTheme } from '@/hooks/useTheme'

export function ThemeToggle() {
  const { resolvedTheme, toggleTheme } = useTheme()
  const isDark = resolvedTheme === 'dark'
  return <button className="theme-toggle" type="button" onClick={toggleTheme} aria-label={isDark ? '切换到浅色主题' : '切换到深色主题'} title={isDark ? '切换到浅色主题' : '切换到深色主题'}><span aria-hidden="true">{isDark ? '☀' : '☾'}</span><span>{isDark ? '浅色' : '深色'}</span></button>
}
