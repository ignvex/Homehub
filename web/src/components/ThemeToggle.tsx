import { useTheme } from '@/hooks/useTheme';

export function ThemeToggle() {
  const { theme, toggleTheme } = useTheme();

  return (
    <button
      className="icon-btn"
      aria-label="切换暗色模式"
      onClick={toggleTheme}
    >
      {theme === 'light' ? '🌓' : '☀️'}
    </button>
  );
}
