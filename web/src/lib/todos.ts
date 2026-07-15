import { api } from '@/lib/api'
import type { EntityBase } from '@/lib/types'

export type TodoStatus = 'planned' | 'pending' | 'in_progress' | 'completed' | 'paused'
export type TodoSort = 'priority' | 'due_date' | 'created_at'

export interface Todo extends EntityBase {
  title: string
  description?: string
  status: TodoStatus
  priority: number
  due_date?: string
  sort_order: number
}

export interface TodoInput {
  title: string
  description?: string
  priority: number
  due_date?: string
}

export const todoStatuses: { value: TodoStatus; label: string }[] = [
  { value: 'planned', label: '计划中' },
  { value: 'pending', label: '待处理' },
  { value: 'in_progress', label: '进行中' },
  { value: 'completed', label: '已完成' },
  { value: 'paused', label: '已暂停' },
]

export const todosApi = {
  list: (status: TodoStatus | '', sort: TodoSort) => {
    const query = new URLSearchParams({ sort })
    if (status) query.set('status', status)
    return api.get<Todo[]>(`/todos?${query}`)
  },
  create: (body: TodoInput) => api.post<Todo>('/todos', body),
  update: (id: number, body: Partial<TodoInput & { status: TodoStatus }>) => api.put<void>(`/todos/${id}`, body),
  updateStatus: (id: number, status: TodoStatus) => api.patch<void>(`/todos/${id}/status`, { status }),
  delete: (id: number) => api.delete<void>(`/todos/${id}`),
  reorder: (ids: number[]) => api.post<void>('/todos/reorder', { ids }),
}
