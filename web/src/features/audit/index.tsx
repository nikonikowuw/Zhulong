import { useEffect } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { AuditClearDialog } from './components/AuditClearDialog'
import { AuditDetailDialog } from './components/AuditDetailDialog'
import { AuditPrimaryButtons } from './components/AuditPrimaryButtons'
import { AuditProvider } from './components/AuditProvider'
import { AuditTable } from './components/AuditTable'
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
  const startTime = search.startTime
  const endTime = search.endTime

  // 如果 URL 只有 preset 而没有具体 startTime/endTime（例如手动输入 ?preset=1h 访问）
  // 在当前挂载时计算一次并同步写入 URL，确保 queryKey 在重渲染过程中完全稳定，避免无限 fetch 循环
  useEffect(() => {
    if (preset && (!search.startTime || !search.endTime)) {
      const computed = computeRangeFromPreset(preset)
      void navigate({
        search: (prev) => ({
          ...prev,
          startTime: computed.startTime,
          endTime: computed.endTime,
        }),
        replace: true,
      })
    }
  }, [preset, search.startTime, search.endTime, navigate])

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

  const { data, isLoading, isFetching, isError, refetch } = useAuditLogs({
    page,
    pageSize,
    actions: actions.length > 0 ? actions : undefined,
    status,
    startTime,
    endTime,
  })

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
    }
    void refetch()
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
          startTime={startTime}
          endTime={endTime}
          onDateRangeChange={handleRangeChange}
        />
      </Main>

      {/* 弹窗与抽屉容器 */}
      <AuditDetailDialog />
      <AuditClearDialog />
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
