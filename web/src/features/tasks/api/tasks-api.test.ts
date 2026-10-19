import { describe, it, expect, vi, beforeEach } from 'vitest'
import { apiClient } from '@/lib/api-client'
import { tasksApi } from './tasks-api'

vi.mock('@/lib/api-client', () => ({
  apiClient: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}))

describe('tasksApi', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('getTasks requests /tasks with query parameters and parses list', async () => {
    const mockData = [
      {
        id: 'task-01',
        cameraId: 'cam-01',
        name: 'Cam 1 Task',
        enabled: true,
        algorithms: [
          {
            id: 'sub-01',
            taskId: 'task-01',
            algorithmId: 'algo-01',
            enabled: true,
            sampleFps: 5,
            configJson: '{"thresh": 0.8}',
            regionConfigJson: '{}',
            createdAt: '2026-10-17T00:00:00Z',
            updatedAt: '2026-10-17T00:00:00Z',
          },
        ],
        createdAt: '2026-10-17T00:00:00Z',
        updatedAt: '2026-10-17T00:00:00Z',
      },
    ]

    vi.mocked(apiClient.get).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: mockData },
    } as never)

    const result = await tasksApi.getTasks({
      cameraId: 'cam-01',
      algorithmId: 'algo-01',
    })

    expect(result).toHaveLength(1)
    expect(result[0].id).toBe('task-01')
    expect(result[0].algorithms[0].sampleFps).toBe(5)
    expect(apiClient.get).toHaveBeenCalledWith('/tasks', {
      params: {
        camera_id: 'cam-01',
        algorithm_id: 'algo-01',
      },
    })
  })

  it('getTask requests /tasks/:id and returns parsed task', async () => {
    const mockTask = {
      id: 'task-single',
      cameraId: 'cam-02',
      name: '',
      enabled: false,
      algorithms: [
        {
          id: 'sub-02',
          taskId: 'task-single',
          algorithmId: 'algo-02',
          enabled: false,
          sampleFps: 10,
          configJson: '{}',
          regionConfigJson: '{}',
          createdAt: '2026-10-17T00:00:00Z',
          updatedAt: '2026-10-17T00:00:00Z',
        },
      ],
      createdAt: '2026-10-17T00:00:00Z',
      updatedAt: '2026-10-17T00:00:00Z',
    }

    vi.mocked(apiClient.get).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: mockTask },
    } as never)

    const result = await tasksApi.getTask('task-single')
    expect(result.id).toBe('task-single')
    expect(result.enabled).toBe(false)
    expect(apiClient.get).toHaveBeenCalledWith('/tasks/task-single')
  })

  it('saveChannelTask puts payload to /tasks/camera/:cameraId', async () => {
    const payload = {
      cameraId: 'cam-channel-1',
      name: 'Channel Task',
      enabled: true,
      algorithms: [
        {
          algorithmId: 'algo-01',
          sampleFps: 10,
          enabled: true,
          configJson: '{}',
        },
      ],
    }

    const mockSaved = {
      id: 'task-channel-1',
      cameraId: 'cam-channel-1',
      name: 'Channel Task',
      enabled: true,
      algorithms: [
        {
          id: 'sub-01',
          taskId: 'task-channel-1',
          algorithmId: 'algo-01',
          sampleFps: 10,
          enabled: true,
          configJson: '{}',
          regionConfigJson: '{}',
          createdAt: '2026-10-17T00:00:00Z',
          updatedAt: '2026-10-17T00:00:00Z',
        },
      ],
      createdAt: '2026-10-17T00:00:00Z',
      updatedAt: '2026-10-17T00:00:00Z',
    }

    vi.mocked(apiClient.put).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: mockSaved },
    } as never)

    const result = await tasksApi.saveChannelTask(payload)
    expect(result.id).toBe('task-channel-1')
    expect(result.algorithms[0].sampleFps).toBe(10)
    expect(apiClient.put).toHaveBeenCalledWith(
      '/tasks/camera/cam-channel-1',
      payload
    )
  })

  it('createTask posts payload to /tasks', async () => {
    const payload = {
      cameraId: 'cam-new',
      algorithmId: 'algo-new',
      sampleFps: 15,
      enabled: true,
      configJson: '{"roi": [0,0,100,100]}',
    }

    const mockCreated = {
      id: 'task-created',
      cameraId: 'cam-new',
      name: '',
      enabled: true,
      algorithms: [
        {
          id: 'sub-new',
          taskId: 'task-created',
          algorithmId: 'algo-new',
          sampleFps: 15,
          enabled: true,
          configJson: '{"roi": [0,0,100,100]}',
          regionConfigJson: '{}',
          createdAt: '2026-10-17T00:00:00Z',
          updatedAt: '2026-10-17T00:00:00Z',
        },
      ],
      createdAt: '2026-10-17T00:00:00Z',
      updatedAt: '2026-10-17T00:00:00Z',
    }

    vi.mocked(apiClient.post).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: mockCreated },
    } as never)

    const result = await tasksApi.createTask(payload)
    expect(result.id).toBe('task-created')
    expect(apiClient.post).toHaveBeenCalledWith('/tasks', {
      cameraId: 'cam-new',
      algorithmId: 'algo-new',
      sampleFps: 15,
      enabled: true,
      configJson: '{"roi": [0,0,100,100]}',
    })
  })

  it('updateTask puts payload to /tasks/:id', async () => {
    const updatePayload = {
      enabled: false,
      sampleFps: 20,
      configJson: '{"updated": true}',
    }

    const mockUpdated = {
      id: 'task-up',
      cameraId: 'cam-01',
      name: '',
      enabled: false,
      algorithms: [
        {
          id: 'sub-01',
          taskId: 'task-up',
          algorithmId: 'algo-01',
          sampleFps: 20,
          enabled: false,
          configJson: '{"updated": true}',
          regionConfigJson: '{}',
          createdAt: '2026-10-17T00:00:00Z',
          updatedAt: '2026-10-17T00:00:00Z',
        },
      ],
      createdAt: '2026-10-17T00:00:00Z',
      updatedAt: '2026-10-17T00:00:00Z',
    }

    vi.mocked(apiClient.put).mockResolvedValue({
      data: { code: 'OK', message: 'success', data: mockUpdated },
    } as never)

    const result = await tasksApi.updateTask('task-up', updatePayload)
    expect(result.algorithms[0].sampleFps).toBe(20)
    expect(result.enabled).toBe(false)
    expect(apiClient.put).toHaveBeenCalledWith('/tasks/task-up', {
      sampleFps: 20,
      enabled: false,
      configJson: '{"updated": true}',
    })
  })

  it('deleteTask sends delete to /tasks/:id', async () => {
    vi.mocked(apiClient.delete).mockResolvedValue({
      data: { code: 'OK', message: 'success' },
    } as never)

    await tasksApi.deleteTask('task-del')
    expect(apiClient.delete).toHaveBeenCalledWith('/tasks/task-del')
  })
})
