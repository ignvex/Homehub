import { api } from '@/lib/api'

export interface TableStats {
  table: string
  row_count: number
  est_bytes: number
  est_mb: string
}

export interface CrawlerProgress {
  type: 'video' | 'article' | 'user'
  enabled: boolean
  stored_count: number
  window_base: number
  window_size: number
  window_scanned: number
  window_progress_pct: string
}

export interface CrawlerStats {
  video: TableStats
  video_page: TableStats
  article: TableStats
  user: TableStats
  total_mb: string
  progress: CrawlerProgress[]
}

export interface HealthStatus {
  status: string
  db?: string
  timestamp?: string
}

export interface UserProfile {
  id: number
  username: string
  email: string
  role: string
  avatar_url: string
}

export const dashboardApi = {
  health: () => api.get<HealthStatus>('/health'),
  stats: () => api.get<CrawlerStats>('/bilibili/stats'),
}

export const profileApi = {
  get: () => api.get<UserProfile>('/auth/profile'),
  update: (body: { username: string; email: string }) => api.put<UserProfile>('/auth/profile', body),
  changePassword: (body: { old_password: string; new_password: string }) => api.post<void>('/auth/change-password', body),
  avatarUrl: (size = 80, revision?: string | number) => `/api/v1/avatar/me?size=${size}${revision ? `&v=${revision}` : ''}`,
}
