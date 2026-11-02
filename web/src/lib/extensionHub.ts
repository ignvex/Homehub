import { api } from '@/lib/api'

// ============================================================
// Extension hub 通道
//
// 这一组接口把 HomeHub 作为宿主，接到 HomeHub Extension 的
// /api/v1/hub/* 签名通道。上游的状态码在服务端已经折成 502/503，
// 这里不需要区分「上游 401」与「网络断了」——按 error_code 分支即可。
// ============================================================

/** ExtensionHubStatus 是对接状态。四层是独立的问题，前端按层给提示。 */
export interface ExtensionHubStatus {
  enabled: boolean
  configured: boolean
  base_url?: string
  public_key?: string
  public_key_pem?: string
  /** 公钥 SHA-256 前 16 位，便于在日志里核对。 */
  key_fingerprint?: string
  /** 私钥来处：database / file / none。 */
  key_source?: string
  reachable: boolean
  authenticated: boolean
  rss_available: boolean
  subscription_count: number
  /** ready 来自 Extension 的 /readyz；null 表示没探到（端点不存在或超时）。 */
  ready: boolean | null
  ready_detail?: string
  error_code?: string
  error_message?: string
}

/** RemoteSubscription 是 Extension 侧的订阅。 */
export interface RemoteSubscription {
  id: number
  url: string
  title: string
  site_url: string
  enabled: boolean
  fetch_interval: string
  next_fetch_at: number | null
  last_fetch_at: number | null
  created_at: number
  last_status: number
  last_error: string
  error_count: number
  unread_count: number
  /** 本地存在同 URL 的订阅源时给出它的 id。 */
  local_feed_id?: number
  in_local: boolean
}

export interface PushResult {
  feed_id: number
  remote_id: number
  created: boolean
  remote: RemoteSubscription
}

export interface SyncItem {
  feed_id: number
  url: string
  status: 'created' | 'updated' | 'failed'
  remote_id?: number
  error?: string
}

export interface SyncResult {
  /** 本次推送到 Extension 的条数，不含归属本机的源。 */
  total: number
  created: number
  updated: number
  failed: number
  /** 归属本机抓取、因此没有推送的条数。 */
  skipped_local: number
  items: SyncItem[]
}

/** AccessKey 是已签发的访问密钥。响应里永远不含明文。 */
export interface AccessKey {
  id: string
  name: string
  client_id: string
  created_at: number
  expires_at: number | null
  last_used_at: number | null
  revoked: boolean
  /** live 由服务端计算，前端不要自己比较 expires_at 与本机时间。 */
  live: boolean
}

/** CreatedKey 的 key 只出现这一次。 */
export interface CreatedKey extends AccessKey {
  key: string
}

/** 签名私钥的来源与公钥。**不含私钥本身**。 */
export interface KeyInfo {
  /** config / none */
  source: 'config' | 'none'
  configured: boolean
  public_key?: string
  /** PKIX PEM 全文，部署时优先用它（自带类型标记，无歧义）。 */
  public_key_pem?: string
  fingerprint?: string
  /** 配了 PEM 但解析不了：粘贴时多半被截断了。 */
  broken?: boolean
}

export const extensionHubApi = {
  status: () => api.get<ExtensionHubStatus>('/extension/status'),
  listSubscriptions: () => api.get<RemoteSubscription[]>('/extension/subscriptions'),
  push: (feedId: number, options: { enabled?: boolean; fetchNow?: boolean } = {}) =>
    api.post<PushResult>('/extension/subscriptions', {
      feed_id: feedId,
      ...(options.enabled === undefined ? {} : { enabled: options.enabled }),
      ...(options.fetchNow === undefined ? {} : { fetch_now: options.fetchNow }),
    }),
  sync: () => api.post<SyncResult>('/extension/subscriptions/sync'),
  removeSubscription: (id: number) =>
    api.delete<{ deleted: boolean; already_deleted: boolean }>(`/extension/subscriptions/${id}`),
  listKeys: () => api.get<AccessKey[]>('/extension/keys'),
  issueKey: (name: string, expiresIn?: string) =>
    api.post<CreatedKey>('/extension/keys', {
      name,
      ...(expiresIn ? { expires_in: expiresIn } : {}),
    }),
  revokeKey: (id: string) =>
    api.delete<{ revoked: boolean; already_deleted: boolean }>(`/extension/keys/${id}`),

  // 签名私钥。读取只回公钥与指纹；写入收 PEM 或 base64，永不回读。
  getKey: () => api.get<KeyInfo>('/extension/key'),
  setKey: (privateKey: string) => api.put<KeyInfo>('/extension/key', { private_key: privateKey }),
  deleteKey: () => api.delete<{ deleted: boolean }>('/extension/key'),
}
