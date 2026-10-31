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

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
