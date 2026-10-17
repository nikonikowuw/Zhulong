import React from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { tasksApi } from '../api/tasks-api'
import { type Task } from '../data/schema'
import { useSyncChannelAlgorithms } from './use-sync-channel-algorithms'

vi.mock('../api/tasks-api', () => ({
  tasksApi: {
    createTask: vi.fn(),
    updateTask: vi.fn(),
    deleteTask: vi.fn(),
  },
}))

describe('useSyncChannelAlgorithms', () => {
  let queryClient: QueryClient

  beforeEach(() => {
    vi.clearAllMocks()
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    })
  })

  function createWrapper() {
    return function Wrapper({ children }: { children: React.ReactNode }) {
      return (
        <QueryClientProvider client={queryClient}>
          {children}
        </QueryClientProvider>
      )
    }
  }

  const existingTasks: Task[] = [
    {
      id: 'task-1',
      cameraId: 'cam-01',
      algorithmId: 'algo-fire',
      enabled: true,
      sampleFps: 5,
      configJson: '{}',
      createdAt: '2026-10-17T00:00:00Z',
      updatedAt: '2026-10-17T00:00:00Z',
    },
    {
      id: 'task-2',
      cameraId: 'cam-01',
      algorithmId: 'algo-helmet',
      enabled: true,
      sampleFps: 10,
      configJson: '{"threshold":0.5}',
      createdAt: '2026-10-17T00:00:00Z',
      updatedAt: '2026-10-17T00:00:00Z',
    },
  ]

  it('executes delete phase first, then creates and updates', async () => {
    const executionOrder: string[] = []

    vi.mocked(tasksApi.deleteTask).mockImplementation(async (id) => {
      executionOrder.push(`delete:${id}`)
    })

    vi.mocked(tasksApi.createTask).mockImplementation(async (dto) => {
      executionOrder.push(`create:${dto.algorithmId}`)
      return {
        id: 'task-new',
        cameraId: dto.cameraId,
        algorithmId: dto.algorithmId,
        enabled: dto.enabled ?? true,
        sampleFps: dto.sampleFps ?? 5,
        configJson: dto.configJson ?? '{}',
        createdAt: '',
        updatedAt: '',
      }
    })

    vi.mocked(tasksApi.updateTask).mockImplementation(async (id, dto) => {
      executionOrder.push(`update:${id}`)
      return {
        id,
        cameraId: 'cam-01',
        algorithmId: 'algo-fire',
        enabled: dto.enabled ?? true,
        sampleFps: dto.sampleFps ?? 5,
        configJson: dto.configJson ?? '{}',
        createdAt: '',
        updatedAt: '',
      }
    })

    const { result } = await renderHook(() => useSyncChannelAlgorithms(), {
      wrapper: createWrapper(),
    })

    // 意图：删除 algo-helmet (task-2)，更新 algo-fire (task-1)，新增 algo-smoking
    await result.current.mutateAsync({
      cameraId: 'cam-01',
      existingTasks,
      drafts: [
        {
          algorithmId: 'algo-fire',
          sampleFps: 8, // fps changed: 5 -> 8
          enabled: true,
          configJson: '{}',
        },
        {
          algorithmId: 'algo-smoking', // new
          sampleFps: 5,
          enabled: true,
          configJson: '{}',
        },
      ],
    })

    // 验证阶段 1：必须先执行 delete，释放算力
    expect(executionOrder[0]).toBe('delete:task-2')
    // 验证阶段 2：再执行 create 与 update
    expect(executionOrder.slice(1)).toContain('create:algo-smoking')
    expect(executionOrder.slice(1)).toContain('update:task-1')
  })

  it('invalidates tasks cache on settlement even when an error occurs', async () => {
    vi.mocked(tasksApi.deleteTask).mockRejectedValueOnce(
      new Error('Database lock error')
    )

    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const { result } = await renderHook(() => useSyncChannelAlgorithms(), {
      wrapper: createWrapper(),
    })

    await expect(
      result.current.mutateAsync({
        cameraId: 'cam-01',
        existingTasks,
        drafts: [], // delete all
      })
    ).rejects.toThrow('Database lock error')

    // 验证 onSettled 确保了缓存刷新
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['tasks'] })
  })
})
