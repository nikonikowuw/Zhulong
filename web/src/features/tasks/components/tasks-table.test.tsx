import { beforeEach, describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type Task } from '../data/schema'
import { TasksTable } from './tasks-table'

const mockCopyToClipboard = vi.hoisted(() => vi.fn())
const mockToastSuccess = vi.hoisted(() => vi.fn())
const mockToastError = vi.hoisted(() => vi.fn())

vi.mock('../hooks/use-tasks', () => ({
  useUpdateTask: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
}))

vi.mock('@/lib/clipboard', () => ({
  copyToClipboard: mockCopyToClipboard,
}))

vi.mock('sonner', () => ({
  toast: {
    success: mockToastSuccess,
    error: mockToastError,
  },
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
    name: '南门分析任务',
    enabled: true,
    algorithms: [
      {
        id: 'sub-01JAYZ7B901234567890123456',
        taskId: '01JAYZ7B901234567890123456',
        algorithmId: 'algo-fire',
        enabled: true,
        sampleFps: 5,
        configJson: '{}',
        regionConfigJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ],
    createdAt: '',
    updatedAt: '',
  },
]

describe('TasksTable', () => {
  const renderTable = () =>
    render(
      <TasksTable
        tasks={mockTasks}
        cameras={mockCameras}
        algorithms={mockAlgorithms}
        onEditTask={vi.fn()}
        onDeleteTask={vi.fn()}
      />
    )

  beforeEach(() => {
    vi.clearAllMocks()
  })

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

    await expect.element(getByText('南门枪机')).toBeInTheDocument()
    await expect.element(getByText('烟火检测算法')).toBeInTheDocument()
    await expect.element(getByText('5 FPS')).toBeInTheDocument()
    // 15 点/帧 × 5 FPS = 75 点
    await expect
      .element(getByText('75 点', { exact: true }))
      .toBeInTheDocument()
    await expect.element(getByText('运行中')).toBeInTheDocument()
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

    await expect.element(getByText('暂无分析任务记录')).toBeInTheDocument()
  })

  it('triggers onDeleteTask when single-algorithm task delete button is clicked', async () => {
    const onDelete = vi.fn()
    const { getByRole } = await render(
      <TasksTable
        tasks={mockTasks}
        cameras={mockCameras}
        algorithms={mockAlgorithms}
        onEditTask={vi.fn()}
        onDeleteTask={onDelete}
      />
    )

    const trashBtn = getByRole('button', { name: /确认删除|删除任务/i })
    await trashBtn.click()
    expect(onDelete).toHaveBeenCalledWith(mockTasks[0])
  })

  it('shows both the task id and the algorithm subscription id', async () => {
    const { getByText } = await renderTable()

    // 两级主键都要可见：主行 = analysis_tasks.id，次行 = task_algorithms.id
    await expect
      .element(getByText(`${mockTasks[0].id.slice(0, 8)}...`))
      .toBeInTheDocument()
    await expect
      .element(getByText(`${mockTasks[0].algorithms[0].id.slice(0, 8)}...`))
      .toBeInTheDocument()
  })

  it('copies the real task id from the task-id button', async () => {
    mockCopyToClipboard.mockResolvedValue(true)

    const { getByRole } = await renderTable()

    await getByRole('button', { name: /^复制任务 ID$/i }).click()

    // 回归：此处曾复制子表 id（task_algorithms.id）却报「任务 ID 已复制」，
    // 而 /tasks/{id} 与审计日志认的是 analysis_tasks.id。
    await vi.waitFor(() => {
      expect(mockCopyToClipboard).toHaveBeenCalledWith(mockTasks[0].id)
    })
    expect(mockToastSuccess).toHaveBeenCalledWith('任务 ID 已复制到剪贴板')
  })

  it('copies the algorithm subscription id from the subscription-id button', async () => {
    mockCopyToClipboard.mockResolvedValue(true)

    const { getByRole } = await renderTable()

    await getByRole('button', { name: /^复制订阅 ID$/i }).click()

    await vi.waitFor(() => {
      expect(mockCopyToClipboard).toHaveBeenCalledWith(
        mockTasks[0].algorithms[0].id
      )
    })
    expect(mockToastSuccess).toHaveBeenCalledWith('订阅 ID 已复制到剪贴板')
  })

  it('reports a failed copy instead of claiming success', async () => {
    // 回归：非安全上下文（明文 HTTP 局域网）下原实现会同步抛 TypeError，
    // 既无失败提示又静默失效；收敛到共享工具后必须如实报失败。
    mockCopyToClipboard.mockResolvedValue(false)

    const { getByRole } = await renderTable()

    await getByRole('button', { name: /^复制任务 ID$/i }).click()

    await vi.waitFor(() => {
      expect(mockToastError).toHaveBeenCalledWith('复制失败，请手动复制')
    })
    expect(mockToastSuccess).not.toHaveBeenCalled()
  })
})
