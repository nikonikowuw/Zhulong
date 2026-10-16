import { useState } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { AlertTriangle, Camera, UserCheck } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { RecordDetailDialog } from './components/detail/record-detail-dialog'
import { RecordsPrimaryButtons } from './components/records-primary-buttons'
import { RecordsToolbar } from './components/records-toolbar'
import { AlarmsView } from './components/views/alarms-view'
import { CapturesView } from './components/views/captures-view'
import { RecognitionsView } from './components/views/recognitions-view'
import { type RecordItem, type RecordsSearchParams } from './data/schema'
import { type RecordsDateRange } from './data/time-presets'
import { useAlarms } from './hooks/use-alarms'
import { useCaptures } from './hooks/use-captures'
import { useRecognitions } from './hooks/use-recognitions'

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

  // 细项过滤
  const matched = search.matched
  const personId = search.personId
  const alarmType = search.alarmType
  const targetType = search.targetType

  // 详情弹窗状态
  const [detailOpen, setDetailOpen] = useState(false)
  const [selectedRecord, setSelectedRecord] = useState<RecordItem | null>(null)

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
          startTime,
          endTime,
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
          startTime,
          endTime,
        }
      : undefined
  )

  // 抓拍记录 Query
  const capturesQuery = useCaptures(
    tab === 'captures'
      ? {
          page,
          pageSize,
          cameraId,
          targetType,
          startTime,
          endTime,
        }
      : undefined
  )

  const handleTabChange = (nextTab: 'recognitions' | 'alarms' | 'captures') => {
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
          />
        </div>

        {/* 核心 Tab 切换与数据表格工作区 */}
        <Tabs
          value={tab}
          onValueChange={(val) =>
            handleTabChange(val as 'recognitions' | 'alarms' | 'captures')
          }
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
          </TabsList>

          <div className='space-y-4'>
            {/* 综合过滤工具栏 */}
            <RecordsToolbar
              tab={tab}
              view={view}
              cameraId={cameraId}
              preset={preset}
              startTime={startTime}
              endTime={endTime}
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
                isLoading={recognitionsQuery.isLoading}
                isError={recognitionsQuery.isError}
                onPageChange={(nextPage) => updateSearch({ page: nextPage })}
                onPageSizeChange={(nextPageSize) =>
                  updateSearch({ pageSize: nextPageSize, page: 1 })
                }
                onSelect={handleOpenDetail}
                onRetry={() => void recognitionsQuery.refetch()}
              />
            )}

            {tab === 'alarms' && (
              <AlarmsView
                items={alarmsQuery.data?.items ?? []}
                total={alarmsQuery.data?.total ?? 0}
                page={page}
                pageSize={pageSize}
                view={view}
                isLoading={alarmsQuery.isLoading}
                isError={alarmsQuery.isError}
                onPageChange={(nextPage) => updateSearch({ page: nextPage })}
                onPageSizeChange={(nextPageSize) =>
                  updateSearch({ pageSize: nextPageSize, page: 1 })
                }
                onSelect={handleOpenDetail}
                onRetry={() => void alarmsQuery.refetch()}
              />
            )}

            {tab === 'captures' && (
              <CapturesView
                items={capturesQuery.data?.items ?? []}
                total={capturesQuery.data?.total ?? 0}
                page={page}
                pageSize={pageSize}
                view={view}
                isLoading={capturesQuery.isLoading}
                isError={capturesQuery.isError}
                onPageChange={(nextPage) => updateSearch({ page: nextPage })}
                onPageSizeChange={(nextPageSize) =>
                  updateSearch({ pageSize: nextPageSize, page: 1 })
                }
                onSelect={handleOpenDetail}
                onRetry={() => void capturesQuery.refetch()}
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
        onOpenChange={setDetailOpen}
      />
    </>
  )
}
