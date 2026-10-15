import { describe, it, expect, vi, beforeEach } from 'vitest'
import { apiClient } from '@/lib/api-client'
import { algorithmsApi } from './algorithms-api'

vi.mock('@/lib/api-client', () => ({
  apiClient: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
  },
}))

describe('algorithms-api', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('getAlgorithms returns parsed algorithms list', async () => {
    const mockData = [
      {
        id: 'test_algo',
        name: '测试算法',
        version: '1.0.0',
        taskType: 'detection',
        category: 'safety',
        targetArch: 'x86_64',
        targetDevice: 'cpu',
        computeCost: 10,
        memoryMb: 64,
        description: '测试描述',
        status: 'ACTIVE',
        createdAt: '2026-10-15T00:00:00Z',
        updatedAt: '2026-10-15T00:00:00Z',
      },
    ]

    vi.mocked(apiClient.get).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: mockData },
    } as never)

    const result = await algorithmsApi.getAlgorithms()
    expect(result).toHaveLength(1)
    expect(result[0].id).toBe('test_algo')
    expect(apiClient.get).toHaveBeenCalledWith('/algorithms')
  })

  it('installAlgorithm posts formData with multipart header', async () => {
    const mockAlgo = {
      id: 'new_algo',
      name: '新算法',
      version: '1.0.0',
      taskType: 'person_structure',
      category: 'security',
      targetArch: 'aarch64',
      targetDevice: 'rknn',
      computeCost: 20,
      memoryMb: 128,
      status: 'DISABLED',
      createdAt: '2026-10-15T00:00:00Z',
      updatedAt: '2026-10-15T00:00:00Z',
    }

    vi.mocked(apiClient.post).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: mockAlgo },
    } as never)

    const file = new File(['mock content'], 'algo.tar.gz', {
      type: 'application/gzip',
    })
    const result = await algorithmsApi.installAlgorithm(file)

    expect(result.id).toBe('new_algo')
    expect(apiClient.post).toHaveBeenCalledWith(
      '/algorithms/install',
      expect.any(FormData),
      expect.objectContaining({
        headers: { 'Content-Type': 'multipart/form-data' },
      })
    )
  })

  it('deleteAlgorithm calls DELETE endpoint', async () => {
    vi.mocked(apiClient.delete).mockResolvedValue({
      data: { code: 'OK', message: 'deleted' },
    } as never)

    await algorithmsApi.deleteAlgorithm('algo_to_delete')
    expect(apiClient.delete).toHaveBeenCalledWith('/algorithms/algo_to_delete')
  })

  it('enableAlgorithm and disableAlgorithm post to subroutes', async () => {
    const mockAlgo = {
      id: 'algo_1',
      name: '算法',
      version: '1.0',
      taskType: 'classification',
      category: 'general',
      targetArch: 'x86_64',
      targetDevice: 'cpu',
      status: 'ACTIVE',
      createdAt: '2026-10-15T00:00:00Z',
      updatedAt: '2026-10-15T00:00:00Z',
    }

    vi.mocked(apiClient.post).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: mockAlgo },
    } as never)

    await algorithmsApi.enableAlgorithm('algo_1')
    expect(apiClient.post).toHaveBeenCalledWith('/algorithms/algo_1/enable')

    await algorithmsApi.disableAlgorithm('algo_1')
    expect(apiClient.post).toHaveBeenCalledWith('/algorithms/algo_1/disable')
  })
})
