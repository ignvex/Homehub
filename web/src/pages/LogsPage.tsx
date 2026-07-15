import { useEffect, useState, type FormEvent } from 'react'
import { Notice, PageState, Pagination, StatusBadge } from '@/components/WorkspaceUI'
import { adminApi, type LogEntry, type LogQuery } from '@/lib/admin'
import { formatDateTime } from '@/lib/types'

const defaultQuery: LogQuery = { page: 1, perPage: 50, level: '', query: '', since: '', until: '' }
const tone = (level: string) => level === 'ERROR' ? 'danger' : level === 'WARN' ? 'warning' : level === 'INFO' ? 'info' : 'neutral'

export function LogsPage() {
  const [query, setQuery] = useState(defaultQuery)
  const [items, setItems] = useState<LogEntry[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  async function load(next = query) { setLoading(true); try { const result = await adminApi.listLogs(next); setItems(result.list); setTotal(result.total); setError('') } catch (cause) { setError(cause instanceof Error ? cause.message : '无法读取系统日志') } finally { setLoading(false) } }
  useEffect(() => { void load(defaultQuery) }, [])
  function submit(event: FormEvent) { event.preventDefault(); const next = { ...query, page: 1 }; setQuery(next); void load(next) }
  function changePage(page: number) { const next = { ...query, page }; setQuery(next); void load(next) }

  return <div className="business-page"><div className="page-toolbar"><div><p>系统事件</p><h2>系统日志</h2></div><button className="button button--quiet" disabled={loading} onClick={() => void load()}>刷新</button></div><Notice message={error} tone="error" onDismiss={() => setError('')} /><form className="filter-bar" onSubmit={submit}><input value={query.query} onChange={event => setQuery(current => ({ ...current, query: event.target.value }))} placeholder="搜索消息或附加字段" /><select value={query.level} onChange={event => setQuery(current => ({ ...current, level: event.target.value }))}><option value="">全部级别</option><option value="DEBUG">DEBUG</option><option value="INFO">INFO</option><option value="WARN">WARN</option><option value="ERROR">ERROR</option></select><label className="form-field">起始时间<input type="datetime-local" value={query.since} onChange={event => setQuery(current => ({ ...current, since: event.target.value }))} /></label><label className="form-field">结束时间<input type="datetime-local" value={query.until} onChange={event => setQuery(current => ({ ...current, until: event.target.value }))} /></label><button className="button button--primary">筛选</button></form>{loading ? <PageState kind="loading" title="正在读取日志" /> : !items.length ? <PageState kind="empty" title="没有符合条件的日志" description="调整级别、关键词或时间范围后重试。" /> : <><div className="table-wrap"><table className="data-table log-table"><thead><tr><th>时间</th><th>级别</th><th>来源</th><th>消息</th></tr></thead><tbody>{items.map(item => <tr key={item.id}><td data-label="时间">{formatDateTime(item.log_time || item.created_at)}</td><td data-label="级别"><StatusBadge tone={tone(item.level)}>{item.level}</StatusBadge></td><td data-label="来源">{item.source || '—'}</td><td data-label="消息"><strong>{item.message}</strong>{item.fields && <details><summary>附加字段</summary><pre>{item.fields}</pre></details>}</td></tr>)}</tbody></table></div><Pagination page={query.page} perPage={query.perPage} total={total} disabled={loading} onChange={changePage} /></>}</div>
}
