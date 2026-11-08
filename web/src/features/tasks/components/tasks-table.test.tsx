import { beforeEach, describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type Task } from '../data/schema'
import { TasksProvider, useTasksContext } from './tasks-provider'
import { TasksTable } from './tasks-table'

const mockCopyToClipboard = vi.hoisted(() => vi.fn())
const mockToastSuccess = vi.hoisted(() => vi.fn())
const mockToastError = vi.hoisted(() => vi.fn())
const mockUpdateMutate = vi.hoisted(() => vi.fn())

vi.mock('../hooks/use-tasks', () => ({
  useUpdateTask: () => ({
    mutate: mockUpdateMutate,
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
    supportsMotionGate: true,
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
    crop: null,
    algorithms: [
      {
        id: 'sub-01JAYZ7B901234567890123456',
        taskId: '01JAYZ7B901234567890123456',
        algorithmId: 'algo-fire',
        enabled: true,
        sampleFps: 5,
        configJson: '{}',
        regionConfigJson: '{}',
        scheduleJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ],
    createdAt: '',
    updatedAt: '',
  },
]

/** 把 Provider 里的弹窗状态暴露成可断言的文本，替代旧的 onDeleteTask 回调断言。 */
function DialogProbe() {
  const { open, currentRow, targetCameraId, targetAlgorithmId } =
    useTasksContext()
  return (
    <div>{`dialog:${open ?? 'none'}:${currentRow?.id ?? ''}:${targetCameraId ?? ''}:${targetAlgorithmId ?? ''}`}</div>
  )
}

function TasksTableHarness({
  tasks,
  defaultExpanded = true,
}: {
  tasks: Task[]
  defaultExpanded?: boolean
}) {
  return (
    <TasksProvider>
      <TasksTable
        tasks={tasks}
        cameras={mockCameras}
        algorithms={mockAlgorithms}
        isLoading={false}
        isError={false}
        onRetry={vi.fn()}
        search={{}}
        navigate={vi.fn()}
        defaultExpanded={defaultExpanded}
      />
      <DialogProbe />
    </TasksProvider>
  )
}

describe('TasksTable', () => {
  const renderTable = () => render(<TasksTableHarness tasks={mockTasks} />)

  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders table headers and row items correctly', async () => {
    const { getByText } = await renderTable()

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
    const { getByText } = await render(<TasksTableHarness tasks={[]} />)

    await expect.element(getByText('暂无分析任务记录')).toBeInTheDocument()
  })

  it('routes single-algorithm deletes to the task delete dialog', async () => {
    const { getByRole, getByText } = await renderTable()

    await getByRole('button', { name: /^操作$/ }).click()
    await getByRole('menuitem', { name: /删除任务/ }).click()

    // 该通道只剩一个算法 → 走整条任务删除，而不是「摘除订阅」
    await expect
      .element(getByText(`dialog:delete:${mockTasks[0].id}`))
      .toBeInTheDocument()
    expect(mockUpdateMutate).not.toHaveBeenCalled()
  })

  it('removes only the subscription when the channel keeps other algorithms', async () => {
    const twoAlgoTask: Task = {
      ...mockTasks[0],
      algorithms: [
        ...mockTasks[0].algorithms,
        {
          ...mockTasks[0].algorithms[0],
          id: 'sub-2',
        },
      ],
    }

    const { getByRole } = await render(
      <TasksTableHarness tasks={[twoAlgoTask]} />
    )

    await getByRole('button', { name: /^操作$/ })
      .first()
      .click()
    // 通道仍有其它算法时，动作是「移除该算法」而不是「删除任务」
    await getByRole('menuitem', { name: /移除该算法/ })
      .first()
      .click()

    expect(mockUpdateMutate).toHaveBeenCalledTimes(1)
    const [payload] = mockUpdateMutate.mock.calls[0] as [
      { id: string; data: { algorithms: { id: string }[] } },
    ]
    expect(payload.id).toBe(twoAlgoTask.id)
    // 只摘掉被点的那条订阅，其余原样回传
    expect(payload.data.algorithms.map((a) => a.id)).toEqual(['sub-2'])
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

  it('defaults to collapsed and can be expanded to reveal sub-table details', async () => {
    // 验证默认折叠行为（defaultExpanded 为 false）
    const { getByRole, getByText } = await render(
      <TasksTableHarness tasks={mockTasks} defaultExpanded={false} />
    )

    // 默认折叠：主行可见，子表格明细不可见
    await expect.element(getByText('南门枪机')).toBeInTheDocument()
    await expect
      .element(getByText('绑定的算法实例明细'))
      .not.toBeInTheDocument()

    // 点击行首展开按钮展开明细
    await getByRole('button', { name: /^展开明细$/ }).click()
    await expect.element(getByText('绑定的算法实例明细')).toBeInTheDocument()
    await expect.element(getByText('烟火检测算法')).toBeInTheDocument()

    // 再次点击折叠
    await getByRole('button', { name: /^折叠明细$/ }).click()
    await expect
      .element(getByText('绑定的算法实例明细'))
      .not.toBeInTheDocument()
  })

  it('opens channel task dialog with targeted algorithm when clicking edit in sub-table', async () => {
    const { getByRole, getByText } = await renderTable()

    await getByRole('button', { name: /^操作$/ })
      .first()
      .click()
    await getByRole('menuitem', { name: /编辑配置/ })
      .first()
      .click()

    await expect
      .element(
        getByText(
          `dialog:channelTask:${mockTasks[0].id}:cam-01:${mockTasks[0].algorithms[0].algorithmId}`
        )
      )
      .toBeInTheDocument()
  })
})
