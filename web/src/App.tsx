import { createContext, useCallback, useEffect, useState, type ComponentType } from 'react'
import { AppShell, type ShellPageMeta } from '@/components/AppShell'
import { homeApi, type AuthStatus } from '@/lib/home'
import { BalancePage } from '@/pages/BalancePage'
import { AppearancePage } from '@/pages/AppearancePage'
import { BookmarksPage } from '@/pages/BookmarksPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { HomePage } from '@/pages/HomePage'
import { LoginPage } from '@/pages/LoginPage'
import { LogsPage } from '@/pages/LogsPage'
import { MediaPage } from '@/pages/MediaPage'
import { PastePage } from '@/pages/PastePage'
import { ProfilePage } from '@/pages/ProfilePage'
import { RSSPage } from '@/pages/RSSPage'
import { SearchPage } from '@/pages/SearchPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { TodosPage } from '@/pages/TodosPage'
import { UsersPage } from '@/pages/UsersPage'

// ============================================================
// Auth Context — 应用级别缓存认证状态，避免每次页面切换重新请求
// ============================================================
const AuthContext = createContext<AuthStatus | null>(null)

// ============================================================
// 客户端导航 — 使用 history.pushState 替代整页跳转，消除白屏
// ============================================================
function getPathname() { return window.location.pathname.replace(/\/$/, '') || '/' }

function useClientNavigation() {
  const [path, setPath] = useState(getPathname)

  // 浏览器前进/后退
  useEffect(() => {
    const handler = () => setPath(getPathname())
    window.addEventListener('popstate', handler)
    return () => window.removeEventListener('popstate', handler)
  }, [])

  // 拦截同源 <a> 标签点击，使用 pushState 实现客户端导航
  useEffect(() => {
    const isSameOrigin = (href: string) => {
      try { const url = new URL(href, window.location.origin); return url.origin === window.location.origin }
      catch { return false }
    }
    const handler = (event: MouseEvent) => {
      const anchor = (event.target as Element).closest('a')
      if (!anchor) return
      const href = anchor.getAttribute('href')
      // 跳过：空链接、外链、锚点、mailto、下载、新窗口、显式标记
      if (!href || href.startsWith('#') || href.startsWith('mailto:') || href.startsWith('javascript:') || anchor.getAttribute('download') !== null || anchor.getAttribute('target') === '_blank' || anchor.getAttribute('rel')?.includes('external') || !isSameOrigin(href)) return
      // 跳过：带有 data-native 属性的链接（强制整页跳转）
      if (anchor.hasAttribute('data-native')) return
      event.preventDefault()
      history.pushState(null, '', href)
      setPath(getPathname())
      window.scrollTo(0, 0)
    }
    document.addEventListener('click', handler)
    return () => document.removeEventListener('click', handler)
  }, [])

  return { path, navigate: useCallback((href: string) => { history.pushState(null, '', href); setPath(getPathname()); window.scrollTo(0, 0) }, []) }
}

// ============================================================
// 页面定义
// ============================================================
interface PageDefinition {
  component: ComponentType
  meta: ShellPageMeta
  admin?: boolean
}

