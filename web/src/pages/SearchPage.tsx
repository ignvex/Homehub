import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { PageState, Pagination } from '@/components/WorkspaceUI'
import { useHotkeys } from '@/hooks/useHotkeys'
import { errorMessage } from '@/hooks/useNotice'
import { useQueryNumber, useQueryState } from '@/hooks/useQueryState'
import { isAbortError } from '@/lib/api'
import { SEARCH_INPUT_ID, searchApi, type SearchResult, type SearchScope } from '@/lib/search'
import { rssApi, type RSSFeed } from '@/lib/rss'

const scopes: { value: SearchScope; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'bookmarks', label: '书签' },
  { value: 'rss', label: 'RSS' },
]

const perPage = 20
const HISTORY_KEY = 'homehub-search-history'
const HISTORY_LIMIT = 8

/** loadHistory 读取本机搜索历史。 */
function loadHistory(): string[] {
  try {
    const raw = localStorage.getItem(HISTORY_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((item): item is string => typeof item === 'string').slice(0, HISTORY_LIMIT)
  } catch {
    return []
  }
}

/** pushHistory 把关键词放到最前并去重。 */
function pushHistory(list: string[], query: string): string[] {
  const next = [query, ...list.filter(item => item !== query)].slice(0, HISTORY_LIMIT)
  try {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(next))
  } catch {
    // 隐私模式下 localStorage 会抛错。搜索历史是锦上添花，失败就算了。
  }
  return next
}

/**
 * Highlight 把文本里匹配关键词的片段包成 <mark>。
 *
 * 用 React 元素而不是 innerHTML：正文和标题都来自外部 feed，
 * 拼 HTML 等于把注入面重新打开——清洗过的正文在这里不该被降级。
 */
function Highlight({ text, query }: { text: string; query: string }): ReactNode {
  const needle = query.trim()
  if (!needle || !text) return text

  const lowerText = text.toLowerCase()
  const lowerNeedle = needle.toLowerCase()
  const parts: ReactNode[] = []
  let from = 0

  for (;;) {
    const at = lowerText.indexOf(lowerNeedle, from)
    if (at < 0) break
    if (at > from) parts.push(text.slice(from, at))
    parts.push(<mark key={`${at}-${parts.length}`}>{text.slice(at, at + needle.length)}</mark>)
    from = at + needle.length
  }

  if (!parts.length) return text
  if (from < text.length) parts.push(text.slice(from))
  return <>{parts}</>
}

