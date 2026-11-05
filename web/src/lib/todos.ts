import { api } from '@/lib/api'
import type { EntityBase } from '@/lib/types'

export type TodoStatus = 'planned' | 'pending' | 'in_progress' | 'completed' | 'paused'
export type TodoSort = 'priority' | 'due_date' | 'created_at'

/** 待办里出现的用户摘要（创建者、负责人、用户目录）。 */
export interface UserBrief {
  id: number
  username: string
}

/** 「谁的待办」筛选：全部 / 我创建的 / 指派给我。 */
export type TodoOwner = '' | 'mine' | 'assigned'

export interface Todo extends EntityBase {
  title: string
  description?: string
  status: TodoStatus
  priority: number
  due_date?: string
  sort_order: number
  /** 创建者；null 是改造前的旧数据，只有 3 级及以上能动。 */
  created_by?: number | null
  assignee_id?: number | null
  creator?: UserBrief | null
  assignee?: UserBrief | null
}

export interface TodoInput {
  title: string
  description?: string
  priority: number
  due_date?: string
  assignee_id?: number
}

/**
 * TodoUpdateBody 是 PUT /todos/:id 的请求体。
 * description / due_date / assignee_id 三态：省略不改，null 清空，值写入。
 */
export interface TodoUpdateBody {
  title?: string
  status?: TodoStatus
  priority?: number
  description?: string | null
  due_date?: string | null
  assignee_id?: number | null
}

export const todoStatuses: { value: TodoStatus; label: string }[] = [
  { value: 'planned', label: '计划中' },
  { value: 'pending', label: '待处理' },
  { value: 'in_progress', label: '进行中' },
  { value: 'completed', label: '已完成' },
  { value: 'paused', label: '已暂停' },
]

export const todosApi = {
  list: (status: TodoStatus | '', sort: TodoSort, owner: TodoOwner = '') => {
    const query = new URLSearchParams({ sort })
    if (status) query.set('status', status)
    if (owner === 'mine') query.set('mine', '1')
    if (owner === 'assigned') query.set('assignee', 'me')
    return api.get<Todo[]>(`/todos?${query}`)
  },
  create: (body: TodoInput) => api.post<Todo>('/todos', body),
  update: (id: number, body: TodoUpdateBody) => api.put<void>(`/todos/${id}`, body),
  updateStatus: (id: number, status: TodoStatus) => api.patch<void>(`/todos/${id}/status`, { status }),
  delete: (id: number) => api.delete<void>(`/todos/${id}`),
  reorder: (ids: number[]) => api.post<void>('/todos/reorder', { ids }),
  /** 启用中的用户（id + 用户名），供负责人选择器使用。 */
  directory: () => api.get<UserBrief[]>('/users/directory'),
}
