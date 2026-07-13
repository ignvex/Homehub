import { useEffect, useState, type ComponentType } from 'react'
import { AppShell, type ShellPageMeta } from '@/components/AppShell'
import { homeApi, type AuthStatus } from '@/lib/home'
import BilibiliPage from '@/pages/BilibiliPage'
import { BookmarksPage } from '@/pages/BookmarksPage'
import { DashboardPage } from '@/pages/DashboardPage'
import { HomePage } from '@/pages/HomePage'
import { LoginPage } from '@/pages/LoginPage'
import { MediaPage } from '@/pages/MediaPage'
import { LogsPage, RSSPage, SearchPage, SettingsPage, TodosPage, UsersPage } from '@/pages/OperationsPages'
import { ProfilePage } from '@/pages/ProfilePage'

interface PageDefinition {
  component: ComponentType
  meta: ShellPageMeta
  admin?: boolean
}

const pages: Record<string, PageDefinition> = {
  '/dashboard': { component: DashboardPage, meta: { eyebrow: '运行概览', title: '仪表盘', description: '查看服务健康、数据规模与采集进度。' } },
  '/bookmarks': { component: BookmarksPage, meta: { eyebrow: '工作空间', title: '书签', description: '维护分组、链接与显示顺序。' } },
  '/rss': { component: RSSPage, meta: { eyebrow: '内容订阅', title: 'RSS', description: '维护订阅源并阅读指定来源的条目。' } },
  '/todos': { component: TodosPage, meta: { eyebrow: '共享任务', title: '待办事项', description: '跟踪所有认证用户共享的任务集合。' } },
  '/media': { component: MediaPage, meta: { eyebrow: '资源库', title: '媒体资源', description: '上传、检查和清理背景媒体。' } },
  '/search': { component: SearchPage, meta: { eyebrow: '跨域检索', title: '数据搜索', description: '检索书签、视频、专栏与用户。' } },
  '/profile': { component: ProfilePage, meta: { eyebrow: '账户', title: '个人中心', description: '维护个人资料、头像与登录密码。' } },
  '/users': { component: UsersPage, meta: { eyebrow: '访问控制', title: '用户管理', description: '管理成员、角色与账户状态。' }, admin: true },
  '/logs': { component: LogsPage, meta: { eyebrow: '系统事件', title: '系统日志', description: '筛选和定位后端运行事件。' }, admin: true },
  '/settings': { component: SettingsPage, meta: { eyebrow: '系统配置', title: '设置', description: '按配置域修改后端支持的参数。' }, admin: true },
  '/appearance': { component: () => <SettingsPage appearance />, meta: { eyebrow: '界面偏好', title: '外观', description: '设置当前浏览器的显示主题。' } },
}

function resolvePage(pathname: string): PageDefinition | null {
  if (pathname === '/bilibili' || pathname.startsWith('/bilibili/')) {
    const [, , rawKind, detailID] = pathname.split('/')
    const kind = rawKind === 'articles' || rawKind === 'users' ? rawKind : 'videos'
    return {
      component: () => <BilibiliPage kind={kind} detailID={detailID} />,
      meta: { eyebrow: '数据探索', title: 'Bilibili', description: '查询已采集的视频、专栏与用户对象。' },
    }
  }
  return pages[pathname] ?? null
}

function ProtectedPage({ page }: { page: PageDefinition }) {
  const [auth, setAuth] = useState<AuthStatus | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    homeApi.auth()
      .then(status => {
        if (!status.logged_in) {
          const returnTo = `${window.location.pathname}${window.location.search}`
          window.location.replace(`/login?returnTo=${encodeURIComponent(returnTo)}`)
          return
        }
        setAuth(status)
      })
      .catch(cause => setError(cause instanceof Error ? cause.message : '无法确认登录状态'))
  }, [])

  if (error) return <main className="route-state" role="alert"><h1>无法打开页面</h1><p>{error}</p><a href="/login">前往登录</a></main>
  if (!auth) return <main className="route-state" aria-busy="true"><p>正在确认会话...</p></main>
  if (page.admin && auth.role !== 'admin') return <main className="route-state"><h1>无权访问</h1><p>此页面仅对管理员开放。</p><a href="/dashboard">返回仪表盘</a></main>

  const Page = page.component
  return <AppShell auth={auth} meta={page.meta}><Page /></AppShell>
}

export default function App() {
  const pathname = window.location.pathname.replace(/\/$/, '') || '/'
  if (pathname === '/') return <HomePage />
  if (pathname === '/login') return <LoginPage />

  const page = resolvePage(pathname)
  if (page) return <ProtectedPage page={page} />

  return <main className="route-state"><p className="eyebrow">404</p><h1>页面不存在</h1><p>请求的地址没有对应的 HomeHub 页面。</p><a href="/">返回首页</a></main>
}
