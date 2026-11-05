import { useEffect, useMemo, useRef, useState, type FormEvent, type RefObject } from 'react'
import { DeleteButton } from '@/components/Icon'
import { ConfirmDialog, Modal, PageState, Pagination } from '@/components/WorkspaceUI'
import { errorMessage, useNotice } from '@/hooks/useNotice'
import { useQueryNumber, useQueryState } from '@/hooks/useQueryState'
import { isAbortError } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { CONTENT_WRITE_LEVEL, hasLevel } from '@/lib/permissions'
import { rssApi, type FetchBy, type RSSEntry, type RSSFeed, type RSSGroup } from '@/lib/rss'
import { RSSFeedManager, RSSGroupManager } from '@/pages/RSSManage'
import { extensionHubApi, type RemoteSubscription } from '@/lib/extensionHub'
import { MagnetCopyButton } from '@/components/MagnetCopyButton'
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

/** 抓取方式的选项。
 *
 * 标签只写「本机 / Extension」：它上面的 legend 已经叫「抓取」，
 * 下拉里再写「本机抓取」「交给 Extension」等于同一句话读三遍。
 * note 解释后果而不是重复名称——选错这一项会改变条目存在哪一侧。
 */
const fetchModes: { value: FetchBy; label: string; note: string }[] = [
  { value: 'local', label: '本机', note: 'HomeHub 抓取，条目存在这里' },
  { value: 'extension', label: 'Extension', note: '对端抓取，去 Extension 阅读' },
]

