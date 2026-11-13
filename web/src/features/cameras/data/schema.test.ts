import { type TFunction } from 'i18next'
import { describe, expect, it } from 'vitest'
import { createCameraFormSchema, cameraSchema } from './schema'

describe('camera schema validation', () => {
  describe('cameraSchema', () => {
    it('parses valid camera entity with defaults', () => {
      const raw = {
        id: 'cam-01',
        name: 'Front Gate Camera',
        enabled: true,
        revision: 1,
        health: 'online',
        session: 'running',
        streams: [
          {
            id: 1,
            role: 'main',
            protocol: 'rtsp',
            rtspUrl: 'rtsp://admin:pass@192.168.1.100:554/live',
            transport: 'tcp',
            codec: 'h264',
            width: 1920,
            height: 1080,
            fps: 25,
            fpsString: '25 fps',
          },
        ],
        createdAt: '2026-10-09T00:00:00Z',
        updatedAt: '2026-10-09T00:00:00Z',
      }
      const parsed = cameraSchema.parse(raw)
      expect(parsed.id).toBe('cam-01')
      expect(parsed.streams[0].width).toBe(1920)
      expect(parsed.degraded).toBe(false)
      expect(parsed.stale).toBe(false)
    })
  })

  describe('createCameraFormSchema', () => {
    const schema = createCameraFormSchema()
    it('accepts valid camera form input with main stream only', () => {
      const input = {
        name: 'Gate Camera',
        enabled: true,
        mainRtspUrl: 'rtsp://admin:pass@192.168.1.100:554/live/ch0',
        mainTransport: 'tcp' as const,
        hasSubStream: false,
        subTransport: 'tcp' as const,
      }
      const result = schema.safeParse(input)
      expect(result.success).toBe(true)
    })

    it('rejects camera form input if main stream does not start with rtsp://', () => {
      const input = {
        name: 'Gate Camera',
        enabled: true,
        mainRtspUrl: 'http://admin:pass@192.168.1.100:554/live/ch0',
        mainTransport: 'tcp' as const,
        hasSubStream: false,
        subTransport: 'tcp' as const,
      }
      const result = schema.safeParse(input)
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0].message).toBe(
          '流地址必须以 rtsp:// 开头且格式合法'
        )
      }
    })

    it('accepts metadata-only edits without requiring RTSP fields', () => {
      const metadataSchema = createCameraFormSchema(undefined, false)
      const result = metadataSchema.safeParse({
        name: 'GB camera',
        enabled: true,
        mainRtspUrl: '',
        mainTransport: 'tcp',
        hasSubStream: false,
        subRtspUrl: '',
        subTransport: 'tcp',
      })

      expect(result.success).toBe(true)
    })

    it('rejects camera form if sub stream is enabled but subRtspUrl is empty', () => {
      const input = {
        name: 'Gate Camera',
        enabled: true,
        mainRtspUrl: 'rtsp://admin:pass@192.168.1.100:554/live/ch0',
        mainTransport: 'tcp' as const,
        hasSubStream: true,
        subRtspUrl: '',
        subTransport: 'tcp' as const,
      }
      const result = schema.safeParse(input)
      expect(result.success).toBe(false)
      if (!result.success) {
        const hasIssue = result.error.issues.some(
          (issue: { message: string }) =>
            issue.message === '启用子流时必须填写子流 RTSP 地址'
        )
        expect(hasIssue).toBe(true)
      }
    })

    it('accepts valid camera form when both main and sub streams are configured', () => {
      const input = {
        name: 'Gate Camera',
        enabled: true,
        mainRtspUrl: 'rtsp://admin:pass@192.168.1.100:554/live/ch0',
        mainTransport: 'tcp' as const,
        hasSubStream: true,
        subRtspUrl: 'rtsp://admin:pass@192.168.1.100:554/live/ch1',
        subTransport: 'udp' as const,
      }
      const result = schema.safeParse(input)
      expect(result.success).toBe(true)
    })

    it('rejects bare rtsp:// without a host and trims surrounding whitespace', () => {
      const bare = schema.safeParse({
        name: 'Gate Camera',
        enabled: true,
        mainRtspUrl: 'rtsp://',
        mainTransport: 'tcp',
        hasSubStream: false,
        subTransport: 'tcp',
      })
      expect(bare.success).toBe(false)

      const barePath = schema.safeParse({
        name: 'Gate Camera',
        enabled: true,
        mainRtspUrl: 'rtsp:///live',
        mainTransport: 'tcp',
        hasSubStream: false,
        subTransport: 'tcp',
      })
      expect(barePath.success).toBe(false)

      const trimmed = schema.safeParse({
        name: '  Gate Camera  ',
        enabled: true,
        mainRtspUrl: '  rtsp://admin:pass@[fe80::1]:554/live  ',
        mainTransport: 'tcp',
        hasSubStream: true,
        subRtspUrl: '  rtsp://192.168.1.100/sub  ',
        subTransport: 'tcp',
      })
      expect(trimmed.success).toBe(true)
      if (trimmed.success) {
        expect(trimmed.data.name).toBe('Gate Camera')
        expect(trimmed.data.mainRtspUrl).toBe(
          'rtsp://admin:pass@[fe80::1]:554/live'
        )
        expect(trimmed.data.subRtspUrl).toBe('rtsp://192.168.1.100/sub')
      }
    })

    it('resolves validation text through i18n instead of leaking a raw key', () => {
      const calls: string[] = []
      const mockT = ((key: string) => {
        calls.push(key)
        return `translated:${key}`
      }) as unknown as TFunction

      const localized = createCameraFormSchema(mockT)
      const result = localized.safeParse({
        name: '',
        enabled: true,
        mainRtspUrl: '',
        mainTransport: 'tcp',
        hasSubStream: false,
        subTransport: 'tcp',
      })

      expect(result.success).toBe(false)
      if (!result.success) {
        for (const issue of result.error.issues) {
          // 绝不能把裸 key 直接交给用户（此前 `FormMessage` 渲染的就是 key 本身）
          expect(issue.message).toMatch(/^translated:/)
          expect(issue.message).not.toMatch(/^cameras\./)
        }
      }
      expect(calls).toContain('validation.nameRequired')
      expect(calls).toContain('validation.mainRtspRequired')
    })
  })
})
