import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties, FormEvent } from 'react'
import { ThemeToggle } from '@/components/ThemeToggle'
import { UserAvatar } from '@/components/UserAvatar'
import { homeApi, type AuthStatus, type BackgroundSettings, type Bookmark, type BookmarkGroup, type MediaAsset, type RSSEntry, type RSSFeed } from '@/lib/home'
import { absoluteDate, externalLinkProps, hostnameOf, isUsableTimestamp, plainSummary, relativeTime } from '@/lib/format'
import { MagnetCopyButton } from '@/components/MagnetCopyButton'
import { PageState } from '@/components/WorkspaceUI'
import { isAbortError } from '@/lib/api'
import { getWebUIConfig } from '@/lib/webui'
import { useAuth } from '@/lib/auth'
import { useNavigate } from '@/lib/navigation'
import { LEVEL_ADMIN, hasLevel, levelLabel } from '@/lib/permissions'
import { mediaURL } from '@/lib/media'

type Preferences = { bookmarks: number[]; feeds: number[] }

const STORAGE_KEY = 'homehub.home.preferences.v1'
const MAX_BOOKMARKS_PER_GROUP = 8
const MAX_ENTRIES_PER_FEED = 5

const fallbackBackground: BackgroundSettings = {
  mode: 'single',
  interval_seconds: 30,
  transition_effect: 'fade',
  fade_duration_ms: 1000,
  selected_asset_ids: [],
}

function loadPreferences(): Preferences {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { bookmarks: [], feeds: [] }
    const parsed = JSON.parse(raw) as Partial<Preferences> | null
    return {
      bookmarks: Array.isArray(parsed?.bookmarks) ? parsed.bookmarks.filter(id => typeof id === 'number') : [],
      feeds: Array.isArray(parsed?.feeds) ? parsed.feeds.filter(id => typeof id === 'number') : [],
    }
  } catch {
    // 隐私模式下读取会抛 SecurityError，JSON 损坏会抛 SyntaxError；两者都退回默认
    return { bookmarks: [], feeds: [] }
  }
}

function persistPreferences(next: Preferences) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  } catch {
    // 隐私模式或配额已满时写入会抛错；本次访问内的选择仍然生效
  }
}

function initialShortcutLabel(): string {
  return /Mac|iPhone|iPad/i.test(navigator.userAgent) ? '⌘ K' : 'Ctrl K'
}

// ============================================================
// 小图标
// ============================================================

function SearchIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><circle cx="11" cy="11" r="6.5" /><path d="m16 16 4.5 4.5" /></svg>
}

function GearIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><circle cx="12" cy="12" r="3.2" /><path d="M12 3.4v2.1m0 13v2.1M4.7 7.8l1.8 1m11 6.4 1.8 1M4.7 16.2l1.8-1m11-6.4 1.8-1" /></svg>
}

/** 书签图标：优先远程 favicon，失败时回退到标题首字母。 */
function BookmarkIcon({ bookmark }: { bookmark: Bookmark }) {
  const [broken, setBroken] = useState(false)
  const initial = (bookmark.title || '?').trim().slice(0, 1).toUpperCase()
  return <span className="bookmark-icon" aria-hidden="true">
    {bookmark.icon && !broken
      ? <img src={bookmark.icon} alt="" loading="lazy" onError={() => setBroken(true)} />
      : <span>{initial}</span>}
  </span>
}

/** 订阅源标记：优先 icon_url，失败时回退到标题首字母。 */
function FeedMark({ feed }: { feed: RSSFeed }) {
  const [broken, setBroken] = useState(false)
  const initial = (feed.title || 'R').trim().slice(0, 1).toUpperCase()
  return <span className="feed-mark" aria-hidden="true">
    {feed.icon_url && !broken
      ? <img src={feed.icon_url} alt="" loading="lazy" onError={() => setBroken(true)} />
      : <span>{initial}</span>}
  </span>
}

function LinkArrow() {
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M8 16 16 8m0 0H9m7 0v7" /></svg>
}

