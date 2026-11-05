import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { HotkeyHelp } from '@/components/HotkeyHelp'
import { ThemeToggle } from '@/components/ThemeToggle'
import { UserAvatar } from '@/components/UserAvatar'
import { useHotkeys } from '@/hooks/useHotkeys'
import type { AuthStatus } from '@/lib/home'
import { useNavigate } from '@/lib/navigation'
import { SEARCH_INPUT_ID } from '@/lib/search'
import { LEVEL_ADMIN, LEVEL_OPERATOR, hasLevel } from '@/lib/permissions'

// ============================================================
// AppShell — 控制台侧边栏布局
//
// 导航使用原生 <a>，由 App 拦截同源点击改为 history.pushState。
// 不依赖 React Router；当前路径由 App 通过 path 传入。
// 页面正文不再画标题块（顶栏已有页面名），页面只放操作按钮。
// ============================================================

interface NavItem { href: string; label: string; icon: ReactNode; minLevel?: number; children?: NavItem[] }
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
    label: '数据洞察', items: [
      { href: '/search', label: '数据搜索', icon: icon(<><circle cx="10.5" cy="10.5" r="5.5" /><path d="m15 15 4 4" /></>) },
      { href: '/balance', label: '余额监控', icon: icon(<><circle cx="12" cy="12" r="10" /><path d="M12 6v12m-4-4 4 4 4-4" /></>) },
    ]
  },
  {
    label: '个人', items: [
      { href: '/profile', label: '个人中心', icon: icon(<><circle cx="12" cy="8" r="3" /><path d="M5 20c.8-4 3.1-6 7-6s6.2 2 7 6" /></>) },
      { href: '/appearance', label: '界面外观', icon: icon(<><circle cx="12" cy="12" r="3" /><path d="M12 3v2m0 14v2M3 12h2m14 0h2M5.6 5.6 7 7m10 10 1.4 1.4M18.4 5.6 17 7M7 17l-1.4 1.4" /></>) },
    ]
  },
  {
    label: '系统管理', items: [
      { href: '/users', label: '用户管理', minLevel: LEVEL_OPERATOR, icon: icon(<><circle cx="9" cy="8" r="3" /><path d="M3 20c.5-4 2.5-6 6-6s5.5 2 6 6M16 7h5m-2.5-2.5v5" /></>) },
      { href: '/logs', label: '系统日志', minLevel: LEVEL_OPERATOR, icon: icon(<><path d="M5 4h14v16H5zM8 8h8m-8 4h8m-8 4h5" /></>) },
      { href: '/settings', label: '设置', minLevel: LEVEL_ADMIN, icon: icon(<><circle cx="12" cy="12" r="3" /><path d="m19 13.5 1-1.5-1-1.5.1-1.8-1.7-.7-.9-1.5-1.8.2L14.2 5 12.5 4l-1.4 1.1-1.8-.2-.9 1.6-1.7.7.1 1.8-1 1.5 1 1.5-.1 1.8 1.7.7.9 1.5 1.8-.2 1.4 1.1 1.7-1.1 1.8.2.9-1.5 1.7-.7z" /></>) },
      { href: '/extension', label: 'Extension 对接', minLevel: LEVEL_OPERATOR, icon: icon(<><path d="M9 3v6m6-6v6M7 9h10v3a5 5 0 0 1-10 0z" /><path d="M12 17v4" /></>) },
    ]
  },
]

/** focusSearchInput 聚焦搜索页的输入框并全选，方便直接改写关键词。 */
function focusSearchInput() {
  const input = document.getElementById(SEARCH_INPUT_ID)
  if (input instanceof HTMLInputElement) { input.focus(); input.select() }
}

/** ShellPageMeta 只承载文档级信息。
 *
 * eyebrow 与 description 曾用于页面顶部的标题块，那块已删除——
 * 界面上不再需要它们，保留字段只是为了兼容既有的 pages 定义，
 * 将来可以一并清掉。
 */
export interface ShellPageMeta { title: string; eyebrow?: string; description?: string }

// ============================================================
// 导航组折叠状态持久化
// ============================================================
const FOLD_STORAGE_KEY = 'homehub-nav-folded'
function loadFoldedGroups(): Set<string> {
  try {
    const raw = localStorage.getItem(FOLD_STORAGE_KEY)
    if (raw) {
      const arr: string[] = JSON.parse(raw)
      if (Array.isArray(arr)) return new Set(arr)
    }
  } catch { /* ignore corrupt storage */ }
  return new Set()
}
function saveFoldedGroups(folded: Set<string>) {
  localStorage.setItem(FOLD_STORAGE_KEY, JSON.stringify([...folded]))
}

