import { useEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties, FormEvent } from 'react'
import { ThemeToggle } from '@/components/ThemeToggle'
import { UserAvatar } from '@/components/UserAvatar'
import { homeApi, type AuthStatus, type BackgroundSettings, type BookmarkGroup, type MediaAsset, type RSSEntry, type RSSFeed } from '@/lib/home'
import { getWebUIConfig } from '@/lib/webui'
import { mediaURL } from '@/lib/media'

type Preferences = { bookmarks: number[]; feeds: number[] }
const STORAGE_KEY = 'homehub.home.preferences.v1'
const fallbackBackground: BackgroundSettings = { mode: 'single', interval_seconds: 30, transition_effect: 'fade', fade_duration_ms: 1000, selected_asset_ids: [] }
const MAX_BOOKMARKS_PER_GROUP = 8
const MAX_ENTRIES_PER_FEED = 5

export function HomePage() {
  const [auth, setAuth] = useState<AuthStatus>({ logged_in: false })
  const [groups, setGroups] = useState<BookmarkGroup[]>([])
  const [feeds, setFeeds] = useState<RSSFeed[]>([])
  const [entries, setEntries] = useState<RSSEntry[]>([])
  const [background, setBackground] = useState(fallbackBackground)
  const [assets, setAssets] = useState<MediaAsset[]>([])
  const [backgroundIndex, setBackgroundIndex] = useState(0)
  const [customizing, setCustomizing] = useState(false)
  const [loading, setLoading] = useState(true)
  const [searchQuery, setSearchQuery] = useState('')
  const [preferences, setPreferences] = useState<Preferences>(() => {
    try { return JSON.parse(localStorage.getItem(STORAGE_KEY) || '') as Preferences } catch { return { bookmarks: [], feeds: [] } }
  })

  // 并行加载所有数据（公开端点不需要登录）
  useEffect(() => {
    let cancelled = false
    const load = () => {
      Promise.allSettled([
        homeApi.auth(),
        getWebUIConfig(),
        homeApi.bookmarks(),
        homeApi.feeds(),
        homeApi.entries(),
      ]).then(([authResult, webuiResult, groupsResult, feedsResult, entriesResult]) => {
        if (cancelled) return
        if (authResult.status === 'fulfilled') setAuth(authResult.value)
        if (webuiResult.status === 'fulfilled') {
          setBackground(webuiResult.value.home.background)
          setAssets(webuiResult.value.home.assets)
        }
        if (groupsResult.status === 'fulfilled') setGroups(groupsResult.value || [])
        if (feedsResult.status === 'fulfilled') setFeeds(feedsResult.value || [])
        if (entriesResult.status === 'fulfilled') setEntries(entriesResult.value.list || [])
        setLoading(false)
      })
    }
    load()
    // 页面恢复可见时刷新 auth（改名后切回首页能立即生效）
    const onVisible = () => { if (document.visibilityState === 'visible') { homeApi.auth().then(a => { if (!cancelled) setAuth(a) }) } }
    document.addEventListener('visibilitychange', onVisible)
    return () => { cancelled = true; document.removeEventListener('visibilitychange', onVisible) }
  }, [])

  // 背景图轮播
  const selectedAssets = useMemo(() => background.selected_asset_ids.map(id => assets.find(asset => asset.id === id)).filter(Boolean) as MediaAsset[], [assets, background.selected_asset_ids])
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

  const visibleGroups = preferences.bookmarks.length ? groups.filter(group => preferences.bookmarks.includes(group.id)) : groups
  const visibleFeeds = preferences.feeds.length ? feeds.filter(feed => preferences.feeds.includes(feed.id)) : feeds

  function savePreferences(next: Preferences) { setPreferences(next); localStorage.setItem(STORAGE_KEY, JSON.stringify(next)) }
  function toggle(kind: keyof Preferences, id: number) {
    const current = preferences[kind]
    savePreferences({ ...preferences, [kind]: current.includes(id) ? current.filter(value => value !== id) : [...current, id] })
  }
  async function logout() { try { await homeApi.logout() } finally { window.location.reload() } }
  function submitSearch(event: FormEvent) {
    event.preventDefault()
    const q = searchQuery.trim()
    if (q) window.location.assign(`/search?q=${encodeURIComponent(q)}`)
  }

  const bgFadeStyle = { '--bg-duration': `${background.fade_duration_ms}ms` } as CSSProperties
  const bgInClass = background.transition_effect === 'slide' ? 'slide-in' : 'bg-fade-in'
  const bgOutClass = background.transition_effect === 'slide' ? 'slide-out' : 'bg-fade-out'
  const hasContent = visibleGroups.length > 0 || visibleFeeds.length > 0

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
      <a className="brand" href="/"><span className="brand-mark">H</span><span>HomeHub</span></a>
      <a href="/paste" className="pastebin-link">Pastebin</a>
      <div className="header-actions">
        {auth.logged_in
          ? <><a href="/profile" className="home-avatar-link"><UserAvatar username={auth.username} size={32} /></a><div className="panel-dropdown"><a className="login-button console-button" href="/dashboard">进入面板</a><div className="panel-dropdown-menu"><a href="/dashboard">进入面板</a><a href="/settings">系统设置</a><a href="/appearance">界面外观</a><button onClick={logout}>退出登录</button></div></div></>
          : <a className="login-button" href="/login?returnTo=%2Fdashboard">登录</a>}
      </div>
    </header>

    <ThemeToggle />

    <main className="home-main">
      <section className="hero">
        <p className="hero-copy">{auth.logged_in ? `欢迎回来，${auth.username}` : '欢迎来到 HomeHub，你的个人资源中枢。'}</p>
        <form className="hero-search" onSubmit={submitSearch}>
          <input className="hero-search-input" value={searchQuery} onChange={event => setSearchQuery(event.target.value)} placeholder="搜索书签或 RSS 条目…" />
          <button type="submit" className="hero-search-btn">搜索</button>
        </form>
      </section>

      {loading ? <section className="empty-panel">正在整理你的内容…</section> : hasContent ? <div className="content-grid">
        {visibleGroups.map(group => {
          const bookmarks = group.bookmarks ?? []
          const displayed = bookmarks.slice(0, MAX_BOOKMARKS_PER_GROUP)
          const hasMore = bookmarks.length > MAX_BOOKMARKS_PER_GROUP
          return <section className="content-card bookmark-section" key={`bookmark-${group.id}`}>
            {bookmarks.length ? <div className="bookmark-grid">{displayed.map(bookmark => <a className="bookmark-item" key={bookmark.id} href={bookmark.url} target="_blank" rel="noreferrer"><span className="bookmark-icon">{bookmark.icon ? <img src={bookmark.icon} alt=""/> : bookmark.title.slice(0, 1).toUpperCase()}</span><span><strong>{bookmark.title}</strong><small>{new URL(bookmark.url).hostname.replace('www.', '')}</small></span><i>↗</i></a>)}{hasMore && <a className="bookmark-item bookmark-more" href="/bookmarks"><span className="bookmark-icon">→</span><span><strong>查看全部</strong></span></a>}</div> : <p className="muted">暂无书签。</p>}
          </section>
        })}
        {visibleFeeds.map(feed => {
          const feedEntries = entries.filter(entry => entry.feed_id === feed.id).slice(0, MAX_ENTRIES_PER_FEED)
          return <section className="content-card rss-section" key={`feed-${feed.id}`}>
            <div className="section-heading"><div><h2>{feed.title || '未命名订阅'}</h2></div><div className="heading-links"><a href={feed.site_url || feed.url} target="_blank" rel="noreferrer">查看站点 ↗</a><span className="heading-sep">·</span><a href="/rss">查看更多 →</a></div></div>
            {feedEntries.length ? <div className="rss-list">{feedEntries.map(entry => <div className="rss-item-row" key={entry.id}><a href={entry.link} target="_blank" rel="noreferrer"><strong>{entry.title}</strong><span>{entry.author || '最新文章'} · {entry.published ? new Date(entry.published).toLocaleDateString('zh-CN') : '最近更新'}</span></a>{entry.magnet_url && <a className="rss-magnet-link" href={entry.magnet_url} title="磁力链接">🧲</a>}</div>)}</div> : <p className="muted">暂无已抓取的文章。</p>}
          </section>
        })}
      </div> : <section className="empty-panel"><span className="empty-icon">⌂</span><div><h2>还没有可展示的内容</h2><p>{auth.logged_in ? '点击右下角齿轮按钮自定义首页，或先添加书签和 RSS。' : '登录后可自定义首页内容，或前往控制台添加书签和订阅。'}</p></div>{!auth.logged_in && <a className="login-button" href="/login">现在登录</a>}</section>}
      {auth.logged_in && <button className="customize-float" onClick={() => setCustomizing(true)} aria-label="自定义首页" title="自定义首页">⚙</button>}
    </main>

    {customizing && <div className="drawer-backdrop" onMouseDown={() => setCustomizing(false)}><aside className="custom-drawer" onMouseDown={event => event.stopPropagation()} aria-label="自定义首页">
      <div className="drawer-heading"><div><span className="section-kicker">PERSONALIZE</span><h2>自定义首页</h2></div><button aria-label="关闭" onClick={() => setCustomizing(false)}>×</button></div>
      <p>选择要在首页出现的内容。未选择任何项时默认展示全部。</p>
      <h3>书签组</h3><div className="choice-list">{groups.length ? groups.map(group => <label key={group.id}><input type="checkbox" checked={preferences.bookmarks.includes(group.id)} onChange={() => toggle('bookmarks', group.id)}/><span>{group.name}</span><small>{group.bookmarks?.length || 0} 项</small></label>) : <span className="muted">暂无书签组</span>}</div>
      <h3>RSS 组</h3><div className="choice-list">{feeds.length ? feeds.map(feed => <label key={feed.id}><input type="checkbox" checked={preferences.feeds.includes(feed.id)} onChange={() => toggle('feeds', feed.id)}/><span>{feed.title}</span></label>) : <span className="muted">暂无 RSS 订阅</span>}</div>
      <button className="drawer-done" onClick={() => setCustomizing(false)}>完成</button>
    </aside></div>}
  </div>
}

