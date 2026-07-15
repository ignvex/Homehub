import { api } from '@/lib/api'

export interface AuthStatus { logged_in: boolean; username?: string; role?: string; email?: string; avatar_url?: string }
export interface Bookmark { id: number; title: string; url: string; icon?: string }
export interface BookmarkGroup { id: number; name: string; bookmarks?: Bookmark[] }
export interface RSSFeed { id: number; title: string; url: string; site_url?: string; icon_url?: string }
export interface RSSEntry { id: number; feed_id: number; title: string; link: string; author?: string; magnet_url?: string; published: string }
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
  background: () => api.get<BackgroundSettings>('/background'),
  bookmarks: () => api.get<BookmarkGroup[]>('/bookmark-groups'),
  feeds: () => api.get<RSSFeed[]>('/rss/feeds'),
  entries: () => api.get<RSSPage>('/rss/entries?page=1&per_page=50'),
  media: () => api.get<MediaAsset[]>('/media-assets'),
  logout: () => api.post<void>('/auth/logout'),
}
