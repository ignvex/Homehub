import { api } from '@/lib/api'

// ============================================================
// 跨源搜索
//
// 书签匹配 标题/URL/分组名，RSS 条目匹配 标题/正文/作者。
// 排序由服务端按命中位置加权（标题 > 作者/分组 > 正文），
// 前端只负责分页与高亮。
// ============================================================

export type SearchScope = 'all' | 'bookmarks' | 'rss'

export interface BookmarkSearchItem {
  id: number
  title: string
  url: string
  group_name: string
}

export interface RssEntrySearchItem {
  id: number
  title: string
  link: string
  author?: string
  feed_title: string
  feed_ids?: number[]
  /** 正文里命中关键词附近的一小段纯文本；为空表示命中在标题或作者上。 */
  snippet?: string
}

export interface SearchResult {
  query: string
  scope: SearchScope
  bookmarks: BookmarkSearchItem[] | null
  bookmarks_total: number
  rss_entries: RssEntrySearchItem[] | null
  rss_entries_total: number
  page: number
  per_page: number
  took_ms: number
}

export interface SearchParams {
  q: string
  scope?: SearchScope
  page?: number
  perPage?: number
  feedId?: number
  groupId?: number
}

export const searchApi = {
  search: (params: SearchParams, init?: RequestInit) => {
    const query = new URLSearchParams({ q: params.q })
    if (params.scope) query.set('scope', params.scope)
    if (params.page && params.page > 1) query.set('page', String(params.page))
    if (params.perPage) query.set('per_page', String(params.perPage))
    if (params.feedId) query.set('feed_id', String(params.feedId))
    if (params.groupId) query.set('group_id', String(params.groupId))
    return api.get<SearchResult>(`/search?${query.toString()}`, init)
  },
}
