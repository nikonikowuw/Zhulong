import { describe, expect, it } from 'vitest'
import {
  burnOverlayOnImage,
  isValidBox,
  toAbsoluteCoords,
} from './canvas-overlay-drawer'

// 1x1 透明 PNG 标准 Base64
const TINY_PNG_DATA_URL =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='

describe('canvas-overlay-drawer', () => {
  describe('isValidBox', () => {
    it('returns false for undefined or malformed box', () => {
      expect(isValidBox(undefined)).toBe(false)
      expect(isValidBox([0, 0, 0, 0])).toBe(false)
      expect(isValidBox([10, 20, 5, 20])).toBe(false)
    })

    it('returns true for positive area boxes', () => {
      expect(isValidBox([0.1, 0.2, 0.8, 0.9])).toBe(true)
      expect(isValidBox([10, 10, 200, 300])).toBe(true)
    })
  })

  describe('toAbsoluteCoords', () => {
    it('converts normalized coords to absolute physical pixels', () => {
      const rect = toAbsoluteCoords([0.1, 0.2, 0.5, 0.6], 1000, 500)
      expect(rect).toEqual({
        x: 100,
        y: 100,
        width: 400,
        height: 200,
      })
    })

    it('preserves absolute pixels and clamps to canvas boundary', () => {
      const rect = toAbsoluteCoords([-10, 50, 1200, 800], 1000, 500)
      expect(rect.x).toBe(0)
      expect(rect.y).toBe(50)
      expect(rect.width).toBe(1000)
      expect(rect.height).toBe(450)
    })
  })

  describe('burnOverlayOnImage', () => {
    it('burns primary and secondary boxes onto image and returns Blob', async () => {
      const blob = await burnOverlayOnImage(TINY_PNG_DATA_URL, {
        primaryBox: [0.1, 0.1, 0.8, 0.8],
        primaryLabel: '张三 (98.5%)',
        palette: 'emerald',
        secondaryBox: [0.05, 0.05, 0.95, 0.95],
        secondaryLabel: '人体外框',
        secondaryPalette: 'violet',
        mimeType: 'image/jpeg',
      })

      expect(blob).toBeInstanceOf(Blob)
      expect(blob.size).toBeGreaterThan(0)
      expect(blob.type).toBe('image/jpeg')
    })

    it('handles top-edge collision without error (y1=0)', async () => {
      const blob = await burnOverlayOnImage(TINY_PNG_DATA_URL, {
        primaryBox: [0.1, 0, 0.9, 0.5],
        primaryLabel: '顶沿告警',
        palette: 'amber',
      })

      expect(blob).toBeInstanceOf(Blob)
      expect(blob.size).toBeGreaterThan(0)
    })

    it('aborts cleanly when signal is already cancelled', async () => {
      const controller = new AbortController()
      controller.abort()

      await expect(
        burnOverlayOnImage(TINY_PNG_DATA_URL, {
          signal: controller.signal,
        })
      ).rejects.toThrow()
    })
  })
})
