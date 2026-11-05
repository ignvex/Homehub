import { api } from '@/lib/api'
import type { EntityBase, PageResult } from '@/lib/types'

/** 抓取方式：local 由 HomeHub 自己抓，extension 推给 hhe。 */
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
  /** 单源调度开关：false 表示手动暂停，不参与后台抓取。 */
  fetch_enabled: boolean
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
  /** 数据库里的单源开关。 */
  fetch_enabled: boolean
  /** 综合判定：总开关开 + 本机抓 + 未被暂停。 */
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
  fetch_enabled?: boolean
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
  /** 暂停 / 恢复这个源的调度。 */
  fetch_enabled?: boolean
  group_id?: number | null
}

/** BatchUpdateFeedBody 把同一组变更应用到多个订阅源。
 *
 * 字段语义与单条更新完全一致：省略不改、空串清空、group_id 传 null 清分组。
 * 服务端在一个事务里执行，不存在的 ID 被忽略。
 */
export interface BatchUpdateFeedBody extends UpdateFeedBody {
  ids: number[]
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
  /** 正文的纯文本摘要。新版列表接口只给这一份，不返回完整 HTML。 */
  excerpt?: string
  /** 是否有正文可展开。正文可能只有图片，此时摘要为空但依然可展开。 */
  has_content?: boolean
  /**
   * 旧版列表接口直接下发的完整正文。
   *
   * 仅用于向后兼容：前端先于后端上线、或浏览器撞上旧缓存时，
   * 列表里可能只有 content 而没有 excerpt/has_content。渲染层靠它
   * 退回旧行为，而不是把「展开正文」整个变没。
   */
  content?: string
  magnet_url?: string
  published: string
  fetched_at: string
}

/** EntryContent 是 GET /rss/entries/:id/content 的响应。 */
export interface RSSEntryContent {
  id: number
  content: string
}

export const rssApi = {
  listFeeds: () => api.get<RSSFeed[]>('/rss/feeds'),
  createFeed: (body: CreateFeedBody) => api.post<RSSFeed>('/rss/feeds', body),
  // group_id 传 null 表示显式清空分组，省略则保持原值 —— 后端按此区分。
  updateFeed: (id: number, body: UpdateFeedBody) => api.put<void>(`/rss/feeds/${id}`, body),
  // 批量更新走单条端点的话，20 个源就是 20 次往返，
  // 中间失败会留下「一半改了、一半没改」的中间态。
  batchUpdateFeeds: (body: BatchUpdateFeedBody) =>
    api.put<{ updated: number }>('/rss/feeds/batch', body),
  schedule: () => api.get<ScheduleState>('/rss/schedule'),
  deleteFeed: (id: number) => api.delete<void>(`/rss/feeds/${id}`),
  fetchFeed: (id: number) => api.post<void>(`/rss/feeds/${id}/fetch`),
  fetchAll: () => api.post<void>('/rss/fetch-all'),
  listEntries: (feedID: number | null, groupID: number | null, page: number, perPage = 20, init?: RequestInit) => {
    const query = new URLSearchParams({ page: String(page), per_page: String(perPage) })
    if (feedID) query.set('feed_id', String(feedID))
    if (groupID) query.set('group_id', String(groupID))
    return api.get<PageResult<RSSEntry>>(`/rss/entries?${query}`, init)
  },
  // 正文按需取：列表只带摘要，展开某条时才调这个端点。
  entryContent: (id: number) => api.get<RSSEntryContent>(`/rss/entries/${id}/content`),
  listGroups: () => api.get<RSSGroup[]>('/rss/groups'),
  createGroup: (name: string) => api.post<RSSGroup>('/rss/groups', { name }),
  updateGroup: (id: number, body: { name?: string; sort_order?: number }) => api.put<void>(`/rss/groups/${id}`, body),
  deleteGroup: (id: number) => api.delete<void>(`/rss/groups/${id}`),
}