export function SearchPage() {
  // 已提交的检索条件放在 URL（replaceState），刷新、前进后退与 /search?q= 链接都能还原。
  const [q, setQ] = useQueryState('q', '')
  const [scopeParam, setScopeParam] = useQueryState('scope', 'all')
  const [page, setPage] = useQueryNumber('page', 1)
  const scope = (scopes.some(option => option.value === scopeParam) ? scopeParam : 'all') as SearchScope

  // 输入框是草稿，提交后才写进 URL 触发检索。
  const [query, setQuery] = useState(q)
  const [feedId, setFeedId] = useState(0)
  const [result, setResult] = useState<SearchResult | null>(null)
  const [feeds, setFeeds] = useState<RSSFeed[]>([])
  const [history, setHistory] = useState<string[]>(() => loadHistory())
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  const abortRef = useRef<AbortController | null>(null)
  const [retry, setRetry] = useState(0)

  // URL 里的 q 被外部改动（例如首页搜索框跳转过来）时同步输入框。
  useEffect(() => { setQuery(q) }, [q])

  // 检索条件变化即重新搜索；新请求取消旧请求，慢响应不会覆盖新结果。
  useEffect(() => {
    const trimmed = q.trim()
    abortRef.current?.abort()
    if (!trimmed) { setResult(null); setLoading(false); return }
    const controller = new AbortController()
    abortRef.current = controller
    setLoading(true)
    searchApi.search({ q: trimmed, scope, page, perPage, feedId: feedId || undefined }, { signal: controller.signal })
      .then(next => {
        setResult(next)
        setError('')
        setHistory(current => pushHistory(current, trimmed))
      })
      .catch(cause => { if (!isAbortError(cause)) setError(errorMessage(cause, '搜索失败')) })
      .finally(() => { if (abortRef.current === controller) setLoading(false) })
    return () => controller.abort()
  }, [q, scope, page, feedId, retry])

  useEffect(() => {
    // 源列表只为了筛选下拉。失败不影响搜索本身。
    rssApi.listFeeds().then(value => setFeeds(value ?? [])).catch(() => undefined)
  }, [])

  // Ctrl/Cmd+K 聚焦搜索框。列表页里手不离键盘找搜索框是常态。
  // 「/」由外壳的全局快捷键处理（useHotkeys 不接受修饰键，所以 Ctrl+K 仍在这里）。
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        inputRef.current?.focus()
        inputRef.current?.select()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  useHotkeys([{ keys: 'Ctrl+K', description: '聚焦搜索框（⌘K）', group: '数据搜索' }])

  function commit(next: string) {
    setPage(1)
    setQ(next.trim())
  }

  function submit(event: FormEvent) {
    event.preventDefault()
    commit(query)
  }

  const bookmarks = result?.bookmarks ?? []
  const entries = result?.rss_entries ?? []
  const hitCount = (result?.bookmarks_total ?? 0) + (result?.rss_entries_total ?? 0)

  return <div className="business-page">
    <div className="page-toolbar page-toolbar--end">
      <span className="toolbar-summary">Ctrl/⌘ + K 聚焦</span>
    </div>

    <form className="filter-bar" onSubmit={submit}>
      <input
        ref={inputRef}
        id={SEARCH_INPUT_ID}
        autoFocus
        value={query}
        onChange={event => setQuery(event.target.value)}
        placeholder="搜索书签、RSS 标题与正文"
        aria-label="搜索关键词"
        required
      />
      <select value={scope} onChange={event => { setPage(1); setScopeParam(event.target.value) }} aria-label="搜索范围">
        {scopes.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
      <select value={feedId} onChange={event => { setPage(1); setFeedId(Number(event.target.value)) }} aria-label="限定订阅源">
        <option value={0}>所有订阅源</option>
        {feeds.map(feed => <option key={feed.id} value={feed.id}>{feed.title || feed.url}</option>)}
      </select>
      <button type="submit" className="button button--primary" disabled={loading}>{loading ? '搜索中…' : '搜索'}</button>
    </form>

    {history.length > 0 && <div className="filter-bar search-history">
      <span className="toolbar-summary">最近：</span>
      {history.map(item => <button
        key={item}
        type="button"
        className="button button--quiet"
        onClick={() => { setQuery(item); commit(item) }}
      >
        {item}
      </button>)}
    </div>}

    {loading && !result
      ? <PageState kind="loading" title="正在搜索" />
      : error && !loading
        ? <PageState kind="error" title="搜索失败" description={error} action={<button type="button" className="button button--primary" onClick={() => { setError(''); setRetry(value => value + 1) }}>重试</button>} />
      : !result
        ? <PageState
          kind="empty"
          title="输入关键词开始检索"
          description="书签匹配标题、URL 与分组名；RSS 条目匹配标题、正文与作者。"
        />
        : hitCount === 0
          ? <PageState
            kind="empty"
            title="没有匹配结果"
            description={`「${result.query}」没有命中。试试更短的关键词，或换个搜索范围。`}
          />
          : <div className={`search-results${loading ? ' is-stale' : ''}`} aria-busy={loading}>
            <p className="toolbar-summary">
              「{result.query}」命中 {hitCount} 条 · 第 {result.page} 页 · 服务端耗时 {result.took_ms} ms
            </p>

            {bookmarks.length > 0 && <section className="data-panel">
              <header className="data-panel-head">
                <div><h2>书签</h2><p>共 {result.bookmarks_total} 条</p></div>
              </header>
              <div className="result-list">
                {bookmarks.map(item => <a className="result-row" key={item.id} href={item.url} target="_blank" rel="noreferrer">
                  <strong><Highlight text={item.title} query={result.query} /></strong>
                  <span>{item.group_name} · <Highlight text={item.url} query={result.query} /></span>
                </a>)}
              </div>
            </section>}

            {entries.length > 0 && <section className="data-panel">
              <header className="data-panel-head">
                <div><h2>RSS 订阅</h2><p>共 {result.rss_entries_total} 条</p></div>
              </header>
              <div className="result-list">
                {entries.map(item => <a className="result-row" key={item.id} href={item.link} target="_blank" rel="noreferrer">
                  <strong><Highlight text={item.title} query={result.query} /></strong>
                  <span>
                    {item.feed_title}
                    {item.author ? ` · ${item.author}` : ''}
                  </span>
                  {item.snippet && <span><Highlight text={item.snippet} query={result.query} /></span>}
                </a>)}
              </div>
            </section>}

            <Pagination
              page={page}
              perPage={result.per_page || perPage}
              total={Math.max(result.bookmarks_total, result.rss_entries_total)}
              disabled={loading}
              onChange={setPage}
            />
          </div>}
  </div>
}