/** 订阅条目行：标题 + 摘要 + 元信息。摘要不截断行数，完整展示。 */
function FeedEntryRow({ entry }: { entry: RSSEntry }) {
  // 新接口给 excerpt（服务端已剥成纯文本）；旧接口只给 content，
  // 那时退回本地解析——否则首页卡片会在版本错开时集体变空。
  const summary = entry.excerpt ?? plainSummary(entry.content)
  return <li>
    {/* is_read 缺失（旧后端）时按已读处理，不给整张卡片都加未读标记。 */}
    <a className={`feed-item${entry.is_read === false ? ' is-unread' : ''}`} href={entry.link} {...externalLinkProps(entry.link)}>
      <span className="feed-item-title">{entry.is_read === false && <span className="visually-hidden">未读：</span>}{entry.title}</span>
      {summary && <span className="feed-item-summary">{summary}</span>}
      <span className="feed-item-meta">
        {entry.author && <><span className="feed-item-author">{entry.author}</span><span className="meta-dot" aria-hidden="true">·</span></>}
        <time dateTime={isUsableTimestamp(entry.published) ? entry.published : undefined} title={absoluteDate(entry.published)}>
          {relativeTime(entry.published) || '最近更新'}
        </time>
      </span>
    </a>
    {entry.magnet_url && <MagnetCopyButton className="feed-magnet" url={entry.magnet_url} title={entry.title} />}
  </li>
}

// ============================================================
// 账户菜单 — 点击展开（原 hover 方案在触屏上不可达）
// ============================================================

