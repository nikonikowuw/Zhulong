import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { userEvent } from 'vitest/browser'
import { type Algorithm } from '@/features/algorithms/data/schema'
import { type Camera } from '@/features/cameras/data/schema'
import { type Task } from '../data/schema'
import { ChannelTaskDialog } from './channel-task-dialog'

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
    status: 'ACTIVE',
    previousVersion: '',
    createdAt: '',
    updatedAt: '',
    ...overrides,
  }
}

function buildTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    cameraId: 'cam-1',
    algorithmId: 'algo-face',
    enabled: true,
    sampleFps: 10,
    configJson: '{"threshold":0.6,"save_crop":false}',
    regionConfigJson: '{}',
    createdAt: '',
    updatedAt: '',
    ...overrides,
  }
}

function renderDialog({
  algorithms = [buildAlgorithm()],
  allTasks = [buildTask()],
}: {
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
        cameras={[mockCamera]}
        algorithms={algorithms}
        allTasks={allTasks}
      />
    </QueryClientProvider>
  )
}

describe('ChannelTaskDialog', () => {
  it('renders the dynamic config form for a bound algorithm', async () => {
    const { getByRole, getByText } = await renderDialog()

    await expect.element(getByText('配置通道算法任务')).toBeInTheDocument()
    await expect.element(getByText('人脸识别')).toBeInTheDocument()

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
})
