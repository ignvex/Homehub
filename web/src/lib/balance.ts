import { api } from '@/lib/api'

export interface TargetResult {
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

export interface BalanceStatus {
  enabled: boolean
  target_count: number
  ok_count: number
  error_count: number
  last_refresh_at: string
}

export const balanceApi = {
  list: () => api.get<TargetResult[]>('/balances'),
  refresh: () => api.post<TargetResult[]>('/balances/refresh'),
  status: () => api.get<BalanceStatus>('/balances/status'),
}
