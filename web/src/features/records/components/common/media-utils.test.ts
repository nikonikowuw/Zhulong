import { describe, expect, it } from 'vitest'
import { getGalleryImageUrl, getMediaUrl } from './media-utils'

describe('getMediaUrl', () => {
  it('returns an empty string for blank input', () => {
    expect(getMediaUrl()).toBe('')
    expect(getMediaUrl('')).toBe('')
    expect(getMediaUrl('   ')).toBe('')
  })

  it('passes through absolute and data URLs unchanged', () => {
    expect(getMediaUrl('http://example.com/a.jpg')).toBe('http://example.com/a.jpg')
    expect(getMediaUrl('https://example.com/a.jpg')).toBe('https://example.com/a.jpg')
    expect(getMediaUrl('data:image/png;base64,AAA')).toBe('data:image/png;base64,AAA')
  })

  it('prefixes a relative media path exactly once', () => {
    const url = getMediaUrl('snapshots/cam-01/2026-10-20/id.jpg')
    expect(url).toBe('/api/v1/media/content/snapshots/cam-01/2026-10-20/id.jpg')
    // 已是完整 URL 时不得重复前缀
    expect(getMediaUrl(url)).toBe(url)
  })

  it('strips leading slashes so the media resolver sees a relative path', () => {
    expect(getMediaUrl('/snapshots/a.jpg')).toBe('/api/v1/media/content/snapshots/a.jpg')
    expect(getMediaUrl('///snapshots/a.jpg')).toBe('/api/v1/media/content/snapshots/a.jpg')
  })
})

describe('getGalleryImageUrl', () => {
  it('builds the person-domain face image endpoint', () => {
    expect(getGalleryImageUrl('p-888', 'f-1')).toBe(
      '/api/v1/persons/p-888/faces/f-1/image'
    )
  })

  it('percent-encodes both identifiers', () => {
    expect(getGalleryImageUrl('p/1', 'f 2')).toBe(
      '/api/v1/persons/p%2F1/faces/f%202/image'
    )
  })

  it('returns an empty string when either identifier is missing', () => {
    // 未匹配的识别记录 personId 为 null，此时必须回退到占位图而不是拼出
    // 一个必然 404 的 URL。
    expect(getGalleryImageUrl(null, 'f-1')).toBe('')
    expect(getGalleryImageUrl('p-1', null)).toBe('')
    expect(getGalleryImageUrl('', '')).toBe('')
    expect(getGalleryImageUrl(undefined, undefined)).toBe('')
  })

  it('never produces a media-content URL', () => {
    // 底库照在 facesDir 下，不属于 mediafile 的媒体根。走 media/content
    // 会被首段白名单以 422 MEDIA_KIND_INVALID 拒绝。
    expect(getGalleryImageUrl('p-1', 'f-1')).not.toContain('/media/content/')
  })
})
