import { useEffect, useMemo, useState } from 'react'
import { ThemeToggle } from '@/components/ThemeToggle'
import { ApiRequestError } from '@/lib/api'
import { homeApi, type AuthStatus, type BackgroundSettings, type BookmarkGroup, type MediaAsset, type RSSEntry, type RSSFeed } from '@/lib/home'
import { getWebUIConfig } from '@/lib/webui'
import { mediaURL } from '@/lib/media'

type Preferences = { bookmarks: number[]; feeds: number[] }
const STORAGE_KEY = 'homehub.home.preferences.v1'
const fallbackBackground: BackgroundSettings = { mode: 'single', interval_seconds: 30, transition_effect: 'fade', fade_duration_ms: 1000, selected_asset_ids: [] }

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
  const [preferences, setPreferences] = useState<Preferences>(() => {
    try { return JSON.parse(localStorage.getItem(STORAGE_KEY) || '') as Preferences } catch { return { bookmarks: [], feeds: [] } }
  })

  useEffect(() => {
    Promise.allSettled([homeApi.auth(), getWebUIConfig()]).then(([authResult, webuiResult]) => {
      if (authResult.status === 'fulfilled') setAuth(authResult.value)
      if (webuiResult.status === 'fulfilled') {
        setBackground(webuiResult.value.home.background)
        setAssets(webuiResult.value.home.assets)
      }
    })
  }, [])

  useEffect(() => {
    if (!auth.logged_in) { setLoading(false); return }
    Promise.allSettled([homeApi.bookmarks(), homeApi.feeds(), homeApi.entries()]).then(([g, f, e]) => {
      if (g.status === 'fulfilled') setGroups(g.value || [])
      if (f.status === 'fulfilled') setFeeds(f.value || [])
      if (e.status === 'fulfilled') setEntries(e.value.list || [])
      setLoading(false)
    })
  }, [auth.logged_in])

  const selectedAssets = useMemo(() => background.selected_asset_ids.map(id => assets.find(asset => asset.id === id)).filter(Boolean) as MediaAsset[], [assets, background.selected_asset_ids])
  useEffect(() => {
    if (selectedAssets.length < 2 || background.mode === 'single') return
    const timer = window.setInterval(() => setBackgroundIndex(index => background.mode === 'random'
      ? (index + 1 + Math.floor(Math.random() * (selectedAssets.length - 1))) % selectedAssets.length
      : (index + 1) % selectedAssets.length), Math.max(background.interval_seconds, 1) * 1000)
    return () => window.clearInterval(timer)
  }, [selectedAssets.length, background.interval_seconds, background.mode])

  const activeAsset = selectedAssets[backgroundIndex % Math.max(selectedAssets.length, 1)]
  const visibleGroups = preferences.bookmarks.length ? groups.filter(group => preferences.bookmarks.includes(group.id)) : groups
  const visibleFeeds = preferences.feeds.length ? feeds.filter(feed => preferences.feeds.includes(feed.id)) : feeds

  function savePreferences(next: Preferences) { setPreferences(next); localStorage.setItem(STORAGE_KEY, JSON.stringify(next)) }
  function toggle(kind: keyof Preferences, id: number) {
    const current = preferences[kind]
    savePreferences({ ...preferences, [kind]: current.includes(id) ? current.filter(value => value !== id) : [...current, id] })
  }
  async function logout() { try { await homeApi.logout() } finally { window.location.reload() } }

  return <div className="home-shell">
    <div className="home-background" aria-hidden="true">
      {activeAsset?.media_type === 'video' ? <video key={activeAsset.id} src={mediaURL(activeAsset.file_path)} autoPlay muted loop playsInline /> : activeAsset ? <div key={activeAsset.id} className="background-image" style={{ backgroundImage: `url("${mediaURL(activeAsset.file_path)}")`, transitionDuration: `${background.fade_duration_ms}ms` }} /> : <div className="background-gradient" />}
      <div className="background-shade" />
    </div>

    <header className="home-header">
      <a className="brand" href="/"><span className="brand-mark">H</span><span>HomeHub</span></a>
      <div className="header-actions">
        <button className="glass-button" onClick={() => setCustomizing(true)}>自定义首页</button>
        {auth.logged_in ? <><a className="login-button console-button" href="/dashboard">进入控制台</a><span className="user-label">{auth.username || '已登录'}</span><button className="login-button" onClick={logout}>退出</button></> : <a className="login-button" href="/login?returnTo=%2Fdashboard">登录</a>}
      </div>
    </header>

    <ThemeToggle />

    <main className="home-main">
      <section className="hero">
        <p className="eyebrow">YOUR PERSONAL SPACE</p>
        <h1>{auth.logged_in ? `欢迎回来，${auth.username || '朋友'}` : '让常用内容，触手可及'}</h1>
        <p className="hero-copy">书签与订阅汇聚一处，保持专注，也保留探索的乐趣。</p>
      </section>

      {!auth.logged_in ? <section className="empty-panel"><span className="empty-icon">⌂</span><div><h2>登录后加载你的首页</h2><p>书签组、RSS 订阅和自定义背景会在这里自动汇集。</p></div><a className="login-button" href="/login">现在登录</a></section> : loading ? <section className="empty-panel">正在整理你的内容…</section> : <div className="content-grid">
        {visibleGroups.map(group => <section className="content-card bookmark-section" key={`bookmark-${group.id}`}>
          <div className="section-heading"><div><span className="section-kicker">BOOKMARKS</span><h2>{group.name}</h2></div><span>{group.bookmarks?.length || 0} 个链接</span></div>
          {group.bookmarks?.length ? <div className="bookmark-grid">{group.bookmarks.map(bookmark => <a className="bookmark-item" key={bookmark.id} href={bookmark.url} target="_blank" rel="noreferrer"><span className="bookmark-icon">{bookmark.icon ? <img src={bookmark.icon} alt=""/> : bookmark.title.slice(0, 1).toUpperCase()}</span><span><strong>{bookmark.title}</strong><small>{new URL(bookmark.url).hostname.replace('www.', '')}</small></span><i>↗</i></a>)}</div> : <p className="muted">这个分组还没有书签。</p>}
        </section>)}
        {visibleFeeds.map(feed => { const feedEntries = entries.filter(entry => entry.feed_id === feed.id).slice(0, 5); return <section className="content-card rss-section" key={`feed-${feed.id}`}>
          <div className="section-heading"><div><span className="section-kicker">RSS FEED</span><h2>{feed.title || '未命名订阅'}</h2></div><a href={feed.site_url || feed.url} target="_blank" rel="noreferrer">查看站点 ↗</a></div>
          {feedEntries.length ? <div className="rss-list">{feedEntries.map(entry => <a href={entry.link} target="_blank" rel="noreferrer" key={entry.id}><strong>{entry.title}</strong><span>{entry.author || '最新文章'} · {entry.published ? new Date(entry.published).toLocaleDateString('zh-CN') : '最近更新'}</span></a>)}</div> : <p className="muted">暂无已抓取的文章。</p>}
        </section>})}
        {!visibleGroups.length && !visibleFeeds.length && <section className="empty-panel">还没有可展示的内容组。点击“自定义首页”检查选择，或先添加书签和 RSS。</section>}
      </div>}
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

export function getApiErrorMessage(error: unknown) { return error instanceof ApiRequestError ? error.message : '暂时无法连接服务' }
