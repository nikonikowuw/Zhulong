import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { AIResultService } from '../services/ai-result-service'
import { type AIResultEventInput } from '../types/ai-result'
import { useAIResults } from './use-ai-results'

describe('useAIResults', () => {
  beforeEach(() => {
    AIResultService.getInstance().resetForTesting()
    vi.useRealTimers()
  })

  it('receives face results for matching camera', async () => {
    const { result, act } = await renderHook(() =>
      useAIResults({ cameraId: 'cam-01', enabled: true })
    )

    expect(result.current.faces).toHaveLength(0)

    const event: AIResultEventInput = {
      camera_id: 'cam-01',
      task_type: 'person_structure',
      pts: 100,
      cost_ms: 10,
      persons: [
        {
          track_id: 1,
          body_box: [0.1, 0.1, 0.5, 0.5],
          body_score: 1.0,
          faces: [
            {
              box: [0.2, 0.2, 0.4, 0.4],
              quality: 0.9,
              matched: true,
              person_id: 'p_01',
              person_name: '测试人员',
              similarity: 0.95,
            },
          ],
        },
      ],
    }

    await act(() => {
      AIResultService.getInstance().dispatchEvent(event)
    })

    expect(result.current.faces).toHaveLength(1)
    expect(result.current.faces[0].person_name).toBe('测试人员')
    expect(result.current.latestResult?.camera_id).toBe('cam-01')
  })

  it('clears faces when camera is disabled or cleared', async () => {
    const { result, act } = await renderHook(() =>
      useAIResults({ cameraId: 'cam-01', enabled: true })
    )

    const event: AIResultEventInput = {
      camera_id: 'cam-01',
      task_type: 'person_structure',
      pts: 100,
      cost_ms: 10,
      persons: [
        {
          body_box: [0, 0, 1, 1],
          body_score: 1,
          faces: [
            {
              box: [0, 0, 0.5, 0.5],
              matched: false,
              similarity: 0.3,
            },
          ],
        },
      ],
    }

    await act(() => {
      AIResultService.getInstance().dispatchEvent(event)
    })
    expect(result.current.faces).toHaveLength(1)

    await act(() => {
      result.current.clearFaces()
    })
    expect(result.current.faces).toHaveLength(0)
  })
})