export function RSSPage() {
  const [feeds, setFeeds] = useState<RSSFeed[]>([])
  const [groups, setGroups] = useState<RSSGroup[]>([])
  const { auth } = useAuth()
  const canWrite = hasLevel(auth, CONTENT_WRITE_LEVEL)
  const notice = useNotice()
  const [entries, setEntries] = useState<RSSEntry[]>([])
  const [total, setTotal] = useState(0)
  // 「全部条目」的计数：与当前筛选范围无关，单独请求。
  const [allTotal, setAllTotal] = useState<number | null>(null)
  // 筛选与页码同步到 URL（replaceState，不新增历史条目），刷新与分享链接都能还原。
  const [feedParam, setFeedParam] = useQueryState('feed', '')
  const [groupParam, setGroupParam] = useQueryState('group', '')
  const [page, setPage] = useQueryNumber('page', 1)
  const selectedFeed = Number(feedParam) > 0 ? Number(feedParam) : null
  const selectedGroup = Number(groupParam) > 0 ? Number(groupParam) : null
  const setSelectedFeed = (id: number | null) => setFeedParam(id ? String(id) : '')
  const setSelectedGroup = (id: number | null) => setGroupParam(id ? String(id) : '')
  const [collapsedGroups, setCollapsedGroups] = useState<Set<number>>(() => new Set())
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [entriesLoading, setEntriesLoading] = useState(false)
  const [entriesError, setEntriesError] = useState('')
  const [busy, setBusy] = useState(false)
  // 弹窗内的错误：弹窗处于 top layer，页面提示会被遮罩挡住。
  const [formError, setFormError] = useState('')
  const entriesRef = useRef<HTMLDivElement>(null)
  const entriesAbort = useRef<AbortController | null>(null)
  // 远端订阅按 URL 索引，用于在调度面板里显示「对面到底抓到了没有」。
  const [remoteByURL, setRemoteByURL] = useState<Record<string, RemoteSubscription>>({})

  const [feedDraft, setFeedDraft] = useState<DraftFeed | null>(null)
  // 两个管理窗口分开开关：它们操作的单位不同，同时打开只会互相遮挡。
  const [showGroups, setShowGroups] = useState(false)
  const [showFeeds, setShowFeeds] = useState(false)
  // 「创建分组」快入口：只输一个名字，不打开完整的管理窗口。
  const [showNewGroup, setShowNewGroup] = useState(false)
  // 拖拽中在外部订阅源；null 表示没在拖。用 state 而不是 dataTransfer
  // 是因为 dragOver 阶段读不到 data（浏览器出于安全只允许在 drop 时读），
  // 而高亮当前落点需要它。
  const [draggingFeed, setDraggingFeed] = useState<number | null>(null)
  // 当前悬停的落点是哪个分组：number = 分组 id，'loose' = 未分组，
  // null = 没悬停在任何落点上。只用于视觉高亮。
  const [dropTarget, setDropTarget] = useState<number | 'loose' | null>(null)
  const [newGroup, setNewGroup] = useState('')
  const [pendingDeleteFeed, setPendingDeleteFeed] = useState<RSSFeed | null>(null)

  // ---------- 数据加载 ----------
  /** loadFeeds 拉取订阅源与分组。
   *
   * silent 为真时连 loading 态都不切——首屏之外的刷新（拖拽分组、
   * 管理窗口里的每次点击）不该让列表闪一下。
   *
   * 这里只负责置位，不判断「要不要置位」：真正的闸门在渲染处——
   * `if (loading && !feeds.length)`。那一条是「分组管理动不了」的根因
   * 修复：管理窗口每次添加/移出都会经 run() 调到这里，若渲染处无条件
   * 按 loading 早退，整棵树会被换成加载页——包括打开中的弹窗。React
   * 卸载后弹窗的展开状态、选中项全部丢失，用户看到的就是「点一下
   * 窗口闪没了，回来又原样」。
   */
  async function loadFeeds(silent = false) {
    if (!silent) setLoading(true)
    try {
      const [feedList, groupList] = await Promise.all([
        rssApi.listFeeds(),
        rssApi.listGroups(),
      ])
      setFeeds(feedList ?? [])
      setGroups(groupList ?? [])
      // 选中的订阅源或分组可能已被删除（包括刚在管理窗口里批量删掉的）。
      // 不清理的话右侧会一直键在一个已不存在的对象上，标题也还挂着。
      // 只在列表非空时校验，避免请求失败的空列表把 URL 里的筛选清掉。
      if (selectedGroup && groupList?.length && !groupList.some(g => g.id === selectedGroup)) setSelectedGroup(null)
      if (selectedFeed && feedList?.length && !feedList.some(f => f.id === selectedFeed)) setSelectedFeed(null)
      setLoadError('')
      void loadAllTotal()
      // 远端状态是侧栏状态点与计数的依据，单独拉、失败不阻塞列表。
      void loadRemote()
    } catch (cause) {
      const text = errorMessage(cause, '无法读取订阅源')
      if (silent) notice.error(text)
      else setLoadError(text)
    } finally {
      if (!silent) setLoading(false)
    }
  }

  /** loadAllTotal 取不带筛选的条目总数（per_page=1，只要 total）。 */
  async function loadAllTotal() {
    try { setAllTotal((await rssApi.listEntries(null, null, 1, 1)).total ?? 0) }
    catch { setAllTotal(null) }
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
      notice.success(result.created
        ? `已推送「${label}」到 Extension，对端开始抓取`
        : `已更新 Extension 上的「${label}」`)
    } catch (cause) {
      const text = errorMessage(cause, '推送失败')
      // 最常见的原因是没启用签名通道；直接说出来，别让人去查网络。
      // 推送按钮在订阅源弹窗里，所以错误画在弹窗内。
      setFormError(text.includes('未启用') || text.includes('70001')
        ? '还没有启用 Extension 签名通道：到「设置 → Extension 对接」填好地址与私钥后再推送'
        : text)
    } finally {
      setBusy(false)
    }
  }

  /** loadEntries 读取当前范围的条目。
   *
   * 新请求会取消上一个未完成的请求：快速切换订阅源时，慢的旧响应
   * 不能覆盖新选择的结果。翻页时保留旧列表（降低透明度）直到新数据到达。
   */
  async function loadEntries(feed = selectedFeed, group = selectedGroup, targetPage = page) {
    entriesAbort.current?.abort()
    const controller = new AbortController()
    entriesAbort.current = controller
    setEntriesLoading(true)
    try {
      const result = await rssApi.listEntries(feed, group, targetPage, perPage, { signal: controller.signal })
      setEntries(result.list ?? [])
      setTotal(result.total ?? 0)
      setEntriesError('')
      entriesRef.current?.scrollTo({ top: 0 })
    } catch (cause) {
      if (isAbortError(cause)) return
      setEntriesError(errorMessage(cause, '无法读取订阅条目'))
    } finally {
      if (entriesAbort.current === controller) setEntriesLoading(false)
    }
  }

  // oxlint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void loadFeeds() }, [])
  // oxlint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void loadEntries(selectedFeed, selectedGroup, page) }, [selectedFeed, selectedGroup, page])
  useEffect(() => () => entriesAbort.current?.abort(), [])

  /** run 执行一次写操作并刷新列表。
   *
   * silentReload 供弹窗内的操作使用：列表刷新不该把整页（连同正在
   * 交互的弹窗）换掉，也不该让列表自己在每次点击后闪一下。
   */
  async function run(action: () => Promise<unknown>, success: string, reloadEntries = false, inModal = false) {
    setBusy(true)
    setFormError('')
    try {
      await action()
      notice.success(success)
      // 写操作后的刷新一律静默：列表不闪，打开中的弹窗也不会被卸载。
      await loadFeeds(true)
      if (reloadEntries) await loadEntries()
      return true
    } catch (cause) {
      const text = errorMessage(cause, '操作失败')
      if (inModal) setFormError(text)
      else notice.error(text)
      return false
    } finally {
      setBusy(false)
    }
  }

  /** runSilent 是管理弹窗内部使用的 run：错误画在弹窗里。 */
  const runSilent = (action: () => Promise<unknown>, success: string) =>
    run(action, success, false, true)

  function closeFeedDraft() { setFeedDraft(null); setFormError('') }

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
        false,
        true,
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
        false,
        true,
      )
    if (saved) { closeFeedDraft(); await loadEntries(); void loadRemote() }
  }

  async function removeFeed() {
    if (!pendingDeleteFeed) return
    const id = pendingDeleteFeed.id
    const saved = await run(() => rssApi.deleteFeed(id), '订阅源及其条目已删除', true, true)
    if (saved) {
      if (selectedFeed === id) { setSelectedFeed(null); setPage(1) }
      setPendingDeleteFeed(null)
      setFormError('')
    }
  }

  // ---------- 分组 ----------
  /** moveFeedToGroup 把一个订阅源移入分组（或移出到未分组）。
   *
   * groupID 为 null 表示「未分组」，必须显式发 null——省略字段在协议里
   * 表示「别动分组」，传 undefined 会让拖拽看起来毫无反应。
   */
  async function moveFeedToGroup(feedID: number, groupID: number | null) {
    const feed = feeds.find(f => f.id === feedID)
    if (!feed) return
    const current = feed.group_id ?? null
    if (current === groupID) return // 拖回原地：什么都不做

    setBusy(true)
    try {
      await rssApi.updateFeed(feedID, { group_id: groupID })
      const target = groupID === null
        ? '未分组'
        : groups.find(g => g.id === groupID)?.name ?? '目标分组'
      notice.success(`「${feed.title || feed.url}」已移入${target}`)
      await loadFeeds(true)
    } catch (cause) {
      notice.error(errorMessage(cause, '移动订阅源失败'))
    } finally {
      setBusy(false)
    }
  }

  /** 拖拽目标的公共事件处理。 */
  function dropHandlers(target: number | 'loose') {
    return {
      onDragOver: (event: React.DragEvent) => {
        if (draggingFeed === null) return
        // 必须 preventDefault，否则浏览器不会触发 drop。
        event.preventDefault()
        event.dataTransfer.dropEffect = 'move'
        setDropTarget(target)
      },
      onDragLeave: (event: React.DragEvent) => {
        // 拖到自己的子元素（组内某个源、分组标题）上也会触发 dragleave，
        // 若直接清空，高亮会在组内移动时反复闪。用 relatedTarget 判断
        // 是否真的离开了这一块。
        const next = event.relatedTarget as Node | null
        if (next && event.currentTarget.contains(next)) return
        setDropTarget(current => (current === target ? null : current))
      },
      onDrop: (event: React.DragEvent) => {
        event.preventDefault()
        const id = draggingFeed
        setDraggingFeed(null)
        setDropTarget(null)
        if (id === null) return
        // 落进一个收起的组会看不到结果——以为没成功。先展开它。
        if (target !== 'loose') {
          setCollapsedGroups(prev => {
            if (!prev.has(target)) return prev
            const next = new Set(prev)
            next.delete(target)
            return next
          })
        }
        void moveFeedToGroup(id, target === 'loose' ? null : target)
      },
    }
  }

  /** deleteFeeds 批量删除：**逐条串行**执行并汇总结果。
   *
   * 两个理由：
   *  1. 并发会破坏孤儿回收。每条删除都要在同一条 NOT EXISTS 里判断
   *     「还有没有别的源收录这条内容」。两个源共享一条内容时并发删除，
   *     各自都看不到对方未提交的关联行，于是两边都判定「还有人引用」
   *     而保留——留下一批没人引用的孤儿条目。串行没有这个窗口。
   *  2. 删除本来就是每条一个事务，要的是「删掉了哪些、哪些没删掉」
   *     这种不完全成功的结果，而不是全有全无。串行才能如实汇总。
   *
   * 不用后端批量端点：合并成一个大事务只会把失败范围扩大。
   */
  async function deleteFeeds(ids: number[]) {
    if (!ids.length) return
    setBusy(true)
    setFormError('')
    const failed: number[] = []
    try {
      for (const id of ids) {
        try {
          await rssApi.deleteFeed(id)
        } catch {
          failed.push(id)
        }
      }
      const done = ids.length - failed.length
      // 批量删除只从订阅管理弹窗发起，失败信息画在弹窗里。
      if (failed.length) setFormError(`删除未全部完成：成功 ${done} 个，失败 ${failed.length} 个，可重试`)
      else notice.success(`已删除 ${done} 个订阅源`)
      await loadFeeds(true)
      await loadEntries()
    } finally {
      setBusy(false)
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

  if (loading && !feeds.length) return <PageState kind="loading" title="正在读取订阅源" />
  if (loadError && !feeds.length) {
    return <PageState
      kind="error"
      title="无法读取订阅源"
      description={loadError}
      action={<button className="button button--primary" onClick={() => void loadFeeds()}>重试</button>}
    />
  }

  return <div className="business-page rss-page">
    {notice.element}

    {!feeds.length
      ? <PageState
        kind="empty"
        title="还没有订阅源"
        description={canWrite ? '添加 RSS 或 Atom 链接后，抓取到的文章会出现在这里。' : '管理员或编辑添加订阅源后，文章会出现在这里。'}
        action={canWrite ? <button className="button button--primary" onClick={() => setFeedDraft({ ...emptyFeedDraft })}>添加第一个订阅源</button> : undefined}
      />
      : <section className="rss-panel">
        {/* 顶部工具栏：所有动作同一层级、同一形态，按使用顺序排。
            样式参照 qBittorrent WebUI——一条压缩的工具条 + 图标文字按钮，
            而不是把按钮分散到各栏里。 */}
        <div className="rss-toolbar">
          {canWrite && <>
          <button type="button" className="rss-tool" onClick={() => setFeedDraft({ ...emptyFeedDraft })}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M5 12h14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" /></svg>
            添加订阅源
          </button>
          <button type="button" className="rss-tool" onClick={() => { setNewGroup(''); setShowNewGroup(true) }}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 7h7l2 2h9v9a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinejoin="round" /></svg>
            创建分组
          </button>
          <button type="button" className="rss-tool" onClick={() => setShowGroups(true)}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h10" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /></svg>
            分组管理
          </button>
          {/* 紧跟在分组管理之后：两者是同一组管理的两个视角，
              一个按组下手、一个按源下手。 */}
          <button type="button" className="rss-tool" onClick={() => setShowFeeds(true)} disabled={!feeds.length}>
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M4 6h16M4 12h16M4 18h16" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />
              <path d="M8 6v12M15 6v12" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" opacity=".45" />
            </svg>
            订阅管理
          </button>
          <span className="rss-tool-sep" aria-hidden="true" />
          <button
            type="button"
            className="rss-tool"
            disabled={busy || !feeds.length}
            onClick={() => void run(() => rssApi.fetchAll(), '已触发全部订阅源抓取', true)}
          >
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8 8 0 1 0-2.3 5.7" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" /><path d="M20 5v6h-6" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></svg>
            全部抓取
          </button>
          </>}
          <span className="rss-toolbar-summary">
            {feeds.length} 个源 · {groups.length} 个分组
            {remoteCount > 0 && ` · ${remoteCount} 远端`}
            {pausedCount > 0 && ` · ${pausedCount} 暂停`}
            {errorFeeds.length > 0 && <span className="is-error"> · {errorFeeds.length} 异常</span>}
          </span>
        </div>

        <div className="rss-body">
        {/* ---------- 左栏 ---------- */}
        <aside className="rss-sidebar" aria-label="订阅源导航">
          <div className="rss-sidebar-head">
            <strong>订阅源</strong>
          </div>
          <nav className="rss-nav">
            {/* 「全部条目」是导航的第一个入口，不是头部的一个按钮。 */}
            <button
              type="button"
              className={`rss-nav-item rss-nav-all${!selectedFeed && !selectedGroup ? ' is-active' : ''}`}
              onClick={selectAll}
            >
              <span className="rss-nav-label">全部条目</span>
              <span className="rss-nav-count">{allTotal === null ? '—' : allTotal.toLocaleString()}</span>
            </button>
            {groups.map(group => {
              const groupFeeds = grouped.byGroup.get(group.id) ?? []
              const expanded = !collapsedGroups.has(group.id)
              return <div
                key={group.id}
                className={`rss-group${dropTarget === group.id ? ' is-drop-target' : ''}`}
                {...dropHandlers(group.id)}
              >
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
                    ? <span className="rss-nav-item rss-nav-empty">暂无订阅源</span>
                    : groupFeeds.map(feed => <FeedNavItem
                      key={feed.id}
                      feed={feed}
                      active={selectedFeed === feed.id}
                      remote={remoteByURL[feed.url]}
                      onSelect={() => selectFeed(feed.id)}
                      dragging={draggingFeed === feed.id}
                      onDragStart={() => setDraggingFeed(feed.id)}
                      onDragEnd={() => { setDraggingFeed(null); setDropTarget(null) }}
                    />)}
                  <button
                    type="button"
                    className="rss-nav-item rss-nav-group-all"
                    onClick={() => selectGroup(group.id)}
                  >
                    <span className="rss-nav-label">查看该分组全部条目</span>
                  </button>
                </div>}
              </div>
            })}

            {/* 未分组块：平时为空时不显示，但拖拽过程中必须出现——
                否则把一个源拖出分组就没有落点，等于出不去。 */}
            {(grouped.loose.length > 0 || draggingFeed !== null) && <div
              className={`rss-group${dropTarget === 'loose' ? ' is-drop-target' : ''}`}
              {...dropHandlers('loose')}
            >
              <span className="rss-group-head rss-group-head--static">
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
                  dragging={draggingFeed === feed.id}
                  onDragStart={() => setDraggingFeed(feed.id)}
                  onDragEnd={() => { setDraggingFeed(null); setDropTarget(null) }}
                />)}
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
              {activeFeed && canWrite && <>
                <button type="button" className="rss-tool" disabled={busy} onClick={() => void run(() => rssApi.fetchFeed(activeFeed.id), `已抓取「${activeFeed.title || activeFeed.url}」`, true)}>抓取</button>
                <button type="button" className="rss-tool" onClick={() => setFeedDraft({ id: activeFeed.id, url: activeFeed.url, title: activeFeed.title, remark: activeFeed.remark ?? '', fetch_interval: activeFeed.fetch_interval, fetch_by: activeFeed.fetch_by ?? 'local', fetch_enabled: activeFeed.fetch_enabled ?? true, group_id: activeFeed.group_id, fetch_now: false })}>编辑</button>
                <DeleteButton label={`删除订阅源「${activeFeed.title || activeFeed.url}」`} onClick={() => setPendingDeleteFeed(activeFeed)} />
              </>}
            </div>
          </header>

          {activeFeed?.error_msg && <p className="rss-feed-error">最近一次抓取失败：{activeFeed.error_msg}</p>}

          {entriesLoading && !entries.length
            ? <PageState kind="loading" title="正在读取条目" />
            : entriesError && !entries.length
            ? <PageState kind="error" title="无法读取订阅条目" description={entriesError} action={<button type="button" className="button button--primary" onClick={() => void loadEntries()}>重试</button>} />
            : !entries.length
              ? <PageState
                kind="empty"
                title="没有可显示的条目"
                description={activeFeed ? '这个订阅源还没有抓到文章，试试手动抓取。' : '切换订阅源，或调整筛选范围。'}
              />
              : <div className={`rss-entries${entriesLoading ? ' is-stale' : ''}`} ref={entriesRef} aria-busy={entriesLoading}>
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
                    {hasBody(entry) && <EntryContent entryID={entry.id} inline={entry.content} />}
                  </div>
                  {entry.magnet_url && <MagnetCopyButton
                    className="rss-magnet"
                    url={entry.magnet_url}
                    title={entry.title}
                    onCopied={ok => ok ? notice.success('磁力链接已复制') : notice.info('自动复制不可用，已选中链接，按 Ctrl+C 复制')}
                  />}
                </article>)}
              </div>}
          <Pagination page={page} perPage={perPage} total={total} disabled={entriesLoading} onChange={setPage} />
        </section>
        </div>
      </section>}


    {/* ---------- 订阅源弹窗：基本信息 + 抓取设置 ---------- */}
    <Modal
      open={feedDraft !== null}
      title={feedDraft?.id ? '订阅源设置' : '添加订阅源'}
      description={feedDraft?.id ? undefined : '只保存，不抓取。保存后再点「抓取」拉内容。'}
      size="large"
      error={formError}
      busy={busy}
      onClose={closeFeedDraft}
      footer={<>
        <button className="button button--quiet" type="button" onClick={closeFeedDraft} disabled={busy}>取消</button>
        <button className="button button--primary" type="submit" form="rss-feed-form" disabled={busy}>{busy ? '保存中…' : '保存'}</button>
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
          {/* 两个选项直接摆在面板里，而不是折在下拉里：这是整个弹窗
              里唯一会改变「条目存在哪一侧」的决定，藏起来不合适。 */}
          <div className="rss-fetch-modes form-field--wide" role="radiogroup" aria-label="抓取方式">
            {fetchModes.map(mode => {
              const selected = (feedDraft?.fetch_by ?? 'local') === mode.value
              return <label key={mode.value} className={`rss-fetch-mode${selected ? ' is-selected' : ''}`}>
                <input
                  type="radio"
                  name="fetch_by"
                  value={mode.value}
                  checked={selected}
                  onChange={() => setFeedDraft(draft => draft ? { ...draft, fetch_by: mode.value } : draft)}
                />
                <span className="rss-fetch-mode-text">
                  <strong>{mode.label}</strong>
                  <small>{mode.note}</small>
                </span>
              </label>
            })}
          </div>

          <div className="rss-fetch-row form-field--wide">
            <label className="form-field">间隔（秒）
              <input type="number" min="60" value={feedDraft?.fetch_interval ?? 3600} onChange={event => setFeedDraft(draft => draft ? { ...draft, fetch_interval: Number(event.target.value) } : draft)} required />
            </label>
            <label className="choice-tile rss-fetch-schedule">
              <input
                type="checkbox"
                checked={feedDraft?.fetch_enabled ?? true}
                onChange={event => setFeedDraft(draft => draft ? { ...draft, fetch_enabled: event.target.checked } : draft)}
              />
              <span>参与后台调度（取消勾选＝暂停抓取，但不删这个源）</span>
            </label>
          </div>

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
                onClick={() => feedDraft?.id && void run(() => rssApi.fetchFeed(feedDraft.id!), '已触发抓取', true, true)}
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

    {/* ---------- 创建分组弹窗 ----------
        与管理分开：这里只做一件事（输名字、回车创建），
        管理那个弹窗还要列出全部组并提供重命名/删除。 */}
    <Modal
      open={showNewGroup}
      title="创建分组"
      description="建好后把订阅源拖到分组标题上即可归入。"
      size="small"
      error={formError}
      busy={busy}
      onClose={() => { setShowNewGroup(false); setFormError('') }}
      footer={<>
        <button className="button button--quiet" type="button" disabled={busy} onClick={() => { setShowNewGroup(false); setFormError('') }}>取消</button>
        <button className="button button--primary" type="submit" form="new-group-form" disabled={busy || !newGroup.trim()}>
          {busy ? '创建中…' : '创建'}
        </button>
      </>}
    >
      <form id="new-group-form" className="form-grid" onSubmit={async event => {
        event.preventDefault()
        const name = newGroup.trim()
        if (!name) return
        const saved = await run(() => rssApi.createGroup(name), `分组「${name}」已创建`, false, true)
        if (saved) {
          setNewGroup('')
          setShowNewGroup(false)
        }
      }}>
        <label className="form-field form-field--wide">分组名称
          <input
            autoFocus
            required
            maxLength={100}
            value={newGroup}
            onChange={event => setNewGroup(event.target.value)}
            placeholder="例如 动漫、技术博客"
          />
        </label>
      </form>
    </Modal>

    {/* ---------- 订阅管理：多选后批量改属性 ---------- */}
    {/* runSilent 包一层：管理窗口里的每次点击都要 silent 刷新，
        否则列表闪一下、弹窗内的展开与选中状态也会被重渲染打断。 */}
    <RSSFeedManager
      open={showFeeds}
      feeds={feeds}
      groups={groups}
      busy={busy}
      error={formError}
      onClose={() => { setShowFeeds(false); setFormError('') }}
      run={runSilent}
      deleteFeeds={deleteFeeds}
    />

    {/* ---------- 分组管理：按组增删订阅源 ---------- */}
    <RSSGroupManager
      open={showGroups}
      feeds={feeds}
      groups={groups}
      busy={busy}
      error={formError}
      onClose={() => { setShowGroups(false); setFormError('') }}
      run={runSilent}
    />

    <ConfirmDialog
      open={pendingDeleteFeed !== null}
      title="删除订阅源"
      description={`将删除「${pendingDeleteFeed?.title || pendingDeleteFeed?.url || ''}」以及它收录的条目关联。其他源仍收录的内容会保留。`}
      confirmLabel="删除"
      danger
      busy={busy}
      error={formError}
      onClose={() => { setPendingDeleteFeed(null); setFormError('') }}
      onConfirm={() => void removeFeed()}
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
function FeedNavItem({ feed, active, remote, dragging, onSelect, onDragStart, onDragEnd }: {
  feed: RSSFeed
  active: boolean
  remote?: RemoteSubscription
  /** 这一项正在被拖拽。 */
  dragging: boolean
  onSelect: () => void
  onDragStart: () => void
  onDragEnd: () => void
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
    className={`rss-nav-item rss-draggable${active ? ' is-active' : ''}${dragging ? ' is-dragging' : ''}`}
    onClick={onSelect}
    title={hint || title}
    draggable
    onDragStart={event => {
      // 不设 data 也能拖，但部分浏览器（Safari）要求至少设一项才启动。
      event.dataTransfer.setData('text/plain', String(feed.id))
      event.dataTransfer.effectAllowed = 'move'
      onDragStart()
    }}
    onDragEnd={onDragEnd}
  >
    <span className={`status-dot ${dot}`} aria-hidden="true" />
    <span className="rss-nav-body">
      <span className="rss-nav-label">{title}</span>
      {feed.remark && <span className="rss-nav-remark">{feed.remark}</span>}
    </span>
    <span className="rss-nav-count">{meta}</span>
  </button>
}

/** deferImages 把正文里的图片推迟到进入视口再请求。
 *
 * 这是分段加载的第二段：正文 HTML 到达后先渲染文字，img 的 src 暂存到
 * data-src，由 useLazyImages 在图片滚进视口时才写回。文章带几十张图时，
 * 展开的瞬间不会同时打满连接，也不会把正文排版挤到图片加载完才出现。
 */
function deferImages(html: string): string {
  const doc = new DOMParser().parseFromString(html, 'text/html')
  for (const img of Array.from(doc.body.querySelectorAll('img[src]'))) {
    const src = img.getAttribute('src')
    if (!src) continue
    img.setAttribute('data-src', src)
    img.removeAttribute('src')
    img.setAttribute('loading', 'lazy')
    img.setAttribute('decoding', 'async')
  }
  return doc.body.innerHTML
}

/** useLazyImages 观察正文容器里的待加载图片，进入视口时恢复 src。 */
function useLazyImages(containerRef: RefObject<HTMLDivElement | null>, active: boolean) {
  useEffect(() => {
    const container = containerRef.current
    if (!active || !container) return
    const images = Array.from(container.querySelectorAll<HTMLImageElement>('img[data-src]'))
    if (!images.length) return

    const reveal = (img: HTMLImageElement) => {
      const src = img.dataset.src
      if (!src) return
      // 占位样式（opacity 0 + 最小高度）要留到 img 真正加载完：
      // 提前撤掉会让正文在下载途中先跳一次高度，加载完再跳一次。
      const settle = () => img.removeAttribute('data-src')
      img.addEventListener('load', settle, { once: true })
      img.addEventListener('error', () => {
        // 失败也要撤掉占位，否则正文里会留下一条永远空白的 64px。
        if (!img.alt) img.alt = '图片加载失败'
        settle()
      }, { once: true })
      img.src = src
    }

    // 没有 IntersectionObserver 时直接全部加载：宁可一次性请求，
    // 也不要留下一片永远空着的图框。
    if (typeof IntersectionObserver === 'undefined') {
      images.forEach(reveal)
      return
    }

    const observer = new IntersectionObserver(entries => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue
        const img = entry.target as HTMLImageElement
        observer.unobserve(img)
        reveal(img)
      }
    }, { rootMargin: '200px 0px' })
    images.forEach(img => observer.observe(img))
    return () => observer.disconnect()
  }, [containerRef, active])
}

