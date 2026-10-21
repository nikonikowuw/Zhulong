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

/**
 * 底库登记照的只读访问 URL。
 *
 * ⚠️ 底库照片存放在 `facesDir`（默认 `<data-dir>/faces`），与 `mediafile` 服务
 * 托管的媒体根（`<media-dir>/{recordings,snapshots,exports}`）是**两个不同的根**。
 * 因此它不能用 `/api/v1/media/content/*` 访问 —— 那条路径的首段白名单会直接
 * 以 422 MEDIA_KIND_INVALID 拒绝，且 `galleryImagePath` 里存的只是裸文件名。
 * 唯一正确的入口是 person 域的按人脸取图端点。
 *
 * @returns 可访问的 URL；缺少 personId 或 faceId 时返回空串（调用方应显示占位）
 */
export function getGalleryImageUrl(
  personId?: string | null,
  faceId?: string | null
): string {
  if (!personId || !faceId) {
    return ''
  }
  return `/api/v1/persons/${encodeURIComponent(personId)}/faces/${encodeURIComponent(faceId)}/image`
}
