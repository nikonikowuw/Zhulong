import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { exportDb, type ExportTaskDraft } from './export-db'

function makeDraft(overrides: Partial<ExportTaskDraft> = {}): ExportTaskDraft {
  return {
    taskId: 'task-test-01',
    mode: 'selection',
    activeTab: 'recognitions',
    tabs: ['recognitions'],
    recordCount: 50,
    totalCount: 50,
    completedKeys: ['k1', 'k2'],
    options: {
      exportCsv: true,
      exportImages: true,
      burnOverlay: true,
      imageKind: 'scene',
    },
    status: 'in_progress',
    createdAt: Date.now() - 1000,
    updatedAt: Date.now() - 1000,
    ...overrides,
  }
}

describe('exportDb (IndexedDB Persistent Pipeline)', () => {
  beforeEach(async () => {
    await exportDb.clearAllDrafts()
  })

  afterEach(async () => {
    await exportDb.clearAllDrafts()
  })

  it('saves and retrieves task draft', async () => {
    await exportDb.saveTaskDraft(makeDraft())
    const retrieved = await exportDb.getTaskDraft('task-test-01')

    expect(retrieved).not.toBeNull()
    expect(retrieved?.taskId).toBe('task-test-01')
    expect(retrieved?.completedKeys).toEqual(['k1', 'k2'])
    expect(retrieved?.totalCount).toBe(50)
    expect(retrieved?.tabs).toEqual(['recognitions'])
    expect(retrieved?.options.exportImages).toBe(true)
  })

  it('returns latest pending task draft', async () => {
    await exportDb.saveTaskDraft(
      makeDraft({ taskId: 'task-old', status: 'paused' })
    )
    await exportDb.saveTaskDraft(makeDraft({ taskId: 'task-new' }))
    await exportDb.saveTaskDraft(
      makeDraft({ taskId: 'task-done', status: 'completed' })
    )

    const latest = await exportDb.getLatestPendingTask()
    expect(latest?.taskId).toBe('task-new')
  })

  it('refuses non-cloneable options instead of corrupting the draft silently', async () => {
    // 类型层面已经禁止，这里守的是运行时：函数/AbortSignal 进 IDB 会抛
    // DataCloneError，早前版本正是这样让图片导出在 UI 里 100% 失败。
    const poisoned = {
      ...makeDraft({ taskId: 'task-poisoned' }),
      options: {
        exportCsv: true,
        exportImages: true,
        burnOverlay: true,
        imageKind: 'scene' as const,
        onProgress: () => undefined,
      },
    }

    await expect(
      exportDb.saveTaskDraft(poisoned as unknown as ExportTaskDraft)
    ).rejects.toThrow()
    expect(await exportDb.getTaskDraft('task-poisoned')).toBeNull()
  })

  it('saves and retrieves binary artifacts, and cleans up completely', async () => {
    const taskId = 'task-artifact-01'
    const binaryData = new Uint8Array([1, 2, 3, 4, 5])

    await exportDb.saveArtifact(taskId, 'img-1.jpg', binaryData)
    const saved = await exportDb.getArtifact(taskId, 'img-1.jpg')
    expect(saved).toEqual(binaryData)

    const all = await exportDb.getAllArtifacts(taskId)
    expect(all).toHaveLength(1)
    expect(all[0].artifactKey).toBe('img-1.jpg')
    expect(all[0].data).toEqual(binaryData)

    await exportDb.clearTaskDraft(taskId)
    const afterClear = await exportDb.getArtifact(taskId, 'img-1.jpg')
    expect(afterClear).toBeNull()
    expect(await exportDb.getAllArtifacts(taskId)).toHaveLength(0)
  })

  it('prunes expired drafts together with their artifacts', async () => {
    await exportDb.saveTaskDraft(makeDraft({ taskId: 'task-stale' }))
    await exportDb.saveArtifact(
      'task-stale',
      'img-1.jpg',
      new Uint8Array([9, 9])
    )

    const pruned = await exportDb.pruneExpiredTasks(0)

    expect(pruned).toBe(1)
    expect(await exportDb.getTaskDraft('task-stale')).toBeNull()
    expect(await exportDb.getAllArtifacts('task-stale')).toHaveLength(0)
  })

  it('keeps fresh drafts when pruning', async () => {
    await exportDb.saveTaskDraft(makeDraft({ taskId: 'task-fresh' }))

    expect(await exportDb.pruneExpiredTasks()).toBe(0)
    expect(await exportDb.getTaskDraft('task-fresh')).not.toBeNull()
  })
})
