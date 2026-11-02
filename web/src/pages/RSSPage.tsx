import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { ConfirmDialog, Modal, Notice, PageState, Pagination } from '@/components/WorkspaceUI'
import { rssApi, type FetchBy, type RSSEntry, type RSSFeed, type RSSGroup } from '@/lib/rss'
import { extensionHubApi, type RemoteSubscription } from '@/lib/extensionHub'
import { isUsableTimestamp, relativeTime } from '@/lib/format'

const perPage = 20
const SIDEBAR_KEY = 'homehub-rss-sidebar-collapsed'

// ============================================================
// RSS 阅读 — 双栏：左侧订阅源导航，右侧条目流
// 订阅源与分组的增删改收进模态框，列表只负责浏览与筛选。
// ============================================================

interface DraftFeed {
  id?: number
  url: string
  title: string
  remark: string
  fetch_interval: number
  fetch_by: FetchBy
  /** 是否参与后台调度。编辑时可以在这里暂停。 */
  fetch_enabled: boolean
  group_id?: number
  /** 保存后是否立即抓一次。只在新建时有意义。 */
  fetch_now: boolean
}

const emptyFeedDraft: DraftFeed = {
  url: '',
  title: '',
  remark: '',
  fetch_interval: 3600,
  fetch_by: 'local',
  fetch_enabled: true,
  fetch_now: false,
}

