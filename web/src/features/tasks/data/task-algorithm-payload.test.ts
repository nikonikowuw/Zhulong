import { describe, expect, it } from 'vitest'
import { type Task } from './schema'
import { taskAlgorithmsPayload } from './task-algorithm-payload'

const CLIP_JSON =
  '{"enabled":true,"preSeconds":5,"postSeconds":10,"recognitionFilter":"all"}'

const task: Task = {
  id: 'task-1',
  cameraId: 'cam-1',
  name: '',
  enabled: true,
  crop: null,
  algorithms: [
    {
      id: 'sub-1',
      taskId: 'task-1',
      algorithmId: 'algo-1',
      enabled: true,
      sampleFps: 5,
      configJson: '{}',
      regionConfigJson: '{}',
      clipConfigJson: CLIP_JSON,
      scheduleJson: '{}',
      createdAt: '',
      updatedAt: '',
    },
    {
      // 旧后端没有 clipConfigJson 字段
      id: 'sub-2',
      taskId: 'task-1',
      algorithmId: 'algo-2',
      enabled: false,
      sampleFps: 3,
      configJson: '{}',
      regionConfigJson: '{}',
      scheduleJson: '{}',
      createdAt: '',
      updatedAt: '',
    },
  ],
  createdAt: '',
  updatedAt: '',
}

describe('taskAlgorithmsPayload', () => {
  it('carries clipConfigJson through the full-replace payload', () => {
    const payload = taskAlgorithmsPayload(task, { id: 'sub-2', enabled: true })
    expect(payload).toEqual([
      expect.objectContaining({
        id: 'sub-1',
        enabled: true,
        clipConfigJson: CLIP_JSON,
      }),
      expect.objectContaining({
        id: 'sub-2',
        enabled: true,
        clipConfigJson: '{}',
      }),
    ])
    // 序列化到线上后字段仍在（不是 undefined 被 JSON.stringify 丢掉）。
    const wire = JSON.parse(JSON.stringify(payload)) as Array<
      Record<string, unknown>
    >
    expect(wire[0].clipConfigJson).toBe(CLIP_JSON)
    expect(wire[1].clipConfigJson).toBe('{}')
  })
})
