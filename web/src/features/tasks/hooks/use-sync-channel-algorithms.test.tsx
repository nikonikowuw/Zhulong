import React from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { tasksApi } from '../api/tasks-api'
import { useSyncChannelAlgorithms } from './use-sync-channel-algorithms'
import { TASKS_QUERY_KEY } from './use-tasks'

vi.mock('../api/tasks-api', () => ({
  tasksApi: {
    saveChannelTask: vi.fn(),
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

  it('saves channel task with configured algorithms atomically', async () => {
    vi.mocked(tasksApi.saveChannelTask).mockResolvedValue({
      id: 'task-1',
      cameraId: 'cam-01',
      name: '',
      enabled: true,
      algorithms: [],
      createdAt: '',
      updatedAt: '',
    })

    const { result } = await renderHook(() => useSyncChannelAlgorithms(), {
      wrapper: createWrapper(),
    })

    await result.current.mutateAsync({
      cameraId: 'cam-01',
      drafts: [
        {
          algorithmId: 'algo-fire',
          sampleFps: 8,
          enabled: true,
          configJson: '{}',
        },
        {
          algorithmId: 'algo-smoking',
          sampleFps: 5,
          enabled: true,
          configJson: '{}',
        },
      ],
    })

    expect(tasksApi.saveChannelTask).toHaveBeenCalledWith({
      cameraId: 'cam-01',
      enabled: true,
      algorithms: [
        {
          algorithmId: 'algo-fire',
          sampleFps: 8,
          enabled: true,
          configJson: '{}',
          regionConfigJson: undefined,
        },
        {
          algorithmId: 'algo-smoking',
          sampleFps: 5,
          enabled: true,
          configJson: '{}',
          regionConfigJson: undefined,
        },
      ],
    })
  })

  it('invalidates tasks cache on settlement even when an error occurs', async () => {
    vi.mocked(tasksApi.saveChannelTask).mockRejectedValue(
      new Error('Database lock error')
    )

    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries')

    const { result } = await renderHook(() => useSyncChannelAlgorithms(), {
      wrapper: createWrapper(),
    })

    await expect(
      result.current.mutateAsync({
        cameraId: 'cam-01',
        drafts: [],
      })
    ).rejects.toThrow('Database lock error')

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: TASKS_QUERY_KEY })
  })
})