/** 判断路径是否匹配导航项（用于高亮 active 状态）。 */
function matchesPath(pathname: string, href: string): boolean {
  return pathname === href || pathname.startsWith(`${href}/`)
}

export function AppShell({ auth, meta, path, children }: { auth: AuthStatus; meta: ShellPageMeta; path: string; children: ReactNode }) {
  const isActivePath = (href: string) => matchesPath(path, href)
  const [open, setOpen] = useState(false)
  const [announcement, setAnnouncement] = useState('')
  const contentRef = useRef<HTMLElement>(null)
  const sidebarRef = useRef<HTMLElement>(null)
  const menuButtonRef = useRef<HTMLButtonElement>(null)
  const firstRender = useRef(true)
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem('homehub-shell-collapsed') === '1')
  const [expandedMenus, setExpandedMenus] = useState<Set<string>>(() => new Set())
  // 导航分组折叠状态 — 默认全部展开
  const [foldedGroups, setFoldedGroups] = useState<Set<string>>(loadFoldedGroups)
  const [helpOpen, setHelpOpen] = useState(false)
  // 跳到搜索页后要等新页面挂载才能聚焦输入框，所以先记下来，在 path 变化后处理。
  const focusSearchPending = useRef(false)
  const navigate = useNavigate()

  useHotkeys([
    {
      keys: '/',
      description: '打开搜索并聚焦输入框',
      handler: () => {
        if (path === '/search') { focusSearchInput(); return }
        focusSearchPending.current = true
        navigate('/search')
      },
    },
    { keys: '?', description: '显示快捷键帮助', handler: () => setHelpOpen(true) },
  ])

  // 页面标题不再画在界面上（原先顶栏与正文各画一遍），但浏览器标签栏
  // 仍需要它：多个标签并排时只靠 favicon 分不出谁是谁。
  useEffect(() => {
    document.title = meta.title ? `${meta.title} · HomeHub` : 'HomeHub'
  }, [meta.title])

  // 路径变化（客户端导航或前进后退）：关闭移动端侧栏，焦点移到正文，并播报新页面标题。
  // 首次渲染不抢焦点，保持浏览器默认行为。
  useEffect(() => {
    setOpen(false)
    if (firstRender.current) { firstRender.current = false; return }
    setAnnouncement(`已打开 ${meta.title}`)
    if (focusSearchPending.current && path === '/search') {
      focusSearchPending.current = false
      focusSearchInput()
      return
    }
    focusSearchPending.current = false
    contentRef.current?.focus({ preventScroll: true })
  }, [path, meta.title])

  // 移动端侧栏：打开时焦点进抽屉，Esc 关闭并把焦点还给 ☰ 按钮。
  useEffect(() => {
    if (!open) return
    sidebarRef.current?.querySelector<HTMLElement>('a, button')?.focus()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setOpen(false)
      menuButtonRef.current?.focus()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [open])

  useEffect(() => {
    localStorage.setItem('homehub-shell-collapsed', collapsed ? '1' : '0')
  }, [collapsed])

  // 导航组折叠状态持久化
  useEffect(() => {
    saveFoldedGroups(foldedGroups)
  }, [foldedGroups])

  function toggleMenu(href: string) {
    setExpandedMenus(prev => {
      const next = new Set(prev)
      if (next.has(href)) next.delete(href)
      else next.add(href)
      return next
    })
  }

  function toggleGroupFold(label: string) {
    setFoldedGroups(prev => {
      const next = new Set(prev)
      if (next.has(label)) next.delete(label)
      else next.add(label)
      return next
    })
  }

  function renderNavItem(item: NavItem) {
    // 有子菜单且侧栏展开时，渲染可折叠子菜单
    if (item.children && !collapsed) {
      const isExpanded = expandedMenus.has(item.href)
      const isChildActive = item.children.some(child => isActivePath(child.href))
      return (
        <div key={item.href} className="nav-collapsible">
          <div className="nav-collapsible-head">
            <a href={item.href} className={isActivePath(item.href) || isChildActive ? 'is-active' : ''} aria-current={path === item.href ? 'page' : undefined}>
              {item.icon}
              <span>{item.label}</span>
            </a>
            <button type="button" className="nav-expand" onClick={() => toggleMenu(item.href)} aria-expanded={isExpanded} aria-label={`${item.label}子菜单`}>
              {isExpanded ? '▾' : '▸'}
            </button>
          </div>
          {isExpanded && (
            <div className="nav-children">
              {item.children.map(child => (
                <a key={child.href} href={child.href} className={isActivePath(child.href) ? 'is-active' : ''} aria-current={path === child.href ? 'page' : undefined}>
                  {child.icon}
                  <span>{child.label}</span>
                </a>
              ))}
            </div>
          )}
        </div>
      )
    }

    // 普通导航项（或收起状态下的所有项）
    return (
      <a
        key={item.href}
        href={item.href}
        title={collapsed ? item.label : undefined}
        className={isActivePath(item.href) ? 'is-active' : ''}
        aria-current={path === item.href ? 'page' : undefined}
      >
        {item.icon}
        <span>{item.label}</span>
      </a>
    )
  }

  return (
    <div className={`shell ${collapsed ? 'shell--collapsed' : ''}`}>
      <button type="button" tabIndex={-1} className={`shell-scrim ${open ? 'is-open' : ''}`} onClick={() => setOpen(false)} aria-label="关闭导航" />

      <aside className={`shell-sidebar ${open ? 'is-open' : ''}`} id="shell-sidebar" ref={sidebarRef}>
        <div className="shell-brand">
          <a href="/dashboard" aria-label="打开仪表盘">
            <span className="shell-brand-mark">H</span>
            <span className="shell-brand-name">HomeHub</span>
          </a>
          <button type="button" onClick={() => setCollapsed(v => !v)} aria-label={collapsed ? '展开侧栏' : '收起侧栏'}>{collapsed ? '›' : '‹'}</button>
        </div>
        <nav className="shell-nav" aria-label="主导航">
          {navigation.map(navGroup => {
            const group = { ...navGroup, items: navGroup.items.filter(item => item.minLevel === undefined || hasLevel(auth, item.minLevel)) }
            if (!group.items.length) return null
            const isFolded = foldedGroups.has(group.label)
            const hasActiveChild = group.items.some(item =>
              isActivePath(item.href) || item.children?.some(c => isActivePath(c.href))
            )
            // 如果分组中有当前活跃的子项，自动展开折叠
            const effectiveFolded = collapsed ? false : (hasActiveChild ? false : isFolded)
            return (
              <section key={group.label}>
                <h2 className="shell-nav-group-title">
                  <button
                    type="button"
                    className={`shell-nav-group-head ${effectiveFolded ? 'is-folded' : ''}`}
                    onClick={() => toggleGroupFold(group.label)}
                    aria-expanded={!effectiveFolded}
                    disabled={collapsed}
                    title={collapsed ? undefined : (effectiveFolded ? '展开分组' : '折叠分组')}
                  >
                    <span>{group.label}</span>
                    {!collapsed && <span className="shell-nav-group-arrow" aria-hidden="true">{effectiveFolded ? '▸' : '▾'}</span>}
                  </button>
                </h2>
                {!collapsed && !effectiveFolded && group.items.map(renderNavItem)}
              </section>
            )
          })}
        </nav>
        <a href="/profile" className="shell-account">
          <UserAvatar username={auth.username} size={36} userId={auth.id} />
          <div>
            <strong>{auth.username || '用户'}</strong>
            <span>账户与个人资料</span>
          </div>
        </a>
      </aside>

      <div className="shell-stage">
        {/* 顶栏只留导航开关与全局动作。原先这里还重复了一遍页面名与
            eyebrow，而页面正文里又有同样的一块（shell-page-head）——
            同一个标题在一屏里出现两次，占掉的正是内容的高度。 */}
        <header className="shell-header">
          <button type="button" ref={menuButtonRef} className="shell-menu" onClick={() => setOpen(true)} aria-label="打开导航" aria-expanded={open} aria-controls="shell-sidebar">☰</button>
          {/* 只显示页面名。曾在这里叠了一个 eyebrow（「运行概览」「内容订阅」），
              它与右边的标题重复表达同一件事，纯占位；正文里的同名大标题块
              也已经删掉，所以这里是页面上唯一的位置提示。 */}
          <strong className="shell-header-title">{meta.title}</strong>
          <div className="shell-header-actions">
            <ThemeToggle />
            <a href="/">返回首页</a>
          </div>
        </header>
        <main className="shell-main" id="shell-content" ref={contentRef} tabIndex={-1}>
          {children}
        </main>
        <p className="visually-hidden" aria-live="polite" aria-atomic="true">{announcement}</p>
      </div>
      <HotkeyHelp open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  )
}
