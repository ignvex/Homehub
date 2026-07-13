import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { request } from '@/lib/api'

type RecordValue = Record<string, unknown>
type ListPayload = { list?: RecordValue[]; total?: number }

function text(value: unknown): string {
  if (value == null) return '—'
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

function useLoad<T>(path: string) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try { setData(await request<T>(path)) }
    catch (cause) { setError(cause instanceof Error ? cause.message : '加载失败') }
    finally { setLoading(false) }
  }, [path])
  useEffect(() => { void load() }, [load])
  return { data, error, loading, load }
}

function Notice({ error, message }: { error?: string; message?: string }) {
  if (!error && !message) return null
  return <p className={`admin-message${error ? ' is-error' : ''}`} role={error ? 'alert' : 'status'}>{error || message}</p>
}

export function RSSPage() {
  const { data: feeds, error, loading, load } = useLoad<RecordValue[]>('/rss/feeds')
  const [entries, setEntries] = useState<RecordValue[]>([])
  const [url, setURL] = useState('')
  const [message, setMessage] = useState('')
  async function action(fn: () => Promise<unknown>, success: string) {
    try { await fn(); setMessage(success); await load() } catch (cause) { setMessage(cause instanceof Error ? cause.message : '操作失败') }
  }
  async function add(event: FormEvent) {
    event.preventDefault()
    await action(() => request('/rss/feeds', { method: 'POST', body: JSON.stringify({ url }) }), '订阅源已添加')
    setURL('')
  }
  async function showEntries(id: unknown) {
    const result = await request<ListPayload>(`/rss/entries?feed_id=${id}&per_page=50`)
    setEntries(result.list ?? [])
  }
  return <section className="admin-page"><header className="admin-page-header"><div><p className="admin-eyebrow">内容订阅</p><h1>RSS 管理</h1></div><button onClick={() => action(() => request('/rss/fetch-all', { method: 'POST' }), '已触发全部抓取')}>抓取全部</button></header>
    <Notice error={error} message={message}/><form className="admin-inline-form" onSubmit={add}><input type="url" value={url} onChange={e => setURL(e.target.value)} placeholder="https://example.com/feed.xml" required/><button>添加订阅</button></form>
    {loading ? <p>加载中…</p> : <div className="admin-card-list">{(feeds ?? []).map(feed => <article className="admin-card" key={text(feed.id)}><h3>{text(feed.title || feed.url)}</h3><p>{text(feed.url)}</p><div className="admin-actions"><button onClick={() => showEntries(feed.id)}>查看条目</button><button onClick={() => action(() => request(`/rss/feeds/${feed.id}/fetch`, { method: 'POST' }), '抓取完成')}>抓取</button><button className="danger" onClick={() => action(() => request(`/rss/feeds/${feed.id}`, { method: 'DELETE' }), '已删除')}>删除</button></div></article>)}</div>}
    {entries.length > 0 && <section><h2>订阅条目</h2><div className="admin-card-list">{entries.map(entry => <a className="admin-card" href={text(entry.link)} target="_blank" rel="noreferrer" key={text(entry.id)}><h3>{text(entry.title)}</h3><p>{text(entry.published_at || entry.created_at)}</p></a>)}</div></section>}
  </section>
}

const todoStatuses = ['planned', 'pending', 'in_progress', 'completed', 'paused']
export function TodosPage() {
  const { data: todos, error, loading, load } = useLoad<RecordValue[]>('/todos')
  const [title, setTitle] = useState('')
  const [priority, setPriority] = useState(0)
  const [message, setMessage] = useState('')
  async function mutate(path: string, options: RequestInit, success: string) {
    try { await request(path, options); setMessage(success); await load() } catch (cause) { setMessage(cause instanceof Error ? cause.message : '操作失败') }
  }
  async function add(event: FormEvent) { event.preventDefault(); await mutate('/todos', { method: 'POST', body: JSON.stringify({ title, priority }) }, '待办已创建'); setTitle('') }
  return <section className="admin-page"><header className="admin-page-header"><div><p className="admin-eyebrow">任务管理</p><h1>TODO</h1></div></header><Notice error={error} message={message}/>
    <form className="admin-inline-form" onSubmit={add}><input value={title} onChange={e => setTitle(e.target.value)} placeholder="待办标题" required/><input type="number" value={priority} onChange={e => setPriority(Number(e.target.value))} aria-label="优先级"/><button>创建</button></form>
    {loading ? <p>加载中…</p> : <div className="admin-card-list">{(todos ?? []).map(todo => <article className="admin-card" key={text(todo.id)}><h3>{text(todo.title)}</h3><p>{text(todo.description)}</p><div className="admin-actions"><select value={text(todo.status)} onChange={e => mutate(`/todos/${todo.id}/status`, { method: 'PATCH', body: JSON.stringify({ status: e.target.value }) }, '状态已更新')}>{todoStatuses.map(status => <option key={status}>{status}</option>)}</select><button className="danger" onClick={() => mutate(`/todos/${todo.id}`, { method: 'DELETE' }, '已删除')}>删除</button></div></article>)}</div>}
  </section>
}

