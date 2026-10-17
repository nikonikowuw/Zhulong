import { describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type ChannelTaskGroup } from '../data/schema'
import { ChannelTasksView } from './channel-tasks-view'

describe('ChannelTasksView', () => {
  it('renders empty placeholder when no channel groups exist', async () => {
    const { getByText } = await render(
      <ChannelTasksView groups={[]} onConfigureCamera={vi.fn()} />
    )
    expect(getByText('暂无分析任务记录')).toBeDefined()
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
            task: {
              id: 'task-1',
              cameraId: 'cam-gate',
              algorithmId: 'algo-face',
              enabled: true,
              sampleFps: 10,
              configJson: '{}',
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

    expect(getByText('东大门球机')).toBeDefined()
    expect(getByText('在线')).toBeDefined()
    expect(getByText('人脸结构化')).toBeDefined()
    expect(getByText('10 FPS')).toBeDefined()
    expect(getByText('200点')).toBeDefined()

    const configBtn = getByRole('button', { name: /配置算法/i })
    await configBtn.click()
    expect(onConfig).toHaveBeenCalledWith('cam-gate')
  })
})
