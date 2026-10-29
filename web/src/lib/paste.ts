import { api } from '@/lib/api'

export interface PasteResponse {
  id: string
  content: string
  created_at: string
  expires_at: string
  delete_token: string
}

export interface PasteSummary {
  id: string
  content_preview: string
  created_at: string
  expires_at: string
}

// 删除密钥按粘贴 ID 存在本地。
//
// 服务端只下发一次 delete_token，且删除接口要求提供它（防止仅凭 URL 里的
// public_id 就能删除别人的粘贴）。存在 localStorage 意味着：
// 同一个浏览器能删自己创建的粘贴，换设备则不能 —— 这与粘贴本身的
// 临时性质一致，过期后也无所谓删不删。
const TOKEN_PREFIX = 'homehub.paste.delete.'

function saveToken(id: string, token: string) {
  try {
    localStorage.setItem(TOKEN_PREFIX + id, token)
  } catch {
    /* 隐私模式或配额满：降级为不可删除 */
  }
}

export function loadPasteToken(id: string): string {
  try {
    return localStorage.getItem(TOKEN_PREFIX + id) ?? ''
  } catch {
    return ''
  }
}

function dropToken(id: string) {
  try {
    localStorage.removeItem(TOKEN_PREFIX + id)
  } catch {
    /* 忽略 */
  }
}

export const pasteApi = {
  list: () => api.get<PasteSummary[]>('/pastes'),
  create: async (content: string) => {
    const result = await api.post<PasteResponse>('/pastes', { content })
    if (result.delete_token) saveToken(result.id, result.delete_token)
    return result
  },
  get: (id: string) => api.get<PasteResponse>(`/pastes/${id}`),
  delete: async (id: string) => {
    const token = loadPasteToken(id)
    await api.delete<void>(`/pastes/${id}${token ? `?token=${encodeURIComponent(token)}` : ''}`)
    dropToken(id)
  },
  // hasToken 用于界面判断当前浏览器是否持有删除权限。
  hasToken: (id: string) => loadPasteToken(id) !== '',
}
