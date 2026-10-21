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

  it('getDeletionImpact calls the read-only impact endpoint', async () => {
    const spy = vi
      .spyOn(apiClient, 'get')
      .mockResolvedValue({
        data: {
          code: 'OK',
          message: 'ok',
          data: {
            algorithmId: 'a1',
            algorithmName: '安全帽检测',
            version: '2.1.0',
            status: 'DISABLED',
            boundTaskCount: 2,
            affectedCameraIds: ['cam-a', 'cam-b'],
            recognitionCount: 10,
            alarmCount: 3,
            captureCount: 7,
            requiresStop: false,
          },
        },
      } as never)

    const impact = await algorithmsApi.getDeletionImpact('a1')

    expect(spy).toHaveBeenCalledWith('/algorithms/a1/deletion-impact')
    expect(impact.boundTaskCount).toBe(2)
    expect(impact.affectedCameraIds).toEqual(['cam-a', 'cam-b'])
    expect(impact.alarmCount).toBe(3)
  })

  it('getDeletionImpact defaults missing counters to zero', async () => {
    // 老固件可能不返回某些字段，用 default 兜住而不是抛 ZodError。
    vi.spyOn(apiClient, 'get').mockResolvedValue({
      data: {
        code: 'OK',
        message: 'ok',
        data: {
          algorithmId: 'a1',
          algorithmName: 'X',
          version: '1.0.0',
          status: 'DISABLED',
        },
      },
    } as never)

    const impact = await algorithmsApi.getDeletionImpact('a1')

    expect(impact.boundTaskCount).toBe(0)
    expect(impact.affectedCameraIds).toEqual([])
    expect(impact.requiresStop).toBe(false)
  })

  it('deleteAlgorithm calls DELETE endpoint', async () => {
    vi.mocked(apiClient.delete).mockResolvedValue({
      data: { code: 'OK', message: 'deleted' },
    } as never)

    await algorithmsApi.deleteAlgorithm('algo_to_delete')
    expect(apiClient.delete).toHaveBeenCalledWith('/algorithms/algo_to_delete')
  })

  it('enableAlgorithm and disableAlgorithm parse the {status} action envelope', async () => {
    // 回归：后端 handler.Enable/Disable 只回 { status }，不是完整算法实体。
    // 曾经这里 mock 了完整实体，掩盖了前端用 algorithmSchema parse 的契约错位，
    // 真实环境下会抛 ZodError → 全局 Toast 显示 “Something went wrong!”。
    vi.mocked(apiClient.post).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: { status: 'ACTIVE' } },
    } as never)

    await expect(algorithmsApi.enableAlgorithm('algo_1')).resolves.toBe(
      'ACTIVE'
    )
    expect(apiClient.post).toHaveBeenCalledWith('/algorithms/algo_1/enable')

    vi.mocked(apiClient.post).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: { status: 'DISABLED' } },
    } as never)

    await expect(algorithmsApi.disableAlgorithm('algo_1')).resolves.toBe(
      'DISABLED'
    )
    expect(apiClient.post).toHaveBeenCalledWith('/algorithms/algo_1/disable')
  })

  it('resetAlgorithm parses the {status} action envelope', async () => {
    // 回归：后端 handler.Reset 同样只回 { status: "DISABLED" }。
    vi.mocked(apiClient.post).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: { status: 'DISABLED' } },
    } as never)

    await expect(algorithmsApi.resetAlgorithm('algo_1')).resolves.toBe(
      'DISABLED'
    )
    expect(apiClient.post).toHaveBeenCalledWith('/algorithms/algo_1/reset')
  })
})
