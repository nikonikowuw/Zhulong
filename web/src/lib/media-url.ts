/**
 * 媒体文件 URL 构造（跨特性共享）。
 *
 * 记录域把图片存成相对路径（`snapshots/...`），HTTP 访问统一走
 * `/api/v1/media/content/*`；底库登记照存在独立的 `faces` 根，只能经人员域的
 * 按人脸取图端点访问。两类路径的拼装规则是全站共用的基础设施，因此放在
 * `@/lib` 而不是某个特性切片里 —— 否则每个消费方都要穿透导入别人的私有目录。
 */

const MEDIA_CONTENT_PREFIX = '/api/v1/media/content/'

/** 判断是否已经是可直接使用的 URL（绝对地址或 data URI）。 */
function isAbsoluteUrl(path: string): boolean {
  return (
    path.startsWith('http://') ||
    path.startsWith('https://') ||
    path.startsWith('data:')
  )
}

/**
 * 把记录域的媒体相对路径转成可访问 URL。
 *
 * @returns 可访问 URL；空路径返回空串（调用方应显示占位图）。
 */
export function getMediaUrl(path?: string): string {
  if (!path || path.trim() === '') {
    return ''
  }
  const trimmed = path.trim()
  if (isAbsoluteUrl(trimmed)) {
    return trimmed
  }
  if (trimmed.startsWith(MEDIA_CONTENT_PREFIX)) {
    return trimmed
  }
  const cleanPath = trimmed.replace(/^\/+/, '')
  return `${MEDIA_CONTENT_PREFIX}${encodeURI(cleanPath)}`
}

/**
 * 底库登记照的只读访问 URL。
 *
 * ⚠️ 底库照片存放在 `facesDir`（默认 `<data-dir>/faces`），与 `mediafile` 服务
 * 托管的媒体根（`<media-dir>/{recordings,snapshots,exports}`）是**两个不同的根**，
 * 因此它不能用 `/api/v1/media/content/*` 访问（首段白名单会以 422
 * MEDIA_KIND_INVALID 拒绝），且 `galleryImagePath` 里存的只是裸文件名。
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
