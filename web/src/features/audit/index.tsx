import { useMemo } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { AuditDetailDialog } from './components/AuditDetailDialog'
import { AuditPrimaryButtons } from './components/AuditPrimaryButtons'
import { AuditProvider } from './components/AuditProvider'
import { AuditTable } from './components/AuditTable'
import { type GetAuditLogsParams } from './data/schema'
import {
  computeRangeFromPreset,
  type AuditDateRange,
} from './data/time-presets'
import { useAuditLogs } from './hooks/use-audit-logs'

const route = getRouteApi('/_authenticated/audit/')

function AuditContent() {
  const { t } = useTranslation('audit')
  const search = route.useSearch()
  const navigate = route.useNavigate()

  const page = typeof search.page === 'number' ? search.page : 1
  const pageSize = typeof search.pageSize === 'number' ? search.pageSize : 20
  const actions = Array.isArray(search.action)
    ? search.action
    : search.action
      ? [search.action]
      : []
  const selectedStatuses = Array.isArray(search.status)
    ? search.status
    : search.status
      ? [search.status]
      : []
  const status = selectedStatuses.length === 1 ? selectedStatuses[0] : undefined
  const preset = search.preset

  // 纯声明式派生有效时间窗口：如果仅指定 preset 而未提供绝对时间戳，在渲染期即刻计算，
  // 杜绝 useEffect 回写导致的「先发全量查、再发范围查」的首屏级联 Fetch 与画面闪烁
  const effectiveRange = useMemo(() => {
    if (preset && (!search.startTime || !search.endTime)) {
      return computeRangeFromPreset(preset)
    }
    return { startTime: search.startTime, endTime: search.endTime }
  }, [preset, search.startTime, search.endTime])

  const handleRangeChange = (params: AuditDateRange) => {
    void navigate({
      search: (prev) => ({
        ...prev,
        page: 1,
        preset: params.preset || undefined,
        startTime: params.startTime || undefined,
        endTime: params.endTime || undefined,
      }),
    })
  }

  // 列表与导出共用同一份归一化筛选条件：路由 search 里 action/status 可能是数组，
  // 而 api 层只收单个 action + actions[]。
  const listParams: GetAuditLogsParams = {
    page,
    pageSize,
    actions: actions.length > 0 ? actions : undefined,
    status,
    startTime: effectiveRange.startTime,
    endTime: effectiveRange.endTime,
  }

  const { data, isLoading, isFetching, isError, refetch } =
    useAuditLogs(listParams)

  const handleRefresh = () => {
    if (preset) {
      const fresh = computeRangeFromPreset(preset)
      void navigate({
        search: (prev) => ({
          ...prev,
          startTime: fresh.startTime,
          endTime: fresh.endTime,
        }),
      })
    } else {
      void refetch()
    }
  }

  const logs = data?.items ?? []
  const total = data?.total ?? 0

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
        {/* 页面标题与主操作区 */}
        <div className='flex flex-wrap items-end justify-between gap-2'>
          <div>
            <h2 className='text-2xl font-bold tracking-tight'>{t('title')}</h2>
            <p className='text-sm text-muted-foreground'>{t('description')}</p>
          </div>
          <AuditPrimaryButtons
            onRefresh={handleRefresh}
            isRefreshing={isFetching}
            filter={listParams}
          />
        </div>

        {/* 核心审计日志表格 */}
        <AuditTable
          data={logs}
          total={total}
          isLoading={isLoading}
          isError={isError}
          onRetry={() => void refetch()}
          search={search}
          navigate={navigate}
          preset={preset}
          startTime={effectiveRange.startTime}
          endTime={effectiveRange.endTime}
          onDateRangeChange={handleRangeChange}
        />
      </Main>

      {/* 弹窗与抽屉容器 */}
      <AuditDetailDialog />
    </>
  )
}

export function Audit() {
  return (
    <AuditProvider>
      <AuditContent />
    </AuditProvider>
  )
}
