import { describe, expect, it } from 'vitest'
import { type Recognition } from '../data/schema'
import { exportDb } from './export-db'
import {
  executeRecordsExport,
  type ExportProgress,
} from './export-zip-pipeline'

const TINY_PNG_DATA_URL =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='

describe('export-zip-pipeline', () => {
  const mockRecognition: Recognition = {
    id: 'rec-001',
    cameraId: 'cam-01',
    algorithmId: 'algo-1',
    pts: 100,
    trackId: 1,
    personId: 'p1',
    personExternalId: 'ext-p1',
    personName: '张三',
    matchedFaceId: 'f1',
    matched: true,
    similarity: 0.95,
    candidates: [],
    faceImagePath: TINY_PNG_DATA_URL,
    galleryImagePath: '',
    imagePath: TINY_PNG_DATA_URL,
    box: [0.1, 0.1, 0.5, 0.5],
    bodyBox: [0.05, 0.05, 0.6, 0.9],
    attributes: {},
    capturedAt: '2026-11-03T08:00:00.000Z',
    createdAt: '2026-11-03T08:00:01.000Z',
  }

  it('exports pure single-tab CSV directly without zip', async () => {
    const progressLog: ExportProgress[] = []
    const result = await executeRecordsExport({
      selectedRecords: {
        tab: 'recognitions',
        items: [mockRecognition],
      },
      exportCsv: true,
      exportImages: false,
      onProgress: (p) => progressLog.push(p),
    })

    expect(result.isZip).toBe(false)
    expect(result.filename.endsWith('.csv')).toBe(true)
    expect(result.blob.type).toContain('text/csv')
    expect(progressLog.some((p) => p.phase === 'completed')).toBe(true)
  })

  it('exports ZIP when images are included', async () => {
    const result = await executeRecordsExport({
      selectedRecords: {
        tab: 'recognitions',
        items: [mockRecognition],
      },
      exportCsv: true,
      exportImages: true,
      burnOverlay: true,
      imageKind: 'scene',
      maxImages: 10,
    })

    expect(result.isZip).toBe(true)
    expect(result.filename.endsWith('.zip')).toBe(true)
    expect(result.blob.size).toBeGreaterThan(0)
  })

  it('stops immediately when AbortSignal is aborted', async () => {
    const controller = new AbortController()
    controller.abort()

    await expect(
      executeRecordsExport({
        selectedRecords: {
          tab: 'recognitions',
          items: [mockRecognition],
        },
        signal: controller.signal,
      })
    ).rejects.toThrow()
  })

  it('resumes export using pre-cached artifacts from IndexedDB', async () => {
    const resumeTaskId = 'test-resume-task'
    const filePath = 'images/recognitions/rec-001_scene.jpg'
    const preCachedBytes = new Uint8Array([10, 20, 30, 40])

    // 预注入缓存
    await exportDb.saveArtifact(resumeTaskId, filePath, preCachedBytes)

    const result = await executeRecordsExport({
      resumeTaskId,
      selectedRecords: {
        tab: 'recognitions',
        items: [mockRecognition],
      },
      exportCsv: false,
      exportImages: true,
      burnOverlay: false,
    })

    expect(result.isZip).toBe(true)
    expect(result.blob.size).toBeGreaterThan(0)
  })
})
