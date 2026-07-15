import { api } from '@/lib/api'
import type { EntityBase, PageResult } from '@/lib/types'

export type UserRole = 'admin' | 'user'
export interface ManagedUser extends EntityBase {
  username: string
  email?: string
  role: UserRole
  is_active: boolean
}

export interface LogEntry extends EntityBase {
  level: string
  message: string
  source?: string
  fields?: string
  log_time: string
}

export interface LogQuery {
  page: number
  perPage: number
  level: string
  query: string
  since: string
  until: string
}

function localDateToRFC3339(value: string): string {
  return value ? new Date(value).toISOString() : ''
}

export const adminApi = {
  listUsers: () => api.get<ManagedUser[]>('/admin/users'),
  createUser: (body: { username: string; password: string; email?: string; role: UserRole }) => api.post<ManagedUser>('/admin/users', body),
  updateUser: (id: number, body: Partial<Pick<ManagedUser, 'username' | 'email' | 'role' | 'is_active'>>) => api.put<ManagedUser>(`/admin/users/${id}`, body),
  deleteUser: (id: number) => api.delete<void>(`/admin/users/${id}`),
  resetPassword: (id: number, newPassword: string) => api.put<void>(`/admin/users/${id}/reset-password`, { new_password: newPassword }),
  refreshAvatar: (id: number) => api.post<void>(`/admin/users/${id}/refresh-avatar`),
  listLogs: (query: LogQuery) => {
    const params = new URLSearchParams({ page: String(query.page), per_page: String(query.perPage) })
    if (query.level) params.set('level', query.level)
    if (query.query) params.set('q', query.query)
    if (query.since) params.set('since', localDateToRFC3339(query.since))
    if (query.until) params.set('until', localDateToRFC3339(query.until))
    return api.get<PageResult<LogEntry>>(`/admin/logs?${params}`)
  },
}