const pages: Record<string, PageDefinition> = {
  '/dashboard': { component: DashboardPage, meta: { eyebrow: '运行概览', title: '仪表盘', description: '查看服务健康、数据库连接与运行时长。' } },
  '/bookmarks': { component: BookmarksPage, meta: { eyebrow: '工作空间', title: '书签', description: '维护分组、链接与显示顺序。' } },
  '/rss': { component: RSSPage, meta: { eyebrow: '内容订阅', title: 'RSS', description: '维护订阅源并阅读指定来源的条目。' } },
  '/todos': { component: TodosPage, meta: { eyebrow: '共享任务', title: '待办事项', description: '跟踪所有认证用户共享的任务集合。' } },
  '/media': { component: MediaPage, meta: { eyebrow: '资源库', title: '媒体资源', description: '上传、检查和清理背景媒体。' } },
  '/search': { component: SearchPage, meta: { eyebrow: '跨域检索', title: '数据搜索', description: '检索书签与 RSS 条目。' } },
  '/profile': { component: ProfilePage, meta: { eyebrow: '账户', title: '个人中心', description: '维护个人资料、头像与登录密码。' } },
  '/users': { component: UsersPage, meta: { eyebrow: '访问控制', title: '用户管理', description: '管理成员、角色与账户状态。' }, admin: true },
  '/logs': { component: LogsPage, meta: { eyebrow: '系统事件', title: '系统日志', description: '筛选和定位后端运行事件。' }, admin: true },
  '/settings': { component: SettingsPage, meta: { eyebrow: '系统配置', title: '设置', description: '按配置域修改后端支持的参数。' }, admin: true },
  '/appearance': { component: AppearancePage, meta: { eyebrow: '界面偏好', title: '外观', description: '管理当前浏览器主题与服务端背景配置。' } },
  '/balance': { component: BalancePage, meta: { eyebrow: '服务监视', title: 'LLM 余额', description: '查看各平台 API 余额与监控状态。' } },
}

// ============================================================
// App 根组件 — 认证缓存 + 客户端导航
// ============================================================
export default function App() {
  const { path } = useClientNavigation()
  const [auth, setAuth] = useState<AuthStatus | null>(null)
  const [authChecked, setAuthChecked] = useState(false)

  // 仅首次加载时检查认证状态（后续客户端导航不再重复请求）
  useEffect(() => {
    homeApi.auth()
      .then(status => { setAuth(status); setAuthChecked(true) })
      .catch(() => { setAuth({ logged_in: false }); setAuthChecked(true) })
  }, [])

  // 公开页面
  if (path === '/') return <AuthContext.Provider value={auth}><HomePage /></AuthContext.Provider>
  if (path === '/login') return <AuthContext.Provider value={auth}><LoginPage /></AuthContext.Provider>
  if (path === '/paste' || path.startsWith('/paste/')) return <PastePage />

  // 认证页面 — 等待首次认证检查
  if (!authChecked) {
    return (
      <AuthContext.Provider value={null}>
        <div className="shell">
          <div className="route-loading" aria-busy="true" style={{ display: 'grid', placeContent: 'center', justifyItems: 'center', gap: 13, minHeight: '100vh' }}>
            <span style={{ width: 23, height: 23, border: '2px solid var(--color-border)', borderTopColor: 'var(--color-accent)', borderRadius: '50%', animation: 'shell-spin .8s linear infinite' }} />
            <p style={{ color: 'var(--color-text-secondary)', fontSize: 12 }}>HomeHub</p>
          </div>
        </div>
      </AuthContext.Provider>
    )
  }

  // 未登录 → 重定向
  if (!auth?.logged_in) {
    const returnTo = encodeURIComponent(`${path}${window.location.search}`)
    window.location.replace(`/login?returnTo=${returnTo}`)
    return null
  }

  // 页面解析
  const page = pages[path] ?? null
  if (!page) {
    return (
      <AuthContext.Provider value={auth}>
        <div className="not-found" role="alert">
          <p>404</p>
          <h1>页面不存在</h1>
          <span>请求的地址没有对应的 HomeHub 页面。</span>
          <a href="/">返回首页</a>
        </div>
      </AuthContext.Provider>
    )
  }

  // 管理员权限检查
  if (page.admin && auth.role !== 'admin') {
    return (
      <AuthContext.Provider value={auth}>
        <div className="shell">
          <main className="route-state" role="alert" style={{ minHeight: '100vh', display: 'grid', placeContent: 'center', justifyItems: 'center', gap: 8 }}>
            <h1>无权访问</h1>
            <p>此页面仅对管理员开放。</p>
            <a href="/dashboard">返回仪表盘</a>
          </main>
        </div>
      </AuthContext.Provider>
    )
  }

  const Page = page.component
  return (
    <AuthContext.Provider value={auth}>
      <AppShell auth={auth} meta={page.meta}><Page /></AppShell>
    </AuthContext.Provider>
  )
}
