import { describe, expect, it } from 'vitest'
import {
  alarmListResponseSchema,
  alarmSchema,
  captureListResponseSchema,
  captureSchema,
  faceCandidateSchema,
  recognitionListResponseSchema,
  recognitionSchema,
  recordsSearchSchema,
} from './schema'

describe('Records Schemas', () => {
  describe('faceCandidateSchema', () => {
    it('parses snake_case fields correctly', () => {
      const raw = {
        person_id: 'p-1',
        person_name: 'Alice',
        face_id: 'f-1',
        similarity: 0.92,
      }
      const parsed = faceCandidateSchema.parse(raw)
      expect(parsed).toEqual({
        personId: 'p-1',
        personName: 'Alice',
        faceId: 'f-1',
        similarity: 0.92,
      })
    })

    it('parses camelCase fields correctly', () => {
      const raw = {
        personId: 'p-2',
        personName: 'Bob',
        faceId: 'f-2',
        similarity: 0.88,
      }
      const parsed = faceCandidateSchema.parse(raw)
      expect(parsed).toEqual({
        personId: 'p-2',
        personName: 'Bob',
        faceId: 'f-2',
        similarity: 0.88,
      })
    })
  })

  describe('recognitionSchema & list', () => {
    it('parses full recognition item', () => {
      const raw = {
        id: 'rec-1',
        cameraId: 'cam-1',
        algorithmId: 'algo-face',
        pts: 1234567,
        trackId: 101,
        personId: 'p-1',
        personName: 'Alice',
        matchedFaceId: 'f-1',
        matched: true,
        similarity: 0.95,
        candidates: [
          {
            person_id: 'p-1',
            person_name: 'Alice',
            face_id: 'f-1',
            similarity: 0.95,
          },
        ],
        faceImagePath: 'snapshots/rec-1-face.jpg',
        galleryImagePath: 'faces/p-1.jpg',
        imagePath: 'snapshots/rec-1-full.jpg',
        box: [0.1, 0.2, 0.3, 0.4],
        bodyBox: [0.05, 0.1, 0.5, 0.8],
        attributes: { age: 28, gender: 'female' },
        capturedAt: '2026-03-31T10:00:00Z',
        createdAt: '2026-03-31T10:00:01Z',
      }
      const parsed = recognitionSchema.parse(raw)
      expect(parsed.id).toBe('rec-1')
      expect(parsed.candidates[0].personName).toBe('Alice')
      expect(parsed.box).toEqual([0.1, 0.2, 0.3, 0.4])
    })

    it('parses recognition list response', () => {
      const listRaw = {
        items: [
          {
            id: 'rec-1',
            cameraId: 'cam-1',
            algorithmId: 'algo-1',
            pts: 123,
            trackId: 1,
            personId: null,
            personName: '',
            matchedFaceId: '',
            matched: false,
            similarity: 0,
            candidates: [],
            faceImagePath: '',
            galleryImagePath: '',
            imagePath: '',
            box: [0, 0, 0, 0],
            bodyBox: [0, 0, 0, 0],
            attributes: {},
            capturedAt: '2026-03-31T10:00:00Z',
            createdAt: '2026-03-31T10:00:01Z',
          },
        ],
        total: 1,
        page: 1,
        pageSize: 20,
      }
      const parsed = recognitionListResponseSchema.parse(listRaw)
      expect(parsed.total).toBe(1)
      expect(parsed.items).toHaveLength(1)
    })
  })

  describe('alarmSchema & list', () => {
    it('parses alarm item', () => {
      const raw = {
        id: 'alarm-1',
        cameraId: 'cam-1',
        algorithmId: 'algo-perimeter',
        alarmType: 'tripwire',
        score: 0.91,
        box: [0.2, 0.3, 0.6, 0.7],
        imagePath: 'snapshots/alarm-1.jpg',
        cropPath: 'snapshots/alarm-1-crop.jpg',
        extra: { ruleName: 'fence-1' },
        pts: 234567,
        capturedAt: '2026-03-31T10:00:00Z',
        createdAt: '2026-03-31T10:00:01Z',
      }
      const parsed = alarmSchema.parse(raw)
      expect(parsed.id).toBe('alarm-1')
      expect(parsed.alarmType).toBe('tripwire')
    })

    it('parses alarm list response', () => {
      const listRaw = {
        items: [],
        total: 0,
        page: 1,
        pageSize: 20,
      }
      const parsed = alarmListResponseSchema.parse(listRaw)
      expect(parsed.items).toEqual([])
    })
  })

  describe('captureSchema & list', () => {
    it('parses capture item', () => {
      const raw = {
        id: 'cap-1',
        cameraId: 'cam-2',
        targetType: 'motor',
        trackId: 42,
        score: 0.85,
        box: [0.1, 0.1, 0.5, 0.5],
        cropPath: 'snapshots/cap-1-crop.jpg',
        imagePath: 'snapshots/cap-1.jpg',
        attributes: { color: 'blue' },
        pts: 345678,
        capturedAt: '2026-03-31T10:00:00Z',
        createdAt: '2026-03-31T10:00:01Z',
      }
      const parsed = captureSchema.parse(raw)
      expect(parsed.targetType).toBe('motor')
      expect(parsed.trackId).toBe(42)
    })

    it('parses capture list response', () => {
      const listRaw = {
        items: [],
        total: 0,
        page: 1,
        pageSize: 20,
      }
      const parsed = captureListResponseSchema.parse(listRaw)
      expect(parsed.items).toEqual([])
    })
  })

  describe('recordsSearchSchema', () => {
    it('provides fallbacks for empty / invalid search params', () => {
      const parsed = recordsSearchSchema.parse({})
      expect(parsed.tab).toBe('recognitions')
      expect(parsed.view).toBe('table')
      expect(parsed.page).toBe(1)
      expect(parsed.pageSize).toBe(20)
    })

    it('handles invalid search params gracefully via catch', () => {
      const parsed = recordsSearchSchema.parse({
        tab: 'unknown',
        view: 'invalid',
        page: -5,
        pageSize: 9999,
      })
      expect(parsed.tab).toBe('recognitions')
      expect(parsed.view).toBe('table')
      expect(parsed.page).toBe(1)
      expect(parsed.pageSize).toBe(20)
    })

    it('parses valid search params', () => {
      const parsed = recordsSearchSchema.parse({
        tab: 'alarms',
        view: 'table',
        page: 2,
        pageSize: 50,
        cameraId: 'cam-9',
        preset: '24h',
        matched: 'true',
        alarmType: 'invasion',
      })
      expect(parsed.tab).toBe('alarms')
      expect(parsed.view).toBe('table')
      expect(parsed.page).toBe(2)
      expect(parsed.pageSize).toBe(50)
      expect(parsed.cameraId).toBe('cam-9')
      expect(parsed.preset).toBe('24h')
      expect(parsed.matched).toBe('true')
      expect(parsed.alarmType).toBe('invasion')
    })
  })
})
