import { useCallback, useMemo, useState } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import {
  type RecordItem,
  type RecordsSearchParams,
  type RecordsTab,
} from '../data/schema'
import {
  type RecordsDateRange,
  resolveRecordsWindow,
} from '../data/time-presets'
import { useAlarms } from './use-alarms'
import { useCameraNames } from './use-camera-names'
import { useCaptures } from './use-captures'
import { useRecognitions } from './use-recognitions'
import { useRecordsSelection } from './use-records-selection'

const route = getRouteApi('/_authenticated/records/')

export function useRecordsPage() {
  const search = route.useSearch()
  const navigate = route.useNavigate()

  const tab = search.tab ?? 'recognitions'
  const view = search.view ?? 'table'
  const page = search.page ?? 1
  const pageSize = search.pageSize ?? 20
  const cameraId = search.cameraId
  const preset = search.preset
  const startTime = search.startTime
  const endTime = search.endTime

  // 以显式刷新的时间戳作为相对预设锚点：既保证 queryKey 不在无关渲染中抖动，
  // 又确保用户点击刷新、切换 Tab 或导出时能够推进到最新时间窗口
  const [windowAnchor, setWindowAnchor] = useState<number>(() => Date.now())

  const activeRange = useMemo(
    () =>
      resolveRecordsWindow(
        { preset, startTime, endTime },
        new Date(windowAnchor)
      ),
    [preset, startTime, endTime, windowAnchor]
  )

  const matched = search.matched
  const personId = search.personId
  const alarmType = search.alarmType
  const targetType = search.targetType

  const [detailOpen, setDetailOpen] = useState(false)
  const [selectedRecord, setSelectedRecord] = useState<RecordItem | null>(null)
  const [selectedTab, setSelectedTab] = useState<RecordsTab>('recognitions')

  const [exportDialogOpen, setExportDialogOpen] = useState(false)
  const [exportDialogMounted, setExportDialogMounted] = useState(false)
  const [exportDialogMode, setExportDialogMode] = useState<
    'selection' | 'batch'
  >('batch')

  const cameraName = useCameraNames()

  const updateSearch = useCallback(
    (updater: Partial<RecordsSearchParams>) => {
      void navigate({
        search: (prev) => {
          const next = { ...prev, ...updater }
          for (const [key, val] of Object.entries(next)) {
            if (val === undefined || val === '') {
              delete (next as Record<string, unknown>)[key]
            }
          }
          return next
        },
      })
    },
    [navigate]
  )

  const recognitionsQuery = useRecognitions(
    tab === 'recognitions'
      ? {
          page,
          pageSize,
          cameraId,
          personId,
          matched:
            matched === 'true' ? true : matched === 'false' ? false : undefined,
          startTime: activeRange.startTime,
          endTime: activeRange.endTime,
        }
      : undefined
  )

  const alarmsQuery = useAlarms(
    tab === 'alarms'
      ? {
          page,
          pageSize,
          cameraId,
          alarmType,
          startTime: activeRange.startTime,
          endTime: activeRange.endTime,
        }
      : undefined
  )

  const capturesQuery = useCaptures(
    tab === 'captures' || tab === 'vehicles'
      ? {
          page,
          pageSize,
          cameraId,
          targetType: tab === 'vehicles' ? 'vehicle' : targetType,
          startTime: activeRange.startTime,
          endTime: activeRange.endTime,
        }
      : undefined
  )

  const handleTabChange = useCallback(
    (nextTab: RecordsTab) => {
      setWindowAnchor(Date.now())
      updateSearch({
        tab: nextTab,
        page: 1,
        matched: undefined,
        personId: undefined,
        alarmType: undefined,
        targetType: undefined,
      })
    },
    [updateSearch]
  )

  const handleRefresh = useCallback(() => {
    const now = Date.now()
    const nextRange = resolveRecordsWindow(
      { preset, startTime, endTime },
      new Date(now)
    )
    setWindowAnchor(now)
    // 若时间窗口未发生位移（如今日全天同一天内或自定义绝对区间），主动触发 refetch
    if (
      nextRange.startTime === activeRange.startTime &&
      nextRange.endTime === activeRange.endTime
    ) {
      if (tab === 'recognitions') void recognitionsQuery.refetch()
      else if (tab === 'alarms') void alarmsQuery.refetch()
      else void capturesQuery.refetch()
    }
  }, [
    preset,
    startTime,
    endTime,
    activeRange.startTime,
    activeRange.endTime,
    tab,
    recognitionsQuery,
    alarmsQuery,
    capturesQuery,
  ])

  const handleRetry = useCallback(() => {
    if (tab === 'recognitions') void recognitionsQuery.refetch()
    else if (tab === 'alarms') void alarmsQuery.refetch()
    else void capturesQuery.refetch()
  }, [tab, recognitionsQuery, alarmsQuery, capturesQuery])

  const handleOpenDetail = useCallback(
    (item: RecordItem) => {
      setSelectedRecord(item)
      setSelectedTab(tab)
      setDetailOpen(true)
    },
    [tab]
  )

  const handleOpenBatchExport = useCallback(() => {
    setWindowAnchor(Date.now())
    setExportDialogMode('batch')
    setExportDialogMounted(true)
    setExportDialogOpen(true)
  }, [])

  const handleOpenSelectionExport = useCallback(() => {
    setExportDialogMode('selection')
    setExportDialogMounted(true)
    setExportDialogOpen(true)
  }, [])

  const handleViewChange = useCallback(
    (nextView: 'grid' | 'table') => updateSearch({ view: nextView }),
    [updateSearch]
  )

  const handleCameraChange = useCallback(
    (nextCam?: string) => updateSearch({ cameraId: nextCam, page: 1 }),
    [updateSearch]
  )

  const handleMatchedChange = useCallback(
    (nextMatched?: 'all' | 'true' | 'false') =>
      updateSearch({ matched: nextMatched, page: 1 }),
    [updateSearch]
  )

  const handlePersonIdChange = useCallback(
    (nextPid?: string) => updateSearch({ personId: nextPid, page: 1 }),
    [updateSearch]
  )

  const handleAlarmTypeChange = useCallback(
    (nextAt?: string) => updateSearch({ alarmType: nextAt, page: 1 }),
    [updateSearch]
  )

  const handleTargetTypeChange = useCallback(
    (nextTt?: string) => updateSearch({ targetType: nextTt, page: 1 }),
    [updateSearch]
  )

  const handlePageChange = useCallback(
    (nextPage: number) => updateSearch({ page: nextPage }),
    [updateSearch]
  )

  const handlePageSizeChange = useCallback(
    (nextPageSize: number) => updateSearch({ pageSize: nextPageSize, page: 1 }),
    [updateSearch]
  )

  const handleDateRangeChange = useCallback(
    (range: RecordsDateRange) => {
      setWindowAnchor(Date.now())
      updateSearch({
        preset: range.preset,
        startTime: range.startTime,
        endTime: range.endTime,
        page: 1,
      })
    },
    [updateSearch]
  )

  const handleResetFilters = useCallback(() => {
    setWindowAnchor(Date.now())
    updateSearch({
      cameraId: undefined,
      preset: undefined,
      startTime: undefined,
      endTime: undefined,
      matched: undefined,
      personId: undefined,
      alarmType: undefined,
      targetType: undefined,
      page: 1,
    })
  }, [updateSearch])

  const isFetching =
    recognitionsQuery.isFetching ||
    alarmsQuery.isFetching ||
    capturesQuery.isFetching

  const currentItems = useMemo<RecordItem[]>(() => {
    if (tab === 'recognitions') return recognitionsQuery.data?.items ?? []
    if (tab === 'alarms') return alarmsQuery.data?.items ?? []
    return capturesQuery.data?.items ?? []
  }, [
    tab,
    recognitionsQuery.data?.items,
    alarmsQuery.data?.items,
    capturesQuery.data?.items,
  ])

  const currentTotal = useMemo<number>(() => {
    if (tab === 'recognitions') return recognitionsQuery.data?.total ?? 0
    if (tab === 'alarms') return alarmsQuery.data?.total ?? 0
    return capturesQuery.data?.total ?? 0
  }, [
    tab,
    recognitionsQuery.data?.total,
    alarmsQuery.data?.total,
    capturesQuery.data?.total,
  ])

  const exportQueryParams = useMemo(
    () => ({
      cameraId,
      startTime: activeRange.startTime,
      endTime: activeRange.endTime,
      matched:
        matched === 'true' ? true : matched === 'false' ? false : undefined,
      personId,
      alarmType,
      targetType,
    }),
    [
      cameraId,
      activeRange.startTime,
      activeRange.endTime,
      matched,
      personId,
      alarmType,
      targetType,
    ]
  )

  const selection = useRecordsSelection(tab, currentItems)

  return {
    tab,
    view,
    page,
    pageSize,
    cameraId,
    activeRange,
    matched,
    personId,
    alarmType,
    targetType,
    cameraName,
    isFetching,
    currentTotal,
    selection,
    recognitionsQuery,
    alarmsQuery,
    capturesQuery,
    detailOpen,
    setDetailOpen,
    selectedRecord,
    selectedTab,
    exportDialogOpen,
    setExportDialogOpen,
    exportDialogMounted,
    exportDialogMode,
    exportQueryParams,
    handleTabChange,
    handleRefresh,
    handleRetry,
    handleOpenDetail,
    handleOpenBatchExport,
    handleOpenSelectionExport,
    handleViewChange,
    handleCameraChange,
    handleMatchedChange,
    handlePersonIdChange,
    handleAlarmTypeChange,
    handleTargetTypeChange,
    handlePageChange,
    handlePageSizeChange,
    handleDateRangeChange,
    handleResetFilters,
  }
}
