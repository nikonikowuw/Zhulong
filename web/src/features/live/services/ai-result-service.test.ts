import { describe, it, expect, beforeEach, vi } from 'vitest'
import { type AIResultEvent, type AIResultEventInput } from '../types/ai-result'
import { AIResultService } from './ai-result-service'

describe('AIResultService', () => {
  beforeEach(() => {
    AIResultService.getInstance().resetForTesting()
    vi.clearAllTimers()
  })

  it('routes dispatched events to subscribed cameras', () => {
    const service = AIResultService.getInstance()
    const received: AIResultEvent[] = []

    const unsub = service.subscribeCamera('cam_test_1', (ev) => {
      received.push(ev)
    })

    const testEvent: AIResultEventInput = {
      camera_id: 'cam_test_1',
      task_type: 'person_structure',
      pts: 1000,
      cost_ms: 15,
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
              person_name: '李四',
              similarity: 0.92,
            },
          ],
        },
      ],
    }

    service.dispatchEvent(testEvent)
    expect(received).toHaveLength(1)
    expect(received[0].camera_id).toBe('cam_test_1')

    // 另一个摄像机事件不应收到
    service.dispatchEvent({
      ...testEvent,
      camera_id: 'cam_test_2',
    })
    expect(received).toHaveLength(1)

    unsub()
    service.dispatchEvent(testEvent)
    expect(received).toHaveLength(1)
  })

  it('extracts recognition records with cooldown deduplication', () => {
    const service = AIResultService.getInstance()
    const records: string[] = []

    service.subscribeRecords((rec) => {
      records.push(`${rec.personName}:${rec.similarity}`)
    })

    const event: AIResultEventInput = {
      camera_id: 'cam_test_1',
      task_type: 'person_structure',
      pts: 2000,
      cost_ms: 10,
      persons: [
        {
          track_id: 2,
          body_box: [0, 0, 1, 1],
          body_score: 1.0,
          faces: [
            {
              box: [0.2, 0.2, 0.3, 0.3],
              quality: 0.8,
              matched: true,
              person_id: 'p_02',
              person_name: '王五',
              similarity: 0.88,
            },
          ],
        },
      ],
    }

    service.dispatchEvent(event)
    expect(records).toEqual(['王五:0.88'])
    expect(service.getRecentRecords()).toHaveLength(1)

    // 立即再次派发同一人事件（在 3 秒冷却内）
    service.dispatchEvent(event)
    expect(records).toHaveLength(1)
    expect(service.getRecentRecords()).toHaveLength(1)

    // 清空记录
    service.clearRecords()
    expect(service.getRecentRecords()).toHaveLength(0)
  })
})
