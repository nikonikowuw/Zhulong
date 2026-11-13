import { describe, expect, it } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import { type Algorithm } from '@/features/algorithms/contract'
import { parseRegionConfig } from '../data/region-config'
import { type Task } from '../data/schema'
import { useChannelTaskDrafts } from './use-channel-task-drafts'

const STORED_REGION = '{"roi":[[0.1,0.1,0.5,0.5]],"mask":[]}'

function buildAlgorithm(id: string, name: string): Algorithm {
  return {
    id,
    name,
    version: '1.0.0',
    taskType: 'detection',
    category: 'security',
    targetArch: 'aarch64',
    targetDevice: 'npu',
    computeCost: 10,
    memoryMb: 64,
    description: '',
    manifestJson: '{}',
    configSchemaJson: '{}',
    status: 'ACTIVE',
    previousVersion: '',
    createdAt: '',
    updatedAt: '',
  } as Algorithm
}

const algorithms = [
  buildAlgorithm('algo-face', '人脸识别'),
  buildAlgorithm('algo-smoke', '烟雾检测'),
]

const tasks: Task[] = [
  {
    id: 'task-01',
    cameraId: 'cam-01',
    name: '',
    enabled: true,
    crop: null,
    algorithms: [
      {
        id: 'sub-01',
        taskId: 'task-01',
        algorithmId: 'algo-face',
        enabled: true,
        sampleFps: 10,
        configJson: '{}',
        regionConfigJson: STORED_REGION,
        scheduleJson: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ],
    createdAt: '',
    updatedAt: '',
  },
]

function renderDrafts(initialCameraId = 'cam-01') {
  return renderHook(() =>
    useChannelTaskDrafts({ initialCameraId, algorithms, allTasks: tasks })
  )
}

describe('useChannelTaskDrafts', () => {
  it('keeps a newly checked algorithm selected', async () => {
    const { result, act } = await renderDrafts()

    expect(result.current.drafts['algo-smoke']?.selected).toBe(false)

    act(() => result.current.toggleAlgo('algo-smoke', true))

    // 回归：曾经在同一批里用陈旧快照整体覆盖 drafts，把这次勾选静默回滚。
    expect(result.current.drafts['algo-smoke']?.selected).toBe(true)
    expect(result.current.activeAlgoId).toBe('algo-smoke')
  })

  it('keeps the active algorithm unchecked and hands over the active slot', async () => {
    const { result, act } = await renderDrafts()

    act(() => result.current.toggleAlgo('algo-smoke', true))
    act(() => result.current.toggleAlgo('algo-face', false))

    expect(result.current.drafts['algo-face']?.selected).toBe(false)
    expect(result.current.activeAlgoId).toBe('algo-smoke')
  })

  it('hands the region draft over when switching algorithms', async () => {
    const { result, act } = await renderDrafts()

    expect(result.current.activeAlgoId).toBe('algo-face')
    expect(result.current.regionEditor.draft.roi).toHaveLength(1)

    act(() => result.current.toggleAlgo('algo-smoke', true))
    expect(result.current.regionEditor.draft.roi).toHaveLength(0)

    act(() => result.current.selectActiveAlgo('algo-face'))
    expect(result.current.activeAlgoId).toBe('algo-face')
    expect(result.current.regionEditor.draft.roi).toHaveLength(1)
  })

  it('preserves tripwire IDs and directions across algorithm switches and payload generation', async () => {
    const { result, act } = await renderDrafts()
    act(() =>
      result.current.regionEditor.changeDraft({
        ...result.current.regionEditor.draft,
        tripwires: [
          {
            id: 'entry',
            name: '入口',
            points: [
              [0.2, 0.5],
              [0.8, 0.5],
            ],
            direction: 'a_to_b',
          },
        ],
      })
    )
    act(() => result.current.toggleAlgo('algo-smoke', true))
    expect(result.current.regionEditor.draft.tripwires ?? []).toEqual([])
    act(() => result.current.selectActiveAlgo('algo-face'))
    expect(result.current.regionEditor.draft.tripwires?.[0].id).toBe('entry')
    const payload = result.current.toPayloadDrafts({ regionEditable: true })
    const face = payload.find((item) => item.algorithmId === 'algo-face')!
    expect(
      parseRegionConfig(face.regionConfigJson).tripwires?.[0].direction
    ).toBe('a_to_b')
  })

  it('keeps unsaved region and crop edits when switching camera away and back', async () => {
    const { result, act } = await renderDrafts()

    act(() => {
      result.current.regionEditor.createRect('roi', [0.2, 0.2, 0.6, 0.6])
      result.current.regionEditor.setCrop([0.1, 0.1, 0.7, 0.7])
    })
    expect(result.current.regionEditor.draft.roi).toHaveLength(2)
    expect(result.current.regionEditor.crop).toEqual([0.1, 0.1, 0.7, 0.7])

    act(() => result.current.changeCamera('cam-02'))
    expect(result.current.cameraId).toBe('cam-02')
    expect(result.current.regionEditor.draft.roi).toHaveLength(0)
    expect(result.current.regionEditor.crop).toBeNull()

    act(() => result.current.changeCamera('cam-01'))
    expect(result.current.regionEditor.draft.roi).toHaveLength(2)
    expect(result.current.regionEditor.crop).toEqual([0.1, 0.1, 0.7, 0.7])
  })

  it('never submits the editing buffer when the region is not editable', async () => {
    const { result, act } = await renderDrafts()

    act(() =>
      result.current.regionEditor.createRect('roi', [0.2, 0.2, 0.6, 0.6])
    )

    const locked = result.current.toPayloadDrafts({ regionEditable: false })
    expect(locked).toHaveLength(1)
    expect(locked[0].regionConfigJson).toBe(STORED_REGION)

    const editable = result.current.toPayloadDrafts({ regionEditable: true })
    expect(parseRegionConfig(editable[0].regionConfigJson).roi).toHaveLength(2)
  })

  it('initializes activeAlgoId with initialAlgorithmId if provided', async () => {
    const { result } = await renderHook(() =>
      useChannelTaskDrafts({
        initialCameraId: 'cam-01',
        initialAlgorithmId: 'algo-smoke',
        algorithms,
        allTasks: tasks,
      })
    )

    expect(result.current.activeAlgoId).toBe('algo-smoke')
  })

  it('only submits selected algorithms', async () => {
    const { result, act } = await renderDrafts()

    act(() => result.current.toggleAlgo('algo-smoke', true))
    act(() => result.current.toggleAlgo('algo-smoke', false))

    const payload = result.current.toPayloadDrafts({ regionEditable: true })
    expect(payload.map((draft) => draft.algorithmId)).toEqual(['algo-face'])
  })
})
