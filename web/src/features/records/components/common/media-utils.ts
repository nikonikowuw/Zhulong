export function getMediaUrl(path?: string): string {
  if (!path || path.trim() === '') {
    return ''
  }
  const trimmed = path.trim()
  if (
    trimmed.startsWith('http://') ||
    trimmed.startsWith('https://') ||
    trimmed.startsWith('data:')
  ) {
    return trimmed
  }
  if (trimmed.startsWith('/api/v1/media/content/')) {
    return trimmed
  }
  const cleanPath = trimmed.replace(/^\/+/, '')
  return `/api/v1/media/content/${encodeURI(cleanPath)}`
}
