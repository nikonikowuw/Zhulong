import { describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type ChannelTaskGroup } from '../data/schema'
import { ChannelTasksView } from './channel-tasks-view'

describe('ChannelTasksView', () => {
  it('renders empty placeholder when no channel groups exist', async () => {
    const { getByText } = await render(
      <ChannelTasksView groups={[]} onConfigureCamera={vi.fn()} />
    )
    await expect.element(getByText('暂无分析任务记录')).toBeInTheDocument()
  })

  it('renders camera cards and configured algorithm badges', async () => {
    const mockGroups: ChannelTaskGroup[] = [
      {
        camera: {
          id: 'cam-gate',
          name: '东大门球机',
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
        },
        items: [
          {
            taskId: 'task-1',
            task: {
              id: 'task-1',
              cameraId: 'cam-gate',
              name: '',
              enabled: true,
              algorithms: [],
              createdAt: '',
              updatedAt: '',
            },
            algorithmItem: {
              id: 'sub-1',
              taskId: 'task-1',
              algorithmId: 'algo-face',
              enabled: true,
              sampleFps: 10,
              configJson: '{}',
              regionConfigJson: '{}',
              createdAt: '',
              updatedAt: '',
            },
            algorithm: {
              id: 'algo-face',
              name: '人脸结构化',
              version: '1.0.0',
              taskType: 'detection',
              category: 'security',
              targetArch: 'aarch64',
              targetDevice: 'rknn',
              computeCost: 20,
              memoryMb: 128,
              description: '',
              manifestJson: '{}',
              configSchemaJson: '{}',
              status: 'ACTIVE',
              previousVersion: '',
              createdAt: '',
              updatedAt: '',
            },
            computePoints: 200,
          },
        ],
        totalChannelPoints: 200,
        activeCount: 1,
        totalCount: 1,
      },
    ]

    const onConfig = vi.fn()
    const { getByText, getByRole } = await render(
      <ChannelTasksView groups={mockGroups} onConfigureCamera={onConfig} />
    )

    await expect.element(getByText('东大门球机')).toBeInTheDocument()
    await expect.element(getByText('在线')).toBeInTheDocument()
    await expect.element(getByText('人脸结构化')).toBeInTheDocument()
    await expect.element(getByText('10 FPS')).toBeInTheDocument()
    // 通道汇总行与本条算法行都含「200 点」，需精确匹配到算法行
    await expect
      .element(getByText('200 点', { exact: true }))
      .toBeInTheDocument()

    const configBtn = getByRole('button', { name: /配置算法/i })
    await configBtn.click()
    expect(onConfig).toHaveBeenCalledWith('cam-gate')
  })

  it('triggers onToggleChannelTask and onDeleteChannelTask callbacks', async () => {
    const mockGroups: ChannelTaskGroup[] = [
      {
        camera: {
          id: 'cam-gate',
          name: '东大门球机',
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
        items: [
          {
            taskId: 'task-1',
            task: {
              id: 'task-1',
              cameraId: 'cam-gate',
              name: '',
              enabled: true,
              algorithms: [],
              createdAt: '',
              updatedAt: '',
            },
            algorithmItem: {
              id: 'sub-1',
              taskId: 'task-1',
              algorithmId: 'algo-face',
              enabled: true,
              sampleFps: 10,
              configJson: '{}',
              regionConfigJson: '{}',
              createdAt: '',
              updatedAt: '',
            },
            algorithm: {
              id: 'algo-face',
              name: '人脸结构化',
              version: '1.0.0',
              taskType: 'detection',
              category: 'security',
              targetArch: 'aarch64',
              targetDevice: 'rknn',
              computeCost: 20,
              memoryMb: 128,
              description: '',
              manifestJson: '{}',
              configSchemaJson: '{}',
              status: 'ACTIVE',
              previousVersion: '',
              createdAt: '',
              updatedAt: '',
            },
            computePoints: 200,
          },
        ],
        totalChannelPoints: 200,
        activeCount: 1,
        totalCount: 1,
      },
    ]

    const onToggle = vi.fn()
    const onDelete = vi.fn()

    const { getByRole } = await render(
      <ChannelTasksView
        groups={mockGroups}
        onConfigureCamera={vi.fn()}
        onToggleChannelTask={onToggle}
        onDeleteChannelTask={onDelete}
      />
    )

    const switchBtn = getByRole('switch', { name: '切换通道分析状态' })
    await switchBtn.click()
    expect(onToggle).toHaveBeenCalledWith(mockGroups[0].items[0].task)

    const deleteBtn = getByRole('button', { name: '删除通道任务' })
    await deleteBtn.click()
    expect(onDelete).toHaveBeenCalledWith(mockGroups[0].items[0].task)
  })
})
