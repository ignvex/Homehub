import { useCallback, useEffect, useMemo, useState, type ComponentType } from 'react'
import { AppShell, type ShellPageMeta } from '@/components/AppShell'
import { AuthContext, type AuthValue } from '@/lib/auth'
import { homeApi, type AuthStatus } from '@/lib/home'
import { LEVEL_ADMIN, LEVEL_OPERATOR, hasLevel, levelLabel } from '@/lib/permissions'
import { NavigationContext, confirmLeave, currentHref, notifyLocationChange, readLocation, type AppLocation, type NavigateOptions } from '@/lib/navigation'
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
import { ExtensionPage } from '@/pages/ExtensionPage'
import { TodosPage } from '@/pages/TodosPage'
import { UsersPage } from '@/pages/UsersPage'

// ============================================================
// 客户端导航 — 使用 history.pushState 替代整页跳转，消除白屏
// ============================================================
function isSameOrigin(href: string) {
  try { return new URL(href, window.location.origin).origin === window.location.origin }
  catch { return false }
}

function useClientNavigation() {
  const [location, setLocation] = useState<AppLocation>(readLocation)

  const navigate = useCallback((href: string, options: NavigateOptions = {}) => {
    if (href === currentHref()) return
    if (!confirmLeave()) return
    if (options.replace) history.replaceState(null, '', href)
    else history.pushState(null, '', href)
    setLocation(readLocation())
    notifyLocationChange()
    window.scrollTo(0, 0)
  }, [])

  // 浏览器前进/后退
  useEffect(() => {
    const handler = () => setLocation(readLocation())
    window.addEventListener('popstate', handler)
    return () => window.removeEventListener('popstate', handler)
  }, [])

  // 拦截同源 <a> 标签点击，使用 pushState 实现客户端导航
  useEffect(() => {
    const handler = (event: MouseEvent) => {
      // 交给浏览器：已被其他处理器拦截、非左键、带修饰键（新标签/新窗口/下载）
      if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
      const anchor = (event.target as Element | null)?.closest?.('a')
      if (!anchor) return
      const href = anchor.getAttribute('href')
      // 跳过：空链接、外链、锚点、mailto、下载、新窗口、显式标记
      if (!href || href.startsWith('#') || href.startsWith('mailto:') || href.startsWith('javascript:') || anchor.getAttribute('download') !== null || (anchor.getAttribute('target') ?? '_self') !== '_self' || anchor.getAttribute('rel')?.includes('external') || !isSameOrigin(href)) return
      // 跳过：带有 data-native 属性的链接（强制整页跳转）
      if (anchor.hasAttribute('data-native')) return
      event.preventDefault()
      const url = new URL(href, window.location.origin)
      navigate(`${url.pathname}${url.search}${url.hash}`)
    }
    document.addEventListener('click', handler)
    return () => document.removeEventListener('click', handler)
  }, [navigate])

  return { location, navigate }
}

