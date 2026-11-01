import { describe, it, expect, beforeEach } from 'vitest'
import { render } from 'vitest-browser-react'
import { AIResultService } from '../services/ai-result-service'
import { type AIResultEventInput } from '../types/ai-result'
import { LivePersonOverlay } from './live-person-overlay'

/** 画布上是否留下了任何不透明像素（即「真的画了东西」）。 */
function hasInk(canvas: HTMLCanvasElement): boolean {
  const ctx = canvas.getContext('2d')
  if (!ctx) return false
  const { data } = ctx.getImageData(0, 0, canvas.width, canvas.height)
  for (let i = 3; i < data.length; i += 4) {
    if (data[i] !== 0) return true
  }
  return false
}

describe('LivePersonOverlay', () => {
  beforeEach(() => {
    AIResultService.getInstance().resetForTesting()
  })

  it('renders a canvas when enabled', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LivePersonOverlay cameraId='cam-01' enabled={true} />
      </div>
    )

    expect(container.querySelector('canvas')).not.toBeNull()
  })

  it('renders nothing when disabled', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LivePersonOverlay cameraId='cam-01' enabled={false} />
      </div>
    )

    expect(container.querySelector('canvas')).toBeNull()
  })

  it('paints a real body box onto the canvas', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LivePersonOverlay cameraId='cam-01' enabled={true} />
      </div>
    )

    const event: AIResultEventInput = {
      camera_id: 'cam-01',
      task_type: 'person_structure',
      pts: 1234,
      cost_ms: 15,
      persons: [
        {
          track_id: 3,
          body_box: [0.1, 0.1, 0.5, 0.9],
          body_score: 0.88,
          faces: [],
        },
      ],
    }

    AIResultService.getInstance().dispatchEvent(event)

    const canvas = container.querySelector('canvas')
    expect(canvas).not.toBeNull()
    expect(hasInk(canvas as HTMLCanvasElement)).toBe(true)
  })

  it('paints nothing when every person is a degenerate face closeup', async () => {
    const { container } = await render(
      <div style={{ width: '400px', height: '300px', position: 'relative' }}>
        <LivePersonOverlay cameraId='cam-01' enabled={true} />
      </div>
    )

    const faceBox: [number, number, number, number] = [0.2, 0.2, 0.4, 0.4]
    const event: AIResultEventInput = {
      camera_id: 'cam-01',
      task_type: 'person_structure',
      pts: 1234,
      cost_ms: 15,
      persons: [
        {
          track_id: 0,
          body_box: faceBox,
          body_score: 0.9,
          faces: [{ box: faceBox, matched: false }],
        },
      ],
    }

    AIResultService.getInstance().dispatchEvent(event)

    const canvas = container.querySelector('canvas')
    expect(canvas).not.toBeNull()
    expect(hasInk(canvas as HTMLCanvasElement)).toBe(false)
  })
})
