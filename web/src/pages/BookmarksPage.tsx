import { useEffect, useState, type FormEvent } from 'react'
import { api } from '@/lib/api'

interface Bookmark {
  id: number
  group_id: number
  title: string
  url: string
  icon: string
  priority: number
}

interface Group {
  id: number
  name: string
  priority: number
  bookmarks?: Bookmark[]
}

export function BookmarksPage() {
  const [groups, setGroups] = useState<Group[]>([])
  const [message, setMessage] = useState('')

  const load = () =>
    api.get<Group[]>('/bookmark-groups')
      .then(setGroups)
      .catch(e => setMessage(e instanceof Error ? e.message : '载入失败'))

  useEffect(() => { void load() }, [])

  async function run(action: () => Promise<unknown>, success = '操作完成') {
    try { await action(); setMessage(success); await load() }
    catch (e) { setMessage(e instanceof Error ? e.message : '操作失败') }
  }

  async function addGroup(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = new FormData(e.currentTarget)
    await run(() => api.post('/bookmark-groups', { name: String(form.get('name')) }), '分组已添加')
    e.currentTarget.reset()
  }

  async function addBookmark(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const form = new FormData(e.currentTarget)
    await run(() => api.post('/bookmarks', {
      group_id: Number(form.get('group_id')),
      title: String(form.get('title')),
      url: String(form.get('url')),
      icon: '',
      priority: 0,
    }), '书签已添加')
    e.currentTarget.reset()
  }

  async function renameGroup(group: Group) {
    const name = prompt('分组名称', group.name)
    if (name && name !== group.name) {
      await run(() => api.put(`/bookmark-groups/${group.id}`, { name, priority: group.priority }), '分组已更新')
    }
  }

  async function editBookmark(bookmark: Bookmark) {
    const title = prompt('书签标题', bookmark.title)
    if (!title) return
    const url = prompt('书签地址', bookmark.url)
    if (!url) return
    await run(() => api.put(`/bookmarks/${bookmark.id}`, {
      title,
      url,
      icon: bookmark.icon,
      priority: bookmark.priority,
    }), '书签已更新')
  }

  async function remove(path: string) {
    if (confirm('确定删除？')) {
      await run(() => api.delete(path), '已删除')
    }
  }

  async function moveGroup(index: number, offset: number) {
    const next = [...groups]
    const target = index + offset
    if (target < 0 || target >= next.length) return
    ;[next[index], next[target]] = [next[target], next[index]]
    await run(() => api.post('/bookmark-groups/reorder', { ids: next.map(g => g.id) }), '分组顺序已更新')
  }

  async function moveBookmark(group: Group, index: number, offset: number) {
    const items = [...(group.bookmarks ?? [])]
    const target = index + offset
    if (target < 0 || target >= items.length) return
    ;[items[index], items[target]] = [items[target], items[index]]
    await run(() => api.post('/bookmarks/reorder', {
      group_id: group.id,
      ids: items.map(b => b.id),
    }), '书签顺序已更新')
  }

  return (
    <div className="business-page">
      <section className="work-panel inline-forms">
        <form onSubmit={addGroup}>
          <strong>新建分组</strong>
          <input name="name" placeholder="分组名称" required />
          <button>添加</button>
        </form>

        <form onSubmit={addBookmark}>
          <strong>添加书签</strong>
          <select name="group_id" required>
            <option value="">选择分组</option>
            {groups.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
          </select>
          <input name="title" placeholder="标题" required />
          <input name="url" type="url" placeholder="https://" required />
          <button>保存</button>
        </form>
      </section>

      {message && <div className="form-message">{message}</div>}

      <div className="bookmark-groups">
        {groups.map((g, groupIndex) => (
          <section className="work-panel bookmark-group" key={g.id}>
            <header>
              <h2>{g.name}</h2>
              <div className="admin-actions">
                <button onClick={() => moveGroup(groupIndex, -1)} disabled={groupIndex === 0}>↑</button>
                <button onClick={() => moveGroup(groupIndex, 1)} disabled={groupIndex === groups.length - 1}>↓</button>
                <button onClick={() => renameGroup(g)}>编辑</button>
                <button onClick={() => remove(`/bookmark-groups/${g.id}`)}>删除</button>
              </div>
            </header>

            <div>
              {g.bookmarks?.map((b, index) => (
                <article key={b.id}>
                  <a href={b.url} target="_blank" rel="noreferrer">
                    <strong>{b.title}</strong>
                    <span>{b.url}</span>
                  </a>
                  <div className="admin-actions">
                    <button onClick={() => moveBookmark(g, index, -1)} disabled={index === 0}>↑</button>
                    <button onClick={() => moveBookmark(g, index, 1)} disabled={index === (g.bookmarks?.length ?? 0) - 1}>↓</button>
                    <button onClick={() => editBookmark(b)}>编辑</button>
                    <button onClick={() => remove(`/bookmarks/${b.id}`)}>×</button>
                  </div>
                </article>
              ))}

              {!g.bookmarks?.length && <p className="empty-state">此分组暂无书签</p>}
            </div>
          </section>
        ))}
      </div>
    </div>
  )
}
