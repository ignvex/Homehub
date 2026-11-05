import { useMemo, useState } from 'react'
import { DeleteButton } from '@/components/Icon'
import { ConfirmDialog, Modal } from '@/components/WorkspaceUI'
import { rssApi, type BatchUpdateFeedBody, type RSSFeed, type RSSGroup } from '@/lib/rss'
import { relativeTime } from '@/lib/format'

// ============================================================
// RSS 管理窗口 — 订阅管理 / 分组管理
//
// 两个窗口做的是同一件事的两个方向：前者按「一组源」下手（多选后批量
// 改属性），后者按「一个组」下手（把源搬进搬出）。拆开是因为两者的
// 操作单位不同，硬塞进一个窗口会让「我选了 8 个源」和「我在看第 3 组」
// 这两个上下文互相打架。
// ============================================================

/** 两个管理窗口共用的接口。 */
interface ManageProps {
  open: boolean
  feeds: RSSFeed[]
  groups: RSSGroup[]
  busy: boolean
  /** 弹窗内的错误（由父级 run 写入）。 */
  error?: string
  onClose: () => void
  /** 执行一次写操作。父级负责 busy 态、成功后重载并提示。 */
  run: (action: () => Promise<unknown>, success: string) => Promise<boolean>
}

/** 订阅管理额外需要批量删除。 */
interface FeedManageProps extends ManageProps {
  /** 逐条串行删除并汇总结果，失败不会遮住已经成功的部分。 */
  deleteFeeds: (ids: number[]) => Promise<void>
}

/** feedLabel 是列表与下拉里统一的显示名。 */
function feedLabel(feed: RSSFeed): string {
  return feed.title || feed.url
}

/** feedState 给出行尾的一行状态描述。 */
function feedState(feed: RSSFeed): string {
  if (feed.fetch_enabled === false) return '已暂停'
  if (feed.error_msg) return '抓取失败'
  if (feed.fetch_by === 'extension') return 'Extension'
  return feed.last_fetch ? relativeTime(feed.last_fetch) : '未抓取'
}

// ============================================================
// 订阅管理
// ============================================================

/** 批量删除确认文案里最多列出的名称数。 */
const MAX_LISTED_NAMES = 5

