import { api } from '@/lib/api'

export type SearchScope = 'all' | 'bookmarks' | 'videos' | 'articles' | 'users' | 'rss'
export interface BookmarkSearchItem { id: number; title: string; url: string; group_name: string }
export interface VideoSearchItem { aid: number; bvid: string; title: string; owner_name: string; pic: string }
export interface ArticleSearchItem { cvid: number; title: string; author_name: string }
export interface UserSearchItem { mid: number; name: string; face: string }
export interface RssEntrySearchItem { id: number; title: string; link: string; feed_title: string }
export interface SearchResult {
  bookmarks: BookmarkSearchItem[] | null
  videos: VideoSearchItem[] | null
  articles: ArticleSearchItem[] | null
  users: UserSearchItem[] | null
  rss_entries: RssEntrySearchItem[] | null
}

export const searchApi = {
  search: (query: string, scope: SearchScope) => api.get<SearchResult>(`/search?q=${encodeURIComponent(query)}&scope=${scope}`),
}
