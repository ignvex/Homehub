import { ThemeToggle } from '@/components/ThemeToggle'

/**
 * App — 前端根组件
 *
 * 本项目采用"群岛架构"：
 * - HTMX 负责页面导航（hx-get / hx-post / hx-boost）
 * - React 作为组件岛，通过 createRoot() 挂载到各页面的 DOM 挂载点
 * - 不使用 React Router 或任何前端路由库
 *
 * 本文件仅提供开发环境入口页面的骨架样式和主题切换。
 * 各业务页面（首页、登录页、书签管理…）由 Go 后端渲染 HTML 壳，
 * 页面中各自的 <script> 标签内通过 createRoot 挂载对应的 React 组件。
 */

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
