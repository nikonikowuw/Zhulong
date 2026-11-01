import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type Task } from '../data/schema'
import { ChannelTaskDialog } from './channel-task-dialog'

/**
 * 用可控替身替换实时画面：真实 CameraFrameSurface 会尝试建流，
 * 无法在单测里确定性地驱动 `live` / `loading` / `unavailable` 三种状态。
 * 它自身已有独立测试；此处只关心弹窗如何按状态开关区域编辑。
 *
 * 刻意**不**渲染 overlay：叠加层的几何正确性由 CameraFrameSurface 的测试负责，
 * 这里若渲染会让同一个「ROI #1」同时出现在画布与右侧图层列表中，断言产生歧义。
 */
let streamState: 'no-camera' | 'loading' | 'live' | 'unavailable' = 'live'

vi.mock('@/features/live', async (importOriginal) => {
  const React = await import('react')
  const actual = await importOriginal<typeof import('@/features/live')>()
  return {
    ...actual,
    CameraFrameSurface: ({
      onStateChange,
    }: {
      onStateChange?: (
        state: 'no-camera' | 'loading' | 'live' | 'unavailable'
      ) => void
    }) => {
      React.useEffect(() => {
        onStateChange?.(streamState)
      }, [onStateChange])
      return React.createElement('div', { 'data-testid': 'frame-surface' })
    },
  }
})

const mockSyncMutate = vi.hoisted(() => vi.fn())
vi.mock('../hooks/use-sync-channel-algorithms', () => ({
  useSyncChannelAlgorithms: () => ({
    mutate: mockSyncMutate,
    isPending: false,
  }),
}))

const mockCamera: Camera = {
  id: 'cam-1',
  name: 'Front Gate Camera',
  enabled: true,
  revision: 1,
  health: 'online',
  session: 'running',
  degraded: false,
  stale: false,
  reason: '',
  streams: [
    {
      id: 1,
      role: 'main',
      protocol: 'rtsp',
      rtspUrl: 'rtsp://127.0.0.1/live',
      transport: 'tcp',
      codec: 'h264',
      width: 1920,
      height: 1080,
      fps: 25,
      fpsString: '25',
    },
  ],
  createdAt: '',
  updatedAt: '',
}

const faceSchemaJson = JSON.stringify({
  type: 'object',
  properties: {
    threshold: {
      type: 'number',
      title: '置信度阈值',
      minimum: 0,
      maximum: 1,
      default: 0.45,
    },
    save_crop: {
      type: 'boolean',
      title: '保存人脸抓拍',
      default: true,
    },
  },
})

function buildAlgorithm(overrides: Partial<Algorithm> = {}): Algorithm {
  return {
    id: 'algo-face',
    name: '人脸识别',
    version: '1.0.0',
    taskType: 'detection',
    category: 'security',
    targetArch: 'aarch64',
    targetDevice: 'npu',
    computeCost: 10,
    memoryMb: 64,
    description: '',
    manifestJson: '{}',
    configSchemaJson: faceSchemaJson,
    supportsMotionGate: true,
    status: 'ACTIVE',
    previousVersion: '',
    createdAt: '',
    updatedAt: '',
    ...overrides,
  }
}

function buildTask(
  overrides: Partial<Task> & {
    configJson?: string
    sampleFps?: number
    algorithmId?: string
    regionConfigJson?: string
  } = {}
): Task {
  const {
    configJson,
    sampleFps,
    algorithmId,
    regionConfigJson,
    ...taskOverrides
  } = overrides

  const subAlgo = {
    id: 'sub-1',
    taskId: taskOverrides.id ?? 'task-1',
    algorithmId: algorithmId ?? 'algo-face',
    enabled: true,
    sampleFps: sampleFps ?? 10,
    configJson: configJson ?? '{"threshold":0.6,"save_crop":false}',
    regionConfigJson: regionConfigJson ?? '{}',
    scheduleJson: '{}',
    createdAt: '',
    updatedAt: '',
  }

  return {
    id: 'task-1',
    cameraId: 'cam-1',
    name: '',
    enabled: true,
    algorithms: [subAlgo],
    createdAt: '',
    updatedAt: '',
    ...taskOverrides,
  }
}