function UserMenu({ auth, onLogout }: { auth: AuthStatus; onLogout: () => void }) {
  const [open, setOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (menuRef.current && !menuRef.current.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setOpen(false)
      triggerRef.current?.focus()
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  return <div className="user-menu" ref={menuRef}>
    <button
      type="button"
      ref={triggerRef}
      className="user-menu-trigger"
      aria-expanded={open}
      aria-controls="home-user-menu"
      onClick={() => setOpen(value => !value)}
    >
      <UserAvatar username={auth.username} size={30} userId={auth.id} />
      <span className="user-menu-name">{auth.username || '账户'}</span>
      <span className="user-menu-caret" aria-hidden="true">▾</span>
    </button>
    {open && <div className="user-menu-panel" id="home-user-menu">
      <p className="user-menu-meta">已登录 · {levelLabel(auth.level)}</p>
      <a href="/dashboard">进入面板</a>
      <a href="/profile">个人资料</a>
      <a href="/appearance">界面外观</a>
      {hasLevel(auth, LEVEL_ADMIN) && <a href="/settings">系统设置</a>}
      <button type="button" className="is-danger" onClick={() => { setOpen(false); onLogout() }}>退出登录</button>
    </div>}
  </div>
}

// ============================================================
// 自定义抽屉 — Esc 关闭、焦点锁定、滚动锁定
// ============================================================

interface CustomizeDrawerProps {
  groups: BookmarkGroup[]
  feeds: RSSFeed[]
  preferences: Preferences
  onChange: (next: Preferences) => void
  onToggle: (kind: keyof Preferences, id: number) => void
  onClose: () => void
}

function CustomizeDrawer({ groups, feeds, preferences, onChange, onToggle, onClose }: CustomizeDrawerProps) {
  const drawerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const node = drawerRef.current
    node?.focus()
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    // 抽屉打开期间让背景区域不可聚焦、不可点击，补足 aria-modal 的实际语义
    const background = Array.from(document.querySelectorAll<HTMLElement>('.home-header, .home-main, .home-footer, .customize-float'))
    background.forEach(element => element.setAttribute('inert', ''))

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose()
        return
      }
      if (event.key !== 'Tab' || !node) return
      const focusables = Array.from(node.querySelectorAll<HTMLElement>('button, input, a[href]'))
        .filter(element => !element.hasAttribute('disabled'))
      if (focusables.length === 0) {
        event.preventDefault()
        node.focus()
        return
      }
      const first = focusables[0]
      const last = focusables[focusables.length - 1]
      const active = document.activeElement
      const inside = active instanceof Node && node.contains(active)
      // 初始焦点落在 aside 自身，或焦点意外逸出到外部时，手动送回边界
      if (event.shiftKey) {
        if (!inside || active === first || active === node) {
          event.preventDefault()
          last.focus()
        }
      } else if (!inside || active === last) {
        event.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.body.style.overflow = previousOverflow
      background.forEach(element => element.removeAttribute('inert'))
    }
  }, [onClose])

  const bookmarkSummary = preferences.bookmarks.length
    ? `已选 ${preferences.bookmarks.length} / ${groups.length}`
    : `默认全部 ${groups.length}`
  const feedSummary = preferences.feeds.length
    ? `已选 ${preferences.feeds.length} / ${feeds.length}`
    : `默认全部 ${feeds.length}`

  return <div className="drawer-backdrop" onMouseDown={onClose}>
    <aside
      className="custom-drawer"
      ref={drawerRef}
      role="dialog"
      aria-modal="true"
      aria-labelledby="customize-title"
      tabIndex={-1}
      onMouseDown={event => event.stopPropagation()}
    >
      <header className="drawer-heading">
        <div>
          <p className="block-eyebrow">PERSONALIZE</p>
          <h2 id="customize-title">自定义首页</h2>
        </div>
        <button type="button" className="drawer-close" onClick={onClose} aria-label="关闭自定义面板">×</button>
      </header>
      <p className="drawer-note">选择要在首页出现的内容。未选择任何一项时默认展示全部。</p>

      <section className="drawer-section">
        <div className="drawer-section-head">
          <h3 id="drawer-groups-title">书签组</h3>
          <div className="drawer-section-tools">
            <span>{bookmarkSummary}</span>
            {preferences.bookmarks.length > 0 && <button type="button" onClick={() => onChange({ ...preferences, bookmarks: [] })}>显示全部</button>}
          </div>
        </div>
        <div className="choice-list" role="group" aria-labelledby="drawer-groups-title">
          {groups.length
            ? groups.map(group => <label className="choice" key={group.id}>
              <input type="checkbox" checked={preferences.bookmarks.includes(group.id)} onChange={() => onToggle('bookmarks', group.id)} />
              <span className="choice-copy"><strong>{group.name}</strong><small>{group.bookmarks?.length ?? 0} 个书签</small></span>
            </label>)
            : <p className="drawer-empty">还没有书签分组。</p>}
        </div>
      </section>

      <section className="drawer-section">
        <div className="drawer-section-head">
          <h3 id="drawer-feeds-title">RSS 订阅</h3>
          <div className="drawer-section-tools">
            <span>{feedSummary}</span>
            {preferences.feeds.length > 0 && <button type="button" onClick={() => onChange({ ...preferences, feeds: [] })}>显示全部</button>}
          </div>
        </div>
        <div className="choice-list" role="group" aria-labelledby="drawer-feeds-title">
          {feeds.length
            ? feeds.map(feed => <label className="choice" key={feed.id}>
              <input type="checkbox" checked={preferences.feeds.includes(feed.id)} onChange={() => onToggle('feeds', feed.id)} />
              <span className="choice-copy"><strong>{feed.title || '未命名订阅'}</strong><small>{hostnameOf(feed.site_url || feed.url)}</small></span>
            </label>)
            : <p className="drawer-empty">还没有 RSS 订阅。</p>}
        </div>
      </section>

      <button type="button" className="drawer-done" onClick={onClose}>完成</button>
    </aside>
  </div>
}

// ============================================================
// 状态占位
// ============================================================

function HomeSkeleton() {
  return <div className="home-skeleton" role="status" aria-busy="true" aria-live="polite">
    <span className="visually-hidden">正在加载首页内容…</span>
    <div className="skeleton-block skeleton-block--wide" />
    <div className="skeleton-row">
      <div className="skeleton-block" />
      <div className="skeleton-block" />
    </div>
  </div>
}

