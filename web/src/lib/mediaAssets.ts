import { api } from '@/lib/api'

// MediaKind 与后端 service.MediaCategory 保持一致。
export type MediaKind = 'image' | 'video' | 'audio' | 'document'

export interface MediaAsset {
  id: number
  filename: string
  original_name: string
  file_path: string
  file_size: number
  mime_type: string
  media_type: MediaKind
  width?: number
  height?: number
  created_at: string
}

// UPLOAD_ACCEPT 是文件选择框的过滤条件。
// 与后端 supportedFormats 对应；后端仍会做扩展名与内容双重校验，
// 这里只是提升选择体验。
export const UPLOAD_ACCEPT = [
  'image/*',
  'video/*',
  'audio/*',
  'application/pdf',
  '.mkv',
  '.avi',
  '.flac',
].join(',')

export const mediaAssetsApi = {
  list: () => api.get<MediaAsset[]>('/media-assets'),
  upload: (file: File) => {
    const data = new FormData()
    data.append('file', file)
    return api.post<MediaAsset>('/media-assets', data)
  },
  delete: (id: number) => api.delete<void>(`/media-assets/${id}`),
}