function renderDialog({
  initialAlgorithmId,
  algorithms = [buildAlgorithm()],
  allTasks = [buildTask()],
}: {
  initialAlgorithmId?: string
  algorithms?: Algorithm[]
  allTasks?: Task[]
} = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <ChannelTaskDialog
        open={true}
        onOpenChange={vi.fn()}
        initialCameraId='cam-1'
        initialAlgorithmId={initialAlgorithmId}
        cameras={[mockCamera]}
        algorithms={algorithms}
        allTasks={allTasks}
      />
    </QueryClientProvider>
  )
}

describe('ChannelTaskDialog', () => {
  beforeEach(() => {
    mockSyncMutate.mockClear()
    streamState = 'live'
  })

  it('renders the dynamic config form for a bound algorithm', async () => {
    const { getByRole, getByText } = await renderDialog()

    await expect.element(getByText('配置通道算法任务')).toBeInTheDocument()
    // `exact`：画布上的标定提示语里也含算法名，模糊匹配会命中两个节点
    await expect
      .element(getByText('人脸识别', { exact: true }))
      .toBeInTheDocument()

    // 已保存的配置回显到控件上（标签与控件必须正确关联）
    await expect
      .element(getByRole('textbox', { name: '置信度阈值' }))
      .toHaveValue('0.6')
    await expect
      .element(getByRole('switch', { name: '保存人脸抓拍' }))
      .not.toBeChecked()
  })

  it('seeds schema defaults when a bound task has an empty config', async () => {
    const { getByRole } = await renderDialog({
      allTasks: [buildTask({ configJson: '{}' })],
    })

    await expect
      .element(getByRole('textbox', { name: '置信度阈值' }))
      .toHaveValue('0.45')
    await expect
      .element(getByRole('switch', { name: '保存人脸抓拍' }))
      .toBeChecked()
  })

  it('shows the zero-config notice for an algorithm without properties', async () => {
    const { getByText } = await renderDialog({
      algorithms: [buildAlgorithm({ configSchemaJson: '{}' })],
    })

    await expect
      .element(getByText('该算法无需额外参数配置（零参算法）'))
      .toBeInTheDocument()
  })

  it('steps the sampling rate with the FPS control and keeps the box in sync', async () => {
    const { getByRole, getByText } = await renderDialog()

    await expect.element(getByText('（1 ~ 30 帧/秒）')).toBeInTheDocument()

    const fpsBox = getByRole('textbox', { name: '采样帧率' })
    await expect.element(fpsBox).toHaveValue('10')

    await getByRole('button', { name: '提高采样帧率' }).click()
    await expect.element(fpsBox).toHaveValue('11')

    await getByRole('button', { name: '降低采样帧率' }).click()
    await expect.element(fpsBox).toHaveValue('10')
  })

  it('clamps a typed sampling rate into the allowed range', async () => {
    const { getByRole } = await renderDialog()

    const fpsBox = getByRole('textbox', { name: '采样帧率' })
    await fpsBox.fill('99')
    await userEvent.keyboard('{Enter}')

    await expect.element(fpsBox).toHaveValue('30')
  })

  it('disables the decrease action at the lower bound', async () => {
    const { getByRole } = await renderDialog({
      allTasks: [buildTask({ sampleFps: 1 })],
    })

    await expect
      .element(getByRole('button', { name: '降低采样帧率' }))
      .toBeDisabled()
  })

  it('displays inactive badge and warning alert when algorithm is DISABLED', async () => {
    const disabledAlgo = buildAlgorithm({ status: 'DISABLED' })
    const { getByText } = await renderDialog({
      algorithms: [disabledAlgo],
      allTasks: [buildTask({ algorithmId: disabledAlgo.id })],
    })

    await expect.element(getByText('未启用')).toBeInTheDocument()
    await expect
      .element(
        getByText(
          '该算法当前在算法管理中处于禁用状态，任务保存后将处于待命态。'
        )
      )
      .toBeInTheDocument()
    await expect
      .element(getByText('保存时自动激活所选算法（立即启动推理）'))
      .toBeInTheDocument()
  })

  it('locks region editing until the first frame is rendered', async () => {
    streamState = 'loading'
    const { getByText } = await renderDialog()

    await expect
      .element(getByText('画面未就绪（只读浏览）'))
      .toBeInTheDocument()
    await expect
      .element(getByText('正在等待主码流首帧…出画后自动解锁绘制。'))
      .toBeInTheDocument()
  })

  it('unlocks region editing and shows the stored region once live', async () => {
    streamState = 'live'
    const { getByText } = await renderDialog({
      allTasks: [
        buildTask({
          regionConfigJson: '{"roi":[[0.1,0.1,0.5,0.5]],"mask":[]}',
        }),
      ],
    })

    await expect.element(getByText('ROI #1')).toBeInTheDocument()
    await expect
      .element(getByText('画面未就绪（只读浏览）'))
      .not.toBeInTheDocument()
  })

  it('never submits the editing buffer while the stream is unavailable', async () => {
    streamState = 'unavailable'
    const stored = '{"roi":[[0.1,0.1,0.5,0.5]],"mask":[]}'
    const { getByRole } = await renderDialog({
      allTasks: [buildTask({ regionConfigJson: stored })],
    })

    await getByRole('button', { name: '保存并下发调度' }).click()

    expect(mockSyncMutate).toHaveBeenCalledTimes(1)
    const payload = mockSyncMutate.mock.calls[0][0] as {
      drafts: { regionConfigJson?: string }[]
    }
    expect(payload.drafts).toHaveLength(1)
    // AC8：无参考画面时不得产生新坐标，只能沿用已保存值。
    expect(payload.drafts[0].regionConfigJson).toBe(stored)
  })

  it('blocks save when algorithm configuration violates schema bounds', async () => {
    const { getByRole } = await renderDialog({
      allTasks: [buildTask({ configJson: '{"threshold":99}' })],
    })

    await getByRole('button', { name: '保存并下发调度' }).click()
    expect(mockSyncMutate).not.toHaveBeenCalled()
  })
})

