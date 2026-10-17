import { describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type Task } from '../data/schema'
import { TasksTable } from './tasks-table'

vi.mock('../hooks/use-tasks', () => ({
  useUpdateTask: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
}))

const mockCameras: Camera[] = [
  {
    id: 'cam-01',
    name: '南门枪机',
    enabled: true,
    revision: 1,
    health: 'online',
    session: 'running',
    degraded: false,
    stale: false,
    reason: '',
    streams: [],
    createdAt: '',
    updatedAt: '',
  },
]

const mockAlgorithms: Algorithm[] = [
  {
    id: 'algo-fire',
    name: '烟火检测算法',
    version: '1.0.0',
    taskType: 'detection',
    category: 'safety',
    targetArch: 'aarch64',
    targetDevice: 'rknn',
    computeCost: 15,
    memoryMb: 64,
    description: '',
    manifestJson: '{}',
    configSchemaJson: '{}',
    status: 'ACTIVE',
    previousVersion: '',
    createdAt: '',
    updatedAt: '',
  },
]

const mockTasks: Task[] = [
  {
    id: '01JAYZ7B901234567890123456',
    cameraId: 'cam-01',
    algorithmId: 'algo-fire',
    enabled: true,
    sampleFps: 5,
    configJson: '{}',
    createdAt: '',
    updatedAt: '',
  },
]

describe('TasksTable', () => {
  it('renders table headers and row items correctly', async () => {
    const onEdit = vi.fn()
    const onDelete = vi.fn()

    const { getByText } = await render(
      <TasksTable
        tasks={mockTasks}
        cameras={mockCameras}
        algorithms={mockAlgorithms}
        onEditTask={onEdit}
        onDeleteTask={onDelete}
      />
    )

    expect(getByText('南门枪机')).toBeDefined()
    expect(getByText('烟火检测算法')).toBeDefined()
    expect(getByText('5 FPS')).toBeDefined()
    expect(getByText('75 点')).toBeDefined() // 15 cost * 5 fps
    expect(getByText('运行中')).toBeDefined()
  })

  it('renders empty message when tasks array is empty', async () => {
    const { getByText } = await render(
      <TasksTable
        tasks={[]}
        cameras={mockCameras}
        algorithms={mockAlgorithms}
        onEditTask={vi.fn()}
        onDeleteTask={vi.fn()}
      />
    )

    expect(getByText('暂无分析任务记录')).toBeDefined()
  })
})
