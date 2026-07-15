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

export const pasteApi = {
  list: () => api.get<PasteSummary[]>('/pastes'),
  create: (content: string) => api.post<PasteResponse>('/pastes', { content }),
  get: (id: string) => api.get<PasteResponse>(`/pastes/${id}`),
  delete: (id: string) => api.delete<void>(`/pastes/${id}`),
}
