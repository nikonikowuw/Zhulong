import { describe, expect, it } from 'vitest'
import { cameraFormSchema, cameraSchema } from './schema'

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

  describe('cameraFormSchema', () => {
    it('accepts valid camera form input with main stream only', () => {
      const input = {
        name: 'Gate Camera',
        enabled: true,
        mainRtspUrl: 'rtsp://admin:pass@192.168.1.100:554/live/ch0',
        mainTransport: 'tcp' as const,
        hasSubStream: false,
        subTransport: 'tcp' as const,
      }
      const result = cameraFormSchema.safeParse(input)
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
      const result = cameraFormSchema.safeParse(input)
      expect(result.success).toBe(false)
      if (!result.success) {
        expect(result.error.issues[0].message).toBe(
          'cameras.validation.rtspInvalid'
        )
      }
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
      const result = cameraFormSchema.safeParse(input)
      expect(result.success).toBe(false)
      if (!result.success) {
        const hasIssue = result.error.issues.some(
          (issue) => issue.message === 'cameras.validation.subRtspRequired'
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
      const result = cameraFormSchema.safeParse(input)
      expect(result.success).toBe(true)
    })
  })
})
