import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { ConfirmDialog, Notice, PageState, Pagination } from '@/components/WorkspaceUI'
import { adminApi, type LogEntry, type LogQuery } from '@/lib/admin'
import { formatDateTime } from '@/lib/types'

// ============================================================
// 系统日志 — 紧凑行式实时流
//
// SSE 推送必须遵守当前筛选条件：否则筛选 ERROR 时仍会混入 INFO，
// 且 total 会被无谓推高导致分页失真。三条约束：
//   1. 推送条目要匹配 level / 关键词 / 时间范围
//   2. total 只在条目真正插入列表时递增
//   3. 列表截断上限不影响 total（total 是服务端筛选结果的总数）
// ============================================================

const defaultQuery: LogQuery = { page: 1, perPage: 50, level: '', query: '', since: '', until: '' }
const LIVE_BUFFER = 200

const levels = ['', 'DEBUG', 'INFO', 'WARN', 'ERROR'] as const
const levelLabel: Record<string, string> = { '': '全部', DEBUG: 'DEBUG', INFO: 'INFO', WARN: 'WARN', ERROR: 'ERROR' }

const ranges: { key: string; label: string; minutes: number | null }[] = [
  { key: '15m', label: '15 分钟', minutes: 15 },
  { key: '1h', label: '1 小时', minutes: 60 },
  { key: '24h', label: '24 小时', minutes: 60 * 24 },
  { key: 'all', label: '全部', minutes: null },
]

