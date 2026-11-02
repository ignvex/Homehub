import { api } from '@/lib/api'

export interface AuthStatus { logged_in: boolean; id?: number; username?: string; role?: string; email?: string; avatar_url?: string }
export interface Bookmark { id: number; title: string; url: string; icon?: string }
export interface BookmarkGroup { id: number; name: string; bookmarks?: Bookmark[] }
export interface RSSFeed { id: number; title: string; url: string; site_url?: string; icon_url?: string }
// 列表接口只返回摘要：正文按需从 /rss/entries/:id/content 取，
// 首页卡片也只需要摘要。content 保留仅为兼容旧后端。
export interface RSSEntry { id: number; feed_ids: number[]; title: string; link: string; author?: string; excerpt?: string; has_content?: boolean; content?: string; magnet_url?: string; published: string }
export interface RSSPage { list: RSSEntry[]; total: number }
export interface MediaAsset { id: number; file_path: string; media_type: 'image' | 'video' }
export interface BackgroundSettings {
  mode: 'single' | 'carousel' | 'random'
  interval_seconds: number
  transition_effect: 'fade' | 'slide'
  fade_duration_ms: number
  selected_asset_ids: number[]
}

export const homeApi = {
  auth: () => api.get<AuthStatus>('/auth/status'),
  bookmarks: () => api.get<BookmarkGroup[]>('/bookmark-groups'),
  feeds: () => api.get<RSSFeed[]>('/rss/feeds'),
  entries: () => api.get<RSSPage>('/rss/entries?page=1&per_page=50'),
  logout: () => api.post<void>('/auth/logout'),
}
