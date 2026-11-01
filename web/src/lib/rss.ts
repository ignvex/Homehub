import { api } from '@/lib/api'
import type { EntityBase, PageResult } from '@/lib/types'

/** 抓取执行者：local 由 HomeHub 自己抓，extension 推给 hhe。 */
export type FetchBy = 'local' | 'extension'

export interface RSSFeed extends EntityBase {
  title: string
  url: string
  site_url: string
  /** 来自 feed 元数据，抓取时会被覆盖。 */
  description?: string
  /** 使用者自己写的备注，抓取不会动它。 */
  remark?: string
  icon_url?: string
  fetch_interval: number
  fetch_by: FetchBy
  last_fetch?: string
  error_msg?: string
  group_id?: number
}

/** 单个订阅源的调度状态，全部由 fetch_interval + last_fetch 推导。 */
export interface FeedSchedule {
  feed_id: number
  title: string
  url: string
  fetch_by: FetchBy
  enabled: boolean
  interval_seconds: number
  last_fetch_at: number | null
  next_fetch_at: number | null
  due: boolean
  overdue_seconds: number
  error_msg?: string
}

export interface ScheduleState {
  enabled: boolean
  tick_seconds: number
  due_count: number
  local_count: number
  extension_count: number
  feeds: FeedSchedule[]
}

/** CreateFeedBody 创建订阅源。除 fetch_now 外不发任何网络请求。 */
export interface CreateFeedBody {
  url: string
  title?: string
  remark?: string
  fetch_interval?: number
  fetch_by?: FetchBy
  group_id?: number
  /** 显式要求保存后抓一次；默认 false。 */
  fetch_now?: boolean
}

/** UpdateFeedBody 省略字段表示不改；title/remark 传空串表示清空。 */
export interface UpdateFeedBody {
  title?: string
  remark?: string
  fetch_interval?: number
  fetch_by?: FetchBy
  group_id?: number | null
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
  createFeed: (body: CreateFeedBody) => api.post<RSSFeed>('/rss/feeds', body),
  // group_id 传 null 表示显式清空分组，省略则保持原值 —— 后端按此区分。
  updateFeed: (id: number, body: UpdateFeedBody) => api.put<void>(`/rss/feeds/${id}`, body),
  schedule: () => api.get<ScheduleState>('/rss/schedule'),
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
