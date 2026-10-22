import { describe, it, expect, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { AIResultService } from '../services/ai-result-service'
import { type AIResultEventInput } from '../types/ai-result'
import { LiveVehicleOverlay } from './live-vehicle-overlay'

describe('LiveVehicleOverlay', () => {
  beforeEach(() => {
    AIResultService.getInstance().resetForTesting()
  })

  it('renders canvas element when enabled', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LiveVehicleOverlay cameraId='cam-01' enabled={true} />
      </div>
    )

    const canvas = container.querySelector('canvas')
    expect(canvas).not.toBeNull()
  })

  it('does not render canvas when enabled is false', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LiveVehicleOverlay cameraId='cam-01' enabled={false} />
      </div>
    )

    const canvas = container.querySelector('canvas')
    expect(canvas).toBeNull()
  })

  it('subscribes to vehicle AI events without throwing', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LiveVehicleOverlay cameraId='cam-01' enabled={true} />
      </div>
    )

    const event: AIResultEventInput = {
      camera_id: 'cam-01',
      task_type: 'vehicle_structure',
      pts: 1234,
      cost_ms: 18,
      vehicles: [
        {
          track_id: 1,
          vehicle_box: [0.1, 0.2, 0.6, 0.7],
          vehicle_score: 0.94,
          vehicle_type: 'car',
          vehicle_color: 'blue',
          plates: [
            {
              box: [0.2, 0.4, 0.35, 0.48],
              score: 0.97,
              plate_number: '粤B88888',
              plate_color: '蓝色',
              plate_type: 0,
            },
          ],
        },
      ],
    }

    AIResultService.getInstance().dispatchEvent(event)
    const canvas = container.querySelector('canvas')
    expect(canvas).not.toBeNull()
  })
})
