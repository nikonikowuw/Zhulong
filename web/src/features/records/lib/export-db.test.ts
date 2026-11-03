import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { exportDb, type ExportTaskDraft } from './export-db'

describe('exportDb (IndexedDB Persistent Pipeline)', () => {
  beforeEach(async () => {
    await exportDb.clearAllDrafts()
  })

  afterEach(async () => {
    await exportDb.clearAllDrafts()
  })

  it('saves and retrieves task draft', async () => {
    const draft: ExportTaskDraft = {
      taskId: 'task-test-01',
      mode: 'selection',
      activeTab: 'recognitions',
      totalCount: 50,
      completedKeys: ['k1', 'k2'],
      options: { exportCsv: true, exportImages: true },
      status: 'in_progress',
      createdAt: Date.now() - 1000,
      updatedAt: Date.now() - 1000,
    }

    await exportDb.saveTaskDraft(draft)
    const retrieved = await exportDb.getTaskDraft('task-test-01')

    expect(retrieved).not.toBeNull()
    expect(retrieved?.taskId).toBe('task-test-01')
    expect(retrieved?.completedKeys).toEqual(['k1', 'k2'])
    expect(retrieved?.totalCount).toBe(50)
  })

  it('returns latest pending task draft', async () => {
    const draft1: ExportTaskDraft = {
      taskId: 'task-old',
      mode: 'batch',
      activeTab: 'alarms',
      totalCount: 10,
      completedKeys: ['k1'],
      options: {},
      status: 'in_progress',
      createdAt: 1000,
      updatedAt: 1000,
    }

    const draft2: ExportTaskDraft = {
      taskId: 'task-new',
      mode: 'selection',
      activeTab: 'recognitions',
      totalCount: 20,
      completedKeys: ['k1', 'k2'],
      options: {},
      status: 'in_progress',
      createdAt: 2000,
      updatedAt: 2000,
    }

    await exportDb.saveTaskDraft(draft1)
    await exportDb.saveTaskDraft(draft2)

    const latest = await exportDb.getLatestPendingTask()
    expect(latest?.taskId).toBe('task-new')
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

    // 清除后
    await exportDb.clearTaskDraft(taskId)
    const afterClear = await exportDb.getArtifact(taskId, 'img-1.jpg')
    expect(afterClear).toBeNull()
    const allAfter = await exportDb.getAllArtifacts(taskId)
    expect(allAfter).toHaveLength(0)
  })
})
