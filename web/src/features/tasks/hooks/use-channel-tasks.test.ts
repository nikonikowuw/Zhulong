import { describe, it, expect } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type Task } from '../data/schema'
import { useChannelTasks } from './use-channel-tasks'
import { useTaskComputeBudget } from './use-task-compute-budget'

const mockCameras: Camera[] = [
  {
    id: 'cam-01',
    name: 'Camera 01',
    enabled: true,
    revision: 1,
    health: 'online',
    session: 'running',
    degraded: false,
    stale: false,
    reason: '',
    streams: [],
    createdAt: '2026-10-17T00:00:00Z',
    updatedAt: '2026-10-17T00:00:00Z',
  },
  {
    id: 'cam-02',
    name: 'Camera 02',
    enabled: true,
    revision: 1,
    health: 'offline',
    session: 'idle',
    degraded: false,
    stale: false,
    reason: '',
    streams: [],
    createdAt: '2026-10-17T00:00:00Z',
    updatedAt: '2026-10-17T00:00:00Z',
  },
]

const mockAlgorithms: Algorithm[] = [
  {
    id: 'algo-face',
    name: '人脸识别',
    version: '1.0.0',
    taskType: 'detection',
    category: 'security',
    targetArch: 'aarch64',
    targetDevice: 'rknn',
    computeCost: 20,
    memoryMb: 128,
    description: 'Face detection',
    manifestJson: '{}',
    configSchemaJson: '{}',
    status: 'ACTIVE',
    previousVersion: '',
    createdAt: '2026-10-17T00:00:00Z',
    updatedAt: '2026-10-17T00:00:00Z',
  },
  {
    id: 'algo-fire',
    name: '烟火检测',
    version: '1.0.0',
    taskType: 'detection',
    category: 'safety',
    targetArch: 'aarch64',
    targetDevice: 'rknn',
    computeCost: 10,
    memoryMb: 64,
    description: 'Fire detection',
    manifestJson: '{}',
    configSchemaJson: '{}',
    status: 'ACTIVE',
    previousVersion: '',
    createdAt: '2026-10-17T00:00:00Z',
    updatedAt: '2026-10-17T00:00:00Z',
  },
]

describe('useTaskComputeBudget', () => {
  it('correctly calculates total used points, percent, and remaining points', async () => {
    const tasks: Task[] = [
      {
        id: 't-1',
        cameraId: 'cam-01',
        algorithmId: 'algo-face',
        enabled: true,
        sampleFps: 10, // 20 cost * 10 = 200 points
        configJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 't-2',
        cameraId: 'cam-01',
        algorithmId: 'algo-fire',
        enabled: false, // disabled should not count towards budget
        sampleFps: 5,
        configJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 't-3',
        cameraId: 'cam-02',
        algorithmId: 'algo-fire',
        enabled: true,
        sampleFps: 10, // 10 cost * 10 = 100 points
        configJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ]

    const { result } = await renderHook(() =>
      useTaskComputeBudget(tasks, mockAlgorithms)
    )

    expect(result.current.totalUsedPoints).toBe(300)
    expect(result.current.remainingPoints).toBe(700)
    expect(result.current.percent).toBe(30)
    expect(result.current.isWarning).toBe(false)
    expect(result.current.isOverBudget).toBe(false)
    expect(result.current.enabledCount).toBe(2)
    expect(result.current.totalCount).toBe(3)
  })

  it('triggers isWarning above 800 and isOverBudget above 1000', async () => {
    const tasks: Task[] = [
      {
        id: 't-heavy',
        cameraId: 'cam-01',
        algorithmId: 'algo-face',
        enabled: true,
        sampleFps: 45, // 20 * 45 = 900 points
        configJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ]

    const { result: warningResult } = await renderHook(() =>
      useTaskComputeBudget(tasks, mockAlgorithms)
    )
    expect(warningResult.current.totalUsedPoints).toBe(900)
    expect(warningResult.current.isWarning).toBe(true)
    expect(warningResult.current.isOverBudget).toBe(false)

    // Over budget
    const overTasks: Task[] = [
      {
        id: 't-over',
        cameraId: 'cam-01',
        algorithmId: 'algo-face',
        enabled: true,
        sampleFps: 55, // 20 * 55 = 1100 points
        configJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ]
    const { result: overResult } = await renderHook(() =>
      useTaskComputeBudget(overTasks, mockAlgorithms)
    )
    expect(overResult.current.totalUsedPoints).toBe(1100)
    expect(overResult.current.isOverBudget).toBe(true)
  })
})

describe('useChannelTasks', () => {
  it('groups multiple tasks under the corresponding camera', async () => {
    const tasks: Task[] = [
      {
        id: 't-1',
        cameraId: 'cam-01',
        algorithmId: 'algo-face',
        enabled: true,
        sampleFps: 5, // 20 * 5 = 100
        configJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 't-2',
        cameraId: 'cam-01',
        algorithmId: 'algo-fire',
        enabled: true,
        sampleFps: 10, // 10 * 10 = 100
        configJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ]

    const { result } = await renderHook(() =>
      useChannelTasks(mockCameras, mockAlgorithms, tasks)
    )

    expect(result.current).toHaveLength(2)

    // Camera 01 has 2 tasks, total points = 200
    const cam1Group = result.current.find((g) => g.camera.id === 'cam-01')
    expect(cam1Group).toBeDefined()
    expect(cam1Group?.items).toHaveLength(2)
    expect(cam1Group?.totalChannelPoints).toBe(200)
    expect(cam1Group?.activeCount).toBe(2)

    // Camera 02 has 0 tasks
    const cam2Group = result.current.find((g) => g.camera.id === 'cam-02')
    expect(cam2Group).toBeDefined()
    expect(cam2Group?.items).toHaveLength(0)
    expect(cam2Group?.totalChannelPoints).toBe(0)
    expect(cam2Group?.activeCount).toBe(0)
  })
})
