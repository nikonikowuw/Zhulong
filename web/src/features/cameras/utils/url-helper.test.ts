import { describe, expect, it } from 'vitest'
import { extractIpFromRtspUrl, maskRtspUrl } from './url-helper'

describe('url-helper', () => {
  describe('maskRtspUrl', () => {
    it('masks password in standard RTSP url', () => {
      const original = 'rtsp://admin:passwd123@192.168.1.100:554/live/ch0'
      const masked = maskRtspUrl(original)
      expect(masked).toBe('rtsp://admin:••••••••@192.168.1.100:554/live/ch0')
      expect(masked.includes('passwd123')).toBe(false)
    })

    it('returns empty string if input is empty', () => {
      expect(maskRtspUrl('')).toBe('')
    })

    it('leaves url unchanged if no credentials present', () => {
      const url = 'rtsp://192.168.1.100:554/live'
      expect(maskRtspUrl(url)).toBe(url)
    })
  })

  describe('extractIpFromRtspUrl', () => {
    it('extracts IP from url with credentials', () => {
      const url = 'rtsp://admin:passwd123@192.168.1.100:554/live'
      expect(extractIpFromRtspUrl(url)).toBe('192.168.1.100')
    })

    it('extracts IP from url without credentials', () => {
      const url = 'rtsp://10.0.0.55:554/stream'
      expect(extractIpFromRtspUrl(url)).toBe('10.0.0.55')
    })

    it('returns fallback dash for invalid or undefined input', () => {
      expect(extractIpFromRtspUrl(undefined)).toBe('-')
      expect(extractIpFromRtspUrl('')).toBe('-')
      expect(extractIpFromRtspUrl('http://example.com')).toBe('-')
    })
  })
})
