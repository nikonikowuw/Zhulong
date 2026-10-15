import { describe, it, expect, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { AIResultService } from '../services/ai-result-service'
import { type AIResultEventInput } from '../types/ai-result'
import { LiveFaceOverlay } from './live-face-overlay'

describe('LiveFaceOverlay', () => {
  beforeEach(() => {
    AIResultService.getInstance().resetForTesting()
  })

  it('renders canvas element when enabled', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LiveFaceOverlay cameraId='cam-01' enabled={true} />
      </div>
    )

    const canvas = container.querySelector('canvas')
    expect(canvas).not.toBeNull()
  })

  it('does not render canvas when enabled is false', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LiveFaceOverlay cameraId='cam-01' enabled={false} />
      </div>
    )

    const canvas = container.querySelector('canvas')
    expect(canvas).toBeNull()
  })

  it('subscribes to AI events without throwing', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LiveFaceOverlay cameraId='cam-01' enabled={true} />
      </div>
    )

    const event: AIResultEventInput = {
      camera_id: 'cam-01',
      task_type: 'person_structure',
      pts: 1234,
      cost_ms: 15,
      persons: [
        {
          body_box: [0, 0, 1, 1],
          body_score: 1.0,
          faces: [
            {
              box: [0.1, 0.1, 0.3, 0.3],
              matched: true,
              person_id: 'p_01',
              person_name: '测试人员',
              similarity: 0.99,
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
