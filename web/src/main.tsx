import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'

// 基础样式导入顺序：变量 → 重置 → 全局
import '@/styles/variables.css'
import '@/styles/reset.css'
import '@/styles/global.css'

// 页面专属样式（按需加载，构建时由 Vite 合并）
import '@/styles/home.css'
import '@/styles/login.css'
import '@/styles/shell.css'
import '@/styles/dashboard.css'
import '@/styles/operations.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
