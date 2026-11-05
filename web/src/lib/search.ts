import { api } from '@/lib/api'

// ============================================================
// 跨源搜索
//
// 书签匹配 标题/URL/分组名，RSS 条目匹配 标题/正文/作者，
// 待办匹配 标题/描述，剪贴板只匹配未过期粘贴的正文。
// 排序由服务端按命中位置加权（标题 > 作者/分组 > 正文），
// 前端只负责分页与高亮。
// ============================================================

/** 搜索页输入框的 id。外壳的 `/` 快捷键跳到搜索页后按它找到输入框聚焦。 */
export const SEARCH_INPUT_ID = 'search-input'

export type SearchScope = 'all' | 'bookmarks' | 'rss' | 'todos' | 'pastes'

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

export interface TodoSearchItem {
  id: number
  title: string
  status: string
  due_date?: string
  /** 描述里命中关键词附近的一段；为空表示命中在标题。 */
  snippet?: string
  created_at: string
}

/** 剪贴板结果不含 delete_token 与完整正文。 */
export interface PasteSearchItem {
  id: number
  /** /paste/:public_id 使用的公开 ID。 */
  public_id: string
  snippet: string
  created_at: string
  expires_at: string
}

export interface SearchResult {
  query: string
  scope: SearchScope
  bookmarks: BookmarkSearchItem[] | null
  bookmarks_total: number
  rss_entries: RssEntrySearchItem[] | null
  rss_entries_total: number
  todos: TodoSearchItem[] | null
  todos_total: number
  pastes: PasteSearchItem[] | null
  pastes_total: number
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
