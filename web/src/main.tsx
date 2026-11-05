import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'

// 样式导入顺序（后导入的覆盖先导入的）：
//   令牌 → 重置 → 全局元素 → 组件层 → 布局与页面
import '@/styles/variables.css'
import '@/styles/reset.css'
import '@/styles/global.css'
import '@/styles/components.css'

import '@/styles/shell.css'
import '@/styles/home.css'
import '@/styles/login.css'
import '@/styles/dashboard.css'
import '@/styles/workspace.css'
import '@/styles/rss.css'
import '@/styles/logs.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

// 开发模式不注册：vite 的模块不带哈希，cache-first 会让热更新失效。
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => {
      // 注册失败只是没有离线壳，不影响正常使用。
    })
  })
}
