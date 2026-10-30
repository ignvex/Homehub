import { useEffect, useState, type FormEvent } from 'react'
import { ConfirmDialog, Modal, Notice, PageState, Pagination, StatusBadge } from '@/components/WorkspaceUI'
import { rssApi, type RSSEntry, type RSSFeed, type RSSGroup } from '@/lib/rss'
import { formatDateTime } from '@/lib/types'

const perPage = 20

export function RSSPage() {
  const [feeds, setFeeds] = useState<RSSFeed[]>([])
  const [groups, setGroups] = useState<RSSGroup[]>([])
  const [entries, setEntries] = useState<RSSEntry[]>([])
  const [selectedFeed, setSelectedFeed] = useState<number | null>(null)
  const [selectedGroup, setSelectedGroup] = useState<number | null>(null)
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [entriesLoading, setEntriesLoading] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [newURL, setNewURL] = useState('')
  const [newGroup, setNewGroup] = useState('')
  const [editFeed, setEditFeed] = useState<RSSFeed | null>(null)
  const [editGroup, setEditGroup] = useState<RSSGroup | null>(null)
  const [pendingDeleteFeed, setPendingDeleteFeed] = useState<RSSFeed | null>(null)
  const [pendingDeleteGroup, setPendingDeleteGroup] = useState<RSSGroup | null>(null)

  async function loadFeeds() {
    setLoading(true)
    try {
      const [feedList, groupList] = await Promise.all([rssApi.listFeeds(), rssApi.listGroups()])
      setFeeds(feedList); setGroups(groupList); setError('')
    } catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取订阅源') }
    finally { setLoading(false) }
  }
  async function loadEntries(nextFeed = selectedFeed, nextGroup = selectedGroup, nextPage = page) {
    setEntriesLoading(true)
    try { const result = await rssApi.listEntries(nextFeed, nextGroup, nextPage, perPage); setEntries(result.list); setTotal(result.total); setError('') }
    catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取订阅条目') }
    finally { setEntriesLoading(false) }
  }
  useEffect(() => { void loadFeeds() }, [])
  useEffect(() => { void loadEntries(selectedFeed, selectedGroup, page) }, [selectedFeed, selectedGroup, page])

  async function run(action: () => Promise<unknown>, success: string, reloadEntries = false) {
    setBusy(true)
    try { await action(); setMessage(success); await loadFeeds(); if (reloadEntries) await loadEntries(); return true }
    catch (cause) { setError(cause instanceof Error ? cause.message : '操作失败'); return false }
    finally { setBusy(false) }
  }
  async function addFeed(event: FormEvent) {
    event.preventDefault()
    const saved = await run(() => rssApi.createFeed(newURL.trim()), '订阅源已添加')
    if (saved) setNewURL('')
  }
  async function saveFeed(event: FormEvent) {
    event.preventDefault()
    if (!editFeed) return
    const saved = await run(() => rssApi.updateFeed(editFeed.id, { title: editFeed.title.trim(), fetch_interval: editFeed.fetch_interval, group_id: editFeed.group_id }), '订阅源已更新')
    if (saved) setEditFeed(null)
  }
  async function removeFeed() {
    if (!pendingDeleteFeed) return
    const saved = await run(() => rssApi.deleteFeed(pendingDeleteFeed.id), '订阅源及其条目已删除', selectedFeed === pendingDeleteFeed.id)
    if (saved) { if (selectedFeed === pendingDeleteFeed.id) { setSelectedFeed(null); setPage(1) }; setPendingDeleteFeed(null) }
  }
  async function addGroup(event: FormEvent) {
    event.preventDefault()
    if (!newGroup.trim()) return
    const saved = await run(() => rssApi.createGroup(newGroup.trim()), '分组已创建')
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
    const saved = await run(() => rssApi.deleteGroup(pendingDeleteGroup.id), '分组已删除，其下订阅源已移回未分组')
    if (saved) setPendingDeleteGroup(null)
  }
  function selectFeed(value: number | null) { setSelectedFeed(value); setSelectedGroup(null); setPage(1) }
  function selectGroup(value: number | null) { setSelectedGroup(value); setSelectedFeed(null); setPage(1) }
  function feedGroupName(feed: RSSFeed): string { return feed.group_id ? groups.find(g => g.id === feed.group_id)?.name || '未分组' : '未分组' }
  // 一条内容可能被多个源收录，全部列出来；源被删但历史条目还在时只显示 ID。
  function feedTitles(feedIDs?: number[]): string {
    if (!feedIDs?.length) return '—'
    return feedIDs.map(id => feeds.find(feed => feed.id === id)?.title || `#${id}`).join('、')
  }

  if (loading) return <PageState kind="loading" title="正在读取订阅源" />
  if (error && !feeds.length) return <PageState kind="error" title="无法读取订阅源" description={error} action={<button className="button button--primary" onClick={() => void loadFeeds()}>重试</button>} />
  return <div className="business-page">
    <div className="page-toolbar"><div><p>内容订阅</p><h2>RSS 阅读与管理</h2></div><div className="toolbar-actions"><button className="button button--quiet" disabled={busy || !feeds.length} onClick={() => void run(() => rssApi.fetchAll(), '已完成全部订阅源抓取', true)}>抓取全部</button><button className="button button--primary" onClick={() => document.getElementById('rss-url')?.focus()}>添加订阅源</button></div></div>
    <Notice message={error} tone="error" onDismiss={() => setError('')} /><Notice message={message} onDismiss={() => setMessage('')} />
    <form className="filter-bar" onSubmit={addFeed}><input id="rss-url" type="url" value={newURL} placeholder="https://example.com/feed.xml" onChange={event => setNewURL(event.target.value)} required /><button className="button button--primary" disabled={busy}>添加订阅</button></form>
    {!feeds.length ? <PageState kind="empty" title="还没有订阅源" description="添加 RSS 或 Atom 链接后，可在此阅读和抓取内容。" /> : <><section className="data-panel"><header className="data-panel-head"><div><h2>订阅源</h2><p>选择一个来源查看其文章，或切换到全部条目。</p></div><select aria-label="筛选订阅源" value={selectedFeed ?? ''} onChange={event => selectFeed(event.target.value ? Number(event.target.value) : null)}><option value="">全部订阅源</option>{feeds.map(feed => <option key={feed.id} value={feed.id}>{feed.title || feed.url}</option>)}</select></header><div className="table-wrap"><table className="data-table"><thead><tr><th>订阅源</th><th>分组</th><th>抓取间隔</th><th>最近抓取</th><th>状态</th><th>操作</th></tr></thead><tbody>{feeds.map(feed => <tr key={feed.id}><td data-label="订阅源"><strong>{feed.title || '未命名订阅源'}</strong><br /><a className="table-link" href={feed.site_url || feed.url} target="_blank" rel="noreferrer">{feed.url}</a></td><td data-label="分组">{feedGroupName(feed)}</td><td data-label="抓取间隔">{Math.max(1, Math.round(feed.fetch_interval / 60))} 分钟</td><td data-label="最近抓取">{formatDateTime(feed.last_fetch)}</td><td data-label="状态">{feed.error_msg ? <StatusBadge tone="danger">抓取异常</StatusBadge> : <StatusBadge tone="success">正常</StatusBadge>}</td><td data-label="操作"><div className="table-actions"><button className="button button--quiet" disabled={busy} onClick={() => void run(() => rssApi.fetchFeed(feed.id), `已抓取“${feed.title || feed.url}”`, selectedFeed === feed.id)}>抓取</button><button className="button button--quiet" onClick={() => setEditFeed({ ...feed })}>编辑</button><button className="icon-button" aria-label="删除订阅源" onClick={() => setPendingDeleteFeed(feed)}>×</button></div></td></tr>)}</tbody></table></div></section>
      <section className="data-panel"><header className="data-panel-head"><div><h2>分组管理</h2><p>删除分组时其下订阅源会移回未分组。</p></div></header><form className="filter-bar" onSubmit={addGroup}><input value={newGroup} placeholder="新分组名称" onChange={event => setNewGroup(event.target.value)} required /><button className="button button--primary" disabled={busy}>创建分组</button></form>{groups.length ? <div className="table-wrap"><table className="data-table"><thead><tr><th>分组名称</th><th>订阅源数</th><th>操作</th></tr></thead><tbody>{groups.map(group => <tr key={group.id}><td data-label="分组名称"><strong>{group.name}</strong></td><td data-label="订阅源数">{feeds.filter(feed => feed.group_id === group.id).length}</td><td data-label="操作"><div className="table-actions"><button className="button button--quiet" onClick={() => setEditGroup({ ...group })}>重命名</button><button className="icon-button" aria-label="删除分组" onClick={() => setPendingDeleteGroup(group)}>×</button></div></td></tr>)}</tbody></table></div> : <PageState kind="empty" title="还没有 RSS 分组" description="创建分组后可在编辑订阅源时指定分组。" />}</section>
      <section className="data-panel"><header className="data-panel-head"><div><h2>{selectedGroup ? groups.find(g => g.id === selectedGroup)?.name || '分组条目' : selectedFeed ? feeds.find(feed => feed.id === selectedFeed)?.title || '订阅条目' : '全部订阅条目'}</h2><p>{total.toLocaleString()} 条已抓取内容</p></div><div className="toolbar-actions"><select aria-label="筛选分组" value={selectedGroup ?? ''} onChange={event => selectGroup(event.target.value ? Number(event.target.value) : null)}><option value="">全部分组</option>{groups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}</select></div></header>{entriesLoading ? <PageState kind="loading" title="正在读取条目" /> : !entries.length ? <PageState kind="empty" title="没有可显示的条目" description="可手动抓取订阅源，或调整筛选范围。" /> : <div className="table-wrap"><table className="data-table"><thead><tr><th>标题</th><th className="rss-th-dl">下载</th></tr></thead><tbody>{entries.map(entry => <tr key={entry.id}><td data-label="标题" className="rss-entry-main"><a className="table-link" href={entry.link} target="_blank" rel="noreferrer">{entry.title}</a><div className="rss-entry-meta">{entry.author || '—'} · {formatDateTime(entry.published)} · {feedTitles(entry.feed_ids)}</div>{entry.content && <details className="rss-entry-content"><summary>展开详情</summary><div dangerouslySetInnerHTML={{__html: entry.content}} /></details>}</td><td data-label="下载" className="rss-entry-action">{entry.magnet_url ? <a className="rss-magnet-link" href={entry.magnet_url} title="磁力链接">🧲</a> : '—'}</td></tr>)}</tbody></table></div>}<Pagination page={page} perPage={perPage} total={total} disabled={entriesLoading} onChange={setPage} /></section></>}
    <Modal open={editFeed !== null} title="编辑订阅源" description="可修改展示标题、自动抓取间隔和所属分组，源 URL 不支持直接变更。" onClose={() => !busy && setEditFeed(null)} footer={<><button className="button button--quiet" type="button" onClick={() => setEditFeed(null)} disabled={busy}>取消</button><button className="button button--primary" type="submit" form="rss-feed-form" disabled={busy}>{busy ? '保存中...' : '保存'}</button></>}><form id="rss-feed-form" className="form-grid" onSubmit={saveFeed}><label className="form-field form-field--wide">标题<input autoFocus value={editFeed?.title ?? ''} onChange={event => setEditFeed(feed => feed ? { ...feed, title: event.target.value } : feed)} required /></label><label className="form-field">抓取间隔（秒）<input type="number" min="60" value={editFeed?.fetch_interval ?? 3600} onChange={event => setEditFeed(feed => feed ? { ...feed, fetch_interval: Number(event.target.value) } : feed)} required /></label><label className="form-field">分组<select value={editFeed?.group_id ?? ''} onChange={event => setEditFeed(feed => feed ? { ...feed, group_id: event.target.value ? Number(event.target.value) : undefined } : feed)}><option value="">未分组</option>{groups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label></form></Modal>
    <Modal open={editGroup !== null} title="重命名分组" onClose={() => !busy && setEditGroup(null)} footer={<><button className="button button--quiet" type="button" onClick={() => setEditGroup(null)} disabled={busy}>取消</button><button className="button button--primary" type="submit" form="rss-group-form" disabled={busy}>{busy ? '保存中...' : '保存'}</button></>}><form id="rss-group-form" className="form-grid" onSubmit={saveGroup}><label className="form-field form-field--wide">分组名称<input autoFocus value={editGroup?.name ?? ''} onChange={event => setEditGroup(group => group ? { ...group, name: event.target.value } : group)} required /></label></form></Modal>
    <ConfirmDialog open={pendingDeleteFeed !== null} title="删除订阅源" description={`将删除“${pendingDeleteFeed?.title || pendingDeleteFeed?.url || ''}”以及该源的全部历史条目。`} confirmLabel="删除" danger busy={busy} onClose={() => setPendingDeleteFeed(null)} onConfirm={() => void removeFeed()} />
    <ConfirmDialog open={pendingDeleteGroup !== null} title="删除分组" description={`将删除分组“${pendingDeleteGroup?.name ?? ''}”，其下订阅源会移回未分组。`} confirmLabel="删除" danger busy={busy} onClose={() => setPendingDeleteGroup(null)} onConfirm={() => void removeGroup()} />
  </div>
}
