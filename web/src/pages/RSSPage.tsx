import { useEffect, useMemo, useRef, useState, type FormEvent, type RefObject } from 'react'
import { DeleteButton, StarIcon } from '@/components/Icon'
import { ImportFileButton, ImportResultModal } from '@/components/ImportResult'
import { ConfirmDialog, Modal, PageState, Pagination } from '@/components/WorkspaceUI'
import { useHotkeys, type Hotkey } from '@/hooks/useHotkeys'
import { errorMessage, useNotice } from '@/hooks/useNotice'
import { useQueryNumber, useQueryState } from '@/hooks/useQueryState'
import { downloadFile, isAbortError } from '@/lib/api'
import { useAuth } from '@/lib/auth'
import { CONTENT_WRITE_LEVEL, hasLevel } from '@/lib/permissions'
import { rssApi, type EntryState, type FetchBy, type MarkReadBody, type OPMLImportResult, type RSSEntry, type RSSFeed, type RSSGroup, type UnreadCounts } from '@/lib/rss'
import { RSSFeedManager, RSSGroupManager } from '@/pages/RSSManage'
import { extensionHubApi, type RemoteSubscription } from '@/lib/extensionHub'
import { MagnetCopyButton } from '@/components/MagnetCopyButton'
import { externalLinkProps, isMagnetURL, isUsableTimestamp, relativeTime } from '@/lib/format'

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

/** 快捷键在帮助弹窗里的分组名。 */
const RSS_HOTKEY_GROUP = '订阅阅读'

/** 阅读状态筛选。空串是「全部」，不写进 URL。 */
const entryStates: { value: EntryState; label: string }[] = [
  { value: '', label: '全部' },
  { value: 'unread', label: '未读' },
  { value: 'starred', label: '星标' },
]

/** isEntryRead 把缺失的 is_read 当作已读：旧后端不下发时，整页都标成未读只会误导。 */
function isEntryRead(entry: RSSEntry): boolean {
  return entry.is_read !== false
}