// ============================================================
// 认证状态 — 应用级缓存，页面切换不重复请求
// ============================================================
function useAuthState() {
  const [auth, setAuth] = useState<AuthStatus | null>(null)
  const refresh = useCallback(async () => {
    try { setAuth(await homeApi.auth()) }
    catch { setAuth(current => current ?? { logged_in: false }) }
  }, [])

  useEffect(() => {
    void refresh()
    // 页面恢复可见时刷新（在别的标签页改名或退出后切回来能立即生效）
    const onVisible = () => { if (document.visibilityState === 'visible') void refresh() }
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [refresh])

  return useMemo<AuthValue>(() => ({ auth, refresh }), [auth, refresh])
}

function LoginRedirect() {
  useEffect(() => {
    window.location.replace(`/login?returnTo=${encodeURIComponent(currentHref())}`)
  }, [])
  return <RouteLoading />
}

function RouteLoading() {
  return <div className="route-loading" role="status" aria-busy="true"><span aria-hidden="true" /><p>HomeHub</p></div>
}

// ============================================================
// 页面定义
// ============================================================
interface PageDefinition {
  component: ComponentType
  meta: ShellPageMeta
  /** 访问页面所需的最低等级（数值上限，见 lib/permissions.ts）；缺省为任何已登录用户。 */
  minLevel?: number
}

const pages: Record<string, PageDefinition> = {
  '/dashboard': { component: DashboardPage, meta: { eyebrow: '运行概览', title: '仪表盘', description: '查看服务健康、数据库连接与运行时长。' } },
  '/bookmarks': { component: BookmarksPage, meta: { eyebrow: '工作空间', title: '书签', description: '维护分组、链接与显示顺序。' } },
  '/rss': { component: RSSPage, meta: { eyebrow: '内容订阅', title: 'RSS', description: '维护订阅源并阅读指定来源的条目。' } },
  '/todos': { component: TodosPage, meta: { eyebrow: '共享任务', title: '待办事项', description: '跟踪所有认证用户共享的任务集合。' } },
  '/media': { component: MediaPage, meta: { eyebrow: '资源库', title: '媒体资源', description: '上传、检查和清理背景媒体。' } },
  '/search': { component: SearchPage, meta: { eyebrow: '跨域检索', title: '数据搜索', description: '检索书签与 RSS 条目。' } },
  '/profile': { component: ProfilePage, meta: { eyebrow: '账户', title: '个人中心', description: '维护个人资料、头像与登录密码。' } },
  '/users': { component: UsersPage, meta: { eyebrow: '访问控制', title: '用户管理', description: '管理成员、角色与账户状态。' }, minLevel: LEVEL_OPERATOR },
  '/logs': { component: LogsPage, meta: { eyebrow: '系统事件', title: '系统日志', description: '筛选和定位后端运行事件。' }, minLevel: LEVEL_OPERATOR },
  '/settings': { component: SettingsPage, meta: { eyebrow: '系统配置', title: '设置', description: '按配置域修改后端支持的参数。' }, minLevel: LEVEL_ADMIN },
  '/extension': { component: ExtensionPage, meta: { eyebrow: '跳实例集成', title: 'Extension 对接', description: '把订阅镜像到 Extension，并为用户签发访问密钥。' }, minLevel: LEVEL_OPERATOR },
  '/appearance': { component: AppearancePage, meta: { eyebrow: '界面偏好', title: '外观', description: '管理当前浏览器主题与服务端背景配置。' } },
  '/balance': { component: BalancePage, meta: { eyebrow: '服务监视', title: 'LLM 余额', description: '查看各平台 API 余额与监控状态。' } },
}

// ============================================================
// App 根组件 — 认证缓存 + 客户端导航
// ============================================================
export default function App() {
  const navigation = useClientNavigation()
  const authValue = useAuthState()
  return (
    <NavigationContext.Provider value={navigation}>
      <AuthContext.Provider value={authValue}>
        <Routes path={navigation.location.pathname} auth={authValue.auth} />
      </AuthContext.Provider>
    </NavigationContext.Provider>
  )
}

function Routes({ path, auth }: { path: string; auth: AuthStatus | null }) {
  // 公开页面
  if (path === '/') return <HomePage />
  if (path === '/login') return <LoginPage />
  if (path === '/paste' || path.startsWith('/paste/')) return <PastePage />

  // 认证页面 — 等待首次认证检查
  if (!auth) return <RouteLoading />

  // 未登录 → 重定向（副作用放在 effect 里，render 保持纯）
  if (!auth.logged_in) return <LoginRedirect />

  // 页面解析
  const page = pages[path] ?? null
  if (!page) {
    return (
      <div className="not-found" role="alert">
        <p>404</p>
        <h1>页面不存在</h1>
        <span>请求的地址没有对应的 HomeHub 页面。</span>
        <a href="/">返回首页</a>
      </div>
    )
  }

  // 等级检查（fail closed：等级缺失视为无权）
  if (page.minLevel !== undefined && !hasLevel(auth, page.minLevel)) {
    return (
      <main className="route-state" role="alert">
        <h1>无权访问</h1>
        <p>此页面需要「{levelLabel(page.minLevel)}」或更高等级。</p>
        <a href="/dashboard">返回仪表盘</a>
      </main>
    )
  }

  const Page = page.component
  return <AppShell auth={auth} meta={page.meta} path={path}><Page /></AppShell>
}
