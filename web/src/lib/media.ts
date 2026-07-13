export function mediaURL(path: string): string {
  const normalized = path.replaceAll('\\', '/').replace(/^\/+/, '')
  return normalized.startsWith('uploads/') ? `/static/${normalized}` : `/${normalized}`
}
