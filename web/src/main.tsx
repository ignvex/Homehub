import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'

// 基础样式导入顺序：变量 → 重置 → 全局
import '@/styles/variables.css'
import '@/styles/reset.css'
import '@/styles/global.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
