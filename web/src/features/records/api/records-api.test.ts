import type { AxiosResponse } from 'axios'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { recordsApi } from './records-api'

function mockResponse<T>(data: T): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

describe('recordsApi', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  describe('getRecognitions & getRecognition', () => {
    it('requests /records/recognitions with query params and parses result', async () => {
      const mockList = {
        items: [
          {
            id: 'rec-1',
            cameraId: 'cam-1',
            algorithmId: 'algo-1',
            pts: 100,
            trackId: 1,
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
            faceImagePath: 'faces/face-1.jpg',
            galleryImagePath: 'gallery/p-1.jpg',
            imagePath: 'snapshots/rec-1.jpg',
            box: [0.1, 0.2, 0.3, 0.4],
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

      const spy = vi
        .spyOn(apiClient, 'get')
        .mockResolvedValueOnce(mockResponse(mockList))

      const res = await recordsApi.getRecognitions({
        page: 1,
        pageSize: 20,
        cameraId: 'cam-1',
        matched: true,
      })

      expect(spy).toHaveBeenCalledWith('/records/recognitions', {
        params: {
          page: 1,
          pageSize: 20,
          cameraId: 'cam-1',
          personId: undefined,
          matched: 'true',
          startTime: undefined,
          endTime: undefined,
        },
      })
      expect(res.total).toBe(1)
      expect(res.items[0].id).toBe('rec-1')
      expect(res.items[0].candidates[0].personName).toBe('Alice')
    })

    it('requests /records/recognitions/:id and parses single record', async () => {
      const mockItem = {
        id: 'rec-2',
        cameraId: 'cam-1',
        algorithmId: 'algo-1',
        pts: 100,
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
      }

      const spy = vi
        .spyOn(apiClient, 'get')
        .mockResolvedValueOnce(mockResponse(mockItem))

      const res = await recordsApi.getRecognition('rec-2')
      expect(spy).toHaveBeenCalledWith('/records/recognitions/rec-2')
      expect(res.id).toBe('rec-2')
      expect(res.matched).toBe(false)
    })
  })

  describe('getAlarms & getAlarm', () => {
    it('requests /records/alarms with filters', async () => {
      const mockList = {
        items: [
          {
            id: 'alarm-1',
            cameraId: 'cam-1',
            algorithmId: 'algo-perimeter',
            alarmType: 'tripwire',
            score: 0.9,
            box: [0.1, 0.2, 0.3, 0.4],
            imagePath: 'snapshots/alarm-1.jpg',
            cropPath: 'snapshots/alarm-1-crop.jpg',
            extra: {},
            pts: 200,
            capturedAt: '2026-03-31T10:00:00Z',
            createdAt: '2026-03-31T10:00:01Z',
          },
        ],
        total: 1,
        page: 1,
        pageSize: 20,
      }

      const spy = vi
        .spyOn(apiClient, 'get')
        .mockResolvedValueOnce(mockResponse(mockList))

      const res = await recordsApi.getAlarms({
        page: 1,
        pageSize: 20,
        alarmType: 'tripwire',
      })

      expect(spy).toHaveBeenCalledWith('/records/alarms', {
        params: {
          page: 1,
          pageSize: 20,
          cameraId: undefined,
          alarmType: 'tripwire',
          startTime: undefined,
          endTime: undefined,
        },
      })
      expect(res.items[0].alarmType).toBe('tripwire')
    })

    it('requests /records/alarms/:id', async () => {
      const mockItem = {
        id: 'alarm-1',
        cameraId: 'cam-1',
        algorithmId: 'algo-perimeter',
        alarmType: 'invasion',
        score: 0.9,
        box: [0, 0, 0, 0],
        imagePath: '',
        cropPath: '',
        extra: {},
        pts: 200,
        capturedAt: '2026-03-31T10:00:00Z',
        createdAt: '2026-03-31T10:00:01Z',
      }

      const spy = vi
        .spyOn(apiClient, 'get')
        .mockResolvedValueOnce(mockResponse(mockItem))

      const res = await recordsApi.getAlarm('alarm-1')
      expect(spy).toHaveBeenCalledWith('/records/alarms/alarm-1')
      expect(res.alarmType).toBe('invasion')
    })
  })

  describe('getCaptures & getCapture', () => {
    it('requests /records/captures with targetType filter', async () => {
      const mockList = {
        items: [
          {
            id: 'cap-1',
            cameraId: 'cam-1',
            targetType: 'face',
            trackId: 1,
            score: 0.88,
            box: [0.1, 0.2, 0.3, 0.4],
            cropPath: 'snapshots/face-crop.jpg',
            imagePath: 'snapshots/full.jpg',
            attributes: {},
            pts: 300,
            capturedAt: '2026-03-31T10:00:00Z',
            createdAt: '2026-03-31T10:00:01Z',
          },
        ],
        total: 1,
        page: 1,
        pageSize: 20,
      }

      const spy = vi
        .spyOn(apiClient, 'get')
        .mockResolvedValueOnce(mockResponse(mockList))

      const res = await recordsApi.getCaptures({
        page: 1,
        pageSize: 20,
        targetType: 'face',
      })

      expect(spy).toHaveBeenCalledWith('/records/captures', {
        params: {
          page: 1,
          pageSize: 20,
          cameraId: undefined,
          targetType: 'face',
          startTime: undefined,
          endTime: undefined,
        },
      })
      expect(res.items[0].targetType).toBe('face')
    })

    it('requests /records/captures/:id', async () => {
      const mockItem = {
        id: 'cap-1',
        cameraId: 'cam-1',
        targetType: 'body',
        trackId: 10,
        score: 0.92,
        box: [0, 0, 0, 0],
        cropPath: '',
        imagePath: '',
        attributes: {},
        pts: 300,
        capturedAt: '2026-03-31T10:00:00Z',
        createdAt: '2026-03-31T10:00:01Z',
      }

      const spy = vi
        .spyOn(apiClient, 'get')
        .mockResolvedValueOnce(mockResponse(mockItem))

      const res = await recordsApi.getCapture('cap-1')
      expect(spy).toHaveBeenCalledWith('/records/captures/cap-1')
      expect(res.targetType).toBe('body')
    })

    it('requests /records/alarms/alarm-types', async () => {
      const mockTypes = ['intrusion', 'tripwire']
      const spy = vi
        .spyOn(apiClient, 'get')
        .mockResolvedValueOnce(mockResponse(mockTypes))

      const res = await recordsApi.getAlarmTypes()
      expect(spy).toHaveBeenCalledWith('/records/alarms/alarm-types')
      expect(res).toEqual(['intrusion', 'tripwire'])
    })

    it('requests /records/captures/target-types', async () => {
      const mockTypes = ['person', 'vehicle']
      const spy = vi
        .spyOn(apiClient, 'get')
        .mockResolvedValueOnce(mockResponse(mockTypes))

      const res = await recordsApi.getCaptureTargetTypes()
      expect(spy).toHaveBeenCalledWith('/records/captures/target-types')
      expect(res).toEqual(['person', 'vehicle'])
    })
  })
})
