import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { ThemeToggle } from '@/components/ThemeToggle'
import { UserAvatar } from '@/components/UserAvatar'
import type { AuthStatus } from '@/lib/home'

// ============================================================
// AppShell — 控制台侧边栏布局
//
// 当前后端为单入口 SPA fallback，因此导航使用原生 <a> 整页装载。
// 不依赖 React Router，也不将完整 HTML 文档注入局部容器。
// ============================================================

interface NavItem { href: string; label: string; icon: ReactNode; admin?: boolean }
interface NavGroup { label: string; items: NavItem[] }

const icon = (path: ReactNode) => <svg viewBox="0 0 24 24" aria-hidden="true">{path}</svg>

const navigation: NavGroup[] = [
  {
    label: '工作空间', items: [
      { href: '/dashboard', label: '仪表盘', icon: icon(<><rect x="4" y="4" width="6" height="6" /><rect x="14" y="4" width="6" height="6" /><rect x="4" y="14" width="6" height="6" /><rect x="14" y="14" width="6" height="6" /></>) },
      { href: '/bookmarks', label: '书签', icon: icon(<><path d="M6 4.5h12v15l-6-3.7-6 3.7z" /></>) },
      { href: '/rss', label: '订阅阅读', icon: icon(<><path d="M5 5a14 14 0 0 1 14 14M5 11a8 8 0 0 1 8 8" /><circle cx="6" cy="18" r="1" /></>) },
      { href: '/todos', label: '待办事项', icon: icon(<><path d="m5 12 3 3 6-7" /><path d="M13 5h6v14H5v-5" /></>) },
      { href: '/media', label: '媒体资源', icon: icon(<><rect x="4" y="5" width="16" height="14" rx="1" /><path d="m5 16 4-4 3 3 2-2 5 5" /><circle cx="15.5" cy="9.5" r="1" /></>) },
    ]
  },
  {
    label: '数据与自动化', items: [
      { href: '/bilibili', label: 'Bilibili', icon: icon(<><rect x="3" y="6" width="18" height="14" rx="2" /><path d="m8 3 4 3 4-3M8 11v3m8-3v3" /></>) },
      { href: '/search', label: '数据搜索', icon: icon(<><circle cx="10.5" cy="10.5" r="5.5" /><path d="m15 15 4 4" /></>) },
    ]
  },
  {
    label: '系统', items: [
      { href: '/appearance', label: '界面外观', icon: icon(<><circle cx="12" cy="12" r="3" /><path d="M12 3v2m0 14v2M3 12h2m14 0h2M5.6 5.6 7 7m10 10 1.4 1.4M18.4 5.6 17 7M7 17l-1.4 1.4" /></>) },
      { href: '/profile', label: '个人中心', icon: icon(<><circle cx="12" cy="8" r="3" /><path d="M5 20c.8-4 3.1-6 7-6s6.2 2 7 6" /></>) },
      { href: '/users', label: '用户管理', admin: true, icon: icon(<><circle cx="9" cy="8" r="3" /><path d="M3 20c.5-4 2.5-6 6-6s5.5 2 6 6M16 7h5m-2.5-2.5v5" /></>) },
      { href: '/logs', label: '系统日志', admin: true, icon: icon(<><path d="M5 4h14v16H5zM8 8h8m-8 4h8m-8 4h5" /></>) },
      { href: '/settings', label: '设置', admin: true, icon: icon(<><circle cx="12" cy="12" r="3" /><path d="m19 13.5 1-1.5-1-1.5.1-1.8-1.7-.7-.9-1.5-1.8.2L14.2 5 12.5 4l-1.4 1.1-1.8-.2-.9 1.6-1.7.7.1 1.8-1 1.5 1 1.5-.1 1.8 1.7.7.9 1.5 1.8-.2 1.4 1.1 1.7-1.1 1.8.2.9-1.5 1.7-.7z" /></>) },
    ]
  },
]

export interface ShellPageMeta { title: string; eyebrow?: string; description?: string }

/**
 * 判断当前路径是否匹配导航项（用于高亮 active 状态）。
 * 不使用 react-router，直接读取 window.location.pathname。
 */
function isActivePath(href: string): boolean {
  const pathname = window.location.pathname
  return pathname === href || pathname.startsWith(`${href}/`)
}

export function AppShell({ auth, meta, children }: { auth: AuthStatus; meta: ShellPageMeta; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem('homehub-shell-collapsed') === '1')

  // 浏览器前进、后退时关闭移动端侧栏。
  useEffect(() => {
    const close = () => setOpen(false)
    window.addEventListener('popstate', close)
    return () => {
      window.removeEventListener('popstate', close)
    }
  }, [])

  useEffect(() => {
    localStorage.setItem('homehub-shell-collapsed', collapsed ? '1' : '0')
  }, [collapsed])

  return (
    <div className={`shell ${collapsed ? 'shell--collapsed' : ''}`}>
      <button className={`shell-scrim ${open ? 'is-open' : ''}`} onClick={() => setOpen(false)} aria-label="关闭导航" />

      <aside className={`shell-sidebar ${open ? 'is-open' : ''}`}>
        <div className="shell-brand">
          <a href="/dashboard" aria-label="打开仪表盘">
            <span className="shell-brand-mark">H</span>
            <span className="shell-brand-name">HomeHub</span>
          </a>
          <button onClick={() => setCollapsed(v => !v)} aria-label={collapsed ? '展开侧栏' : '收起侧栏'}>‹</button>
        </div>
        <nav className="shell-nav" aria-label="主导航">
          {navigation.map(group => (
            <section key={group.label}>
              <h2>{group.label}</h2>
              {group.items.map(item => (
                <a
                  key={item.href}
                  href={item.href}
                  title={collapsed ? item.label : undefined}
                  className={isActivePath(item.href) ? 'is-active' : ''}
                >
                  {item.icon}
                  <span>{item.label}</span>
                </a>
              ))}
            </section>
          ))}
        </nav>
        <a href="/profile" className="shell-account">
          <UserAvatar username={auth.username} size={36} />
          <div>
            <strong>{auth.username || '用户'}</strong>
            <span>账户与个人资料</span>
          </div>
        </a>
      </aside>

      <div className="shell-stage">
        <header className="shell-header">
          <button className="shell-menu" onClick={() => setOpen(true)} aria-label="打开导航">☰</button>
          <div>
            <span>{meta.eyebrow || 'HOMEHUB'}</span>
            <strong>{meta.title}</strong>
          </div>
          <div className="shell-header-actions">
            <ThemeToggle />
            <a href="/">返回首页</a>
          </div>
        </header>
        <main className="shell-main" id="shell-content">
          <div className="shell-page-head">
            <div>
              <p>{meta.eyebrow || 'WORKSPACE'}</p>
              <h1>{meta.title}</h1>
              {meta.description && <span>{meta.description}</span>}
            </div>
          </div>
          {children}
        </main>
      </div>
    </div>
  )
}
