import { describe, it, expect } from 'vitest'
import { algorithmSchema, algorithmListResponseSchema } from './schema'

describe('algorithm schema', () => {
  it('parses valid algorithm object correctly', () => {
    const raw = {
      id: 'face_recognition',
      name: '人脸识别',
      version: '1.0.0',
      taskType: 'person_structure',
      category: 'security',
      targetArch: 'x86_64',
      targetDevice: 'cpu',
      computeCost: 20,
      memoryMb: 128,
      description: '人脸检测与识别',
      manifestJson: '{"id":"face_recognition"}',
      configSchemaJson: '{}',
      status: 'ACTIVE',
      previousVersion: '',
      createdAt: '2026-10-15T00:00:00Z',
      updatedAt: '2026-10-15T00:00:00Z',
    }

    const parsed = algorithmSchema.parse(raw)
    expect(parsed.id).toBe('face_recognition')
    expect(parsed.taskType).toBe('person_structure')
    expect(parsed.status).toBe('ACTIVE')
  })

  it('populates defaults for optional fields', () => {
    const raw = {
      id: 'helmet_detector',
      name: '安全帽检测',
      version: '0.1.0',
      taskType: 'detection',
      category: 'safety',
      targetArch: 'aarch64',
      targetDevice: 'rknn',
      createdAt: '2026-10-15T00:00:00Z',
      updatedAt: '2026-10-15T00:00:00Z',
    }

    const parsed = algorithmSchema.parse(raw)
    expect(parsed.computeCost).toBe(10)
    expect(parsed.memoryMb).toBe(64)
    expect(parsed.status).toBe('DISABLED')
    expect(parsed.description).toBe('')
  })

  it('parses array of algorithms', () => {
    const list = [
      {
        id: 'algo_1',
        name: '算法 1',
        version: '1.0',
        taskType: 'detection',
        category: 'general',
        targetArch: 'x86_64',
        targetDevice: 'cpu',
        createdAt: '2026-10-15T00:00:00Z',
        updatedAt: '2026-10-15T00:00:00Z',
      },
    ]

    const parsed = algorithmListResponseSchema.parse(list)
    expect(parsed).toHaveLength(1)
    expect(parsed[0].id).toBe('algo_1')
  })
})
