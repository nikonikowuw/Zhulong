import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { AIResultService } from '../services/ai-result-service'
import { type AIResultEventInput } from '../types/ai-result'
import { useRecognitionRecords } from './use-recognition-records'

describe('useRecognitionRecords', () => {
  beforeEach(() => {
    AIResultService.getInstance().resetForTesting()
  })

  it('filters records by matched status and search query', async () => {
    const { result, act } = await renderHook(() => useRecognitionRecords())

    const event1: AIResultEventInput = {
      camera_id: 'cam-01',
      task_type: 'person_structure',
      pts: 1,
      cost_ms: 10,
      persons: [
        {
          body_box: [0, 0, 1, 1],
          body_score: 1,
          faces: [
            {
              box: [0.1, 0.1, 0.3, 0.3],
              matched: true,
              person_id: 'p_01',
              person_name: '张三',
              similarity: 0.95,
            },
          ],
        },
      ],
    }

    const event2: AIResultEventInput = {
      camera_id: 'cam-02',
      task_type: 'person_structure',
      pts: 2,
      cost_ms: 10,
      persons: [
        {
          body_box: [0, 0, 1, 1],
          body_score: 1,
          faces: [
            {
              box: [0.2, 0.2, 0.4, 0.4],
              matched: false,
              person_id: '',
              person_name: '',
              similarity: 0.4,
            },
          ],
        },
      ],
    }

    await act(() => {
      AIResultService.getInstance().dispatchEvent(event1)
      AIResultService.getInstance().dispatchEvent(event2)
    })

    expect(result.current.records).toHaveLength(2)

    // 过滤仅已识别
    await act(() => {
      result.current.setStatusFilter('matched')
    })
    expect(result.current.records).toHaveLength(1)
    expect(result.current.records[0].personName).toBe('张三')

    // 过滤未识别
    await act(() => {
      result.current.setStatusFilter('unmatched')
    })
    expect(result.current.records).toHaveLength(1)
    expect(result.current.records[0].matched).toBe(false)

    // 恢复全量并搜索
    await act(() => {
      result.current.setStatusFilter('all')
      result.current.setSearchQuery('张三')
    })
    expect(result.current.records).toHaveLength(1)
    expect(result.current.records[0].personName).toBe('张三')

    // 清空记录
    await act(() => {
      result.current.clearRecords()
    })
    expect(result.current.records).toHaveLength(0)
  })
})
