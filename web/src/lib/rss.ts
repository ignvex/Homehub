import { api } from '@/lib/api'
import type { EntityBase, PageResult } from '@/lib/types'

export interface RSSFeed extends EntityBase {
  title: string
  url: string
  site_url: string
  description?: string
  icon_url?: string
  fetch_interval: number
  last_fetch?: string
  error_msg?: string
  group_id?: number
}

export interface RSSGroup extends EntityBase {
  name: string
  sort_order: number
  feeds?: RSSFeed[]
}

export interface RSSEntry extends EntityBase {
  /** 收录该条目的全部订阅源；同一内容可被多个源收录 */
  feed_ids: number[]
  title: string
  link: string
  author?: string
  content?: string
  magnet_url?: string
  published: string
  fetched_at: string
}

export const rssApi = {
  listFeeds: () => api.get<RSSFeed[]>('/rss/feeds'),
  createFeed: (url: string, groupId?: number) => api.post<RSSFeed>('/rss/feeds', { url, ...(groupId ? { group_id: groupId } : {}) }),
  updateFeed: (id: number, body: { title?: string; fetch_interval?: number; group_id?: number }) => api.put<void>(`/rss/feeds/${id}`, body),
  deleteFeed: (id: number) => api.delete<void>(`/rss/feeds/${id}`),
  fetchFeed: (id: number) => api.post<void>(`/rss/feeds/${id}/fetch`),
  fetchAll: () => api.post<void>('/rss/fetch-all'),
  listEntries: (feedID: number | null, groupID: number | null, page: number, perPage = 20) => {
    const query = new URLSearchParams({ page: String(page), per_page: String(perPage) })
    if (feedID) query.set('feed_id', String(feedID))
    if (groupID) query.set('group_id', String(groupID))
    return api.get<PageResult<RSSEntry>>(`/rss/entries?${query}`)
  },
  listGroups: () => api.get<RSSGroup[]>('/rss/groups'),
  createGroup: (name: string) => api.post<RSSGroup>('/rss/groups', { name }),
  updateGroup: (id: number, body: { name?: string; sort_order?: number }) => api.put<void>(`/rss/groups/${id}`, body),
  deleteGroup: (id: number) => api.delete<void>(`/rss/groups/${id}`),
}
