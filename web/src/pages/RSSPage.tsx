import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { ConfirmDialog, Modal, Notice, PageState, Pagination, StatusBadge } from '@/components/WorkspaceUI'
import { rssApi, type FetchBy, type RSSEntry, type RSSFeed, type RSSGroup, type ScheduleState } from '@/lib/rss'
import { isUsableTimestamp, relativeTime } from '@/lib/format'
import { formatDateTime } from '@/lib/types'

const perPage = 20

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
  const [loading, setLoading] = useState(true)
  const [entriesLoading, setEntriesLoading] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [schedule, setSchedule] = useState<ScheduleState | null>(null)
  const [showSchedule, setShowSchedule] = useState(false)
  // 一次只编辑一行的间隔：同时开多个输入框容易改错行。
  const [intervalDraft, setIntervalDraft] = useState<{ feedID: number; value: string } | null>(null)

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
      const [feedList, groupList, scheduleState] = await Promise.all([
        rssApi.listFeeds(),
        rssApi.listGroups(),
        rssApi.schedule().catch(() => null),
      ])
      setSchedule(scheduleState)
      setFeeds(feedList ?? [])
      setGroups(groupList ?? [])
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法读取订阅源')
    } finally {
      setLoading(false)
    }
  }

  async function loadSchedule() {
    try {
      setSchedule(await rssApi.schedule())
    } catch {
      // 调度视图是附加信息，读不到不影响阅读。
      setSchedule(null)
    }
  }

  /** patchSchedule 更新单源的调度字段，成功后刷新面板。 */
  async function patchSchedule(feedID: number, body: { fetch_enabled?: boolean; fetch_interval?: number }, note: string) {
    setBusy(true)
    try {
      await rssApi.updateFeed(feedID, body)
      setMessage(note)
      setError('')
      await Promise.all([loadSchedule(), loadFeeds()])
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '调整失败')
    } finally {
      setBusy(false)
    }
  }

  /** saveInterval 提交间隔编辑。 */
  async function saveInterval(feedID: number) {
    if (!intervalDraft || intervalDraft.feedID !== feedID) return
    const value = Number(intervalDraft.value)
    if (!Number.isFinite(value) || value < 60) {
      setError('抓取间隔不能小于 60 秒')
      return
    }
    setIntervalDraft(null)
    await patchSchedule(feedID, { fetch_interval: value }, `抓取间隔已改为 ${formatInterval(value)}`)
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
          ...(feedDraft.group_id ? { group_id: feedDraft.group_id } : {}),
          // 保存就是保存：只有勾了这一项才抓。
          fetch_now: feedDraft.fetch_now,
        }),
        feedDraft.fetch_now ? '订阅源已添加，正在后台抓取' : '订阅源已添加',
      )
    if (saved) { setFeedDraft(null); await loadEntries(); await loadSchedule() }
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
        <button
          className="button button--quiet"
          onClick={() => { setShowSchedule(true); void loadSchedule() }}
          title="查看每个源的下次抓取时间与到期情况"
        >
          调度{schedule && schedule.due_count > 0 ? ` · ${schedule.due_count} 待抓` : ''}
        </button>
      </div>
      <span className="toolbar-summary">
        {feeds.length} 个订阅源 · {groups.length} 个分组
        {schedule && schedule.extension_count > 0 && ` · ${schedule.extension_count} 个交给 Extension`}
        {schedule && !schedule.enabled && (
          <span style={{ color: 'var(--color-warning)' }}> · 后台抓取已关闭</span>
        )}
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
                <button className="button button--quiet" onClick={() => setFeedDraft({ id: activeFeed.id, url: activeFeed.url, title: activeFeed.title, remark: activeFeed.remark ?? '', fetch_interval: activeFeed.fetch_interval, fetch_by: activeFeed.fetch_by ?? 'local', group_id: activeFeed.group_id, fetch_now: false })}>编辑</button>
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

    {/* ---------- 调度面板 ---------- */}
    <Modal
      open={showSchedule}
      title="抓取调度"
      description="直接在这里调整调度：点间隔可以改，暂停后这个源不再参与后台抓取。全局开关与扫描周期在设置页的 RSS 面板。"
      size="large"
      onClose={() => setShowSchedule(false)}
      footer={<>
        <button className="button button--quiet" type="button" onClick={() => void loadSchedule()}>刷新</button>
        <button className="button button--primary" type="button" onClick={() => setShowSchedule(false)}>关闭</button>
      </>}
    >
      {!schedule
        ? <PageState kind="empty" title="没有调度信息" description="订阅源列表为空，或调度接口不可用。" />
        : <>
          <div className="metric-row">
            <div className="metric-label"><span>后台抓取</span><small>config.json 的 rss.background_fetch_enabled</small></div>
            <div className="metric-value">
              <StatusBadge tone={schedule.enabled ? 'success' : 'warning'}>{schedule.enabled ? '已启用' : '已关闭'}</StatusBadge>
            </div>
          </div>
          <div className="metric-row">
            <div className="metric-label"><span>扫描周期</span><small>多久检查一次有没有到期的源</small></div>
            <div className="metric-value"><strong>{schedule.tick_seconds}</strong> 秒</div>
          </div>
          <div className="metric-row">
            <div className="metric-label"><span>当前到期</span><small>等下一轮扫描就会被抓</small></div>
            <div className="metric-value">
              <StatusBadge tone={schedule.due_count > 0 ? 'warning' : 'neutral'}>{schedule.due_count} 个</StatusBadge>
            </div>
          </div>
          <div className="metric-row">
            <div className="metric-label"><span>执行者分布</span><small>本机抓取 / 交给 Extension</small></div>
            <div className="metric-value">
              <strong>{schedule.local_count}</strong> 本机 · <strong>{schedule.extension_count}</strong> 远端
            </div>
          </div>

          {!schedule.enabled && schedule.local_count > 0 && <Notice
            message="后台抓取已关闭（全局）：这些源只会在你手动点「抓取」时更新。到设置页的 RSS 面板打开「启用后台抓取」。"
            tone="info"
          />}

          {schedule.feeds.length > 0 && <div className="table-wrap" style={{ marginTop: 16 }}>
            <table className="data-table">
              <thead><tr><th>订阅源</th><th>执行者</th><th>间隔</th><th>上次抓取</th><th>下次抓取</th><th>状态</th><th>操作</th></tr></thead>
              <tbody>
                {schedule.feeds.map(item => <tr key={item.feed_id}>
                  <td data-label="订阅源">
                    <strong>{item.title || item.url}</strong>
                    {item.error_msg && <p className="table-subtext">{item.error_msg}</p>}
                  </td>
                  <td data-label="执行者">
                    <StatusBadge tone={item.fetch_by === 'extension' ? 'info' : 'neutral'}>
                      {item.fetch_by === 'extension' ? 'Extension' : '本机'}
                    </StatusBadge>
                  </td>
                  <td data-label="间隔">
                    {intervalDraft?.feedID === item.feed_id
                      ? <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
                        <input
                          autoFocus
                          type="number"
                          min={60}
                          step={60}
                          value={intervalDraft.value}
                          onChange={event => setIntervalDraft({ feedID: item.feed_id, value: event.target.value })}
                          onKeyDown={event => {
                            if (event.key === 'Enter') { event.preventDefault(); void saveInterval(item.feed_id) }
                            if (event.key === 'Escape') setIntervalDraft(null)
                          }}
                          aria-label="新的抓取间隔（秒）"
                          style={{ width: 84 }}
                        />
                        <button type="button" className="button button--primary" disabled={busy} onClick={() => void saveInterval(item.feed_id)}>存</button>
                        <button type="button" className="button button--quiet" onClick={() => setIntervalDraft(null)}>取消</button>
                      </div>
                      : <button
                        type="button"
                        className="button button--quiet"
                        title="点击修改抓取间隔"
                        onClick={() => setIntervalDraft({ feedID: item.feed_id, value: String(item.interval_seconds) })}
                      >
                        {formatInterval(item.interval_seconds)}
                      </button>}
                  </td>
                  <td data-label="上次抓取">{item.last_fetch_at ? formatDateTime(new Date(item.last_fetch_at * 1000).toISOString()) : '从未'}</td>
                  <td data-label="下次抓取">{item.next_fetch_at ? formatDateTime(new Date(item.next_fetch_at * 1000).toISOString()) : (item.fetch_by === 'local' ? '等待首轮' : '—')}</td>
                  <td data-label="状态">
                    {item.fetch_by === 'extension'
                      ? <StatusBadge tone="neutral">不参与本机调度</StatusBadge>
                      : !item.fetch_enabled
                        ? <StatusBadge tone="neutral">已暂停</StatusBadge>
                        : !schedule.enabled
                          ? <StatusBadge tone="warning">总开关关闭</StatusBadge>
                          : item.due
                            ? <StatusBadge tone="warning">已到期 {formatInterval(item.overdue_seconds)}</StatusBadge>
                            : <StatusBadge tone="success">等待中</StatusBadge>}
                  </td>
                  <td data-label="操作">
                    <div className="table-actions">
                      <button
                        type="button"
                        className="button button--quiet"
                        disabled={busy || !item.fetch_enabled || item.fetch_by === 'extension'}
                        title={item.fetch_by === 'extension' ? '这个源由 Extension 抓取，请到对面触发' : item.fetch_enabled ? '立即抓一次' : '先恢复调度'}
                        onClick={() => void run(() => rssApi.fetchFeed(item.feed_id), `已抓取「${item.title || item.url}」`, true)}
                      >
                        抓取
                      </button>
                      <button
                        type="button"
                        className="button button--quiet"
                        disabled={busy}
                        onClick={() => void patchSchedule(
                          item.feed_id,
                          { fetch_enabled: !item.fetch_enabled },
                          item.fetch_enabled ? `已暂停「${item.title || item.url}」` : `已恢复「${item.title || item.url}」`,
                        )}
                      >
                        {item.fetch_enabled ? '暂停' : '恢复'}
                      </button>
                    </div>
                  </td>
                </tr>)}
              </tbody>
            </table>
          </div>}
        </>}
    </Modal>

    {/* ---------- 订阅源弹窗 ---------- */}
    <Modal
      open={feedDraft !== null}
      title={feedDraft?.id ? '编辑订阅源' : '添加订阅源'}
      description={feedDraft?.id ? '可修改备注、展示标题、抓取间隔、执行者与分组；源 URL 不支持变更。' : '只保存，不抓取。保存后再点「抓取」拉取内容，或勾选下面的「保存后立即抓取」。'}
      onClose={() => !busy && setFeedDraft(null)}
      footer={<>
        <button className="button button--quiet" type="button" onClick={() => setFeedDraft(null)} disabled={busy}>取消</button>
        <button className="button button--primary" type="submit" form="rss-feed-form" disabled={busy}>{busy ? '保存中...' : '保存'}</button>
      </>}
    >
      <form id="rss-feed-form" className="form-grid" onSubmit={saveFeed}>
        {!feedDraft?.id && <label className="form-field form-field--wide">订阅源 URL
          <input autoFocus type="url" value={feedDraft?.url ?? ''} onChange={event => setFeedDraft(draft => draft ? { ...draft, url: event.target.value } : draft)} placeholder="https://example.com/feed.xml" required />
        </label>}
        <label className="form-field form-field--wide">备注
          <input
            autoFocus={Boolean(feedDraft?.id)}
            value={feedDraft?.remark ?? ''}
            onChange={event => setFeedDraft(draft => draft ? { ...draft, remark: event.target.value } : draft)}
            placeholder="给自己看的说明，例如「只在周末更新」「需要代理」"
            maxLength={500}
          />
        </label>
        <label className="form-field form-field--wide">展示标题
          <input
            value={feedDraft?.title ?? ''}
            onChange={event => setFeedDraft(draft => draft ? { ...draft, title: event.target.value } : draft)}
            placeholder="留空则抓取后自动补全；填了就不会被覆盖"
            maxLength={255}
          />
        </label>
        <label className="form-field">抓取间隔（秒）
          <input type="number" min="60" value={feedDraft?.fetch_interval ?? 3600} onChange={event => setFeedDraft(draft => draft ? { ...draft, fetch_interval: Number(event.target.value) } : draft)} required />
        </label>
        <label className="form-field">分组
          <select value={feedDraft?.group_id ?? ''} onChange={event => setFeedDraft(draft => draft ? { ...draft, group_id: event.target.value ? Number(event.target.value) : undefined } : draft)}>
            <option value="">未分组</option>
            {groups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}
          </select>
        </label>
        <label className="form-field form-field--wide">由谁抓取
          <select
            value={feedDraft?.fetch_by ?? 'local'}
            onChange={event => setFeedDraft(draft => draft ? { ...draft, fetch_by: event.target.value as FetchBy } : draft)}
          >
            <option value="local">本机抓取（条目存在 HomeHub）</option>
            <option value="extension">交给 Extension（条目存在对面）</option>
          </select>
        </label>
        <p className="form-hint" style={{ gridColumn: '1 / -1', margin: 0, fontSize: 'var(--text-xs)', color: 'var(--color-text-secondary)' }}>
          {feedDraft?.fetch_by === 'extension'
            ? '交给 Extension 后，本机不再抓取这个源，条目也只存在对面——阅读要去 Extension 的界面，并且需要在这个源的列表里点「推送」。'
            : '本机按间隔抓取，条目存在 HomeHub，可以在右侧阅读。'}
        </p>
        {!feedDraft?.id && <label className="choice-tile" style={{ gridColumn: '1 / -1' }}>
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
/** formatInterval 把秒数写成「2 小时」「30 分钟」。 */
function formatInterval(seconds: number): string {
  if (!seconds || seconds <= 0) return '—'
  if (seconds < 60) return `${seconds} 秒`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟`
  const hours = minutes / 60
  if (hours < 24) return `${Number.isInteger(hours) ? hours : hours.toFixed(1)} 小时`
  const days = hours / 24
  return `${Number.isInteger(days) ? days : days.toFixed(1)} 天`
}

function FeedNavItem({ feed, active, onSelect }: { feed: RSSFeed; active: boolean; onSelect: () => void }) {
  const title = feed.title || feed.url
  const remote = feed.fetch_by === 'extension'
  const paused = feed.fetch_enabled === false
  const hint = [
    feed.remark,
    feed.error_msg ? `抓取失败：${feed.error_msg}` : '',
    remote ? '由 Extension 抓取，本机不参与' : '',
    paused ? '调度已暂停' : '',
  ].filter(Boolean).join(' · ')

  return <button
    type="button"
    className={`rss-nav-item${active ? ' is-active' : ''}`}
    onClick={onSelect}
    title={hint || title}
  >
    <span className={`status-dot ${remote || paused ? 'is-warn' : feed.error_msg ? 'is-error' : 'is-ok'}`} aria-hidden="true" />
    <span className="rss-nav-label">{title}</span>
    {feed.remark && <span className="rss-nav-remark">{feed.remark}</span>}
    <span className="rss-nav-count">{remote ? '远端' : paused ? '已暂停' : feed.last_fetch ? relativeTime(feed.last_fetch) : '未抓取'}</span>
  </button>
}