/** datetime-local 需要本地时间且不含秒。 */
function toLocalInput(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function levelClass(level: string): string {
  const upper = level.toUpperCase()
  if (upper === 'ERROR') return 'is-error'
  if (upper === 'WARN') return 'is-warn'
  if (upper === 'INFO') return 'is-info'
  return ''
}

/** 判断一条推送是否落在当前筛选条件内。 */
function matchesQuery(entry: LogEntry, query: LogQuery): boolean {
  if (query.level && entry.level.toUpperCase() !== query.level.toUpperCase()) return false
  if (query.query && !entry.message.toLowerCase().includes(query.query.toLowerCase())) return false
  const stamp = new Date(entry.log_time || entry.created_at).getTime()
  if (Number.isNaN(stamp)) return true
  if (query.since && stamp < new Date(query.since).getTime()) return false
  if (query.until && stamp > new Date(query.until).getTime()) return false
  return true
}

export function LogsPage() {
  const [query, setQuery] = useState<LogQuery>(defaultQuery)
  const [items, setItems] = useState<LogEntry[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [clearing, setClearing] = useState(false)
  const [showClear, setShowClear] = useState(false)
  const [live, setLive] = useState(true)
  const [pending, setPending] = useState(0)

  // SSE 回调读取最新值，避免闭包捕获旧 query
  const queryRef = useRef(query)
  const liveRef = useRef(live)
  const seenRef = useRef<Set<number>>(new Set())
  useEffect(() => { queryRef.current = query }, [query])
  useEffect(() => { liveRef.current = live }, [live])

  const load = useCallback(async (next: LogQuery) => {
    setLoading(true)
    try {
      const result = await adminApi.listLogs(next)
      const list = result.list ?? []
      setItems(list)
      setTotal(result.total ?? 0)
      seenRef.current = new Set(list.map(entry => entry.id))
      setPending(0)
      setError('')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '无法读取系统日志')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load(defaultQuery) }, [load])

  // SSE 实时推送
  useEffect(() => {
    const source = new EventSource('/api/v1/admin/logs/stream')
    source.onmessage = (event) => {
      let entry: LogEntry
      try {
        entry = JSON.parse(event.data) as LogEntry
      } catch {
        return
      }
      if (!entry?.id || seenRef.current.has(entry.id)) return

      const current = queryRef.current
      // 不匹配当前筛选条件：直接丢弃，不污染列表也不动 total
      if (!matchesQuery(entry, current)) return

      if (!liveRef.current) {
        // 暂停期间只累计提示，不插入
        setPending(count => count + 1)
        return
      }
      // 只在第一页插入；翻页状态下静默累计，避免列表与页码错位
      if (current.page !== 1) {
        setPending(count => count + 1)
        return
      }

      seenRef.current.add(entry.id)
      setItems(prev => [entry, ...prev].slice(0, LIVE_BUFFER))
      setTotal(prev => prev + 1)
    }
    source.onerror = () => { /* EventSource 自带重连，这里不处理 */ }
    return () => source.close()
  }, [])

  function submit(event: FormEvent) {
    event.preventDefault()
    const next = { ...query, page: 1 }
    setQuery(next)
    void load(next)
  }

  function changePage(page: number) {
    const next = { ...query, page }
    setQuery(next)
    void load(next)
  }

  function applyLevel(level: string) {
    const next = { ...query, level, page: 1 }
    setQuery(next)
    void load(next)
  }

  function applyRange(minutes: number | null) {
    const next = minutes === null
      ? { ...query, since: '', until: '', page: 1 }
      : { ...query, since: toLocalInput(new Date(Date.now() - minutes * 60_000)), until: '', page: 1 }
    setQuery(next)
    void load(next)
  }

  // 当前时间范围对应哪个快捷档
  const activeRange = useMemo(() => {
    if (!query.since) return 'all'
    const minutes = Math.round((Date.now() - new Date(query.since).getTime()) / 60_000)
    const hit = ranges.find(range => range.minutes !== null && Math.abs(range.minutes - minutes) <= 2)
    return hit?.key ?? 'custom'
  }, [query.since])

  function resumeLive() {
    setLive(true)
    setPending(0)
    void load({ ...query, page: 1 })
  }

  async function clearAll() {
    setClearing(true)
    try {
      await adminApi.deleteLogs()
      setMessage('日志已全部清除')
      setShowClear(false)
      setItems([])
      setTotal(0)
      setPending(0)
      seenRef.current = new Set()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '清除失败')
    } finally {
      setClearing(false)
    }
  }

  return <div className="business-page logs-page">
    <div className="page-toolbar">
      <div className="toolbar-actions">
        <button className="button button--quiet" disabled={loading} onClick={() => void load(query)}>刷新</button>
        <button
          type="button"
          className="logs-live"
          aria-pressed={live}
          onClick={() => (live ? setLive(false) : resumeLive())}
        >
          <span className={`status-dot ${live ? 'is-ok' : 'is-warn'}`} aria-hidden="true" />
          {live ? '实时推送中' : '已暂停'}
        </button>
      </div>
      <span className="toolbar-summary">{total.toLocaleString()} 条记录</span>
    </div>

    <Notice message={error} tone="error" onDismiss={() => setError('')} />
    <Notice message={message} onDismiss={() => setMessage('')} />

    <form className="logs-filters" onSubmit={submit}>
      <input
        type="search"
        value={query.query}
        onChange={event => setQuery(current => ({ ...current, query: event.target.value }))}
        placeholder="搜索日志消息"
        aria-label="搜索日志消息"
      />
      <div className="logs-chip-group" role="group" aria-label="按级别筛选">
        {levels.map(level => <button
          key={level || 'all'}
          type="button"
          aria-pressed={query.level === level}
          onClick={() => applyLevel(level)}
        >{levelLabel[level]}</button>)}
      </div>
      <span className="logs-chip-label">时间</span>
      <div className="logs-chip-group" role="group" aria-label="按时间范围筛选">
        {ranges.map(range => <button
          key={range.key}
          type="button"
          aria-pressed={activeRange === range.key}
          onClick={() => applyRange(range.minutes)}
        >{range.label}</button>)}
      </div>
      <label className="form-field" style={{ flex: '0 1 170px' }}>
        <span className="visually-hidden">起始时间</span>
        <input type="datetime-local" value={query.since} onChange={event => setQuery(current => ({ ...current, since: event.target.value }))} />
      </label>
      <button className="button button--primary">筛选</button>
    </form>

    <section className="logs-stream" aria-label="日志流">
      <header className="logs-stream-head">
        <div>
          <h2>日志流</h2>
          <p>
            第 {query.page} 页
            {query.level && ` · ${query.level}`}
            {query.query && ` · 关键词「${query.query}」`}
            {total > items.length && ` · 本页 ${items.length} 条`}
          </p>
        </div>
        <div className="logs-stream-actions">
          {!live && <span className="status-badge status-badge--warning">已暂停</span>}
        </div>
      </header>

      {!live && pending > 0 && <div className="logs-pending">
        暂停期间收到 {pending} 条日志 · <button type="button" onClick={resumeLive}>恢复并刷新</button>
      </div>}

      {loading
        ? <PageState kind="loading" title="正在读取日志" />
        : !items.length
          ? <PageState
            kind="empty"
            title="没有符合条件的日志"
            description="调整级别、关键词或时间范围后重试。"
          />
          : <div className="logs-list">
            {items.map(entry => <article className={`log-entry ${levelClass(entry.level)}`} key={entry.id}>
              <time className="log-time" dateTime={entry.log_time || entry.created_at}>
                {formatDateTime(entry.log_time || entry.created_at)}
              </time>
              <span className={`log-level ${levelClass(entry.level)}`}>{entry.level}</span>
              <span className="log-source" title={entry.source}>{entry.source || '—'}</span>
              <div className="log-message">
                {entry.message}
                {entry.fields && <details className="log-fields">
                  <summary>附加字段</summary>
                  <pre>{entry.fields}</pre>
                </details>}
              </div>
            </article>)}
          </div>}

      <Pagination page={query.page} perPage={query.perPage} total={total} disabled={loading} onChange={changePage} />
    </section>

    <section className="logs-danger">
      <div>
        <strong>清除全部日志</strong>
        <p>删除数据库中的全部日志记录，此操作不可撤销。</p>
      </div>
      <button className="button button--danger" disabled={clearing} onClick={() => setShowClear(true)}>清除全部</button>
    </section>

    <ConfirmDialog
      open={showClear}
      title="清除全部日志"
      description="将删除所有系统日志记录，此操作不可撤销。是否继续？"
      confirmLabel="清除全部"
      danger
      busy={clearing}
      onClose={() => setShowClear(false)}
      onConfirm={() => void clearAll()}
    />
  </div>
}
