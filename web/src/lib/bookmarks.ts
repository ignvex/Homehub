import { api } from '@/lib/api'

export interface Bookmark {
  id: number
  group_id: number
  title: string
  url: string
  icon: string
  priority: number
  sort_order?: number
}

export interface BookmarkGroup {
  id: number
  name: string
  priority: number
  bookmarks?: Bookmark[]
}

export interface BookmarkInput {
  group_id: number
  title: string
  url: string
  icon?: string
  priority?: number
}

export const bookmarksApi = {
  listGroups: () => api.get<BookmarkGroup[]>('/bookmark-groups'),
  createGroup: (name: string) => api.post<BookmarkGroup>('/bookmark-groups', { name }),
  updateGroup: (id: number, body: { name: string; priority?: number }) => api.put<void>(`/bookmark-groups/${id}`, body),
  deleteGroup: (id: number) => api.delete<void>(`/bookmark-groups/${id}`),
  reorderGroups: (ids: number[]) => api.post<void>('/bookmark-groups/reorder', { ids }),
  create: (body: BookmarkInput) => api.post<Bookmark>('/bookmarks', body),
  update: (id: number, body: Partial<Omit<BookmarkInput, 'group_id'>>) => api.put<void>(`/bookmarks/${id}`, body),
  delete: (id: number) => api.delete<void>(`/bookmarks/${id}`),
  reorder: (groupID: number, ids: number[]) => api.post<void>('/bookmarks/reorder', { group_id: groupID, ids }),
}