describe('ChannelTaskDialog region editing', () => {
  beforeEach(() => {
    streamState = 'live'
    mockSyncMutate.mockClear()
  })

  it('activates the algorithm passed via initialAlgorithmId', async () => {
    const { getByRole } = await renderDialog({
      initialAlgorithmId: 'algo-smoke',
      algorithms: [
        buildAlgorithm({ id: 'algo-face', name: '人脸识别' }),
        buildAlgorithm({ id: 'algo-smoke', name: '烟雾检测' }),
      ],
      allTasks: [
        buildTask({
          algorithms: [
            {
              id: 'sub-1',
              taskId: 'task-1',
              algorithmId: 'algo-face',
              enabled: true,
              sampleFps: 5,
              configJson: '{}',
              regionConfigJson: '{}',
              scheduleJson: '{}',
              createdAt: '',
              updatedAt: '',
            },
            {
              id: 'sub-2',
              taskId: 'task-1',
              algorithmId: 'algo-smoke',
              enabled: true,
              sampleFps: 8,
              configJson: '{}',
              regionConfigJson: '{}',
              scheduleJson: '{}',
              createdAt: '',
              updatedAt: '',
            },
          ],
        }),
      ],
    })

    const smokeCard = getByRole('button', { name: /烟雾检测/ })
    await expect.element(smokeCard).toHaveAttribute('aria-pressed', 'true')
  })

  it('keeps a newly checked algorithm selected', async () => {
    const { getByRole } = await renderDialog({
      algorithms: [
        buildAlgorithm(),
        buildAlgorithm({ id: 'algo-smoke', name: '烟雾检测' }),
      ],
    })

    const smoke = getByRole('checkbox', { name: '烟雾检测' })
    await expect.element(smoke).not.toBeChecked()

    await smoke.click()

    await expect.element(smoke).toBeChecked()
  })
})
