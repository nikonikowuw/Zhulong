import type { AxiosResponse } from 'axios'
import { describe, expect, it, vi } from 'vitest'
import { apiClient, type ApiResponse } from '@/lib/api-client'
import { auditApi } from './audit-api'

function mockResponse<T>(data: T): AxiosResponse<ApiResponse<T>> {
  return {
    data: { code: 'OK', message: 'success', data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config: {} as AxiosResponse['config'],
  }
}

describe('auditApi', () => {
  it('getAuditLogs serializes multi-action filters as repeated query parameters', async () => {
    const mockData = {
      items: [
        {
          id: 1,
          createdAt: '2026-10-10T12:00:00Z',
          ip: '192.168.1.100',
          username: 'admin',
          action: 'camera.create',
          target: 'camera:1',
          detail: '{"name":"Gate"}',
          status: 'success',
          errorMsg: '',
        },
      ],
      total: 1,
      page: 1,
      pageSize: 20,
    }

    const spy = vi
      .spyOn(apiClient, 'get')
      .mockResolvedValueOnce(mockResponse(mockData))

    const res = await auditApi.getAuditLogs({
      page: 2,
      pageSize: 20,
      actions: ['auth.login', 'camera.create'],
      status: 'success',
    })

    expect(res).toEqual(mockData)
    expect(spy).toHaveBeenCalledWith('/audit/logs', {
      params: {
        page: 2,
        pageSize: 20,
        action: undefined,
        actions: ['auth.login', 'camera.create'],
        status: 'success',
        startTime: undefined,
        endTime: undefined,
      },
      paramsSerializer: { indexes: null },
    })
  })

  it('preserves the legacy single-action query parameter', async () => {
    const spy = vi
      .spyOn(apiClient, 'get')
      .mockResolvedValueOnce(
        mockResponse({ items: [], total: 0, page: 1, pageSize: 20 })
      )

    await auditApi.getAuditLogs({ action: 'camera.create' })

    expect(spy).toHaveBeenCalledWith('/audit/logs', {
      params: {
        page: 1,
        pageSize: 20,
        action: 'camera.create',
        actions: undefined,
        status: undefined,
        startTime: undefined,
        endTime: undefined,
      },
      paramsSerializer: { indexes: null },
    })
  })

  it('rejects malformed list responses instead of substituting an empty result', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(mockResponse(null))

    await expect(auditApi.getAuditLogs()).rejects.toThrow()
  })

  it('rejects audit records with invalid RFC3339 timestamps', async () => {
    vi.spyOn(apiClient, 'get').mockResolvedValueOnce(
      mockResponse({
        items: [
          {
            id: 1,
            createdAt: '2026-02-30T12:00:00Z',
            ip: '',
            username: 'admin',
            action: 'auth.login',
            target: '',
            detail: '',
            status: 'success',
            errorMsg: '',
          },
        ],
        total: 1,
        page: 1,
        pageSize: 20,
      })
    )

    await expect(auditApi.getAuditLogs()).rejects.toThrow()
  })

  it('rejects malformed clear responses', async () => {
    vi.spyOn(apiClient, 'delete').mockResolvedValueOnce(
      mockResponse({ cleared: 'many' })
    )

    await expect(auditApi.clearAuditLogs()).rejects.toThrow()
  })

  it('clearAuditLogs sends DELETE to /audit/logs with optional before parameter', async () => {
    const mockCleared = { cleared: 42 }
    const spy = vi
      .spyOn(apiClient, 'delete')
      .mockResolvedValueOnce(mockResponse(mockCleared))

    const res = await auditApi.clearAuditLogs({
      before: '2026-10-01T00:00:00Z',
    })

    expect(res).toEqual(mockCleared)
    expect(spy).toHaveBeenCalledWith('/audit/logs', {
      params: {
        before: '2026-10-01T00:00:00Z',
      },
    })
  })
})
