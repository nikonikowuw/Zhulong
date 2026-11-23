import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Algorithm } from '@/features/algorithms/contract'
import { type Camera } from '@/features/cameras/contract'
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
let mockSyncPending = false
vi.mock('../hooks/use-sync-channel-algorithms', () => ({
  useSyncChannelAlgorithms: () => ({
    mutate: mockSyncMutate,
    isPending: mockSyncPending,
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
    crop: null,
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
  onOpenChange = vi.fn(),
}: {
  initialAlgorithmId?: string
  algorithms?: Algorithm[]
  allTasks?: Task[]
  onOpenChange?: (open: boolean) => void
} = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })

  return render(
    <QueryClientProvider client={queryClient}>
      <ChannelTaskDialog
        open={true}
        onOpenChange={onOpenChange}
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
    mockSyncPending = false
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

  it('submits the task-level crop window alongside the algorithm drafts', async () => {
    streamState = 'live'
    const { getByRole } = await renderDialog({
      allTasks: [buildTask({ crop: [0.1, 0.2, 0.6, 0.8] })],
    })

    await getByRole('button', { name: '保存并下发调度' }).click()

    expect(mockSyncMutate).toHaveBeenCalledTimes(1)
    const payload = mockSyncMutate.mock.calls[0][0] as {
      crop?: number[] | null
    }
    // 整任务 upsert：crop 必须显式随载荷提交，省略会被服务端当成“保留旧值”。
    expect(payload.crop).toEqual([0.1, 0.2, 0.6, 0.8])
  })

  it('keeps the saved crop when the stream is unavailable (read-only)', async () => {
    streamState = 'unavailable'
    const { getByRole } = await renderDialog({
      allTasks: [buildTask({ crop: [0.2, 0.2, 0.8, 0.8] })],
    })

    await getByRole('button', { name: '保存并下发调度' }).click()

    const payload = mockSyncMutate.mock.calls[0][0] as {
      crop?: number[] | null
    }
    // 画面不可用时不许编辑 crop，但也不能顺手把它清掉。
    expect(payload.crop).toEqual([0.2, 0.2, 0.8, 0.8])
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

  it('keeps freeze button enabled when stream is live even with no algorithm selected', async () => {
    streamState = 'live'
    const { getByRole } = await renderDialog({
      allTasks: [],
    })

    await expect
      .element(getByRole('button', { name: '冻结画面' }))
      .not.toBeDisabled()
  })

  it('keeps budget meter visible and stacks layout vertically without horizontal overflow on narrow viewports', async () => {
    const { getByText, getByTestId } = await renderDialog()

    await expect.element(getByText(/本通道算力/)).toBeVisible()

    const workbenchEl = document.querySelector(
      '[data-slot="channel-task-workbench"]'
    ) as HTMLElement
    const frameEl = getByTestId('frame-surface').element()
    const matrixHeading = getByText('算法调度矩阵').element()

    const frameRect = frameEl.getBoundingClientRect()
    const matrixRect = matrixHeading.getBoundingClientRect()

    // chromium-default (414px < lg)：上下堆叠、画布保有高度且工作台不横向溢出
    expect(frameRect.height).toBeGreaterThanOrEqual(200)
    expect(frameRect.bottom).toBeLessThanOrEqual(matrixRect.top + 4)
    expect(workbenchEl.scrollWidth).toBeLessThanOrEqual(
      workbenchEl.clientWidth + 1
    )
  })

  it('switches back to the params tab when validation fails from the schedule tab', async () => {
    const { getByRole, getByText } = await renderDialog({
      allTasks: [buildTask({ configJson: '{"threshold":99}' })],
    })

    await getByRole('tab', { name: /分时布防计划/ }).click()
    await expect.element(getByText('启用分时计划')).toBeVisible()

    await getByRole('button', { name: '保存并下发调度' }).click()

    expect(mockSyncMutate).not.toHaveBeenCalled()
    await expect
      .element(getByRole('textbox', { name: '置信度阈值' }))
      .toHaveValue('99')
  })

  it('switches back to params tab and opens raw JSON mode when invalid JSON fails validation', async () => {
    const { getByRole, getByText, getByPlaceholder } = await renderDialog({
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
              configJson: '{"threshold":0.5}',
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
              sampleFps: 5,
              configJson: '{ bad json',
              regionConfigJson: '{}',
              scheduleJson: '{}',
              createdAt: '',
              updatedAt: '',
            },
          ],
        }),
      ],
    })

    await getByRole('tab', { name: /分时布防计划/ }).click()
    await getByRole('button', { name: '保存并下发调度' }).click()

    expect(mockSyncMutate).not.toHaveBeenCalled()
    await expect.element(getByText('语法错误')).toBeVisible()
    await expect.element(getByPlaceholder('{}')).toHaveValue('{ bad json')
  })

  it('supports Delete and Ctrl+Z shortcuts for regions while ignoring inputs', async () => {
    const { getByRole, getByText } = await renderDialog({
      allTasks: [
        buildTask({
          regionConfigJson: '{"roi":[[0.1,0.1,0.5,0.5]],"mask":[]}',
        }),
      ],
    })

    // 选中右侧列表中的 ROI #1
    await getByText('ROI #1').click()

    // 焦点在输入框内时按 Backspace / Ctrl+Z 不应误删区域
    const fpsInput = getByRole('textbox', { name: '采样帧率' })
    await fpsInput.click()
    await userEvent.keyboard('{Backspace}')
    await expect.element(getByText('ROI #1')).toBeInTheDocument()

    // 聚焦到非输入区域（列表项）后按 Delete 删除区域，再按 Ctrl+Z 撤销恢复
    await getByText('ROI #1').click()
    await userEvent.keyboard('{Delete}')
    await expect.element(getByText('ROI #1')).not.toBeInTheDocument()

    await userEvent.keyboard('{Control>}z{/Control}')
    await expect.element(getByText('ROI #1')).toBeInTheDocument()
  })

  it('clears and undoes crop window via Delete and Ctrl+Z when crop tool is active', async () => {
    const { getByRole } = await renderDialog({
      allTasks: [buildTask({ crop: [0.1, 0.2, 0.6, 0.8] })],
    })

    const clearCropBtn = getByRole('button', { name: '清除裁剪' })
    await expect.element(clearCropBtn).not.toBeDisabled()

    await getByRole('button', { name: '裁剪窗口' }).click()
    await userEvent.keyboard('{Delete}')
    await expect.element(clearCropBtn).toBeDisabled()

    await userEvent.keyboard('{Control>}z{/Control}')
    await expect.element(clearCropBtn).not.toBeDisabled()
  })

  it('fills the full viewport via body portal and locks/restores body scroll', async () => {
    document.body.style.overflow = 'auto'
    const { getByRole, unmount } = await renderDialog()

    const dialog = getByRole('dialog')
    await expect.element(dialog).toBeVisible()
    await expect.element(dialog).toHaveAttribute('aria-modal', 'true')

    const el = dialog.element()
    expect(el.parentElement).toBe(document.body)

    const rect = el.getBoundingClientRect()
    expect(rect.top).toBe(0)
    expect(rect.left).toBe(0)
    expect(Math.round(rect.width)).toBe(window.innerWidth)
    expect(Math.round(rect.height)).toBe(window.innerHeight)

    expect(document.body.style.overflow).toBe('hidden')

    await unmount()
    expect(document.body.style.overflow).toBe('auto')
    document.body.style.overflow = ''
  })

  it('closes on Escape when idle while ignoring Escape inside inputs', async () => {
    const onOpenChange = vi.fn()
    const { getByRole } = await renderDialog({ onOpenChange })

    // 焦点在输入框内时按 Escape 不误关工作台
    const thresholdInput = getByRole('textbox', { name: '置信度阈值' })
    await thresholdInput.click()
    await userEvent.keyboard('{Escape}')
    expect(onOpenChange).not.toHaveBeenCalled()

    // 失焦后按 Escape 正常关闭
    ;(thresholdInput.element() as HTMLElement).blur()
    await userEvent.keyboard('{Escape}')
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('prevents closing via Escape or cancel button while saving', async () => {
    mockSyncPending = true
    const onOpenChange = vi.fn()
    const { getByRole } = await renderDialog({ onOpenChange })

    await userEvent.keyboard('{Escape}')
    await getByRole('button', { name: '取消' }).click()
    expect(onOpenChange).not.toHaveBeenCalled()
  })
})
