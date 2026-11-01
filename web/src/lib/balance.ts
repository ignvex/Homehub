import { api } from '@/lib/api'

// ============================================================
// 余额监控
//
// 监控点自 1.1.0 起存在数据库里，增删改走这里的接口。
// api_key 只进不出：读接口一律只返回掩码与 has_api_key。
// ============================================================

/** BalanceTarget 是一个监控点，含最新缓存结果。 */
export interface BalanceTarget {
  id: number
  name: string
  type: string
  base_url: string
  enabled: boolean
  sort_order: number
  has_api_key: boolean
  api_key_masked?: string
  balance: number
  currency?: string
  status: 'ok' | 'error' | 'checking' | 'idle'
  last_error?: string
  last_checked_at: string
}

/** BalanceTargetInput 是写接口的请求体；省略的字段表示「保持原值」。 */
export interface BalanceTargetInput {
  name?: string
  type?: string
  base_url?: string
  /** 传空串表示清空密钥，不传表示不改。 */
  api_key?: string
  enabled?: boolean
  sort_order?: number
}

export interface BalanceStatus {
  enabled: boolean
  target_count: number
  ok_count: number
  error_count: number
  last_refresh_at: string
}

/** TargetResult 是全量刷新返回的原始结果。 */
export interface TargetResult {
  id: number
  name: string
  type: string
  balance: number
  currency: string
  enabled: boolean
  status: string
  base_url: string
  last_checked_at: string
  last_error: string
}

export const balanceApi = {
  list: () => api.get<BalanceTarget[]>('/balances'),
  refresh: () => api.post<TargetResult[]>('/balances/refresh'),
  refreshOne: (id: number) => api.post<BalanceTarget>(`/balances/${id}/refresh`),
  status: () => api.get<BalanceStatus>('/balances/status'),
  create: (input: BalanceTargetInput) => api.post<BalanceTarget>('/balances', input),
  update: (id: number, input: BalanceTargetInput) => api.put<BalanceTarget>(`/balances/${id}`, input),
  remove: (id: number) => api.delete<{ deleted: boolean; id: number }>(`/balances/${id}`),
}
