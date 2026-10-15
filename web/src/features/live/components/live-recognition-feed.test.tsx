import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { AIResultService } from '../services/ai-result-service'
import { type AIResultEventInput } from '../types/ai-result'
import { LiveRecognitionFeed } from './live-recognition-feed'

describe('LiveRecognitionFeed', () => {
  beforeEach(() => {
    AIResultService.getInstance().resetForTesting()
  })

  it('renders null when isOpen is false', async () => {
    const { container } = await render(
      <LiveRecognitionFeed isOpen={false} onClose={vi.fn()} />
    )
    expect(container.firstChild).toBeNull()
  })

  it('renders empty state when no records exist', async () => {
    const { container } = await render(
      <LiveRecognitionFeed isOpen={true} onClose={vi.fn()} />
    )
    expect(container.textContent).toContain('实时识别流')
    expect(container.textContent).toContain('暂无识别记录')
  })

  it('displays incoming records with person name and similarity badge', async () => {
    const { container } = await render(
      <LiveRecognitionFeed
        isOpen={true}
        onClose={vi.fn()}
        cameraNames={{ 'cam-01': '南门枪机' }}
      />
    )

    const event: AIResultEventInput = {
      camera_id: 'cam-01',
      task_type: 'person_structure',
      pts: 100,
      cost_ms: 12,
      persons: [
        {
          body_box: [0, 0, 1, 1],
          body_score: 1.0,
          faces: [
            {
              box: [0.1, 0.1, 0.3, 0.3],
              quality: 0.95,
              matched: true,
              person_id: 'p_admin',
              person_name: '张三丰',
              similarity: 0.98,
            },
          ],
        },
      ],
    }

    AIResultService.getInstance().dispatchEvent(event)

    await expect.poll(() => container.textContent).toContain('张三丰')
    expect(container.textContent).toContain('南门枪机')
    expect(container.textContent).toContain('98%')
  })
})
