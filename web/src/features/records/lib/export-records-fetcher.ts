import { recordsApi } from '../api/records-api'
import { type RecordItem, type RecordsTab } from '../data/schema'
import {
  ExportLimitError,
  type ExportLabels,
  type ExportPhase,
  type ExportQueryConfig,
} from './export-types'

const RECORD_PAGE_SIZE = 100

export type EmitExportProgress = (
  phase: ExportPhase,
  current: number,
  total: number,
  message: string | undefined,
  ratio: number
) => void

function assertNotAborted(signal?: AbortSignal): void {
  if (signal?.aborted) {
    throw new DOMException('Export cancelled', 'AbortError')
  }
}

export async function runConcurrentPool<T>(
  items: T[],
  limit: number,
  signal: AbortSignal | undefined,
  fn: (item: T) => Promise<void>
): Promise<void> {
  let currentIndex = 0

  const workers = Array.from(
    { length: Math.min(limit, items.length) },
    async () => {
      while (currentIndex < items.length) {
        assertNotAborted(signal)
        const index = currentIndex++
        await fn(items[index])
      }
    }
  )

  await Promise.all(workers)
}

interface RecordPage {
  items: RecordItem[]
  total: number
}

async function requestRecordPage(
  tab: RecordsTab,
  query: ExportQueryConfig,
  page: number,
  pageSize: number,
  endTime: string,
  signal?: AbortSignal
): Promise<RecordPage> {
  if (tab === 'recognitions') {
    const resp = await recordsApi.getRecognitions(
      {
        page,
        pageSize,
        cameraId: query.cameraId,
        personId: query.personId,
        matched: query.matched,
        startTime: query.startTime,
        endTime,
      },
      { signal }
    )
    return { items: resp.items, total: resp.total }
  }
  if (tab === 'alarms') {
    const resp = await recordsApi.getAlarms(
      {
        page,
        pageSize,
        cameraId: query.cameraId,
        alarmType: query.alarmType,
        startTime: query.startTime,
        endTime,
      },
      { signal }
    )
    return { items: resp.items, total: resp.total }
  }
  const resp = await recordsApi.getCaptures(
    {
      page,
      pageSize,
      cameraId: query.cameraId,
      targetType: tab === 'vehicles' ? 'vehicle' : query.targetType,
      startTime: query.startTime,
      endTime,
    },
    { signal }
  )
  return { items: resp.items, total: resp.total }
}

export async function probeRecordTotals(
  query: ExportQueryConfig,
  tabs: RecordsTab[],
  endTime: string,
  signal: AbortSignal | undefined
): Promise<number> {
  assertNotAborted(signal)
  const totals = await Promise.all(
    tabs.map((tab) =>
      requestRecordPage(tab, query, 1, 1, endTime, signal).then((p) => p.total)
    )
  )
  return totals.reduce((sum, n) => sum + n, 0)
}

export async function fetchRecordsByQuery(
  query: ExportQueryConfig,
  tabs: RecordsTab[],
  endTime: string,
  expectedTotal: number,
  maxRecords: number,
  signal: AbortSignal | undefined,
  emit: EmitExportProgress,
  labels?: ExportLabels
): Promise<Map<RecordsTab, RecordItem[]>> {
  const dataset = new Map<RecordsTab, RecordItem[]>()
  const denom = Math.max(expectedTotal, 1)
  let fetched = 0

  await Promise.all(
    tabs.map(async (tab) => {
      const items: RecordItem[] = []
      let page = 1
      while (true) {
        assertNotAborted(signal)
        const resp = await requestRecordPage(
          tab,
          query,
          page,
          RECORD_PAGE_SIZE,
          endTime,
          signal
        )
        items.push(...resp.items)
        fetched += resp.items.length

        if (fetched > maxRecords) {
          throw new ExportLimitError('records', maxRecords, fetched)
        }

        const totalEst = Math.max(expectedTotal, fetched)
        const message =
          labels?.progress?.fetching?.(tab, fetched, totalEst) ??
          `Fetching ${tab}... (${fetched}/${totalEst})`
        emit('fetching_records', fetched, totalEst, message, fetched / denom)

        if (items.length >= resp.total || resp.items.length === 0) break
        page++
      }
      dataset.set(tab, items)
    })
  )

  return dataset
}
