import { useMemo, useState } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { CarFront, AlertTriangle, Camera, UserCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { RecordDetailDialog } from './components/detail/record-detail-dialog'
import { RecordsExportDialog } from './components/export/records-export-dialog'
import { RecordsSelectionBar } from './components/export/records-selection-bar'
import { RecordsPrimaryButtons } from './components/records-primary-buttons'
import { RecordsToolbar } from './components/records-toolbar'
import { AlarmsView } from './components/views/alarms-view'
import { CapturesView } from './components/views/captures-view'
import { RecognitionsView } from './components/views/recognitions-view'
import {
  type RecordItem,
  type RecordsSearchParams,
  type RecordsTab,
} from './data/schema'
import {
  type RecordsDateRange,
  resolveRecordsWindow,
} from './data/time-presets'
import { useAlarms } from './hooks/use-alarms'
import { useCameraNames } from './hooks/use-camera-names'
import { useCaptures } from './hooks/use-captures'
import { useRecognitions } from './hooks/use-recognitions'
import { useRecordsSelection } from './hooks/use-records-selection'

const route = getRouteApi('/_authenticated/records/')

export function Records() {
  const { t } = useTranslation('records')
  const search = route.useSearch()
  const navigate = route.useNavigate()

  // 状态机解析 (从强类型 URL 状态派生，默认模式为 table)
  const tab = search.tab ?? 'recognitions'
  const view = search.view ?? 'table'
  const page = search.page ?? 1
  const pageSize = search.pageSize ?? 20
  const cameraId = search.cameraId
  const preset = search.preset
  const startTime = search.startTime
  const endTime = search.endTime

  // 生效中的时间窗口：URL 只记意图（预设）或自定义绝对时间，缺省时落到「今日全天」。
  // memo 必须存在：相对预设内部取 now()，每次渲染重算会让 react-query 的 key 抖动。
  const activeRange = useMemo(
    () => resolveRecordsWindow({ preset, startTime, endTime }),
    [preset, startTime, endTime]
  )

  // 细项过滤
  const matched = search.matched
  const personId = search.personId
  const alarmType = search.alarmType
  const targetType = search.targetType

  // 详情弹窗状态
  const [detailOpen, setDetailOpen] = useState(false)
  const [selectedRecord, setSelectedRecord] = useState<RecordItem | null>(null)

  // 导出弹窗状态
  const [exportDialogOpen, setExportDialogOpen] = useState(false)
  const [exportDialogMode, setExportDialogMode] = useState<
    'selection' | 'batch'
  >('batch')

  // 记录行只存 cameraId，展示时必须解析成摄像机名称
  const cameraName = useCameraNames()

  // 状态驱动的路由跳转辅助
  const updateSearch = (updater: Partial<RecordsSearchParams>) => {
    void navigate({
      search: (prev) => {
        const next = { ...prev, ...updater }
        // 清理 undefined 属性保持 URL 整洁
        for (const [key, val] of Object.entries(next)) {
          if (val === undefined || val === '') {
            delete (next as Record<string, unknown>)[key]
          }
        }
        return next
      },
    })
  }

  // 识别记录 Query
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

  // 告警记录 Query
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

  // 抓拍记录 Query
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

  const handleTabChange = (nextTab: RecordsTab) => {
    updateSearch({
      tab: nextTab,
      page: 1,
      // 切换 Tab 时重置细项专属参数
      matched: undefined,
      personId: undefined,
      alarmType: undefined,
      targetType: undefined,
    })
  }

  const handleRefresh = () => {
    if (tab === 'recognitions') void recognitionsQuery.refetch()
    else if (tab === 'alarms') void alarmsQuery.refetch()
    else void capturesQuery.refetch()
  }

  const handleOpenDetail = (item: RecordItem) => {
    setSelectedRecord(item)
    setDetailOpen(true)
  }

  const isFetching =
    recognitionsQuery.isFetching ||
    alarmsQuery.isFetching ||
    capturesQuery.isFetching

  // 当前列表行与总条数计算
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

  // 单 Tab 隔离多选状态
  const selection = useRecordsSelection(tab, currentItems)

  return (
    <>
      <Header fixed>
        <Search className='me-auto' />
        <LanguageSwitch />
        <ThemeSwitch />
        <ConfigDrawer />
        <ProfileDropdown />
      </Header>

      <Main className='flex flex-1 flex-col gap-4 sm:gap-6'>
        {/* 页面标题与主操作区（100% 对齐 Cameras / Audit 标题行基线与按钮规范） */}
        <div className='flex flex-wrap items-end justify-between gap-2'>
          <div>
            <h2 className='text-2xl font-bold tracking-tight'>{t('title')}</h2>
            <p className='text-sm text-muted-foreground'>{t('description')}</p>
          </div>

          <RecordsPrimaryButtons
            onRefresh={handleRefresh}
            isRefreshing={isFetching}
            onExport={() => {
              setExportDialogMode('batch')
              setExportDialogOpen(true)
            }}
          />
        </div>

        {/* 核心 Tab 切换与数据表格工作区 */}
        <Tabs
          value={tab}
          onValueChange={(val) => handleTabChange(val as RecordsTab)}
          className='space-y-4'
        >
          <TabsList className='h-9'>
            <TabsTrigger value='recognitions' className='gap-1.5 text-xs'>
              <UserCheck className='size-3.5' />
              <span>{t('tabs.recognitions')}</span>
            </TabsTrigger>
            <TabsTrigger value='alarms' className='gap-1.5 text-xs'>
              <AlertTriangle className='size-3.5' />
              <span>{t('tabs.alarms')}</span>
            </TabsTrigger>
            <TabsTrigger value='captures' className='gap-1.5 text-xs'>
              <Camera className='size-3.5' />
              <span>{t('tabs.captures')}</span>
            </TabsTrigger>
            <TabsTrigger value='vehicles' className='gap-1.5 text-xs'>
              <CarFront className='size-3.5' />
              <span>{t('tabs.vehicles')}</span>
            </TabsTrigger>
          </TabsList>

          <div className='space-y-4'>
            {/* 综合过滤工具栏 */}
            <RecordsToolbar
              tab={tab}
              view={view}
              cameraId={cameraId}
              dateRange={activeRange}
              matched={matched}
              personId={personId}
              alarmType={alarmType}
              targetType={targetType}
              onTabChange={handleTabChange}
              onViewChange={(nextView) => updateSearch({ view: nextView })}
              onCameraChange={(nextCam) =>
                updateSearch({ cameraId: nextCam, page: 1 })
              }
              onDateRangeChange={(range: RecordsDateRange) =>
                updateSearch({
                  preset: range.preset,
                  startTime: range.startTime,
                  endTime: range.endTime,
                  page: 1,
                })
              }
              onMatchedChange={(nextMatched) =>
                updateSearch({ matched: nextMatched, page: 1 })
              }
              onPersonIdChange={(nextPid) =>
                updateSearch({ personId: nextPid, page: 1 })
              }
              onAlarmTypeChange={(nextAt) =>
                updateSearch({ alarmType: nextAt, page: 1 })
              }
              onTargetTypeChange={(nextTt) =>
                updateSearch({ targetType: nextTt, page: 1 })
              }
              onExport={() => {
                setExportDialogMode('batch')
                setExportDialogOpen(true)
              }}
              onResetFilters={() =>
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
              }
            />

            {/* 视图内容区 */}
            {tab === 'recognitions' && (
              <RecognitionsView
                items={recognitionsQuery.data?.items ?? []}
                total={recognitionsQuery.data?.total ?? 0}
                page={page}
                pageSize={pageSize}
                view={view}
                cameraName={cameraName}
                isLoading={recognitionsQuery.isLoading}
                isError={recognitionsQuery.isError}
                onPageChange={(nextPage) => updateSearch({ page: nextPage })}
                onPageSizeChange={(nextPageSize) =>
                  updateSearch({ pageSize: nextPageSize, page: 1 })
                }
                onSelect={handleOpenDetail}
                onRetry={() => void recognitionsQuery.refetch()}
                isSelected={selection.isSelected}
                onToggleSelect={selection.toggleItem}
                isAllPageSelected={selection.isAllPageSelected}
                isPartiallySelected={selection.isPartiallySelected}
                onToggleSelectAll={selection.toggleSelectAllCurrentPage}
              />
            )}

            {tab === 'alarms' && (
              <AlarmsView
                items={alarmsQuery.data?.items ?? []}
                total={alarmsQuery.data?.total ?? 0}
                page={page}
                pageSize={pageSize}
                view={view}
                cameraName={cameraName}
                isLoading={alarmsQuery.isLoading}
                isError={alarmsQuery.isError}
                onPageChange={(nextPage) => updateSearch({ page: nextPage })}
                onPageSizeChange={(nextPageSize) =>
                  updateSearch({ pageSize: nextPageSize, page: 1 })
                }
                onSelect={handleOpenDetail}
                onRetry={() => void alarmsQuery.refetch()}
                isSelected={selection.isSelected}
                onToggleSelect={selection.toggleItem}
                isAllPageSelected={selection.isAllPageSelected}
                isPartiallySelected={selection.isPartiallySelected}
                onToggleSelectAll={selection.toggleSelectAllCurrentPage}
              />
            )}

            {(tab === 'captures' || tab === 'vehicles') && (
              <CapturesView
                mode={tab === 'vehicles' ? 'vehicles' : 'captures'}
                items={capturesQuery.data?.items ?? []}
                total={capturesQuery.data?.total ?? 0}
                page={page}
                pageSize={pageSize}
                view={view}
                cameraName={cameraName}
                isLoading={capturesQuery.isLoading}
                isError={capturesQuery.isError}
                onPageChange={(nextPage) => updateSearch({ page: nextPage })}
                onPageSizeChange={(nextPageSize) =>
                  updateSearch({ pageSize: nextPageSize, page: 1 })
                }
                onSelect={handleOpenDetail}
                onRetry={() => void capturesQuery.refetch()}
                isSelected={selection.isSelected}
                onToggleSelect={selection.toggleItem}
                isAllPageSelected={selection.isAllPageSelected}
                isPartiallySelected={selection.isPartiallySelected}
                onToggleSelectAll={selection.toggleSelectAllCurrentPage}
              />
            )}
          </div>
        </Tabs>
      </Main>

      {/* 研判详情弹窗 */}
      <RecordDetailDialog
        tab={tab}
        record={selectedRecord}
        open={detailOpen}
        cameraName={cameraName}
        onOpenChange={setDetailOpen}
      />

      {/* 底部多选浮动操作条 */}
      <RecordsSelectionBar
        selectedCount={selection.selectedCount}
        onExportSelected={() => {
          setExportDialogMode('selection')
          setExportDialogOpen(true)
        }}
        onClearSelection={selection.clearSelection}
      />

      {/* 导出配置与进度弹窗 */}
      <RecordsExportDialog
        open={exportDialogOpen}
        onOpenChange={setExportDialogOpen}
        activeTab={tab}
        mode={exportDialogMode}
        selectedItems={selection.selectedItems}
        queryParams={{
          cameraId,
          startTime: activeRange.startTime,
          endTime: activeRange.endTime,
          matched:
            matched === 'true' ? true : matched === 'false' ? false : undefined,
          personId,
          alarmType,
          targetType: tab === 'vehicles' ? 'vehicle' : targetType,
        }}
        estimatedTotal={currentTotal}
        cameraName={cameraName}
      />
    </>
  )
}
