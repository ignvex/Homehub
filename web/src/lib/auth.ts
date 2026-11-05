import { createContext, useContext } from 'react'
import type { AuthStatus } from '@/lib/home'

export interface AuthValue {
  /** null 表示首次检查尚未完成。 */
  auth: AuthStatus | null
  /** 重新请求 /auth/status（如改名后、页面重新可见时）。 */
  refresh: () => Promise<void>
}

export const AuthContext = createContext<AuthValue>({ auth: null, refresh: async () => undefined })

/** 应用级认证状态，由 App 在启动时请求一次。 */
export function useAuth(): AuthValue {
  return useContext(AuthContext)
}
