import { useEffect, useState, type FormEvent } from 'react'
import { Notice, PageState } from '@/components/WorkspaceUI'
import { searchApi, type SearchResult, type SearchScope } from '@/lib/search'

const scopes: { value: SearchScope; label: string }[] = [{ value: 'all', label: '全部' }, { value: 'bookmarks', label: '书签' }, { value: 'videos', label: '视频' }, { value: 'articles', label: '专栏' }, { value: 'users', label: '用户' }, { value: 'rss', label: 'RSS' }]

export function SearchPage() {
  const params = new URLSearchParams(window.location.search)
  const initialQuery = params.get('q') || ''

  const [query, setQuery] = useState(initialQuery)
  const [scope, setScope] = useState<SearchScope>('all')
  const [result, setResult] = useState<SearchResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function doSearch(q: string, s: SearchScope) {
    if (!q.trim()) return
    setLoading(true)
    try { setResult(await searchApi.search(q.trim(), s)); setError('') }
    catch (cause) { setError(cause instanceof Error ? cause.message : '搜索失败') }
    finally { setLoading(false) }
  }
  async function search(event: FormEvent) {
    event.preventDefault()
    await doSearch(query, scope)
  }
  useEffect(() => {
    if (initialQuery) void doSearch(initialQuery, 'all')
  }, [])

  const count = result ? Object.values(result).reduce((sum, list) => sum + (list?.length ?? 0), 0) : 0
  return <div className="business-page"><div className="page-toolbar"><div><p>跨域检索</p><h2>数据搜索</h2></div></div><Notice message={error} tone="error" onDismiss={() => setError('')} /><form className="filter-bar" onSubmit={search}><input autoFocus value={query} onChange={event => setQuery(event.target.value)} placeholder="搜索书签、视频、RSS、专栏或用户" required /><select value={scope} onChange={event => setScope(event.target.value as SearchScope)}>{scopes.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select><button className="button button--primary" disabled={loading}>{loading ? '搜索中...' : '搜索'}</button></form>{loading ? <PageState kind="loading" title="正在搜索" /> : !result ? <PageState kind="empty" title="输入关键词开始检索" description="搜索会按数据源返回最多 10 条匹配记录。" /> : count === 0 ? <PageState kind="empty" title="没有匹配结果" description="尝试使用更短的关键词，或更换搜索范围。" /> : <div className="search-results"><SearchSection title="书签" items={result.bookmarks ?? []} render={item => <a className="result-row" href={item.url} target="_blank" rel="noreferrer"><strong>{item.title}</strong><span>{item.group_name} · {item.url}</span></a>} /><SearchSection title="视频" items={result.videos ?? []} render={item => <a className="result-row result-row--media" href={`/bilibili/videos/${item.aid}`}>{item.pic && <img className="result-thumb" src={item.pic} alt="" loading="lazy" />}<div className="result-row-body"><strong>{item.title}</strong><span>{item.owner_name} · {item.bvid || `AV${item.aid}`}</span></div></a>} /><SearchSection title="专栏" items={result.articles ?? []} render={item => <a className="result-row" href={`/bilibili/articles/${item.cvid}`}><strong>{item.title}</strong><span>{item.author_name} · CV{item.cvid}</span></a>} /><SearchSection title="用户" items={result.users ?? []} render={item => <a className="result-row result-row--media" href={`/bilibili/users/${item.mid}`}>{item.face && <img className="result-thumb result-thumb--round" src={item.face} alt="" loading="lazy" />}<div className="result-row-body"><strong>{item.name}</strong><span>MID {item.mid}</span></div></a>} /><SearchSection title="RSS 订阅" items={result.rss_entries ?? []} render={item => <a className="result-row" href={item.link} target="_blank" rel="noreferrer"><strong>{item.title}</strong><span>{item.feed_title}</span></a>} /></div>}</div>
}

function SearchSection<T extends { title?: string; name?: string; id?: number; aid?: number; cvid?: number; mid?: number }>({ title, items, render }: { title: string; items: T[]; render: (item: T) => React.ReactNode }) { if (!items.length) return null; return <section className="data-panel"><header className="data-panel-head"><div><h2>{title}</h2><p>{items.length} 条结果</p></div></header><div className="result-list">{items.map((item, index) => <div key={item.id ?? item.aid ?? item.cvid ?? item.mid ?? index}>{render(item)}</div>)}</div></section> }
