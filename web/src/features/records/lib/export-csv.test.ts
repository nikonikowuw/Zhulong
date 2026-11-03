import { describe, expect, it } from 'vitest'
import { type Alarm, type Capture, type Recognition } from '../data/schema'
import {
  escapeCsvField,
  exportAlarmsCsv,
  exportCapturesCsv,
  exportRecognitionsCsv,
  exportRecordsToCsv,
  UTF8_BOM,
} from './export-csv'

describe('export-csv', () => {
  describe('escapeCsvField', () => {
    it('escapes empty or null values', () => {
      expect(escapeCsvField(null)).toBe('')
      expect(escapeCsvField(undefined)).toBe('')
      expect(escapeCsvField('')).toBe('')
    })

    it('returns raw string when no special characters exist', () => {
      expect(escapeCsvField('hello_world')).toBe('hello_world')
      expect(escapeCsvField(12345)).toBe('12345')
    })

    it('wraps and escapes quotes, commas, and newlines', () => {
      expect(escapeCsvField('hello,world')).toBe('"hello,world"')
      expect(escapeCsvField('quote "test"')).toBe('"quote ""test"""')
      expect(escapeCsvField('multi\nline')).toBe('"multi\nline"')
      expect(escapeCsvField('multi\r\nline')).toBe('"multi\r\nline"')
    })

    it('stringifies objects', () => {
      expect(escapeCsvField({ a: 1, b: 'two' })).toBe(
        '"{""a"":1,""b"":""two""}"'
      )
    })

    it('neutralises spreadsheet formula injection', () => {
      expect(escapeCsvField('=1+1')).toBe("'=1+1")
      expect(escapeCsvField('@SUM(A1)')).toBe("'@SUM(A1)")
      expect(escapeCsvField('+8613800000000')).toBe("'+8613800000000")
      expect(escapeCsvField('-12:30')).toBe("'-12:30")
    })

    it('never prefixes plain numbers (would turn them into text)', () => {
      expect(escapeCsvField(-0.5)).toBe('-0.5')
      expect(escapeCsvField(-12345)).toBe('-12345')
    })
  })

  describe('exportRecognitionsCsv', () => {
    const mockRecognition: Recognition = {
      id: '01JH0000000000000000000001',
      cameraId: 'cam-01',
      algorithmId: 'algo-face',
      algorithmName: 'FaceNet',
      algorithmVersion: '1.2.0',
      pts: 123456,
      trackId: 42,
      personId: 'p-01',
      personExternalId: 'EXT-1001',
      personName: '张三, 工程师',
      matchedFaceId: 'f-01',
      matched: true,
      similarity: 0.9456,
      candidates: [],
      faceImagePath: 'snapshots/face.jpg',
      galleryImagePath: 'faces/gallery.jpg',
      imagePath: 'snapshots/scene.jpg',
      box: [0.1, 0.2, 0.3, 0.4],
      bodyBox: [0.05, 0.1, 0.4, 0.8],
      attributes: {},
      capturedAt: '2026-11-03T08:30:00.000Z',
      createdAt: '2026-11-03T08:30:01.000Z',
    }

    it('starts with UTF-8 BOM', () => {
      const csv = exportRecognitionsCsv([mockRecognition])
      expect(csv.startsWith(UTF8_BOM)).toBe(true)
    })

    it('maps fields correctly with camera resolver', () => {
      const csv = exportRecognitionsCsv([mockRecognition], (id) =>
        id === 'cam-01' ? '东门摄像机' : id
      )

      expect(csv).toContain('东门摄像机')
      expect(csv).toContain('FaceNet')
      expect(csv).toContain('"张三, 工程师"')
      expect(csv).toContain('EXT-1001')
      expect(csv).toContain('0.9456')
      expect(csv).toContain('01JH0000000000000000000001')
    })

    it('writes both local time and ISO UTC (never a UTC-only timestamp)', () => {
      const capturedAt = '2026-11-03T08:30:00.000Z'
      const csv = exportRecognitionsCsv([mockRecognition])
      const local = new Date(capturedAt)
      const pad = (n: number) => String(n).padStart(2, '0')
      const expectedLocal = `${local.getFullYear()}-${pad(local.getMonth() + 1)}-${pad(local.getDate())} ${pad(local.getHours())}:${pad(local.getMinutes())}:${pad(local.getSeconds())}`

      expect(csv).toContain('Captured At, Local')
      expect(csv).toContain('UTC ISO8601')
      expect(csv).toContain(capturedAt)
      expect(csv).toContain(expectedLocal)
    })
  })

  describe('exportAlarmsCsv', () => {
    const mockAlarm: Alarm = {
      id: '01JH0000000000000000000002',
      cameraId: 'cam-02',
      algorithmId: 'algo-fire',
      algorithmName: 'SmokeFire',
      algorithmVersion: '2.0.1',
      alarmType: 'smoke_detected',
      score: 0.88,
      box: [0.2, 0.3, 0.5, 0.6],
      imagePath: 'snapshots/fire.jpg',
      cropPath: 'snapshots/fire_crop.jpg',
      extra: { level: 'high' },
      pts: 654321,
      capturedAt: '2026-11-03T09:00:00.000Z',
      createdAt: '2026-11-03T09:00:02.000Z',
    }

    it('exports alarm fields properly', () => {
      const csv = exportAlarmsCsv([mockAlarm], () => '西区大门')
      expect(csv.startsWith(UTF8_BOM)).toBe(true)
      expect(csv).toContain('smoke_detected')
      expect(csv).toContain('西区大门')
      expect(csv).toContain('0.8800')
      expect(csv).toContain('"{""level"":""high""}"')
    })
  })

  describe('exportCapturesCsv', () => {
    const mockCapture: Capture = {
      id: '01JH0000000000000000000003',
      cameraId: 'cam-03',
      algorithmId: 'algo-vehicle',
      algorithmName: 'VehiclePlate',
      algorithmVersion: '1.0.0',
      targetType: 'vehicle',
      trackId: 101,
      score: 0.9912,
      box: [0.1, 0.1, 0.9, 0.9],
      cropPath: 'snapshots/car_crop.jpg',
      imagePath: 'snapshots/car.jpg',
      attributes: { plate: '粤B12345' },
      pts: 987654,
      capturedAt: '2026-11-03T10:00:00.000Z',
      createdAt: '2026-11-03T10:00:01.000Z',
    }

    it('exports capture fields properly', () => {
      const csv = exportCapturesCsv([mockCapture])
      expect(csv.startsWith(UTF8_BOM)).toBe(true)
      expect(csv).toContain('vehicle')
      expect(csv).toContain('0.9912')
      expect(csv).toContain('粤B12345')
    })
  })

  describe('exportRecordsToCsv router', () => {
    it('dispatches to corresponding table export', () => {
      const csv = exportRecordsToCsv('vehicles', [
        {
          id: 'v-1',
          cameraId: 'c1',
          targetType: 'vehicle',
          trackId: 1,
          score: 0.9,
          box: [0, 0, 1, 1],
          cropPath: '',
          imagePath: '',
          attributes: {},
          pts: 0,
          capturedAt: '2026-11-03T10:00:00.000Z',
          createdAt: '',
        },
      ])
      expect(csv).toContain('Target Type')
    })
  })
})
