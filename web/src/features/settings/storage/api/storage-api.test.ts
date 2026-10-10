import type { AxiosResponse } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import {
  type CleanupSummary,
  type PathTestResponse,
  type StorageConfig,
  type StorageStatus,
} from '../types'
import { storageApi } from './storage-api'

function mockResponse<T>(data: T): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

describe('storageApi', () => {
  const mockStatus: StorageStatus = {
    mediaDirectory: '/mnt/storage/media',
    mountPoint: '/mnt/storage',
    fsType: 'ext4',
    totalBytes: 100 * 1024 * 1024 * 1024,
    usedBytes: 70 * 1024 * 1024 * 1024,
    freeBytes: 30 * 1024 * 1024 * 1024,
    usagePercent: 70,
    breakdown: {
      recordingsBytes: 50 * 1024 * 1024 * 1024,
      snapshotsBytes: 10 * 1024 * 1024 * 1024,
      exportsBytes: 5 * 1024 * 1024 * 1024,
      otherBytes: 5 * 1024 * 1024 * 1024,
    },
    status: 'healthy',
    deviceId: 2049,
    isExternal: true,
    canWrite: true,
    updatedAt: '2026-10-07T12:00:00Z',
  }

  const mockConfig: StorageConfig = {
    mediaDirectory: '/mnt/storage/media',
    recordingsRetentionDays: 15,
    snapshotsRetentionDays: 90,
    exportsRetentionHours: 48,
    highWatermarkPercent: 90,
    lowWatermarkPercent: 80,
    emergencyStopPercent: 95,
    emergencyStopMinMb: 2048,
  }

  it('getStatus requests GET /system/storage/status', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockStatus))

    const res = await storageApi.getStatus()
    expect(res).toEqual(mockStatus)
    expect(apiClient.get).toHaveBeenCalledWith('/system/storage/status')
  })

  it('getConfig requests GET /system/storage/config', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(mockConfig))

    const res = await storageApi.getConfig()
    expect(res).toEqual(mockConfig)
    expect(apiClient.get).toHaveBeenCalledWith('/system/storage/config')
  })

  it('updateConfig sends PUT /system/storage/config', async () => {
    vi.spyOn(apiClient, 'put').mockResolvedValueOnce(mockResponse(mockStatus))

    const payload = {
      mediaDirectory: '/mnt/storage/media',
      recordingsRetentionDays: 30,
      snapshotsRetentionDays: 180,
      exportsRetentionHours: 72,
      highWatermarkPercent: 88,
      lowWatermarkPercent: 75,
      emergencyStopPercent: 96,
      emergencyStopMinMb: 4096,
    }
    const res = await storageApi.updateConfig(payload)
    expect(res).toEqual(mockStatus)
    expect(apiClient.put).toHaveBeenCalledWith(
      '/system/storage/config',
      payload
    )
  })

  it('testPath sends POST /system/storage/test', async () => {
    const mockTestRes: PathTestResponse = {
      path: '/mnt/storage/media',
      exists: true,
      writable: true,
      isMount: true,
      mountPoint: '/mnt/storage',
      fsType: 'ext4',
      totalBytes: 100 * 1024 * 1024 * 1024,
      freeBytes: 30 * 1024 * 1024 * 1024,
      deviceId: 2049,
      isExternal: true,
    }
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(mockResponse(mockTestRes))

    const payload = { path: '/mnt/storage/media' }
    const res = await storageApi.testPath(payload)
    expect(res).toEqual(mockTestRes)
    expect(apiClient.post).toHaveBeenCalledWith('/system/storage/test', payload)
  })

  it('triggerCleanup sends POST /system/storage/cleanup', async () => {
    const mockSummary: CleanupSummary = {
      triggerReason: 'manual',
      deletedFiles: 12,
      freedBytes: 1024 * 1024 * 1024,
      durationMs: 340,
      targetStatus: 'completed',
      finishedAt: '2026-10-07T12:05:00Z',
    }
    vi.spyOn(apiClient, 'post').mockResolvedValueOnce(mockResponse(mockSummary))

    const res = await storageApi.triggerCleanup()
    expect(res).toEqual(mockSummary)
    expect(apiClient.post).toHaveBeenCalledWith('/system/storage/cleanup')
  })
})
