import { ThemeToggle } from '@/components/ThemeToggle'

function App() {
  return (
    <div className="app-container">
      <header className="app-header">
        <a href="/" className="logo">HomeHub</a>
        <div className="header-actions">
          <ThemeToggle />
        </div>
      </header>

      <main style={{ marginTop: 32 }}>
        <div className="card">
          <h1>HomeHub 前端开发环境</h1>
          <p style={{ marginTop: 8, color: 'var(--color-text-secondary)' }}>
            Vite + React + TypeScript + HTMX + CSS3 已就绪。
          </p>
        </div>
      </main>
    </div>
  )
}

export default App
