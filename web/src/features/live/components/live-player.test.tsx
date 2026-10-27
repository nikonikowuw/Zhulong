import { createRef } from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { AIResultService } from '../services/ai-result-service'
import { LivePlayer, type LivePlayerRef } from './live-player'

describe('LivePlayer', () => {
  it('正确挂载组件并提供 ref 方法集合', async () => {
    const playerRef = createRef<LivePlayerRef>()
    const onStatsChange = vi.fn()
    const onError = vi.fn()

    const { container } = await render(
      <LivePlayer
        ref={playerRef}
        onStatsChange={onStatsChange}
        onError={onError}
      />
    )

    expect(container).not.toBeNull()
    expect(playerRef.current).not.toBeNull()
    expect(typeof playerRef.current?.decode).toBe('function')
    expect(typeof playerRef.current?.screenshot).toBe('function')
    expect(typeof playerRef.current?.setMute).toBe('function')
    expect(typeof playerRef.current?.reset).toBe('function')

    // 验证安全调用无异常
    playerRef.current?.setMute(true)
    playerRef.current?.reset()
  })

  it('renders overlay canvas when showAIOverlay is true and omits it when false', async () => {
    const { container: c1 } = await render(
      <LivePlayer cameraId='cam-01' showAIOverlay={true} />
    )
    const canvases1 = c1.querySelectorAll('canvas')
    expect(canvases1.length).toBe(2)

    const { container: c2 } = await render(
      <LivePlayer cameraId='cam-01' showAIOverlay={false} />
    )
    const canvases2 = c2.querySelectorAll('canvas')
    expect(canvases2.length).toBe(1)
  })

  it('supports screenshot when vehicle perception is present without faces', async () => {
    const playerRef = createRef<LivePlayerRef>()
    const { container } = await render(
      <LivePlayer ref={playerRef} cameraId='cam-veh-01' showAIOverlay={true} />
    )
    const canvases = container.querySelectorAll('canvas')
    expect(canvases.length).toBe(2)

    // 给 canvas 赋尺寸模拟已解码出帧
    canvases[0].width = 640
    canvases[0].height = 360
    canvases[1].width = 640
    canvases[1].height = 360

    AIResultService.getInstance().dispatchEvent({
      camera_id: 'cam-veh-01',
      pts: 100,
      task_type: 'vehicle_structure',
      vehicles: [
        {
          track_id: 1,
          vehicle_box: [0.1, 0.1, 0.5, 0.5],
          vehicle_score: 0.95,
          vehicle_type: 'car',
          vehicle_color: 'white',
          plates: [
            {
              box: [0.2, 0.4, 0.3, 0.45],
              score: 0.9,
              plate_number: '粤B12345',
              plate_color: 'blue',
              plate_type: 1,
            },
          ],
        },
      ],
    })

    // 验证在仅有车辆识别目标时，screenshot 能够成功执行而不抛错
    expect(() => {
      playerRef.current?.screenshot('test-vehicle')
    }).not.toThrow()
  })
})
