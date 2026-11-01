import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { Notice, PageState, Pagination } from '@/components/WorkspaceUI'
import { searchApi, type SearchResult, type SearchScope } from '@/lib/search'
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
  const params = new URLSearchParams(window.location.search)
  const initialQuery = params.get('q') || ''

  const [query, setQuery] = useState(initialQuery)
  const [scope, setScope] = useState<SearchScope>('all')
  const [feedId, setFeedId] = useState(0)
  const [page, setPage] = useState(1)
  const [result, setResult] = useState<SearchResult | null>(null)
  const [feeds, setFeeds] = useState<RSSFeed[]>([])
  const [history, setHistory] = useState<string[]>(() => loadHistory())
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  const doSearch = useCallback(async (q: string, s: SearchScope, targetPage: number, feed: number) => {
    const trimmed = q.trim()
    if (!trimmed) return
    setLoading(true)
    try {
      const next = await searchApi.search({
        q: trimmed,
        scope: s,
        page: targetPage,
        perPage: perPage,
        feedId: feed || undefined,
      })
      setResult(next)
      setError('')
      setHistory(current => pushHistory(current, trimmed))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '搜索失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    // 源列表只为了筛选下拉。失败不影响搜索本身。
    rssApi.listFeeds().then(value => setFeeds(value ?? [])).catch(() => undefined)
  }, [])

  useEffect(() => {
    if (initialQuery) void doSearch(initialQuery, 'all', 1, 0)
    // 只跑一次：URL 里的 q 是首次进入的初始值。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Ctrl/Cmd+K 与 / 聚焦搜索框。列表页里手不离键盘找搜索框是常态。
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      const typing = target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        inputRef.current?.focus()
        inputRef.current?.select()
        return
      }
      if (event.key === '/' && !typing) {
        event.preventDefault()
        inputRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  async function submit(event: FormEvent) {
    event.preventDefault()
    setPage(1)
    await doSearch(query, scope, 1, feedId)
  }

  async function changePage(next: number) {
    setPage(next)
    await doSearch(query, scope, next, feedId)
  }

  const bookmarks = result?.bookmarks ?? []
  const entries = result?.rss_entries ?? []
  const hitCount = (result?.bookmarks_total ?? 0) + (result?.rss_entries_total ?? 0)

  return <div className="business-page">
    <div className="page-toolbar">
      <div>
        <p>跨域检索</p>
        <h2>数据搜索</h2>
      </div>
      <div className="toolbar-actions">
        <span style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>Ctrl/⌘ + K 聚焦</span>
      </div>
    </div>

    <Notice message={error} tone="error" onDismiss={() => setError('')} />

    <form className="filter-bar" onSubmit={submit}>
      <input
        ref={inputRef}
        autoFocus
        value={query}
        onChange={event => setQuery(event.target.value)}
        placeholder="搜索书签、RSS 标题与正文"
        aria-label="搜索关键词"
        required
      />
      <select value={scope} onChange={event => setScope(event.target.value as SearchScope)} aria-label="搜索范围">
        {scopes.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
      <select value={feedId} onChange={event => setFeedId(Number(event.target.value))} aria-label="限定订阅源">
        <option value={0}>所有订阅源</option>
        {feeds.map(feed => <option key={feed.id} value={feed.id}>{feed.title || feed.url}</option>)}
      </select>
      <button className="button button--primary" disabled={loading}>{loading ? '搜索中...' : '搜索'}</button>
    </form>

    {history.length > 0 && <div className="filter-bar" style={{ gap: 8, flexWrap: 'wrap' }}>
      <span style={{ fontSize: 12, color: 'var(--color-text-secondary)' }}>最近：</span>
      {history.map(item => <button
        key={item}
        type="button"
        className="button button--quiet"
        onClick={() => {
          setQuery(item)
          setPage(1)
          void doSearch(item, scope, 1, feedId)
        }}
      >
        {item}
      </button>)}
    </div>}

    {loading
      ? <PageState kind="loading" title="正在搜索" />
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
          : <>
            <p style={{ fontSize: 12, color: 'var(--color-text-secondary)', margin: '0 0 12px' }}>
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
              onChange={next => void changePage(next)}
            />
          </>}
  </div>
}