/** hasBody 判断这条条目是否有正文可展开。
 *
 * 优先用服务端给的 has_content；缺失时（前端先上线、后端还是旧版本，
 * 或浏览器撞上旧响应缓存）退回按正文/摘要是否存在推断。
 * 少这一次兜底，两边版本错开的那一刻「展开正文」会静默消失。 */
function hasBody(entry: RSSEntry): boolean {
  return entry.has_content ?? Boolean(entry.content || entry.excerpt)
}

/** EntryContent 是条目的正文折叠区。
 *
 * 点击展开才请求 GET /rss/entries/:id/content——列表里只带摘要，
 * 正文可能几十上百 KB。加载成功后按「文本先、图片后」的顺序渲染。
 * 一次加载会记住结果，收起再展开不重复请求。
 *
 * inline 是旧版列表接口已经下发的正文：有它就直接用，不发请求。
 */
function EntryContent({ entryID, inline }: { entryID: number; inline?: string }) {
  const [open, setOpen] = useState(false)
  // 惰性初始化：只有旧接口才会带 inline，不必在每次渲染里解析 HTML。
  const [html, setHtml] = useState(() => (inline ? deferImages(inline) : ''))
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(Boolean(inline))
  const [error, setError] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)
  // 快速连点展开/收起会进两次 load，这里挡住并发的第二个请求。
  const loadingRef = useRef(false)

  async function load() {
    if (loaded || loadingRef.current) return
    loadingRef.current = true
    // 重试时先清掉上一次的错误：否则「正在读取」会被旧错误挡住，
    // 看起来像点了没反应。
    setError('')
    setLoading(true)
    try {
      const data = await rssApi.entryContent(entryID)
      setHtml(deferImages(data.content ?? ''))
      setLoaded(true)
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '正文加载失败')
    } finally {
      loadingRef.current = false
      setLoading(false)
    }
  }

  useLazyImages(containerRef, open && loaded)

  return <details
    className="rss-entry-detail"
    onToggle={event => {
      const next = event.currentTarget.open
      setOpen(next)
      if (next) void load()
    }}
  >
    <summary>{open ? '收起正文' : '展开正文'}</summary>
    {error
      ? <p className="rss-entry-content-empty is-error">{error}</p>
      : !loaded && loading
        ? <p className="rss-entry-content-empty">正在读取正文…</p>
        : loaded && (html.trim()
          ? <div ref={containerRef} className="rss-entry-content" dangerouslySetInnerHTML={{ __html: html }} />
          : <p className="rss-entry-content-empty">这条没有可显示的正文，可以打开原文查看。</p>)}
  </details>
}