/** 计数延迟重拉的间隔：连续 j/m 标记时只拉一次，用服务端数字校正本地调整。 */
const COUNTS_REFRESH_DELAY_MS = 1500

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
  const [stateParam, setStateParam] = useQueryState('state', '')
  const entryState = (entryStates.some(item => item.value === stateParam) ? stateParam : '') as EntryState
  // 未读计数：侧栏的每个源、每个分组与「全部条目」都从这一份读。
  const [unread, setUnread] = useState<UnreadCounts | null>(null)
  const countsSeq = useRef(0)
  // 每个条目最近一次已读/星标操作的序号；旧请求失败时不回滚新状态。
  const opSeq = useRef(new Map<string, number>())
  const countsTimer = useRef<number | undefined>(undefined)
  // 键盘选中的条目下标（-1 = 未选中）与已展开正文的条目。
  const [selectedIndex, setSelectedIndex] = useState(-1)
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set())
  const entryRefs = useRef(new Map<number, HTMLElement>())
  // 最新的条目状态：快捷键与点击拿到的 entry 可能是上一次渲染的快照，
  // 连按 m 时若按快照判断，同一条会被重复计数。
  const latestEntries = useRef<RSSEntry[]>([])
  latestEntries.current = entries
  const [confirmMarkAll, setConfirmMarkAll] = useState(false)
  const [opmlResult, setOpmlResult] = useState<OPMLImportResult | null>(null)
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
      void loadCounts()
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

  /** loadCounts 取未读计数。只认最后一次请求的结果，慢的旧响应不能覆盖新数字。 */
  async function loadCounts() {
    window.clearTimeout(countsTimer.current)
    const seq = ++countsSeq.current
    try {
      const counts = await rssApi.unreadCounts()
      if (seq === countsSeq.current) setUnread({ total: counts.total ?? 0, by_feed: counts.by_feed ?? {}, by_group: counts.by_group ?? {} })
    } catch {
      // 计数只是辅助信息：失败时保留上一份，不打断阅读。
    }
  }

  /** scheduleCounts 在一串单条标记之后重拉一次计数。 */
  function scheduleCounts() {
    window.clearTimeout(countsTimer.current)
    countsTimer.current = window.setTimeout(() => void loadCounts(), COUNTS_REFRESH_DELAY_MS)
  }

  /** adjustCounts 按一条条目的已读变化本地调整计数（delta = -1 标已读，+1 标未读）。
   *
   * 服务端按源计数不去重、按分组在组内去重、总数全局去重，这里照同样的口径算，
   * 稍后再由 scheduleCounts 用服务端数字校正。 */
  function adjustCounts(entry: RSSEntry, delta: number) {
    // 失败回滚的那一次调整也要让在途的计数响应作废，否则它会盖掉回滚结果。
    countsSeq.current += 1
    setUnread(current => {
      if (!current) return current
      const byFeed = { ...current.by_feed }
      const byGroup = { ...current.by_group }
      const touchedGroups = new Set<number>()
      for (const id of entry.feed_ids ?? []) {
        byFeed[id] = Math.max(0, (byFeed[id] ?? 0) + delta)
        const groupID = feeds.find(feed => feed.id === id)?.group_id
        if (groupID) touchedGroups.add(groupID)
      }
      for (const id of touchedGroups) byGroup[id] = Math.max(0, (byGroup[id] ?? 0) + delta)
      return { total: Math.max(0, current.total + delta), by_feed: byFeed, by_group: byGroup }
    })
  }

  /** nextOp 为某条目的已读/星标操作分配递增序号，用来判断失败回滚是否已过时。 */
  function nextOp(kind: 'read' | 'star', id: number): number {
    const key = `${kind}:${id}`
    const seq = (opSeq.current.get(key) ?? 0) + 1
    opSeq.current.set(key, seq)
    return seq
  }

  function patchEntry(id: number, patch: Partial<RSSEntry>) {
    setEntries(list => list.map(item => item.id === id ? { ...item, ...patch } : item))
  }

  /** setEntryRead 乐观更新已读状态；请求失败时回滚列表与计数。 */
  async function setEntryRead(snapshot: RSSEntry, read: boolean) {
    const entry = latestEntries.current.find(item => item.id === snapshot.id) ?? snapshot
    if (isEntryRead(entry) === read) return
    const seq = nextOp('read', entry.id)
    latestEntries.current = latestEntries.current.map(item => item.id === entry.id ? { ...item, is_read: read } : item)
    patchEntry(entry.id, { is_read: read })
    adjustCounts(entry, read ? -1 : 1)
    try {
      await rssApi.setRead(entry.id, read)
      scheduleCounts()
    } catch (cause) {
      // 只有这次仍是最后一次操作时才回滚，否则会盖掉之后的切换。
      // 计数不在本地反向调整：本地值被 Math.max(0) 截过，反加回去可能多出 1，
      // 直接以服务端为准。
      if (opSeq.current.get(`read:${entry.id}`) === seq) {
        latestEntries.current = latestEntries.current.map(item => item.id === entry.id ? { ...item, is_read: !read } : item)
        patchEntry(entry.id, { is_read: !read })
      }
      void loadCounts()
      notice.error(errorMessage(cause, read ? '标记已读失败' : '标记未读失败'))
    }
  }

  /** toggleStar 乐观切换星标；失败回滚。 */
  async function toggleStar(snapshot: RSSEntry) {
    const entry = latestEntries.current.find(item => item.id === snapshot.id) ?? snapshot
    const next = !entry.is_starred
    const seq = nextOp('star', entry.id)
    latestEntries.current = latestEntries.current.map(item => item.id === entry.id ? { ...item, is_starred: next } : item)
    patchEntry(entry.id, { is_starred: next })
    try {
      await rssApi.setStarred(entry.id, next)
    } catch (cause) {
      if (opSeq.current.get(`star:${entry.id}`) === seq) {
        latestEntries.current = latestEntries.current.map(item => item.id === entry.id ? { ...item, is_starred: !next } : item)
        patchEntry(entry.id, { is_starred: !next })
      }
      notice.error(errorMessage(cause, next ? '加星标失败' : '取消星标失败'))
    }
  }

  /** toggleExpanded 展开或收起正文；展开即视为读过。 */
  function toggleExpanded(entry: RSSEntry, open = !expanded.has(entry.id)) {
    setExpanded(current => {
      if (current.has(entry.id) === open) return current
      const next = new Set(current)
      if (open) next.add(entry.id)
      else next.delete(entry.id)
      return next
    })
    if (open) void setEntryRead(entry, true)
  }

  /** openOriginal 在新标签打开原文并标为已读。 */
  function openOriginal(entry: RSSEntry) {
    if (!entry.link) return
    // 磁力链接就地交给协议处理器，不弹空白标签页。
    if (isMagnetURL(entry.link)) window.location.assign(entry.link)
    else window.open(entry.link, '_blank', 'noopener,noreferrer')
    void setEntryRead(entry, true)
  }

  /** scopeBody 是「全部标为已读」作用的范围：当前选中的源、分组或全部。 */
  function scopeBody(): MarkReadBody {
    if (selectedFeed) return { feed_id: selectedFeed }
    if (selectedGroup) return { group_id: selectedGroup }
    return {}
  }

  async function markAllRead() {
    setBusy(true)
    setFormError('')
    try {
      const { marked } = await rssApi.markRead(scopeBody())
      setConfirmMarkAll(false)
      notice.success(marked ? `已将 ${marked.toLocaleString()} 条标为已读` : '没有需要标记的未读条目')
      // 当前页的条目都在这个范围里，直接置为已读；未读视图则整页重拉。
      setEntries(list => list.map(item => ({ ...item, is_read: true })))
      await loadCounts()
      if (entryState === 'unread') await loadEntries()
    } catch (cause) {
      setFormError(errorMessage(cause, '标记已读失败'))
    } finally {
      setBusy(false)
    }
  }

  async function exportOPML() {
    try {
      await downloadFile('/rss/opml', 'homehub-feeds.opml')
    } catch (cause) {
      notice.error(errorMessage(cause, '导出 OPML 失败'))
    }
  }

  async function importOPML(file: File) {
    setBusy(true)
    try {
      const result = await rssApi.importOPML(file)
      setOpmlResult({ ...result, errors: result.errors ?? [] })
      await loadFeeds(true)
      await loadEntries()
    } catch (cause) {
      notice.error(errorMessage(cause, '导入 OPML 失败'))
    } finally {
      setBusy(false)
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
  async function loadEntries(feed = selectedFeed, group = selectedGroup, targetPage = page, state = entryState) {
    entriesAbort.current?.abort()
    const controller = new AbortController()
    entriesAbort.current = controller
    setEntriesLoading(true)
    try {
      const result = await rssApi.listEntries(feed, group, targetPage, perPage, { signal: controller.signal }, state)
      setEntries(result.list ?? [])
      setTotal(result.total ?? 0)
      setEntriesError('')
      setSelectedIndex(-1)
      setExpanded(new Set())
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
  useEffect(() => { void loadEntries(selectedFeed, selectedGroup, page, entryState) }, [selectedFeed, selectedGroup, page, entryState])
  useEffect(() => () => { entriesAbort.current?.abort(); window.clearTimeout(countsTimer.current) }, [])

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

  function selectState(next: EntryState) { setStateParam(next); setPage(1) }

  // ---------- 键盘阅读 ----------
  const active = selectedIndex >= 0 ? entries[selectedIndex] ?? null : null
  const pages = Math.max(1, Math.ceil(total / perPage))

  /** focusEntry 选中第 index 条并把它滚到可见。 */
  function focusEntry(index: number) {
    if (!entries.length) return
    const next = Math.min(Math.max(index, 0), entries.length - 1)
    setSelectedIndex(next)
    const element = entryRefs.current.get(entries[next].id)
    element?.focus({ preventScroll: true })
    element?.scrollIntoView({ block: 'nearest' })
  }

  const rssHotkeys: Hotkey[] = [
    { keys: 'j', description: '下一条', group: RSS_HOTKEY_GROUP, handler: () => focusEntry(selectedIndex + 1) },
    { keys: 'k', description: '上一条', group: RSS_HOTKEY_GROUP, handler: () => focusEntry(selectedIndex < 0 ? 0 : selectedIndex - 1) },
    { keys: 'o', description: '展开 / 收起当前条', group: RSS_HOTKEY_GROUP, handler: () => { if (active) toggleExpanded(active) } },
    // Enter 只在焦点落在条目上时生效（由条目自己处理），否则会吞掉按钮的回车。
    { keys: 'Enter', description: '展开 / 收起当前条（焦点在条目上时）', group: RSS_HOTKEY_GROUP },
    { keys: 's', description: '切换星标', group: RSS_HOTKEY_GROUP, handler: () => { if (active) void toggleStar(active) } },
    { keys: 'm', description: '切换已读 / 未读', group: RSS_HOTKEY_GROUP, handler: () => { if (active) void setEntryRead(active, !isEntryRead(active)) } },
    { keys: 'v', description: '新标签打开原文', group: RSS_HOTKEY_GROUP, handler: () => { if (active) openOriginal(active) } },
    { keys: 'Shift+A', description: '当前范围全部标为已读', group: RSS_HOTKEY_GROUP, handler: () => { if (feeds.length) { setFormError(''); setConfirmMarkAll(true) } } },
    { keys: 'r', description: '刷新列表', group: RSS_HOTKEY_GROUP, handler: () => { void loadEntries(); void loadCounts() } },
    { keys: '[', description: '上一页', group: RSS_HOTKEY_GROUP, handler: () => { if (page > 1 && !entriesLoading) setPage(page - 1) } },
    { keys: ']', description: '下一页', group: RSS_HOTKEY_GROUP, handler: () => { if (page < pages && !entriesLoading) setPage(page + 1) } },
  ]
  useHotkeys(rssHotkeys)

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
  const scopeUnread = unread
    ? selectedFeed ? unread.by_feed[selectedFeed] ?? 0 : selectedGroup ? unread.by_group[selectedGroup] ?? 0 : unread.total
    : null
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
        action={canWrite ? <div className="toolbar-actions">
          <button className="button button--primary" onClick={() => setFeedDraft({ ...emptyFeedDraft })}>添加第一个订阅源</button>
          <ImportFileButton label="导入 OPML" accept=".opml,.xml,text/x-opml,text/xml,application/xml" busy={busy} onFile={file => void importOPML(file)} />
        </div> : undefined}
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
          {canWrite && <span className="rss-tool-sep" aria-hidden="true" />}
          <button type="button" className="rss-tool" onClick={() => void exportOPML()}>导出 OPML</button>
          {canWrite && <ImportFileButton className="rss-tool" label="导入 OPML" accept=".opml,.xml,text/x-opml,text/xml,application/xml" busy={busy} onFile={file => void importOPML(file)} />}
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
              <UnreadBadge count={unread?.total} />
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
                  <UnreadBadge count={unread?.by_group[group.id]} />
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
                      unread={unread?.by_feed[feed.id]}
                      onSelect={() => selectFeed(feed.id)}
                      dragging={draggingFeed === feed.id}
                      draggable={canWrite}
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
                  unread={unread?.by_feed[feed.id]}
                  onSelect={() => selectFeed(feed.id)}
                  dragging={draggingFeed === feed.id}
                  draggable={canWrite}
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
              <p>
                {total.toLocaleString()} 条{entryState === 'unread' ? '未读' : entryState === 'starred' ? '星标' : '内容'}
                {scopeUnread !== null && entryState !== 'unread' ? ` · ${scopeUnread.toLocaleString()} 未读` : ''}
                {activeFeed?.last_fetch ? ` · 最近抓取 ${relativeTime(activeFeed.last_fetch)}` : ''}
              </p>
            </div>
            <div className="rss-main-actions">
              <div className="segmented" role="radiogroup" aria-label="阅读状态">
                {entryStates.map(option => <label key={option.value || 'all'} className={entryState === option.value ? 'is-on' : ''}>
                  <input
                    type="radio"
                    name="rss-entry-state"
                    className="visually-hidden"
                    value={option.value}
                    checked={entryState === option.value}
                    onChange={() => selectState(option.value)}
                  />
                  {option.label}
                </label>)}
              </div>
              <button
                type="button"
                className="rss-tool"
                disabled={busy || scopeUnread === 0}
                onClick={() => { setFormError(''); setConfirmMarkAll(true) }}
              >
                全部标为已读
              </button>
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
                title={entryState === 'unread' ? '没有未读条目' : entryState === 'starred' ? '没有星标条目' : '没有可显示的条目'}
                description={entryState ? '切换到「全部」查看其余条目。' : activeFeed ? '这个订阅源还没有抓到文章，试试手动抓取。' : '切换订阅源，或调整筛选范围。'}
              />
              : <div className={`rss-entries${entriesLoading ? ' is-stale' : ''}`} ref={entriesRef} aria-busy={entriesLoading}>
                {entries.map((entry, index) => {
                  const read = isEntryRead(entry)
                  return <article
                  className={`rss-entry${read ? '' : ' is-unread'}${index === selectedIndex ? ' is-selected' : ''}`}
                  key={entry.id}
                  tabIndex={-1}
                  aria-current={index === selectedIndex || undefined}
                  ref={element => {
                    if (element) entryRefs.current.set(entry.id, element)
                    else entryRefs.current.delete(entry.id)
                  }}
                  onClick={() => setSelectedIndex(index)}
                  onKeyDown={event => {
                    if (event.key !== 'Enter' || event.target !== event.currentTarget) return
                    event.preventDefault()
                    toggleExpanded(entry)
                  }}
                >
                  <span className={`rss-entry-dot${read ? '' : ' is-unread'}`} aria-hidden="true" />
                  <div className="rss-entry-body">
                    <a className="rss-entry-title" href={entry.link} {...externalLinkProps(entry.link)} onClick={() => void setEntryRead(entry, true)}>
                      {!read && <span className="visually-hidden">未读：</span>}
                      {entry.title}
                    </a>
                    <div className="rss-entry-meta">
                      {entry.author && <><span>{entry.author}</span><span className="meta-dot" aria-hidden="true">·</span></>}
                      <time dateTime={isUsableTimestamp(entry.published) ? entry.published : undefined}>{relativeTime(entry.published) || '时间未知'}</time>
                      {!selectedFeed && entry.feed_ids?.length > 0 && <>
                        <span className="meta-dot" aria-hidden="true">·</span>
                        <span className="rss-entry-source">{feedTitles(entry.feed_ids)}</span>
                      </>}
                    </div>
                    {hasBody(entry) && <EntryContent entryID={entry.id} inline={entry.content} open={expanded.has(entry.id)} onOpenChange={open => toggleExpanded(entry, open)} />}
                  </div>
                  <div className="rss-entry-actions">
                    <button
                      type="button"
                      className={`icon-button rss-star${entry.is_starred ? ' is-on' : ''}`}
                      aria-pressed={Boolean(entry.is_starred)}
                      aria-label={`星标「${entry.title}」`}
                      title={entry.is_starred ? '取消星标' : '加星标'}
                      onClick={() => void toggleStar(entry)}
                    >
                      <StarIcon />
                    </button>
                    <button
                      type="button"
                      className="button button--quiet"
                      aria-label={`将「${entry.title}」标为${read ? '未读' : '已读'}`}
                      onClick={() => void setEntryRead(entry, !read)}
                    >
                      {read ? '标为未读' : '标为已读'}
                    </button>
                  </div>
                  {entry.magnet_url && <MagnetCopyButton
                    className="rss-magnet"
                    url={entry.magnet_url}
                    title={entry.title}
                    onCopied={ok => ok ? notice.success('磁力链接已复制') : notice.info('自动复制不可用，已选中链接，按 Ctrl+C 复制')}
                  />}
                </article>
                })}
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
      open={confirmMarkAll}
      title="全部标为已读"
      description={`将把${activeFeed ? `订阅源「${activeFeed.title || activeFeed.url}」` : activeGroup ? `分组「${activeGroup.name}」` : '全部订阅源'}的${scopeUnread !== null ? ` ${scopeUnread.toLocaleString()} 条` : ''}未读条目标为已读。之后仍可逐条标回未读。`}
      confirmLabel="标为已读"
      busy={busy}
      error={formError}
      onClose={() => { setConfirmMarkAll(false); setFormError('') }}
      onConfirm={() => void markAllRead()}
    />

    <ImportResultModal title="OPML 导入结果" unit="个订阅源" result={opmlResult} onClose={() => setOpmlResult(null)} />

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
/** UnreadBadge 是侧栏的未读数；0 或未知时不渲染。 */
function UnreadBadge({ count }: { count?: number }) {
  if (!count) return null
  return <span className="rss-unread" title={`${count} 条未读`}>
    {count > 999 ? '999+' : count}
    <span className="visually-hidden"> 条未读</span>
  </span>
}

function FeedNavItem({ feed, active, remote, unread, dragging, draggable, onSelect, onDragStart, onDragEnd }: {
  feed: RSSFeed
  active: boolean
  /** 这个源的未读数。 */
  unread?: number
  /** 只有内容写权限（≤3 级）才能拖拽归组。 */
  draggable: boolean
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
    className={`rss-nav-item${draggable ? ' rss-draggable' : ''}${active ? ' is-active' : ''}${dragging ? ' is-dragging' : ''}`}
    onClick={onSelect}
    title={hint || title}
    draggable={draggable}
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
    <UnreadBadge count={unread} />
    <span className="rss-nav-count">{meta}</span>
  </button>
}

/** prepareContent 在渲染前整理正文：图片推迟到进入视口再请求，磁力链接不开新窗口。
 *
 * 这是分段加载的第二段：正文 HTML 到达后先渲染文字，img 的 src 暂存到
 * data-src，由 useLazyImages 在图片滚进视口时才写回。文章带几十张图时，
 * 展开的瞬间不会同时打满连接，也不会把正文排版挤到图片加载完才出现。
 *
 * 顺手去掉磁力链接上的 target：magnet 在新标签页里打开只会多出一个空白窗口，
 * PikPak 一类扩展在原页面就能接住点击。后端清洗已经会剥掉它，这里兜的是
 * 清洗规则加入前就已入库的正文。
 */
function prepareContent(html: string): string {
  const doc = new DOMParser().parseFromString(html, 'text/html')
  for (const link of Array.from(doc.body.querySelectorAll('a[target]'))) {
    if (isMagnetURL(link.getAttribute('href') ?? undefined)) link.removeAttribute('target')
  }
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
function EntryContent({ entryID, inline, open, onOpenChange }: {
  entryID: number
  inline?: string
  /** 展开状态由列表持有：快捷键 o / Enter 与点击 summary 改的是同一份。 */
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  // 惰性初始化：只有旧接口才会带 inline，不必在每次渲染里解析 HTML。
  const [html, setHtml] = useState(() => (inline ? prepareContent(inline) : ''))
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
      setHtml(prepareContent(data.content ?? ''))
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
  // 由快捷键展开时不会经过 summary 的点击，所以按 open 拉正文而不是在 onToggle 里拉。
  // oxlint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { if (open) void load() }, [open])

  return <details
    className="rss-entry-detail"
    open={open}
    onToggle={event => {
      const next = event.currentTarget.open
      if (next !== open) onOpenChange(next)
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
