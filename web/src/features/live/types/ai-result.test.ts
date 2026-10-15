import { describe, it, expect } from 'vitest'
import { parseAIResultEvent, faceSubItemSchema } from './ai-result'

describe('ai-result schema and parser', () => {
  it('parses valid person_structure event with matched face', () => {
    const raw = {
      camera_id: 'cam_01',
      algorithm_id: 'algo_face_rec',
      pts: 123456,
      timestamp: '2026-10-14T10:00:00Z',
      task_type: 'person_structure',
      cost_ms: 18,
      persons: [
        {
          track_id: 1,
          body_box: [0.1, 0.2, 0.5, 0.8],
          body_score: 0.98,
          faces: [
            {
              box: [0.15, 0.22, 0.35, 0.45],
              quality: 0.92,
              landmarks: [
                [0.2, 0.28],
                [0.3, 0.28],
                [0.25, 0.35],
                [0.22, 0.41],
                [0.28, 0.41],
              ],
              matched: true,
              person_id: 'p_admin_001',
              person_name: '张三',
              similarity: 0.95,
              candidates: [
                {
                  person_id: 'p_admin_001',
                  person_name: '张三',
                  similarity: 0.95,
                },
              ],
            },
          ],
        },
      ],
    }

    const parsed = parseAIResultEvent(raw)
    expect(parsed).not.toBeNull()
    expect(parsed?.camera_id).toBe('cam_01')
    expect(parsed?.persons[0].faces[0].matched).toBe(true)
    expect(parsed?.persons[0].faces[0].person_name).toBe('张三')
    expect(parsed?.persons[0].faces[0].landmarks).toHaveLength(5)
  })

  it('parses JSON string representation safely', () => {
    const jsonStr = JSON.stringify({
      camera_id: 'cam_02',
      task_type: 'person_structure',
      pts: 555,
      cost_ms: 12,
      persons: [
        {
          body_box: [0, 0, 1, 1],
          body_score: 1.0,
          faces: [
            {
              box: [0.2, 0.2, 0.4, 0.4],
              matched: false,
              similarity: 0.45,
            },
          ],
        },
      ],
    })

    const parsed = parseAIResultEvent(jsonStr)
    expect(parsed).not.toBeNull()
    expect(parsed?.camera_id).toBe('cam_02')
    expect(parsed?.persons[0].faces[0].matched).toBe(false)
    expect(parsed?.persons[0].faces[0].person_name).toBe('')
  })

  it('returns null on invalid or malformed data', () => {
    expect(parseAIResultEvent(null)).toBeNull()
    expect(parseAIResultEvent(undefined)).toBeNull()
    expect(parseAIResultEvent('not-json')).toBeNull()
    expect(parseAIResultEvent({ missing_camera_id: true })).toBeNull()
  })

  it('populates defaults for optional fields', () => {
    const face = faceSubItemSchema.parse({
      box: [0.1, 0.1, 0.3, 0.3],
    })
    expect(face.quality).toBe(0)
    expect(face.matched).toBe(false)
    expect(face.similarity).toBe(0)
    expect(face.person_name).toBe('')
    expect(face.landmarks).toEqual([])
  })
})
