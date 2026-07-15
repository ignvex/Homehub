import { useEffect, useState, type FormEvent } from 'react'
import { ConfirmDialog, Modal, Notice, PageState } from '@/components/WorkspaceUI'
import { bookmarksApi, type Bookmark, type BookmarkGroup } from '@/lib/bookmarks'

const emptyBookmark = { group_id: 0, title: '', url: '', icon: '' }

export function BookmarksPage() {
  const [groups, setGroups] = useState<BookmarkGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const [groupDraft, setGroupDraft] = useState<{ id?: number; name: string; priority?: number } | null>(null)
  const [bookmarkDraft, setBookmarkDraft] = useState<(typeof emptyBookmark & { id?: number; priority?: number }) | null>(null)
  const [pendingDelete, setPendingDelete] = useState<{ kind: 'group' | 'bookmark'; id: number; name: string } | null>(null)

  async function load() {
    setLoading(true)
    try { setGroups(await bookmarksApi.listGroups()); setError('') }
    catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取书签') }
    finally { setLoading(false) }
  }
  useEffect(() => { void load() }, [])

  async function run(action: () => Promise<unknown>, success: string) {
    setBusy(true)
    try { await action(); setMessage(success); await load(); return true }
    catch (cause) { setError(cause instanceof Error ? cause.message : '操作失败'); return false }
    finally { setBusy(false) }
  }

  async function saveGroup(event: FormEvent) {
    event.preventDefault()
    if (!groupDraft?.name.trim()) return
    const saved = await run(() => groupDraft.id
      ? bookmarksApi.updateGroup(groupDraft.id, { name: groupDraft.name.trim(), priority: groupDraft.priority })
      : bookmarksApi.createGroup(groupDraft.name.trim()), groupDraft.id ? '分组已更新' : '分组已创建')
    if (saved) setGroupDraft(null)
  }

  async function saveBookmark(event: FormEvent) {
    event.preventDefault()
    if (!bookmarkDraft) return
    const { id, group_id, title, url, icon, priority } = bookmarkDraft
    const saved = await run(() => id
      ? bookmarksApi.update(id, { title: title.trim(), url: url.trim(), icon: icon.trim(), priority })
      : bookmarksApi.create({ group_id, title: title.trim(), url: url.trim(), icon: icon.trim(), priority }), id ? '书签已更新' : '书签已创建')
    if (saved) setBookmarkDraft(null)
  }

  async function moveGroup(index: number, offset: number) {
    const ordered = [...groups]
    const target = index + offset
    if (target < 0 || target >= ordered.length) return
    ;[ordered[index], ordered[target]] = [ordered[target], ordered[index]]
    await run(() => bookmarksApi.reorderGroups(ordered.map(group => group.id)), '分组顺序已更新')
  }

  async function moveBookmark(group: BookmarkGroup, index: number, offset: number) {
    const items = [...(group.bookmarks ?? [])]
    const target = index + offset
    if (target < 0 || target >= items.length) return
    ;[items[index], items[target]] = [items[target], items[index]]
    await run(() => bookmarksApi.reorder(group.id, items.map(item => item.id)), '书签顺序已更新')
  }

  async function removePending() {
    if (!pendingDelete) return
    const { kind, id } = pendingDelete
    const saved = await run(() => kind === 'group' ? bookmarksApi.deleteGroup(id) : bookmarksApi.delete(id), kind === 'group' ? '分组与其书签已删除' : '书签已删除')
    if (saved) setPendingDelete(null)
  }

  if (loading) return <PageState kind="loading" title="正在读取书签" />
  if (error && !groups.length) return <PageState kind="error" title="无法读取书签" description={error} action={<button className="button button--primary" onClick={() => void load()}>重试</button>} />

  return <div className="business-page">
    <div className="page-toolbar"><div><p>工作空间</p><h2>书签与分组</h2></div><div className="toolbar-actions"><button className="button button--quiet" onClick={() => setGroupDraft({ name: '' })}>新建分组</button><button className="button button--primary" disabled={!groups.length} onClick={() => setBookmarkDraft({ ...emptyBookmark, group_id: groups[0]?.id ?? 0 })}>添加书签</button></div></div>
    <Notice message={error} tone="error" onDismiss={() => setError('')} /><Notice message={message} onDismiss={() => setMessage('')} />
    {!groups.length ? <PageState kind="empty" title="还没有书签分组" description="先创建分组，再将常用链接归类到其中。" action={<button className="button button--primary" onClick={() => setGroupDraft({ name: '' })}>创建分组</button>} /> : <div className="bookmark-groups">
      {groups.map((group, groupIndex) => <section className="data-panel bookmark-group" key={group.id}>
        <header className="data-panel-head"><div><h2>{group.name}</h2><p>{group.bookmarks?.length ?? 0} 个书签</p></div><div className="admin-actions"><button className="icon-button" title="上移分组" aria-label="上移分组" disabled={groupIndex === 0 || busy} onClick={() => void moveGroup(groupIndex, -1)}>↑</button><button className="icon-button" title="下移分组" aria-label="下移分组" disabled={groupIndex === groups.length - 1 || busy} onClick={() => void moveGroup(groupIndex, 1)}>↓</button><button className="button button--quiet" onClick={() => setGroupDraft({ id: group.id, name: group.name, priority: group.priority })}>编辑</button><button className="button button--quiet" onClick={() => setBookmarkDraft({ ...emptyBookmark, group_id: group.id })}>添加书签</button><button className="icon-button" title="删除分组" aria-label="删除分组" onClick={() => setPendingDelete({ kind: 'group', id: group.id, name: group.name })}>×</button></div></header>
        {!group.bookmarks?.length ? <PageState kind="empty" title="此分组尚无书签" description="添加一个链接开始使用。" /> : <div className="table-wrap"><table className="data-table"><thead><tr><th>标题</th><th>地址</th><th>图标</th><th>操作</th></tr></thead><tbody>{group.bookmarks.map((bookmark, index) => <BookmarkRow key={bookmark.id} bookmark={bookmark} index={index} total={group.bookmarks?.length ?? 0} busy={busy} onMove={offset => void moveBookmark(group, index, offset)} onEdit={() => setBookmarkDraft({ id: bookmark.id, group_id: group.id, title: bookmark.title, url: bookmark.url, icon: bookmark.icon, priority: bookmark.priority })} onDelete={() => setPendingDelete({ kind: 'bookmark', id: bookmark.id, name: bookmark.title })} />)}</tbody></table></div>}
      </section>)}
    </div>}
    <Modal open={groupDraft !== null} title={groupDraft?.id ? '编辑分组' : '新建分组'} onClose={() => !busy && setGroupDraft(null)} footer={<><button className="button button--quiet" type="button" onClick={() => setGroupDraft(null)} disabled={busy}>取消</button><button className="button button--primary" type="submit" form="bookmark-group-form" disabled={busy}>{busy ? '保存中...' : '保存'}</button></>}><form id="bookmark-group-form" className="form-grid" onSubmit={saveGroup}><label className="form-field form-field--wide">分组名称<input autoFocus value={groupDraft?.name ?? ''} onChange={event => setGroupDraft(draft => draft ? { ...draft, name: event.target.value } : draft)} required maxLength={100} /></label></form></Modal>
    <Modal open={bookmarkDraft !== null} title={bookmarkDraft?.id ? '编辑书签' : '添加书签'} onClose={() => !busy && setBookmarkDraft(null)} footer={<><button className="button button--quiet" type="button" onClick={() => setBookmarkDraft(null)} disabled={busy}>取消</button><button className="button button--primary" type="submit" form="bookmark-form" disabled={busy}>{busy ? '保存中...' : '保存'}</button></>}><form id="bookmark-form" className="form-grid" onSubmit={saveBookmark}><label className="form-field">分组<select value={bookmarkDraft?.group_id ?? 0} disabled={Boolean(bookmarkDraft?.id)} onChange={event => setBookmarkDraft(draft => draft ? { ...draft, group_id: Number(event.target.value) } : draft)}>{groups.map(group => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label><label className="form-field">标题<input value={bookmarkDraft?.title ?? ''} onChange={event => setBookmarkDraft(draft => draft ? { ...draft, title: event.target.value } : draft)} required maxLength={200} /></label><label className="form-field form-field--wide">URL<input type="url" value={bookmarkDraft?.url ?? ''} onChange={event => setBookmarkDraft(draft => draft ? { ...draft, url: event.target.value } : draft)} required /></label><label className="form-field form-field--wide">图标 URL（留空时由服务端尝试获取）<input type="url" value={bookmarkDraft?.icon ?? ''} onChange={event => setBookmarkDraft(draft => draft ? { ...draft, icon: event.target.value } : draft)} /></label></form></Modal>
    <ConfirmDialog open={pendingDelete !== null} title={`删除${pendingDelete?.kind === 'group' ? '分组' : '书签'}`} description={`将删除“${pendingDelete?.name ?? ''}”。${pendingDelete?.kind === 'group' ? '该分组下的书签也会一并删除。' : ''}`} confirmLabel="删除" danger busy={busy} onClose={() => setPendingDelete(null)} onConfirm={() => void removePending()} />
  </div>
}

function BookmarkRow({ bookmark, index, total, busy, onMove, onEdit, onDelete }: { bookmark: Bookmark; index: number; total: number; busy: boolean; onMove: (offset: number) => void; onEdit: () => void; onDelete: () => void }) {
  return <tr><td data-label="标题"><a className="table-link" href={bookmark.url} target="_blank" rel="noreferrer">{bookmark.title}</a></td><td data-label="地址">{bookmark.url}</td><td data-label="图标">{bookmark.icon ? <img src={bookmark.icon} alt="" width="20" height="20" /> : '自动获取'}</td><td data-label="操作"><div className="table-actions"><button className="icon-button" disabled={busy || index === 0} onClick={() => onMove(-1)} aria-label="上移书签">↑</button><button className="icon-button" disabled={busy || index === total - 1} onClick={() => onMove(1)} aria-label="下移书签">↓</button><button className="button button--quiet" onClick={onEdit}>编辑</button><button className="icon-button" onClick={onDelete} aria-label="删除书签">×</button></div></td></tr>
}