export function SettingsPage({ appearance = false }: { appearance?: boolean }) {
  const { data: config, error, loading, load } = useLoad<RecordValue>('/config')
  const [draft, setDraft] = useState('')
  const [message, setMessage] = useState('')
  useEffect(() => { if (config) setDraft(JSON.stringify(config, null, 2)) }, [config])
  async function save(event: FormEvent) {
    event.preventDefault()
    try { await request('/config', { method: 'PUT', body: JSON.stringify(JSON.parse(draft)) }); setMessage('配置已保存'); await load() }
    catch (cause) { setMessage(cause instanceof Error ? cause.message : '保存失败') }
  }
  if (appearance) return <AppearanceSettings/>
  return <section className="admin-page"><header className="admin-page-header"><div><p className="admin-eyebrow">系统配置</p><h1>系统设置</h1></div></header><Notice error={error} message={message}/>{loading ? <p>加载中…</p> : <form onSubmit={save}><textarea className="config-editor" value={draft} onChange={e => setDraft(e.target.value)} spellCheck={false}/><button>保存配置</button></form>}</section>
}

function AppearanceSettings() {
  const [theme, setTheme] = useState(localStorage.getItem('theme') || 'system')
  const apply = (value: string) => { setTheme(value); localStorage.setItem('theme', value); const resolved = value === 'system' ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light') : value; document.documentElement.dataset.theme = resolved }
  return <section className="admin-page"><header className="admin-page-header"><div><p className="admin-eyebrow">界面偏好</p><h1>外观设置</h1></div></header><div className="admin-card"><h2>主题</h2><div className="theme-options">{['system', 'light', 'dark'].map(value => <button className={theme === value ? 'is-active' : ''} onClick={() => apply(value)} key={value}>{value === 'system' ? '跟随系统' : value === 'light' ? '浅色' : '深色'}</button>)}</div></div></section>
}

export function SearchPage() {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<RecordValue>({})
  const [error, setError] = useState('')
  async function search(event: FormEvent) { event.preventDefault(); try { setResults(await request<RecordValue>(`/search?q=${encodeURIComponent(query)}`)); setError('') } catch (cause) { setError(cause instanceof Error ? cause.message : '搜索失败') } }
  const groups = Object.entries(results).filter(([, value]) => Array.isArray(value)) as [string, RecordValue[]][]
  return <section className="admin-page"><header className="admin-page-header"><div><p className="admin-eyebrow">全局检索</p><h1>搜索</h1></div></header><Notice error={error}/><form className="admin-inline-form" onSubmit={search}><input value={query} onChange={e => setQuery(e.target.value)} placeholder="搜索视频、专栏、用户…" required/><button>搜索</button></form>{groups.map(([name, items]) => <section key={name}><h2>{name}</h2><div className="admin-card-list">{items.map((item, index) => <article className="admin-card" key={text(item.id || item.aid || item.mid || index)}><h3>{text(item.title || item.name || item.username)}</h3><p>{text(item.description || item.url || item.id)}</p></article>)}</div></section>)}</section>
}

export function UsersPage() {
  const { data, error, loading, load } = useLoad<RecordValue[]>('/admin/users')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [message, setMessage] = useState('')
  async function mutate(path: string, options: RequestInit, success: string) { try { await request(path, options); setMessage(success); await load() } catch (cause) { setMessage(cause instanceof Error ? cause.message : '操作失败') } }
  async function add(event: FormEvent) { event.preventDefault(); await mutate('/admin/users', { method: 'POST', body: JSON.stringify({ username, password }) }, '用户已创建'); setUsername(''); setPassword('') }
  return <section className="admin-page"><header className="admin-page-header"><div><p className="admin-eyebrow">权限管理</p><h1>用户管理</h1></div></header><Notice error={error} message={message}/><form className="admin-inline-form" onSubmit={add}><input value={username} onChange={e => setUsername(e.target.value)} placeholder="用户名" required/><input type="password" value={password} onChange={e => setPassword(e.target.value)} placeholder="密码" required/><button>创建用户</button></form>{loading ? <p>加载中…</p> : <div className="admin-card-list">{(data ?? []).map(user => <article className="admin-card" key={text(user.id)}><h3>{text(user.username)}</h3><p>{text(user.role)} · {text(user.created_at)}</p><button className="danger" onClick={() => mutate(`/admin/users/${user.id}`, { method: 'DELETE' }, '用户已删除')}>删除</button></article>)}</div>}</section>
}

export function LogsPage() {
  const [level, setLevel] = useState('')
  const { data, error, loading, load } = useLoad<ListPayload>(`/admin/logs?per_page=100${level ? `&level=${level}` : ''}`)
  return <section className="admin-page"><header className="admin-page-header"><div><p className="admin-eyebrow">运行审计</p><h1>系统日志</h1></div><div className="admin-actions"><select value={level} onChange={e => setLevel(e.target.value)}><option value="">全部级别</option><option>DEBUG</option><option>INFO</option><option>WARN</option><option>ERROR</option></select><button onClick={load}>刷新</button></div></header><Notice error={error}/>{loading ? <p>加载中…</p> : <div className="table-wrap"><table><thead><tr><th>时间</th><th>级别</th><th>来源</th><th>消息</th></tr></thead><tbody>{(data?.list ?? []).map(log => <tr key={text(log.id)}><td>{text(log.created_at)}</td><td>{text(log.level)}</td><td>{text(log.source)}</td><td>{text(log.message)}</td></tr>)}</tbody></table></div>}</section>
}
