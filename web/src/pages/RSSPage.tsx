import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { ConfirmDialog, Modal, Notice, PageState, Pagination } from '@/components/WorkspaceUI'
import { rssApi, type RSSEntry, type RSSFeed, type RSSGroup } from '@/lib/rss'
import { isUsableTimestamp, relativeTime } from '@/lib/format'

const perPage = 20

// ============================================================
// RSS 阅读 — 双栏：左侧订阅源导航，右侧条目流
// 订阅源与分组的增删改收进模态框，列表只负责浏览与筛选。
// ============================================================

interface DraftFeed {
  id?: number
  url: string
  title: string
  fetch_interval: number
  group_id?: number
}

const emptyFeedDraft: DraftFeed = { url: '', title: '', fetch_interval: 3600 }

export function RSSPage() {
  const [feeds, setFeeds] = useState<RSSFeed[]>([])
  const [groups, setGroups] = useState<RSSGroup[]>([])
  const [entries, setEntries] = useState<RSSEntry[]>([])
  const [total, setTotal] = useState(0)
  const [selectedFeed, setSelectedFeed] = useState<number | null>(null)
  const [selectedGroup, setSelectedGroup] = useState<number | null>(null)
  const [page, setPage] = useState(1)
  const [collapsedGroups, setCollapsedGroups] = useState<Set<number>>(() => new Set())
  const [loading, setLoading] = useState(true)
  const [entriesLoading, setEntriesLoading] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)

  const [feedDraft, setFeedDraft] = useState<DraftFeed | null>(null)
  const [showGroups, setShowGroups] = useState(false)
  const [newGroup, setNewGroup] = useState('')
  const [editGroup, setEditGroup] = useState<RSSGroup | null>(null)
  const [pendingDeleteFeed, setPendingDeleteFeed] = useState<RSSFeed | null>(null)
  const [pendingDeleteGroup, setPendingDeleteGroup] = useState<RSSGroup | null>(null)

  // ---------- 数据加载 ----------
  async function loadFeeds() {
    setLoading(true)
    try {
      const [feedList, groupList] = await Promise.all([rssApi.listFeeds(), rssApi.listGroups()])
      setFeeds(feedList ?? [])
      setGroups(groupList ?? [])
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法读取订阅源')
    } finally {
      setLoading(false)
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
          title: feedDraft.title.trim() || undefined,
          fetch_interval: feedDraft.fetch_interval,
          group_id: feedDraft.group_id,
        }),
        '订阅源已更新',
      )
      : await run(() => rssApi.createFeed(feedDraft.url.trim(), feedDraft.group_id), '订阅源已添加')
    if (saved) { setFeedDraft(null); await loadEntries() }
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
    <div className="page-toolbar">
      <div className="toolbar-actions">
        <button className="button button--quiet" disabled={busy || !feeds.length} onClick={() => void run(() => rssApi.fetchAll(), '已触发全部订阅源抓取', true)}>
          抓取全部
        </button>
        <button className="button button--primary" onClick={() => setFeedDraft({ ...emptyFeedDraft })}>添加订阅源</button>
        <button className="button button--quiet" onClick={() => setShowGroups(true)}>管理分组</button>
      </div>
      <span className="toolbar-summary">
        {feeds.length} 个订阅源 · {groups.length} 个分组
        {errorFeeds.length > 0 && <strong className="is-error"> · {errorFeeds.length} 个异常</strong>}
      </span>
    </div>

    <Notice message={error} tone="error" onDismiss={() => setError('')} />
    <Notice message={message} onDismiss={() => setMessage('')} />

    {!feeds.length
      ? <PageState
        kind="empty"
        title="还没有订阅源"
        description="添加 RSS 或 Atom 链接后，抓取到的文章会出现在这里。"
        action={<button className="button button--primary" onClick={() => setFeedDraft({ ...emptyFeedDraft })}>添加第一个订阅源</button>}
      />
      : <div className="rss-layout">
        {/* ---------- 左栏 ---------- */}
        <aside className="rss-sidebar" aria-label="订阅源导航">
          <div className="rss-sidebar-head">
            <strong>订阅源</strong>
            <button type="button" onClick={selectAll}>全部条目</button>
          </div>
          <nav className="rss-nav">
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
                    : groupFeeds.map(feed => <FeedNavItem key={feed.id} feed={feed} active={selectedFeed === feed.id} onSelect={() => selectFeed(feed.id)} />)}
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
                {grouped.loose.map(feed => <FeedNavItem key={feed.id} feed={feed} active={selectedFeed === feed.id} onSelect={() => selectFeed(feed.id)} />)}
              </div>
            </div>}
          </nav>
        </aside>

        {/* ---------- 右栏 ---------- */}
        <section className="rss-main" aria-label="订阅条目">
          <header className="rss-main-head">
            <div>
              <h2>{heading}</h2>
              <p>{total.toLocaleString()} 条内容{activeFeed?.last_fetch ? ` · 最近抓取 ${relativeTime(activeFeed.last_fetch)}` : ''}</p>
            </div>
            <div className="rss-main-actions">
              {activeFeed && <>
                <button className="button button--quiet" disabled={busy} onClick={() => void run(() => rssApi.fetchFeed(activeFeed.id), `已抓取「${activeFeed.title || activeFeed.url}」`, true)}>抓取</button>
                <button className="button button--quiet" onClick={() => setFeedDraft({ id: activeFeed.id, url: activeFeed.url, title: activeFeed.title, fetch_interval: activeFeed.fetch_interval, group_id: activeFeed.group_id })}>编辑</button>
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
      </div>}

    {/* ---------- 订阅源弹窗 ---------- */}
    <Modal
      open={feedDraft !== null}
      title={feedDraft?.id ? '编辑订阅源' : '添加订阅源'}
      description={feedDraft?.id ? '可修改展示标题、抓取间隔与所属分组；源 URL 不支持直接变更。' : '粘贴 RSS 或 Atom 链接，添加后会自动抓取一次。'}
      onClose={() => !busy && setFeedDraft(null)}
      footer={<>
        <button className="button button--quiet" type="button" onClick={() => setFeedDraft(null)} disabled={busy}>取消</button>
        <button className="button button--primary" type="submit" form="rss-feed-form" disabled={busy}>{busy ? '保存中...' : '保存'}</button>
      </>}
    >
      <form id="rss-feed-form" className="form-grid" onSubmit={saveFeed}>
        {feedDraft?.id
          ? <label className="form-field form-field--wide">标题<input autoFocus value={feedDraft.title} onChange={event => setFeedDraft(draft => draft ? { ...draft, title: event.target.value } : draft)} maxLength={255} /></label>
          : <label className="form-field form-field--wide">订阅源 URL<input autoFocus type="url" value={feedDraft?.url ?? ''} onChange={event => setFeedDraft(draft => draft ? { ...draft, url: event.target.value } : draft)} placeholder="https://example.com/feed.xml" required /></label>}
        <label className="form-field">抓取间隔（秒）<input type="number" min="60" value={feedDraft?.fetch_interval ?? 3600} onChange={event => setFeedDraft(draft => draft ? { ...draft, fetch_interval: Number(event.target.value) } : draft)} required /></label>
        <label className="form-field">分组<select value={feedDraft?.group_id ?? ''} onChange={event => setFeedDraft(draft => draft ? { ...draft, group_id: event.target.value ? Number(event.target.value) : undefined } : draft)}><option value="">未分组</option>{groups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label>
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
function FeedNavItem({ feed, active, onSelect }: { feed: RSSFeed; active: boolean; onSelect: () => void }) {
  const title = feed.title || feed.url
  return <button
    type="button"
    className={`rss-nav-item${active ? ' is-active' : ''}`}
    onClick={onSelect}
    title={feed.error_msg ? `抓取失败：${feed.error_msg}` : title}
  >
    <span className={`status-dot ${feed.error_msg ? 'is-error' : 'is-ok'}`} aria-hidden="true" />
    <span className="rss-nav-label">{title}</span>
    <span className="rss-nav-count">{feed.last_fetch ? relativeTime(feed.last_fetch) : '未抓取'}</span>
  </button>
}
