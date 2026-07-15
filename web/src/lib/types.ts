export interface PageResult<T> {
  list: T[]
  total: number
  page: number
  per_page: number
}

export interface EntityBase {
  id: number
  created_at: string
  updated_at: string
}

export function formatDateTime(value?: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN')
}

export function formatFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  const value = bytes / (1024 ** index)
  return `${value.toFixed(index === 0 ? 0 : value >= 10 ? 1 : 2)} ${units[index]}`
}
