import { api } from '@/lib/api'

export interface HealthStatus {
  status: string
  db?: string
  uptime_seconds?: number
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
}

export const profileApi = {
  get: () => api.get<UserProfile>('/auth/profile'),
  update: (body: { username: string; email: string }) => api.put<UserProfile>('/auth/profile', body),
  changePassword: (body: { old_password: string; new_password: string }) => api.post<void>('/auth/change-password', body),
  // 头像尺寸由后端 gravatar.default_size 统一决定，这里只负责缓存破坏参数。
  avatarUrl: (revision?: string | number) => `/api/v1/avatar/me${revision ? `?v=${revision}` : ''}`,
}
