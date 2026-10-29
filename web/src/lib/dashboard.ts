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
  avatarUrl: (size = 80, revision?: string | number) => `/api/v1/avatar/me?size=${size}${revision ? `&v=${revision}` : ''}`,
}