function HomeEmpty({ loggedIn }: { loggedIn: boolean }) {
  return <section className="home-empty">
    <div className="home-empty-art" aria-hidden="true"><span>H</span></div>
    <div className="home-empty-copy">
      <p className="block-eyebrow">START HERE</p>
      <h2>这个首页还空着</h2>
      <p>{loggedIn
        ? '去控制台添加书签分组和 RSS 订阅，它们会自动出现在这里。'
        : '登录后可以管理书签与订阅，这个页面会成为你的日常起点。'}</p>
      <div className="home-empty-actions">
        <a className="home-cta home-cta--primary" href={loggedIn ? '/bookmarks' : '/login?returnTo=%2Fdashboard'}>
          {loggedIn ? '添加书签' : '登录 HomeHub'}
        </a>
        <a className="home-cta" href="/paste">看看 Pastebin</a>
      </div>
    </div>
  </section>
}

// ============================================================
// 首页
// ============================================================

export function HomePage() {
  const { auth: authState } = useAuth()
  const auth: AuthStatus = authState ?? { logged_in: false }
  const navigate = useNavigate()
  const [groups, setGroups] = useState<BookmarkGroup[]>([])
  const [feeds, setFeeds] = useState<RSSFeed[]>([])
  // 每个订阅源各自的条目；'error' 表示该源读取失败。
  const [entriesByFeed, setEntriesByFeed] = useState<Record<number, RSSEntry[] | 'error'>>({})
  const [loadError, setLoadError] = useState('')
  const [reloadKey, setReloadKey] = useState(0)
  const [background, setBackground] = useState(fallbackBackground)
  const [assets, setAssets] = useState<MediaAsset[]>([])
  const [backgroundIndex, setBackgroundIndex] = useState(0)
  const [customizing, setCustomizing] = useState(false)
  const [loading, setLoading] = useState(true)
  const [searchQuery, setSearchQuery] = useState('')
  const [preferences, setPreferences] = useState<Preferences>(loadPreferences)
  const [shortcutLabel] = useState(initialShortcutLabel)
  const searchRef = useRef<HTMLInputElement>(null)
  const customizeButtonRef = useRef<HTMLButtonElement>(null)

  // 背景配置单独加载：失败时保持默认渐变背景，不影响内容区。
  useEffect(() => {
    let cancelled = false
    getWebUIConfig()
      .then(config => { if (!cancelled) { setBackground(config.home.background); setAssets(config.home.assets) } })
      .catch(() => undefined)
    return () => { cancelled = true }
  }, [])

  // 书签与订阅源（公开端点不需要登录）。认证状态由 App 的 AuthContext 统一维护，这里不再重复请求。
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    Promise.allSettled([homeApi.bookmarks(), homeApi.feeds()]).then(([groupsResult, feedsResult]) => {
      if (cancelled) return
      const failed: string[] = []
      if (groupsResult.status === 'fulfilled') setGroups(groupsResult.value || [])
      else failed.push('书签')
      if (feedsResult.status === 'fulfilled') setFeeds(feedsResult.value || [])
      else failed.push('订阅')
      // 失败与「真的没有内容」必须区分：否则网络出错时首页会显示「这个首页还空着」。
      setLoadError(failed.length ? `${failed.join('与')}读取失败，请检查网络后重试。` : '')
      setLoading(false)
    })
    return () => { cancelled = true }
  }, [reloadKey])

  const selectedAssets = useMemo(
    () => (background.selected_asset_ids ?? [])
      .map(id => assets.find(asset => asset.id === id))
      .filter(Boolean) as MediaAsset[],
    [assets, background.selected_asset_ids],
  )

  // 背景图轮播
  useEffect(() => {
    if (selectedAssets.length < 2 || background.mode === 'single') return
    const timer = window.setInterval(() => setBackgroundIndex(index => background.mode === 'random'
      ? (index + 1 + Math.floor(Math.random() * (selectedAssets.length - 1))) % selectedAssets.length
      : (index + 1) % selectedAssets.length), Math.max(background.interval_seconds, 1) * 1000)
    return () => window.clearInterval(timer)
  }, [selectedAssets.length, background.interval_seconds, background.mode])

  // 背景图交叉淡入淡出
  const currentAssetRef = useRef<MediaAsset | null>(null)
  const [bgLayers, setBgLayers] = useState<{ current: MediaAsset | null; outgoing: MediaAsset | null }>({ current: null, outgoing: null })
  const activeAsset = selectedAssets[backgroundIndex % Math.max(selectedAssets.length, 1)] ?? null
  useEffect(() => {
    if (activeAsset?.id === currentAssetRef.current?.id) return
    setBgLayers({ current: activeAsset, outgoing: currentAssetRef.current })
    currentAssetRef.current = activeAsset
    const timer = setTimeout(() => setBgLayers(prev => ({ ...prev, outgoing: null })), background.fade_duration_ms + 100)
    return () => clearTimeout(timer)
  }, [activeAsset, background.fade_duration_ms])

  // ⌘K / Ctrl K 聚焦搜索
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        searchRef.current?.focus()
        searchRef.current?.select()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  const bookmarksInView = preferences.bookmarks.length
    ? groups.filter(group => preferences.bookmarks.includes(group.id))
    : groups
  const feedsInView = preferences.feeds.length
    ? feeds.filter(feed => preferences.feeds.includes(feed.id))
    : feeds
  // 偏好里选中的分组/订阅已被删除时回退到全部，避免出现空白首页
  const visibleGroups = bookmarksInView.length ? bookmarksInView : groups
  const visibleFeeds = feedsInView.length ? feedsInView : feeds

  const loggedIn = auth.logged_in
  const hasContent = visibleGroups.length > 0 || visibleFeeds.length > 0

  // 每张订阅卡片单独请求自己的最新 N 条（并发）。全局取前 50 条再按源过滤
  // 会让更新少的源永远拿不到条目，误报「还没有已抓取的文章」。
  const visibleFeedKey = visibleFeeds.map(feed => feed.id).join(',')
  useEffect(() => {
    if (!visibleFeedKey) return
    const ids = visibleFeedKey.split(',').map(Number)
    const controller = new AbortController()
    Promise.all(ids.map(id => homeApi.feedEntries(id, MAX_ENTRIES_PER_FEED, { signal: controller.signal })
      .then(result => [id, result.list ?? []] as const)
      .catch(cause => [id, isAbortError(cause) ? null : 'error'] as const)))
      .then(results => {
        if (controller.signal.aborted) return
        setEntriesByFeed(Object.fromEntries(results.filter(([, value]) => value !== null)) as Record<number, RSSEntry[] | 'error'>)
      })
    return () => controller.abort()
  }, [visibleFeedKey, reloadKey])

  function updatePreferences(next: Preferences) {
    setPreferences(next)
    persistPreferences(next)
  }

  const togglePreference = useCallback((kind: keyof Preferences, id: number) => {
    const values = preferences[kind]
    const next = { ...preferences, [kind]: values.includes(id) ? values.filter(value => value !== id) : [...values, id] }
    setPreferences(next)
    persistPreferences(next)
  }, [preferences])

  const closeCustomize = useCallback(() => {
    setCustomizing(false)
    window.setTimeout(() => customizeButtonRef.current?.focus(), 0)
  }, [])

  async function logout() {
    try { await homeApi.logout() } finally { window.location.reload() }
  }

  function submitSearch(event: FormEvent) {
    event.preventDefault()
    const query = searchQuery.trim()
    if (!query) {
      searchRef.current?.focus()
      return
    }
    navigate(`/search?q=${encodeURIComponent(query)}`)
  }

  const bgFadeStyle = { '--bg-duration': `${background.fade_duration_ms}ms` } as CSSProperties
  const bgInClass = background.transition_effect === 'slide' ? 'slide-in' : 'bg-fade-in'
  const bgOutClass = background.transition_effect === 'slide' ? 'slide-out' : 'bg-fade-out'

  return <div className="home-shell">
    <div className="home-background" aria-hidden="true">
      <div className="background-gradient" />
      {bgLayers.outgoing && (
        bgLayers.outgoing.media_type === 'video'
          ? <video className={`background-layer ${bgOutClass}`} src={mediaURL(bgLayers.outgoing.file_path)} autoPlay muted loop playsInline style={bgFadeStyle} />
          : <div className={`background-image ${bgOutClass}`} style={{ backgroundImage: `url("${mediaURL(bgLayers.outgoing.file_path)}")`, ...bgFadeStyle }} />
      )}
      {bgLayers.current && (
        bgLayers.current.media_type === 'video'
          ? <video className={`background-layer ${bgInClass}`} key={bgLayers.current.id} src={mediaURL(bgLayers.current.file_path)} autoPlay muted loop playsInline style={bgFadeStyle} />
          : <div className={`background-image ${bgInClass}`} key={bgLayers.current.id} style={{ backgroundImage: `url("${mediaURL(bgLayers.current.file_path)}")`, ...bgFadeStyle }} />
      )}
      <div className="background-shade" />
    </div>

    <header className="home-header">
      <a className="brand" href="/" aria-label="HomeHub 首页">
        <span className="brand-mark" aria-hidden="true">H</span>
        <span className="brand-name">HomeHub</span>
      </a>
      <nav className="home-nav" aria-label="快捷入口">
        <a href="/paste">Pastebin</a>
        {loggedIn && <a href="/todos">待办</a>}
      </nav>
      <div className="header-actions">
        <ThemeToggle />
        {loggedIn
          ? <UserMenu auth={auth} onLogout={logout} />
          : <a className="login-button" href="/login?returnTo=%2Fdashboard">登录</a>}
      </div>
    </header>

    <main className="home-main">
      {/* 页面主标题保留给屏幕阅读器与文档大纲，视觉上只展示搜索框 */}
      <h1 className="visually-hidden">HomeHub 首页</h1>
      <section className="hero" aria-label="站内搜索">
        <form className="hero-search" role="search" onSubmit={submitSearch}>
          <span className="hero-search-icon" aria-hidden="true"><SearchIcon /></span>
          <input
            ref={searchRef}
            className="hero-search-input"
            value={searchQuery}
            onChange={event => setSearchQuery(event.target.value)}
            placeholder="搜索书签或订阅条目…"
            aria-label="搜索书签或订阅条目"
            autoComplete="off"
          />
          {searchQuery && <button type="button" className="hero-search-clear" onClick={() => { setSearchQuery(''); searchRef.current?.focus() }} aria-label="清空搜索">×</button>}
          <kbd className="hero-search-kbd" aria-hidden="true">{shortcutLabel}</kbd>
          <button type="submit" className="hero-search-btn">搜索</button>
        </form>
      </section>

      {loading ? <HomeSkeleton /> : loadError && !hasContent ? <PageState kind="error" title="首页内容读取失败" description={loadError} action={<button type="button" className="home-cta home-cta--primary" onClick={() => setReloadKey(key => key + 1)}>重试</button>} /> : hasContent ? <>
        {loadError && <PageState kind="error" title="部分内容读取失败" description={loadError} action={<button type="button" className="home-cta home-cta--primary" onClick={() => setReloadKey(key => key + 1)}>重试</button>} />}
        {visibleGroups.length > 0 && <section className="home-block" aria-labelledby="home-bookmarks-title">
          <header className="block-head">
            {/* 区块名仅供辅助技术，视觉上只留管理入口 */}
            <h2 id="home-bookmarks-title" className="visually-hidden">书签</h2>
            <a className="block-link" href="/bookmarks">管理书签<span aria-hidden="true"> →</span></a>
          </header>
          <div className="bookmark-groups">
            {visibleGroups.map(group => {
              const bookmarks = group.bookmarks ?? []
              const displayed = bookmarks.slice(0, MAX_BOOKMARKS_PER_GROUP)
              const remaining = bookmarks.length - displayed.length
              return <article className="home-bookmark-group" key={group.id}>
                <header className="home-group-head">
                  <h3>{group.name}</h3>
                  <span className="home-group-count" aria-label={`${bookmarks.length} 个书签`}>{bookmarks.length}</span>
                  {remaining > 0 && <a className="home-group-more" href="/bookmarks">还有 {remaining} 个<span aria-hidden="true"> →</span></a>}
                </header>
                {displayed.length
                  ? <div className="bookmark-grid">
                    {displayed.map(bookmark => <a className="bookmark-item" key={bookmark.id} href={bookmark.url} target="_blank" rel="noreferrer" title={bookmark.title}>
                      <BookmarkIcon bookmark={bookmark} />
                      <span className="bookmark-text">
                        <strong>{bookmark.title}</strong>
                        <small>{hostnameOf(bookmark.url)}</small>
                      </span>
                      <span className="bookmark-arrow" aria-hidden="true"><LinkArrow /></span>
                    </a>)}
                  </div>
                  : <p className="home-group-empty">这个分组还没有书签。</p>}
              </article>
            })}
          </div>
        </section>}

        {visibleFeeds.length > 0 && <section className="home-block" aria-labelledby="home-feeds-title">
          <header className="block-head">
            <h2 id="home-feeds-title" className="visually-hidden">订阅</h2>
            <a className="block-link" href="/rss">管理订阅<span aria-hidden="true"> →</span></a>
          </header>
          <div className="feed-grid">
            {visibleFeeds.map(feed => {
              const feedState = entriesByFeed[feed.id]
              const feedEntries = Array.isArray(feedState) ? feedState.slice(0, MAX_ENTRIES_PER_FEED) : []
              const feedLink = feed.site_url || feed.url
              const feedName = feed.title || '未命名订阅'
              return <article className="feed-card" key={feed.id}>
                <header className="feed-head">
                  <FeedMark feed={feed} />
                  <div className="feed-title">
                    <h3>{feedName}</h3>
                  </div>
                  <a className="feed-open" href={feedLink} target="_blank" rel="noreferrer" aria-label={`在浏览器中打开 ${feedName} 站点`}>
                    <LinkArrow />
                  </a>
                </header>
                {feedState === undefined
                  ? <p className="feed-empty" role="status">正在读取…</p>
                  : feedState === 'error'
                    ? <p className="feed-empty" role="alert">条目读取失败。<button type="button" className="feed-retry" onClick={() => setReloadKey(key => key + 1)}>重试</button></p>
                    : feedEntries.length
                      ? <ul className="feed-list">
                        {feedEntries.map(entry => <FeedEntryRow key={entry.id} entry={entry} />)}
                      </ul>
                      : <p className="feed-empty">这个订阅还没有已抓取的文章。</p>}
              </article>
            })}
          </div>
        </section>}
      </> : <HomeEmpty loggedIn={loggedIn} />}
    </main>

    <footer className="home-footer">
      <span>HomeHub · 个人资源中枢</span>
      <span className="footer-dot" aria-hidden="true">·</span>
      <a href="/paste">Pastebin</a>
      {loggedIn && <>
        <span className="footer-dot" aria-hidden="true">·</span><a href="/bookmarks">书签</a>
        <span className="footer-dot" aria-hidden="true">·</span><a href="/rss">订阅</a>
        <span className="footer-dot" aria-hidden="true">·</span><a href="/todos">待办</a>
        <span className="footer-dot" aria-hidden="true">·</span><a href="/dashboard">控制台</a>
      </>}
    </footer>

    {loggedIn && <button
      type="button"
      ref={customizeButtonRef}
      className="customize-float"
      onClick={() => setCustomizing(true)}
      aria-label="自定义首页内容"
      title="自定义首页内容"
    ><GearIcon /></button>}

    {customizing && <CustomizeDrawer
      groups={groups}
      feeds={feeds}
      preferences={preferences}
      onChange={updatePreferences}
      onToggle={togglePreference}
      onClose={closeCustomize}
    />}
  </div>
}
