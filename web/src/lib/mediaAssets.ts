import { api } from '@/lib/api'

export interface MediaAsset {
  id: number
  filename: string
  original_name: string
  file_path: string
  file_size: number
  mime_type: string
  media_type: 'image' | 'video'
  width?: number
  height?: number
  created_at: string
}

export const mediaAssetsApi = {
  list: () => api.get<MediaAsset[]>('/media-assets'),
  upload: (file: File) => {
    const data = new FormData()
    data.append('file', file)
    return api.post<MediaAsset>('/media-assets', data)
  },
  delete: (id: number) => api.delete<void>(`/media-assets/${id}`),
}