export function RSSFeedManager({ open, feeds, groups, busy, error, onClose, run, deleteFeeds }: FeedManageProps) {
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<Set<number>>(() => new Set())
  const [targetGroup, setTargetGroup] = useState('')
  const [intervalSec, setIntervalSec] = useState('3600')
  const [pendingDelete, setPendingDelete] = useState(false)

  // 过滤只影响"看得到哪些"，不影响"选中了哪些"：
  // 先筛出几个再清空搜索框批量处理，是这类窗口的常见用法。
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return feeds
    return feeds.filter(feed =>
      feedLabel(feed).toLowerCase().includes(q) ||
      feed.url.toLowerCase().includes(q) ||
      (feed.remark ?? '').toLowerCase().includes(q))
  }, [feeds, query])

  // 从 feeds 反查而不是直接存选中的 label：源被删除或重命名后，
  // 选中集合里的陈旧 ID 不应该再被发去更新。
  const selectedIDs = useMemo(
    () => feeds.filter(feed => selected.has(feed.id)).map(feed => feed.id),
    [feeds, selected],
  )
  const allVisibleSelected = visible.length > 0 && visible.every(feed => selected.has(feed.id))

  function toggle(id: number) {
    setSelected(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function toggleAll() {
    setSelected(prev => {
      const next = new Set(prev)
      for (const feed of visible) {
        if (allVisibleSelected) next.delete(feed.id)
        else next.add(feed.id)
      }
      return next
    })
  }

  /** batch 把一次批量变更落到当前选中项上，成功后清空选中。 */
  async function batch(patch: Omit<BatchUpdateFeedBody, 'ids'>, success: string) {
    if (!selectedIDs.length) return
    const ids = selectedIDs
    const ok = await run(
      () => rssApi.batchUpdateFeeds({ ids, ...patch }),
      `${success}（${ids.length} 个）`,
    )
    if (ok) setSelected(new Set())
  }

  async function removeSelected() {
    const ids = selectedIDs
    setPendingDelete(false)
    if (!ids.length) return
    await deleteFeeds(ids)
    setSelected(new Set())
  }

  const groupName = (feed: RSSFeed) =>
    groups.find(group => group.id === feed.group_id)?.name ?? '未分组'

  // 关闭时清空选中与筛选：下次打开不应带着上次的勾选去批量操作。
  function close() {
    setSelected(new Set())
    setQuery('')
    onClose()
  }

  // 确认文案：列出名称（最多 5 个），并点出不在当前筛选结果里的数量——
  // 筛选后看不到的已选项最容易被误删。
  const selectedFeeds = feeds.filter(feed => selected.has(feed.id))
  const hiddenCount = selectedFeeds.filter(feed => !visible.includes(feed)).length
  const listedNames = selectedFeeds.slice(0, MAX_LISTED_NAMES).map(feed => `「${feedLabel(feed)}」`).join('、')
  const moreNames = selectedFeeds.length > MAX_LISTED_NAMES ? `等 ${selectedFeeds.length} 个` : ''
  const deleteDescription = `将删除 ${listedNames}${moreNames} 订阅源及其收录关联，其他源仍收录的内容会保留。`
    + (hiddenCount > 0 ? `其中 ${hiddenCount} 个不在当前筛选结果中。` : '')

  return <>
    <Modal
      open={open}
      title="订阅管理"
      description="勾选多个订阅源后批量修改分组、抓取方式或调度状态。"
      size="large"
      error={pendingDelete ? undefined : error}
      busy={busy}
      onClose={close}
      footer={<button className="button button--quiet" type="button" onClick={close} disabled={busy}>关闭</button>}
    >
      <div className="rss-manage">
        <div className="rss-manage-bar">
          <label className="rss-manage-search">
            <span className="visually-hidden">搜索订阅源</span>
            <input
              type="search"
              value={query}
              onChange={event => setQuery(event.target.value)}
              placeholder="按标题、URL 或备注筛选"
            />
          </label>
          <span className="rss-manage-count">
            {query
              ? `${visible.length} / ${feeds.length} 个源`
              : `${feeds.length} 个源`}
            {selectedIDs.length > 0 && ` · 已选 ${selectedIDs.length}`}
          </span>
        </div>

        {feeds.length === 0
          ? <p className="rss-manage-empty">还没有订阅源。</p>
          : visible.length === 0
            ? <p className="rss-manage-empty">没有匹配「{query}」的订阅源。</p>
            : <div className="rss-manage-table">
              <table className="data-table">
                <thead>
                  <tr>
                    <th className="rss-col-check">
                      <input
                        type="checkbox"
                        checked={allVisibleSelected}
                        // 部分选中时显示 indeterminate：只勾了几个源却看着
                        // 像没选，会让人再点一次全选，结果把已选的也取消。
                        ref={el => {
                          if (el) el.indeterminate = !allVisibleSelected && visible.some(feed => selected.has(feed.id))
                        }}
                        onChange={toggleAll}
                        aria-label={allVisibleSelected ? '取消全选' : '全选当前筛选结果'}
                      />
                    </th>
                    <th>订阅源</th>
                    <th className="rss-col-group">分组</th>
                    <th className="rss-col-mode">抓取</th>
                    <th className="rss-col-state">状态</th>
                  </tr>
                </thead>
                <tbody>
                  {visible.map(feed => <tr key={feed.id} className={selected.has(feed.id) ? 'is-selected' : undefined}>
                    <td className="rss-col-check" data-label="选择">
                      <input
                        type="checkbox"
                        checked={selected.has(feed.id)}
                        onChange={() => toggle(feed.id)}
                        aria-label={`选择 ${feedLabel(feed)}`}
                      />
                    </td>
                    <td data-label="订阅源">
                      <span className="rss-manage-name">{feedLabel(feed)}</span>
                      {feed.title && <small className="table-subtext">{feed.url}</small>}
                    </td>
                    <td className="rss-col-group" data-label="分组">{groupName(feed)}</td>
                    <td className="rss-col-mode" data-label="抓取">
                      {feed.fetch_by === 'extension'
                        ? <span className="rss-tag is-remote">Extension</span>
                        : <span className="rss-tag">本机</span>}
                    </td>
                    <td className="rss-col-state" data-label="状态">
                      <span className="rss-state-cell">
                        <span className={`status-dot ${feed.fetch_enabled === false ? 'is-warn' : feed.error_msg ? 'is-error' : 'is-ok'}`} aria-hidden="true" />
                        {feedState(feed)}
                      </span>
                    </td>
                  </tr>)}
                </tbody>
              </table>
            </div>}

        {/* 批量操作栏：没有选中时整栏淡下去但保留位置，
            否则窗口高度会随选中状态跳动。 */}
        <div className={`rss-batch${selectedIDs.length ? '' : ' is-idle'}`}>
          <div className="rss-batch-row">
            <span className="rss-batch-label">移入分组</span>
            <select value={targetGroup} onChange={event => setTargetGroup(event.target.value)} disabled={!selectedIDs.length}>
              <option value="">未分组</option>
              {groups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}
            </select>
            <button
              type="button"
              className="button button--quiet"
              disabled={!selectedIDs.length || busy}
              onClick={() => void batch(
                { group_id: targetGroup ? Number(targetGroup) : null },
                targetGroup ? `已移入「${groups.find(g => g.id === Number(targetGroup))?.name ?? '分组'}」` : '已移到未分组',
              )}
            >
              应用
            </button>
          </div>

          <div className="rss-batch-row">
            <span className="rss-batch-label">抓取方式</span>
            <button type="button" className="button button--quiet" disabled={!selectedIDs.length || busy} onClick={() => void batch({ fetch_by: 'local' }, '已改为本机抓取')}>本机</button>
            <button type="button" className="button button--quiet" disabled={!selectedIDs.length || busy} onClick={() => void batch({ fetch_by: 'extension' }, '已改为 Extension 抓取')}>Extension</button>
            <span className="rss-batch-sep" aria-hidden="true" />
            <button type="button" className="button button--quiet" disabled={!selectedIDs.length || busy} onClick={() => void batch({ fetch_enabled: false }, '已暂停')}>暂停</button>
            <button type="button" className="button button--quiet" disabled={!selectedIDs.length || busy} onClick={() => void batch({ fetch_enabled: true }, '已恢复')}>恢复</button>
          </div>

          <div className="rss-batch-row">
            <span className="rss-batch-label">抓取间隔</span>
            <input
              type="number"
              min="60"
              value={intervalSec}
              onChange={event => setIntervalSec(event.target.value)}
              disabled={!selectedIDs.length}
              aria-label="抓取间隔（秒）"
              className="rss-batch-interval"
            />
            <span className="rss-batch-unit">秒</span>
            <button
              type="button"
              className="button button--quiet"
              disabled={!selectedIDs.length || busy || !(Number(intervalSec) > 0)}
              onClick={() => void batch({ fetch_interval: Number(intervalSec) }, '已更新抓取间隔')}
            >
              应用
            </button>
            <span className="rss-batch-sep" aria-hidden="true" />
            <button
              type="button"
              className="button button--danger"
              disabled={!selectedIDs.length || busy}
              onClick={() => setPendingDelete(true)}
            >
              删除选中
            </button>
          </div>
        </div>
      </div>
    </Modal>

    <ConfirmDialog
      open={pendingDelete}
      title="删除订阅源"
      description={deleteDescription}
      confirmLabel="删除"
      danger
      busy={busy}
      error={error}
      onClose={() => setPendingDelete(false)}
      onConfirm={() => void removeSelected()}
    />
  </>
}

// ============================================================
// 分组管理
// ============================================================

export function RSSGroupManager({ open, feeds, groups, busy, error, onClose, run }: ManageProps) {
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set())
  const [newGroup, setNewGroup] = useState('')
  const [renaming, setRenaming] = useState<number | null>(null)
  const [renameValue, setRenameValue] = useState('')
  const [pendingDelete, setPendingDelete] = useState<RSSGroup | null>(null)
  // 每个区块各自的"待加入"选择：null 表示未分组区块。
  const [addPick, setAddPick] = useState<Record<string, string>>({})

  const loose = useMemo(() => feeds.filter(feed => !feed.group_id), [feeds])

  function toggle(id: number) {
    setExpanded(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  async function createGroup() {
    const name = newGroup.trim()
    if (!name) return
    const ok = await run(() => rssApi.createGroup(name), `分组「${name}」已创建`)
    if (ok) setNewGroup('')
  }

  async function commitRename(group: RSSGroup) {
    const name = renameValue.trim()
    if (!name || name === group.name) {
      setRenaming(null)
      return
    }
    const ok = await run(() => rssApi.updateGroup(group.id, { name }), '分组已重命名')
    if (ok) setRenaming(null)
  }

  /** move 把源移入分组（null = 未分组）。 */
  async function move(feed: RSSFeed, groupID: number | null) {
    const ok = await run(
      () => rssApi.updateFeed(feed.id, { group_id: groupID }),
      groupID === null
        ? `「${feedLabel(feed)}」已移出分组`
        : `「${feedLabel(feed)}」已移入「${groups.find(g => g.id === groupID)?.name ?? '分组'}」`,
    )
    if (ok) {
      const key = String(groupID ?? 'loose')
      setAddPick(prev => ({ ...prev, [key]: '' }))
    }
  }

  async function removeGroup() {
    if (!pendingDelete) return
    const group = pendingDelete
    const ok = await run(() => rssApi.deleteGroup(group.id), '分组已删除，其下订阅源已移回未分组')
    if (ok) setPendingDelete(null)
  }

  /** candidates 返回还没在该分组里的源，供"添加到本组"下拉使用。 */
  function candidates(groupID: number | null): RSSFeed[] {
    return feeds.filter(feed => (feed.group_id ?? null) !== groupID)
  }

  /** 一个分组区块。group 为 null 时渲染"未分组"。 */
  function renderBlock(group: RSSGroup | null) {
    const groupID = group?.id ?? null
    const key = String(groupID ?? 'loose')
    const members = group ? feeds.filter(feed => feed.group_id === group.id) : loose
    const isOpen = group ? expanded.has(group.id) : expanded.has(-1)
    const pool = candidates(groupID)
    return <div className="rss-group-card" key={key}>
      <div className="rss-group-card-head">
        {/* 重命名时整条头行换成输入框：折叠按钮与输入框并排会在
            同一行里争宽度，而且「点名字折叠」和「点名字改名」两个
            意图会撞在一起。 */}
        {group && renaming === group.id
          ? <>
            <span className="rss-group-arrow" aria-hidden="true">▾</span>
            <div className="rss-group-rename">
              <input
                autoFocus
                value={renameValue}
                onChange={event => setRenameValue(event.target.value)}
                onKeyDown={event => {
                  if (event.key === 'Enter') { event.preventDefault(); void commitRename(group) }
                  // 只取消重命名：不阻止的话 Esc 会冒泡到 dialog，把整个管理窗口关掉。
                  if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setRenaming(null) }
                }}
                maxLength={100}
                aria-label="分组名称"
              />
              <button className="button button--quiet" type="button" disabled={busy} onClick={() => void commitRename(group)}>保存</button>
              <button className="button button--quiet" type="button" disabled={busy} onClick={() => setRenaming(null)}>取消</button>
            </div>
          </>
          : <>
            <button
              type="button"
              className="rss-group-toggle"
              aria-expanded={isOpen}
              onClick={() => toggle(group ? group.id : -1)}
            >
              <span className="rss-group-arrow" aria-hidden="true">▾</span>
              <strong>{group ? group.name : '未分组'}</strong>
              <span className="rss-nav-count">{members.length}</span>
            </button>
            {group && <div className="rss-group-card-actions">
              <button
                className="icon-button"
                type="button"
                aria-label={`重命名分组 ${group.name}`}
                onClick={() => { setRenaming(group.id); setRenameValue(group.name) }}
              >✎</button>
              <DeleteButton label={`删除分组「${group.name}」`} onClick={() => setPendingDelete(group)} />
            </div>}
          </>}
      </div>

      {isOpen && <div className="rss-group-card-body">
        {members.length === 0
          ? <p className="rss-manage-empty">这个分组还没有订阅源。</p>
          : <ul className="rss-group-members">
            {members.map(feed => <li key={feed.id}>
              <span className={`status-dot ${feed.fetch_enabled === false ? 'is-warn' : feed.error_msg ? 'is-error' : 'is-ok'}`} aria-hidden="true" />
              <span className="rss-group-member-name" title={feed.url}>{feedLabel(feed)}</span>
              {group && <button
                type="button"
                className="button button--quiet"
                disabled={busy}
                onClick={() => void move(feed, null)}
              >移出</button>}
            </li>)}
          </ul>}

        {/* 添加：只列还不在本组里的源。组内已有的源不出现在下拉里，
            比"选完之后提示已经在组里"少一次无意义的往返。 */}
        {pool.length > 0 && <div className="rss-group-add">
          <label>
            <span className="visually-hidden">选择要加入的订阅源</span>
            <select
              value={addPick[key] ?? ''}
              onChange={event => setAddPick(prev => ({ ...prev, [key]: event.target.value }))}
              disabled={busy}
            >
              <option value="">选择要加入的订阅源…</option>
              {pool.map(feed => <option key={feed.id} value={feed.id}>{feedLabel(feed)}</option>)}
            </select>
          </label>
          <button
            type="button"
            className="button button--quiet"
            disabled={busy || !addPick[key]}
            onClick={() => {
              const feed = pool.find(item => item.id === Number(addPick[key]))
              if (feed) {
                void move(feed, groupID)
                return
              }
              // 选中项可能已经被别的操作移进本组（或整个被删掉），
              // 此时下拉里已没有它。清掉陈旧选择，别让按钮看起来
              // 可点却什么也不发生。
              setAddPick(prev => ({ ...prev, [key]: '' }))
            }}
          >添加</button>
        </div>}
      </div>}
    </div>
  }

  return <>
    <Modal
      open={open}
      title="管理分组"
      description="展开分组可以添加或移出订阅源；删除分组时其下订阅源会移回未分组。"
      size="large"
      error={pendingDelete ? undefined : error}
      busy={busy}
      onClose={onClose}
      footer={<button className="button button--quiet" type="button" onClick={onClose} disabled={busy}>关闭</button>}
    >
      <div className="rss-group-manage">
        <form
          className="rss-group-create"
          onSubmit={event => { event.preventDefault(); void createGroup() }}
        >
          <input
            value={newGroup}
            onChange={event => setNewGroup(event.target.value)}
            placeholder="新分组名称，例如 动漫、技术博客"
            maxLength={100}
            aria-label="新分组名称"
          />
          <button className="button button--primary" disabled={busy || !newGroup.trim()}>创建分组</button>
        </form>

        <div className="rss-group-cards">
          {groups.map(group => renderBlock(group))}
          {renderBlock(null)}
        </div>
      </div>
    </Modal>

    <ConfirmDialog
      open={pendingDelete !== null}
      title="删除分组"
      description={`将删除分组「${pendingDelete?.name ?? ''}」，其下订阅源会移回未分组。`}
      confirmLabel="删除"
      danger
      busy={busy}
      error={error}
      onClose={() => setPendingDelete(null)}
      onConfirm={() => void removeGroup()}
    />
  </>
}