export function RSSPage() {
  const [feeds, setFeeds] = useState<RSSFeed[]>([])
  const [groups, setGroups] = useState<RSSGroup[]>([])
  const [entries, setEntries] = useState<RSSEntry[]>([])
  const [total, setTotal] = useState(0)
  const [selectedFeed, setSelectedFeed] = useState<number | null>(null)
  const [selectedGroup, setSelectedGroup] = useState<number | null>(null)
  const [page, setPage] = useState(1)
  const [collapsedGroups, setCollapsedGroups] = useState<Set<number>>(() => new Set())
  // 侧栏收起状态记在本机：这是一次性的阅读偏好，不该占服务端配置。
  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem(SIDEBAR_KEY) === '1'
    } catch {
      return false
    }
  })
  const [loading, setLoading] = useState(true)
  const [entriesLoading, setEntriesLoading] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  // 远端订阅按 URL 索引，用于在调度面板里显示「对面到底抓到了没有」。
  const [remoteByURL, setRemoteByURL] = useState<Record<string, RemoteSubscription>>({})

  const [feedDraft, setFeedDraft] = useState<DraftFeed | null>(null)
  const [showGroups, setShowGroups] = useState(false)
  const [newGroup, setNewGroup] = useState('')
  const [editGroup, setEditGroup] = useState<RSSGroup | null>(null)
  const [pendingDeleteFeed, setPendingDeleteFeed] = useState<RSSFeed | null>(null)
  const [pendingDeleteGroup, setPendingDeleteGroup] = useState<RSSGroup | null>(null)

  function toggleSidebar() {
    setSidebarCollapsed(current => {
      const next = !current
      try {
        localStorage.setItem(SIDEBAR_KEY, next ? '1' : '0')
      } catch {
        // 隐私模式下 localStorage 会抛错；折叠本身仍然生效。
      }
      return next
    })
  }

  // ---------- 数据加载 ----------
  async function loadFeeds() {
    setLoading(true)
    try {
      const [feedList, groupList] = await Promise.all([
        rssApi.listFeeds(),
        rssApi.listGroups(),
      ])
      setFeeds(feedList ?? [])
      setGroups(groupList ?? [])
      setError('')
      // 远端状态是侧栏状态点与计数的依据，单独拉、失败不阻塞列表。
      void loadRemote()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法读取订阅源')
    } finally {
      setLoading(false)
    }
  }

  /** loadRemote 取远端订阅状态。签名通道没启用时静默留空。 */
  async function loadRemote() {
    try {
      const list = await extensionHubApi.listSubscriptions()
      const map: Record<string, RemoteSubscription> = {}
      for (const item of list) map[item.url] = item
      setRemoteByURL(map)
    } catch {
      // 没启用或对端不可达：远端信息留空，不影响本机调度面板。
      setRemoteByURL({})
    }
  }

  /** pushToExtension 把这个源推给 Extension 抓取。 */
  async function pushToExtension(feedID: number, label: string) {
    setBusy(true)
    try {
      const result = await extensionHubApi.push(feedID, { fetchNow: true })
      setMessage(result.created
        ? `已推送「${label}」到 Extension，对端开始抓取`
        : `已更新 Extension 上的「${label}」`)
      setError('')
    } catch (cause) {
      const text = cause instanceof Error ? cause.message : '推送失败'
      // 最常见的原因是没启用签名通道；直接说出来，别让人去查网络。
      setError(text.includes('未启用') || text.includes('70001')
        ? '还没有启用 Extension 签名通道：到「设置 → Extension 对接」填好地址与私钥后再推送'
        : text)
    } finally {
      setBusy(false)
    }
  }

  async function loadEntries(feed = selectedFeed, group = selectedGroup, targetPage = page) {
    setEntriesLoading(true)
    try {
      const result = await rssApi.listEntries(feed, group, targetPage, perPage)
      setEntries(result.list ?? [])
      setTotal(result.total ?? 0)
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法读取订阅条目')
    } finally {
      setEntriesLoading(false)
    }
  }

  useEffect(() => { void loadFeeds() }, [])
  useEffect(() => { void loadEntries(selectedFeed, selectedGroup, page) }, [selectedFeed, selectedGroup, page])

  async function run(action: () => Promise<unknown>, success: string, reloadEntries = false) {
    setBusy(true)
    try {
      await action()
      setMessage(success)
      await loadFeeds()
      if (reloadEntries) await loadEntries()
      return true
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '操作失败')
      return false
    } finally {
      setBusy(false)
    }
  }

  // ---------- 订阅源 ----------
  async function saveFeed(event: FormEvent) {
    event.preventDefault()
    if (!feedDraft) return
    const saved = feedDraft.id
      ? await run(
        () => rssApi.updateFeed(feedDraft.id!, {
          // 显式传空串表示清空：省略会被后端理解成「别动」。
          title: feedDraft.title.trim(),
          remark: feedDraft.remark.trim(),
          fetch_interval: feedDraft.fetch_interval,
          fetch_by: feedDraft.fetch_by,
          fetch_enabled: feedDraft.fetch_enabled,
          // 未分组要显式发 null：省略字段会让后端理解为“保持原分组”。
          group_id: feedDraft.group_id ?? null,
        }),
        '订阅源已更新',
      )
      : await run(
        () => rssApi.createFeed({
          url: feedDraft.url.trim(),
          ...(feedDraft.title.trim() ? { title: feedDraft.title.trim() } : {}),
          ...(feedDraft.remark.trim() ? { remark: feedDraft.remark.trim() } : {}),
          fetch_interval: feedDraft.fetch_interval,
          fetch_by: feedDraft.fetch_by,
          fetch_enabled: feedDraft.fetch_enabled,
          ...(feedDraft.group_id ? { group_id: feedDraft.group_id } : {}),
          // 保存就是保存：只有勾了这一项才抓。
          fetch_now: feedDraft.fetch_now,
        }),
        feedDraft.fetch_now ? '订阅源已添加，正在后台抓取' : '订阅源已添加',
      )
    if (saved) { setFeedDraft(null); await loadEntries(); void loadRemote() }
  }

  async function removeFeed() {
    if (!pendingDeleteFeed) return
    const id = pendingDeleteFeed.id
    const saved = await run(() => rssApi.deleteFeed(id), '订阅源及其条目已删除', true)
    if (saved) {
      if (selectedFeed === id) { setSelectedFeed(null); setPage(1) }
      setPendingDeleteFeed(null)
    }
  }

  // ---------- 分组 ----------
  async function addGroup(event: FormEvent) {
    event.preventDefault()
    const name = newGroup.trim()
    if (!name) return
    const saved = await run(() => rssApi.createGroup(name), '分组已创建')
    if (saved) setNewGroup('')
  }

  async function saveGroup(event: FormEvent) {
    event.preventDefault()
    if (!editGroup) return
    const saved = await run(() => rssApi.updateGroup(editGroup.id, { name: editGroup.name.trim() }), '分组已更新')
    if (saved) setEditGroup(null)
  }

  async function removeGroup() {
    if (!pendingDeleteGroup) return
    const id = pendingDeleteGroup.id
    const saved = await run(() => rssApi.deleteGroup(id), '分组已删除，其下订阅源已移回未分组', true)
    if (saved) {
      if (selectedGroup === id) { setSelectedGroup(null); setPage(1) }
      setPendingDeleteGroup(null)
    }
  }

  // ---------- 选中 ----------
  function selectAll() { setSelectedFeed(null); setSelectedGroup(null); setPage(1) }
  function selectFeed(id: number) { setSelectedFeed(id); setSelectedGroup(null); setPage(1) }
  function selectGroup(id: number) { setSelectedGroup(id); setSelectedFeed(null); setPage(1) }
  function toggleGroup(id: number) {
    setCollapsedGroups(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  // ---------- 派生 ----------
  const grouped = useMemo(() => {
    const byGroup = new Map<number, RSSFeed[]>()
    const loose: RSSFeed[] = []
    for (const feed of feeds) {
      if (feed.group_id) {
        const list = byGroup.get(feed.group_id) ?? []
        list.push(feed)
        byGroup.set(feed.group_id, list)
      } else {
        loose.push(feed)
      }
    }
    return { byGroup, loose }
  }, [feeds])

  const activeFeed = selectedFeed ? feeds.find(feed => feed.id === selectedFeed) ?? null : null
  const activeGroup = selectedGroup ? groups.find(group => group.id === selectedGroup) ?? null : null
  const heading = activeGroup ? activeGroup.name : activeFeed ? (activeFeed.title || activeFeed.url) : '全部条目'
  const errorFeeds = feeds.filter(feed => feed.error_msg)
  const remoteCount = feeds.filter(feed => feed.fetch_by === 'extension').length
  const pausedCount = feeds.filter(feed => feed.fetch_enabled === false).length

  function feedTitles(feedIDs?: number[]): string {
    if (!feedIDs?.length) return ''
    return feedIDs.map(id => feeds.find(feed => feed.id === id)?.title || `#${id}`).join('、')
  }

  if (loading) return <PageState kind="loading" title="正在读取订阅源" />
  if (error && !feeds.length) {
    return <PageState
      kind="error"
      title="无法读取订阅源"
      description={error}
      action={<button className="button button--primary" onClick={() => void loadFeeds()}>重试</button>}
    />
  }

  return <div className="business-page rss-page">
    <Notice message={error} tone="error" onDismiss={() => setError('')} />
    <Notice message={message} onDismiss={() => setMessage('')} />

    {!feeds.length
      ? <PageState
        kind="empty"
        title="还没有订阅源"
        description="添加 RSS 或 Atom 链接后，抓取到的文章会出现在这里。"
        action={<button className="button button--primary" onClick={() => setFeedDraft({ ...emptyFeedDraft })}>添加第一个订阅源</button>}
      />
      : <>
        {/* 展开把手：贴着左侧导航栏与内容区的交界，垂直居中。
            放在 rss-layout 之外是因为它要相对视口定位——放在里面会被
            内容区的 padding 挡住，或者像之前那样压住「全部条目」标题。 */}
        {sidebarCollapsed && <button
          type="button"
          className="rss-sidebar-reopen"
          onClick={toggleSidebar}
          title="展开订阅源栏"
        >
          订阅源
        </button>}
        <div className={`rss-layout${sidebarCollapsed ? ' is-sidebar-collapsed' : ''}`}>
        {/* ---------- 左栏 ---------- */}
        <aside className="rss-sidebar" aria-label="订阅源导航">
          <div className="rss-sidebar-head">
            <strong>订阅源</strong>
            <div className="rss-sidebar-head-actions">
              <button
                type="button"
                className="rss-sidebar-icon"
                onClick={() => setShowGroups(true)}
                aria-label="管理分组"
                title="管理分组"
              >
                <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h10" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>
              </button>
              <button
                type="button"
                className="rss-sidebar-icon"
                onClick={toggleSidebar}
                aria-label="收起订阅源栏"
                title="收起，让条目占满宽度"
              >
                <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m14 6-6 6 6 6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" /></svg>
              </button>
            </div>
          </div>
          <nav className="rss-nav">
            {/* 「全部条目」是导航的第一个入口，不是头部的一个按钮。 */}
            <button
              type="button"
              className={`rss-nav-item rss-nav-all${!selectedFeed && !selectedGroup ? ' is-active' : ''}`}
              onClick={selectAll}
            >
              <span className="rss-nav-label">全部条目</span>
              <span className="rss-nav-count">{total.toLocaleString()}</span>
            </button>
            {groups.map(group => {
              const groupFeeds = grouped.byGroup.get(group.id) ?? []
              const expanded = !collapsedGroups.has(group.id)
              return <div key={group.id}>
                <button
                  type="button"
                  className="rss-group-head"
                  aria-expanded={expanded}
                  onClick={() => toggleGroup(group.id)}
                >
                  <span className="rss-group-arrow" aria-hidden="true">▾</span>
                  <span className="rss-nav-label">{group.name}</span>
                  <span className="rss-nav-count">{groupFeeds.length}</span>
                </button>
                {expanded && <div className="rss-group-feeds">
                  {groupFeeds.length === 0
                    ? <span className="rss-nav-item" style={{ color: 'var(--color-text-secondary)', fontSize: 'var(--text-xs)' }}>暂无订阅源</span>
                    : groupFeeds.map(feed => <FeedNavItem
                      key={feed.id}
                      feed={feed}
                      active={selectedFeed === feed.id}
                      remote={remoteByURL[feed.url]}
                      onSelect={() => selectFeed(feed.id)}
                    />)}
                  <button
                    type="button"
                    className="rss-nav-item"
                    onClick={() => selectGroup(group.id)}
                    style={{ color: 'var(--color-link)', fontSize: 'var(--text-xs)' }}
                  >
                    <span className="rss-nav-label">查看该分组全部条目</span>
                  </button>
                </div>}
              </div>
            })}

            {grouped.loose.length > 0 && <div>
              <span className="rss-group-head" style={{ cursor: 'default' }}>
                <span className="rss-group-arrow" aria-hidden="true">▾</span>
                <span className="rss-nav-label">未分组</span>
                <span className="rss-nav-count">{grouped.loose.length}</span>
              </span>
              <div className="rss-group-feeds">
                {grouped.loose.map(feed => <FeedNavItem
                  key={feed.id}
                  feed={feed}
                  active={selectedFeed === feed.id}
                  remote={remoteByURL[feed.url]}
                  onSelect={() => selectFeed(feed.id)}
                />)}
              </div>
            </div>}
          </nav>
          <div className="rss-sidebar-foot">
            <button
              type="button"
              className="rss-sidebar-add"
              onClick={() => setFeedDraft({ ...emptyFeedDraft })}
            >
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>
              添加订阅源
            </button>
            <p className="rss-sidebar-summary">
              {feeds.length} 个源 · {groups.length} 个分组
              {remoteCount > 0 && ` · ${remoteCount} 远端`}
              {pausedCount > 0 && ` · ${pausedCount} 暂停`}
              {errorFeeds.length > 0 && <span className="is-error"> · {errorFeeds.length} 异常</span>}
            </p>
          </div>
        </aside>

        {/* ---------- 右栏 ---------- */}
        <section className="rss-main" aria-label="订阅条目">
          <header className="rss-main-head">
            <div>
              <h2>{heading}</h2>
              <p>{total.toLocaleString()} 条内容{activeFeed?.last_fetch ? ` · 最近抓取 ${relativeTime(activeFeed.last_fetch)}` : ''}</p>
            </div>
            <div className="rss-main-actions">
              {!activeFeed && <button
                className="button button--quiet"
                disabled={busy || !feeds.length}
                onClick={() => void run(() => rssApi.fetchAll(), '已触发全部订阅源抓取', true)}
              >
                全部抓取
              </button>}
              {activeFeed && <>
                <button className="button button--quiet" disabled={busy} onClick={() => void run(() => rssApi.fetchFeed(activeFeed.id), `已抓取「${activeFeed.title || activeFeed.url}」`, true)}>抓取</button>
                <button className="button button--quiet" onClick={() => setFeedDraft({ id: activeFeed.id, url: activeFeed.url, title: activeFeed.title, remark: activeFeed.remark ?? '', fetch_interval: activeFeed.fetch_interval, fetch_by: activeFeed.fetch_by ?? 'local', fetch_enabled: activeFeed.fetch_enabled ?? true, group_id: activeFeed.group_id, fetch_now: false })}>编辑</button>
                <button className="icon-button" aria-label="删除该订阅源" onClick={() => setPendingDeleteFeed(activeFeed)}>×</button>
              </>}
            </div>
          </header>

          {activeFeed?.error_msg && <p className="rss-feed-error">最近一次抓取失败：{activeFeed.error_msg}</p>}

          {entriesLoading
            ? <PageState kind="loading" title="正在读取条目" />
            : !entries.length
              ? <PageState
                kind="empty"
                title="没有可显示的条目"
                description={activeFeed ? '这个订阅源还没有抓到文章，试试手动抓取。' : '切换订阅源，或调整筛选范围。'}
              />
              : <div className="rss-entries">
                {entries.map(entry => <article className="rss-entry" key={entry.id}>
                  <div className="rss-entry-body">
                    <a className="rss-entry-title" href={entry.link} target="_blank" rel="noreferrer">{entry.title}</a>
                    <div className="rss-entry-meta">
                      {entry.author && <><span>{entry.author}</span><span className="meta-dot" aria-hidden="true">·</span></>}
                      <time dateTime={isUsableTimestamp(entry.published) ? entry.published : undefined}>{relativeTime(entry.published) || '时间未知'}</time>
                      {!selectedFeed && entry.feed_ids?.length > 0 && <>
                        <span className="meta-dot" aria-hidden="true">·</span>
                        <span className="rss-entry-source">{feedTitles(entry.feed_ids)}</span>
                      </>}
                    </div>
                    {entry.content && <details className="rss-entry-detail">
                      <summary>展开正文</summary>
                      <div className="rss-entry-content" dangerouslySetInnerHTML={{ __html: entry.content }} />
                    </details>}
                  </div>
                  {entry.magnet_url && <a className="rss-magnet" href={entry.magnet_url} title="磁力链接" aria-label={`磁力链接：${entry.title}`}>🧲</a>}
                </article>)}
              </div>}
          <Pagination page={page} perPage={perPage} total={total} disabled={entriesLoading} onChange={setPage} />
        </section>
        </div>
      </>}


    {/* ---------- 订阅源弹窗：基本信息 + 抓取设置 ---------- */}
    <Modal
      open={feedDraft !== null}
      title={feedDraft?.id ? '订阅源设置' : '添加订阅源'}
      description={feedDraft?.id ? undefined : '只保存，不抓取。保存后再点「抓取」拉内容。'}
      size="large"
      onClose={() => !busy && setFeedDraft(null)}
      footer={<>
        <button className="button button--quiet" type="button" onClick={() => setFeedDraft(null)} disabled={busy}>取消</button>
        <button className="button button--primary" type="submit" form="rss-feed-form" disabled={busy}>{busy ? '保存中...' : '保存'}</button>
      </>}
    >
      <form id="rss-feed-form" className="rss-feed-form" onSubmit={saveFeed}>
        {!feedDraft?.id && <label className="form-field form-field--wide">订阅源 URL
          <input autoFocus type="url" value={feedDraft?.url ?? ''} onChange={event => setFeedDraft(draft => draft ? { ...draft, url: event.target.value } : draft)} placeholder="https://example.com/feed.xml" required />
        </label>}

        <fieldset className="rss-form-group">
          <legend>基本信息</legend>
          <label className="form-field">展示标题
            <input
              autoFocus={Boolean(feedDraft?.id)}
              value={feedDraft?.title ?? ''}
              onChange={event => setFeedDraft(draft => draft ? { ...draft, title: event.target.value } : draft)}
              placeholder="留空则抓取后自动补全"
              maxLength={255}
            />
          </label>
          <label className="form-field">分组
            <select value={feedDraft?.group_id ?? ''} onChange={event => setFeedDraft(draft => draft ? { ...draft, group_id: event.target.value ? Number(event.target.value) : undefined } : draft)}>
              <option value="">未分组</option>
              {groups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}
            </select>
          </label>
          <label className="form-field form-field--wide">备注
            <input
              value={feedDraft?.remark ?? ''}
              onChange={event => setFeedDraft(draft => draft ? { ...draft, remark: event.target.value } : draft)}
              placeholder="给自己看的说明，例如「只在周末更新」"
              maxLength={500}
            />
          </label>
        </fieldset>

        <fieldset className="rss-form-group">
          <legend>抓取</legend>
          <label className="form-field">执行者
            <select
              value={feedDraft?.fetch_by ?? 'local'}
              onChange={event => setFeedDraft(draft => draft ? { ...draft, fetch_by: event.target.value as FetchBy } : draft)}
            >
              <option value="local">本机抓取</option>
              <option value="extension">交给 Extension</option>
            </select>
          </label>
          <label className="form-field">间隔（秒）
            <input type="number" min="60" value={feedDraft?.fetch_interval ?? 3600} onChange={event => setFeedDraft(draft => draft ? { ...draft, fetch_interval: Number(event.target.value) } : draft)} required />
          </label>
          <label className="choice-tile form-field--wide">
            <input
              type="checkbox"
              checked={feedDraft?.fetch_enabled ?? true}
              onChange={event => setFeedDraft(draft => draft ? { ...draft, fetch_enabled: event.target.checked } : draft)}
            />
            <span>参与后台调度（取消勾选＝暂停抓取，但不删这个源）</span>
          </label>

          <p className="rss-form-hint form-field--wide">
            {feedDraft?.fetch_by === 'extension'
              ? '条目存在 Extension 一侧，本机只保留这个源的登记。阅读需在 Extension 界面进行；保存后需要点「推送」对端才会开始抓。'
              : '本机按间隔抓取，条目存在 HomeHub。'}
          </p>

          {/* 已有订阅源时，抓取动作直接在这里触发，不必回到列表。 */}
          {feedDraft?.id !== undefined && <div className="rss-form-actions form-field--wide">
            {feedDraft?.fetch_by === 'extension'
              ? <button
                type="button"
                className="button button--quiet"
                disabled={busy}
                onClick={() => feedDraft?.id && void pushToExtension(feedDraft.id, feedDraft.title || feedDraft.url)}
              >
                推送到 Extension
              </button>
              : <button
                type="button"
                className="button button--quiet"
                disabled={busy || feedDraft?.fetch_enabled === false}
                onClick={() => feedDraft?.id && void run(() => rssApi.fetchFeed(feedDraft.id!), '已触发抓取', true)}
              >
                立即抓取
              </button>}
          </div>}
        </fieldset>

        {!feedDraft?.id && <label className="choice-tile form-field--wide">
          <input
            type="checkbox"
            checked={feedDraft?.fetch_now ?? false}
            onChange={event => setFeedDraft(draft => draft ? { ...draft, fetch_now: event.target.checked } : draft)}
          />
          <span>保存后立即抓取一次（默认不抓）</span>
        </label>}
      </form>
    </Modal>

    {/* ---------- 分组管理弹窗 ---------- */}
    <Modal
      open={showGroups}
      title="管理分组"
      description="分组用于整理订阅源；删除分组时其下订阅源会移回未分组。"
      onClose={() => !busy && setShowGroups(false)}
      footer={<button className="button button--quiet" type="button" onClick={() => setShowGroups(false)} disabled={busy}>关闭</button>}
    >
      <form className="form-grid" onSubmit={addGroup} style={{ marginBottom: 'var(--space-5)' }}>
        <label className="form-field" style={{ gridColumn: '1 / -1' }}>新建分组
          <div style={{ display: 'flex', gap: 'var(--space-2)' }}>
            <input value={newGroup} onChange={event => setNewGroup(event.target.value)} placeholder="分组名称" maxLength={100} />
            <button className="button button--primary" disabled={busy || !newGroup.trim()}>创建</button>
          </div>
        </label>
      </form>
      {groups.length === 0
        ? <p className="confirm-copy">还没有分组。创建后可以在添加订阅源时选择。</p>
        : <div className="rss-group-list">
          {groups.map(group => <div className="rss-group-row" key={group.id}>
            <strong>{group.name}</strong>
            <small>{feeds.filter(feed => feed.group_id === group.id).length} 个源</small>
            <button className="icon-button" aria-label={`重命名分组 ${group.name}`} onClick={() => setEditGroup({ ...group })}>✎</button>
            <button className="icon-button" aria-label={`删除分组 ${group.name}`} onClick={() => setPendingDeleteGroup(group)}>×</button>
          </div>)}
        </div>}
    </Modal>

    {/* ---------- 重命名分组 ---------- */}
    <Modal
      open={editGroup !== null}
      title="重命名分组"
      onClose={() => !busy && setEditGroup(null)}
      footer={<>
        <button className="button button--quiet" type="button" onClick={() => setEditGroup(null)} disabled={busy}>取消</button>
        <button className="button button--primary" type="submit" form="rss-group-form" disabled={busy}>{busy ? '保存中...' : '保存'}</button>
      </>}
    >
      <form id="rss-group-form" className="form-grid" onSubmit={saveGroup}>
        <label className="form-field form-field--wide">分组名称<input autoFocus value={editGroup?.name ?? ''} onChange={event => setEditGroup(group => group ? { ...group, name: event.target.value } : group)} required maxLength={100} /></label>
      </form>
    </Modal>

    <ConfirmDialog
      open={pendingDeleteFeed !== null}
      title="删除订阅源"
      description={`将删除「${pendingDeleteFeed?.title || pendingDeleteFeed?.url || ''}」以及它收录的条目关联。其他源仍收录的内容会保留。`}
      confirmLabel="删除"
      danger
      busy={busy}
      onClose={() => setPendingDeleteFeed(null)}
      onConfirm={() => void removeFeed()}
    />
    <ConfirmDialog
      open={pendingDeleteGroup !== null}
      title="删除分组"
      description={`将删除分组「${pendingDeleteGroup?.name ?? ''}」，其下订阅源会移回未分组。`}
      confirmLabel="删除"
      danger
      busy={busy}
      onClose={() => setPendingDeleteGroup(null)}
      onConfirm={() => void removeGroup()}
    />
  </div>
}

/** 侧栏中的单个订阅源：状态灯 + 标题 + 最近抓取。 */
/** FeedNavItem 是左栏的一个订阅源条目。
 *
 * 只负责浏览与筛选：改抓取方式、暂停、推送都在「调度」面板里做。
 * 侧栏是导航，不是控制台——把操作塞进这里会挤掉标题，而且每行
 * 五个可点区域，误触比操作本身更常发生。
 */
function FeedNavItem({ feed, active, remote, onSelect }: {
  feed: RSSFeed
  active: boolean
  remote?: RemoteSubscription
  onSelect: () => void
}) {
  const title = feed.title || feed.url
  const isRemote = feed.fetch_by === 'extension'
  const paused = feed.fetch_enabled === false

  // 状态点在列表中承担「一眼看出好坏」，具体原因放 title 里。
  // 远端源以对面的抓取结果为准：本机只有一个标记，不能拿它判健康。
  const failed = isRemote ? (remote?.error_count ?? 0) > 0 || !!feed.error_msg : !!feed.error_msg
  const dot = paused ? 'is-warn' : failed ? 'is-error' : isRemote && !remote ? 'is-warn' : 'is-ok'

  const meta = isRemote
    ? (remote ? (remote.error_count > 0 ? `对端失败 ${remote.error_count} 次` : `对端 ${remote.unread_count} 条`) : '待推送')
    : paused
      ? '已暂停'
      : feed.last_fetch
        ? relativeTime(feed.last_fetch)
        : '未抓取'

  const hint = [
    feed.remark,
    feed.error_msg ? `抓取失败：${feed.error_msg}` : '',
    isRemote ? (remote?.last_error ? `对端：${remote.last_error}` : '由 Extension 抓取') : '',
    paused ? '不参与后台调度' : '',
  ].filter(Boolean).join(' · ')

  return <button
    type="button"
    className={`rss-nav-item${active ? ' is-active' : ''}`}
    onClick={onSelect}
    title={hint || title}
  >
    <span className={`status-dot ${dot}`} aria-hidden="true" />
    <span className="rss-nav-body">
      <span className="rss-nav-label">{title}</span>
      {feed.remark && <span className="rss-nav-remark">{feed.remark}</span>}
    </span>
    <span className="rss-nav-count">{meta}</span>
  </button>
}
