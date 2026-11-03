import { strFromU8, unzipSync } from 'fflate'
import { beforeEach, describe, expect, it } from 'vitest'
import { type Recognition } from '../data/schema'
import { exportDb } from './export-db'
import {
  DEFAULT_MAX_RECORDS,
  executeRecordsExport,
  ExportLimitError,
  type ExportProgress,
} from './export-zip-pipeline'

const TINY_PNG_DATA_URL =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=='

function makeRecognition(overrides: Partial<Recognition> = {}): Recognition {
  return {
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
    ...overrides,
  }
}

async function readZip(result: { blob: Blob }) {
  const entries = unzipSync(new Uint8Array(await result.blob.arrayBuffer()))
  const manifest = entries['manifest.json']
    ? JSON.parse(strFromU8(entries['manifest.json']))
    : null
  return { entries, manifest }
}

describe('export-zip-pipeline', () => {
  beforeEach(async () => {
    await exportDb.clearAllDrafts()
  })

  it('exports pure single-tab CSV directly without zip', async () => {
    const progressLog: ExportProgress[] = []
    const result = await executeRecordsExport({
      selectedRecords: {
        tab: 'recognitions',
        items: [makeRecognition()],
      },
      exportCsv: true,
      exportImages: false,
      onProgress: (p) => progressLog.push(p),
    })

    expect(result.filename.endsWith('.csv')).toBe(true)
    expect(result.blob.type).toContain('text/csv')
    expect(progressLog.some((p) => p.phase === 'completed')).toBe(true)
    expect(progressLog.every((p) => (p.percent ?? 0) <= 100)).toBe(true)
  })

  it('survives the dialog-shaped options (AbortSignal + callbacks)', async () => {
    // 回归：把整个 options 落盘会在 IDBObjectStore.put() 抛
    // DataCloneError（AbortSignal / 函数不可结构化克隆），图片导出必崩。
    const controller = new AbortController()
    const result = await executeRecordsExport({
      selectedRecords: {
        tab: 'recognitions',
        items: [makeRecognition()],
      },
      exportCsv: true,
      exportImages: true,
      burnOverlay: true,
      imageKind: 'scene',
      maxImages: 10,
      signal: controller.signal,
      resolveCameraName: () => '东门摄像机',
      onProgress: () => undefined,
    })

    expect(result.filename.endsWith('.zip')).toBe(true)
    expect(result.failedImages).toBe(0)
  })

  it('archives reports, timestamped images and a manifest', async () => {
    const result = await executeRecordsExport({
      selectedRecords: {
        tab: 'recognitions',
        items: [makeRecognition()],
      },
      exportCsv: true,
      exportImages: true,
      burnOverlay: false,
      imageKind: 'both',
      resolveCameraName: () => '东门摄像机',
    })

    const { entries, manifest } = await readZip(result)
    const keys = Object.keys(entries)

    expect(keys).toContain('recognitions_records.csv')
    expect(keys).toContain('manifest.json')
    // 图片名沿用 evidenceFileName：可读时间戳 + ULID，不是裸 ULID
    expect(
      keys.some((k) =>
        /^images\/recognitions\/\d{8}_\d{6}-rec-001_scene\.jpg$/.test(k)
      )
    ).toBe(true)
    expect(
      keys.some((k) =>
        /^images\/recognitions\/\d{8}_\d{6}-rec-001_closeup\.jpg$/.test(k)
      )
    ).toBe(true)

    expect(manifest.recordCount).toBe(1)
    expect(manifest.images).toMatchObject({
      jobs: 2,
      exported: 2,
      failed: 0,
      truncated: 0,
    })
    expect(manifest.reports).toEqual(['recognitions_records.csv'])

    const csv = strFromU8(entries['recognitions_records.csv'])
    expect(csv).toContain('东门摄像机')
  })

  it('records image failures in the manifest instead of dropping them silently', async () => {
    const result = await executeRecordsExport({
      selectedRecords: {
        tab: 'recognitions',
        items: [
          makeRecognition({
            id: 'rec-broken',
            imagePath: 'data:image/png;base64,!!!not-base64!!!',
          }),
        ],
      },
      exportCsv: true,
      exportImages: true,
      burnOverlay: false,
      imageKind: 'scene',
    })

    const { entries, manifest } = await readZip(result)
    expect(result.failedImages).toBe(1)
    expect(manifest.images.failed).toBe(1)
    expect(manifest.failures).toHaveLength(1)
    expect(manifest.failures[0].recordId).toBe('rec-broken')
    // 报表照常产出，缺的只是图片
    expect(entries['recognitions_records.csv']).toBeDefined()
  })

  it('stops immediately when AbortSignal is already aborted', async () => {
    const controller = new AbortController()
    controller.abort()

    await expect(
      executeRecordsExport({
        selectedRecords: {
          tab: 'recognitions',
          items: [makeRecognition()],
        },
        signal: controller.signal,
      })
    ).rejects.toThrow()
  })

  it('pauses the draft when cancelled mid-flight so it can be resumed', async () => {
    const controller = new AbortController()
    const items = Array.from({ length: 6 }, (_, index) =>
      makeRecognition({ id: `rec-cancel-${index}` })
    )

    const promise = executeRecordsExport({
      resumeTaskId: 'task-cancel',
      selectedRecords: { tab: 'recognitions', items },
      exportCsv: true,
      exportImages: true,
      burnOverlay: false,
      signal: controller.signal,
      onProgress: (p) => {
        if (p.phase === 'processing_images' && p.current >= 1) {
          controller.abort()
        }
      },
    })

    await expect(promise).rejects.toThrow()

    const draft = await exportDb.getTaskDraft('task-cancel')
    expect(draft?.status).toBe('paused')
    expect(draft?.options).toEqual({
      exportCsv: true,
      exportImages: true,
      burnOverlay: false,
      imageKind: 'scene',
      maxImages: undefined,
    })
  })

  it('resumes from the task snapshot instead of the current selection', async () => {
    // 第一次任务：只有 rec-original
    await executeRecordsExport({
      resumeTaskId: 'task-snapshot',
      selectedRecords: {
        tab: 'recognitions',
        items: [makeRecognition({ id: 'rec-original' })],
      },
      exportCsv: true,
      exportImages: true,
      burnOverlay: false,
      imageKind: 'scene',
    })

    // 续传时传入完全不同的数据集，必须被忽略
    const resumed = await executeRecordsExport({
      resumeTaskId: 'task-snapshot',
      selectedRecords: {
        tab: 'recognitions',
        items: [makeRecognition({ id: 'rec-intruder' })],
      },
      exportCsv: true,
      exportImages: true,
      burnOverlay: false,
      imageKind: 'scene',
    })

    const { entries, manifest } = await readZip(resumed)
    const keys = Object.keys(entries)

    expect(keys.some((k) => k.includes('rec-original'))).toBe(true)
    expect(keys.some((k) => k.includes('rec-intruder'))).toBe(false)
    expect(strFromU8(entries['recognitions_records.csv'])).toContain(
      'rec-original'
    )
    expect(manifest.recordCount).toBe(1)
    expect(manifest.images.exported).toBe(1)
  })

  it('rejects selections above the record budget', async () => {
    await expect(
      executeRecordsExport({
        selectedRecords: {
          tab: 'recognitions',
          items: [makeRecognition(), makeRecognition({ id: 'rec-002' })],
        },
        maxRecords: 1,
      })
    ).rejects.toBeInstanceOf(ExportLimitError)
  })

  it('defaults to a bounded record budget', () => {
    expect(DEFAULT_MAX_RECORDS).toBeGreaterThan(0)
    expect(DEFAULT_MAX_RECORDS).toBeLessThanOrEqual(50_000)
  })
})
