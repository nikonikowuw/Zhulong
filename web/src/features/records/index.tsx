import { lazy, Suspense } from 'react'
import { AlertTriangle, Camera, CarFront, UserCheck } from 'lucide-react'
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
import { RecordsSelectionBar } from './components/export/records-selection-bar'
import { RecordsPrimaryButtons } from './components/records-primary-buttons'
import { RecordsToolbar } from './components/records-toolbar'
import { AlarmsView } from './components/views/alarms-view'
import { CapturesView } from './components/views/captures-view'
import { RecognitionsView } from './components/views/recognitions-view'
import { type RecordsTab } from './data/schema'
import { useRecordsPage } from './hooks/use-records-page'

const LazyRecordsExportDialog = lazy(() =>
  import('./components/export/records-export-dialog').then((m) => ({
    default: m.RecordsExportDialog,
  }))
)

export function Records() {
  const { t } = useTranslation('records')
  const {
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
  } = useRecordsPage()

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
        <div className='flex flex-wrap items-end justify-between gap-2'>
          <div className='min-w-0 flex-1'>
            <h2 className='text-2xl font-bold tracking-tight'>{t('title')}</h2>
            <p className='max-w-3xl text-sm text-muted-foreground'>
              {t('description')}
            </p>
          </div>

          <RecordsPrimaryButtons
            onRefresh={handleRefresh}
            isRefreshing={isFetching}
            onExport={handleOpenBatchExport}
          />
        </div>

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
            <RecordsToolbar
              tab={tab}
              view={view}
              cameraId={cameraId}
              dateRange={activeRange}
              matched={matched}
              personId={personId}
              alarmType={alarmType}
              targetType={targetType}
              onViewChange={handleViewChange}
              onCameraChange={handleCameraChange}
              onDateRangeChange={handleDateRangeChange}
              onMatchedChange={handleMatchedChange}
              onPersonIdChange={handlePersonIdChange}
              onAlarmTypeChange={handleAlarmTypeChange}
              onTargetTypeChange={handleTargetTypeChange}
              onResetFilters={handleResetFilters}
            />

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
                onPageChange={handlePageChange}
                onPageSizeChange={handlePageSizeChange}
                onSelect={handleOpenDetail}
                onRetry={handleRetry}
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
                onPageChange={handlePageChange}
                onPageSizeChange={handlePageSizeChange}
                onSelect={handleOpenDetail}
                onRetry={handleRetry}
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
                onPageChange={handlePageChange}
                onPageSizeChange={handlePageSizeChange}
                onSelect={handleOpenDetail}
                onRetry={handleRetry}
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

      <RecordDetailDialog
        tab={selectedTab}
        record={selectedRecord}
        open={detailOpen}
        cameraName={cameraName}
        onOpenChange={setDetailOpen}
      />

      <RecordsSelectionBar
        selectedCount={selection.selectedCount}
        onExportSelected={handleOpenSelectionExport}
        onClearSelection={selection.clearSelection}
      />

      {exportDialogMounted && (
        <Suspense fallback={null}>
          <LazyRecordsExportDialog
            open={exportDialogOpen}
            onOpenChange={setExportDialogOpen}
            activeTab={tab}
            mode={exportDialogMode}
            selectedItems={selection.selectedItems}
            queryParams={exportQueryParams}
            estimatedTotal={currentTotal}
            cameraName={cameraName}
          />
        </Suspense>
      )}
    </>
  )
}
