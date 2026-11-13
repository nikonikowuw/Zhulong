import { useEffect, useRef } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { errorMessageOf } from '@/lib/handle-server-error'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { PersonsDialogs } from './components/persons-dialogs'
import { PersonsPrimaryButtons } from './components/persons-primary-buttons'
import {
  PersonsProvider,
  usePersonsActions,
  usePersonsState,
} from './components/persons-provider'
import { PersonsTable } from './components/persons-table'
import { type Person } from './data/schema'
import {
  useLatestFaceReextractJob,
  usePerson,
  usePersons,
} from './hooks/use-persons'

const route = getRouteApi('/_authenticated/persons/')

/** 稳定的空数组常量：`data?.items ?? []` 每次渲染新建引用会让表格无谓重算。 */
const EMPTY_PERSONS: Person[] = []

function PersonsUrlSync({
  personId,
  navigate,
}: {
  personId?: string
  navigate: ReturnType<typeof route.useNavigate>
}) {
  const { open } = usePersonsState()
  const { setOpen, setCurrentRow } = usePersonsActions()
  const targetPersonQuery = usePerson(personId)

  // 当 URL 参数携带精确 personId 时，加载人员数据并打开面容档案弹窗
  const hasOpenedRef = useRef<string | null>(null)
  const hasErroredRef = useRef<string | null>(null)
  useEffect(() => {
    if (
      personId &&
      targetPersonQuery.data &&
      hasOpenedRef.current !== personId
    ) {
      hasOpenedRef.current = personId
      hasErroredRef.current = null
      setCurrentRow(targetPersonQuery.data)
      setOpen('faces')
    }
  }, [personId, targetPersonQuery.data, setCurrentRow, setOpen])

  // 深链接的 personId 不存在或查询失败时提示并移除无效参数，避免页面静默卡死
  useEffect(() => {
    if (
      personId &&
      targetPersonQuery.isError &&
      hasErroredRef.current !== personId
    ) {
      hasErroredRef.current = personId
      toast.error(errorMessageOf(targetPersonQuery.error))
      void navigate({
        replace: true,
        search: (prev) => {
          const next = { ...prev }
          delete next.personId
          return next
        },
      })
    }
  }, [personId, targetPersonQuery.isError, targetPersonQuery.error, navigate])

  // 弹窗关闭时清除 URL 中的 personId 参数
  const prevOpenRef = useRef(open)
  useEffect(() => {
    if (prevOpenRef.current && !open && personId) {
      hasOpenedRef.current = null
      hasErroredRef.current = null
      void navigate({
        search: (prev) => {
          const next = { ...prev }
          delete next.personId
          return next
        },
      })
    }
    prevOpenRef.current = open
  }, [open, personId, navigate])

  return null
}

/**
 * 挂载页面时检查一次是否存在未完成的特征重提任务，如有则挂回 Provider 继续轮询。
 */
function PersonsReextractResume() {
  const { faceReextractJobId } = usePersonsState()
  const { setFaceReextractJobId } = usePersonsActions()
  const hasCheckedRef = useRef(false)
  const latestQuery = useLatestFaceReextractJob(!faceReextractJobId)
  const latestJob = latestQuery.data

  useEffect(() => {
    if (hasCheckedRef.current) return
    if (latestQuery.isFetched === false || latestQuery.isStale) return

    hasCheckedRef.current = true
    if (!faceReextractJobId && latestJob) {
      const isActive =
        latestJob.status === 'PENDING' ||
        latestJob.status === 'EXTRACTING' ||
        latestJob.status === 'PROCESSING'
      if (isActive) {
        setFaceReextractJobId(latestJob.jobId)
      }
    }
  }, [
    faceReextractJobId,
    latestJob,
    latestQuery.isFetched,
    latestQuery.isStale,
    setFaceReextractJobId,
  ])

  return null
}

function PersonsContent() {
  const { t } = useTranslation('persons')
  const search = route.useSearch()
  const navigate = route.useNavigate()

  const page = typeof search.page === 'number' ? search.page : 1
  const pageSize = typeof search.pageSize === 'number' ? search.pageSize : 10
  const searchQuery =
    typeof search.search === 'string' && search.search.trim().length > 0
      ? search.search.trim()
      : undefined

  /**
   * 人员列表的底库筛选是**单值**（后端 `faceStatus` 只接受 enrolled / missing）。
   *
   * 曾经的实现是「数组长度恰为 1 时才取值，否则 undefined」—— 用户同时勾两个
   * 选项后，请求会悄悄变成「不过滤」，而界面和导出都还显示着筛选生效。现在
   * 路由 schema 直接把它收敛成单值字符串（见 `search-schema.ts`）。
   */
  const faceStatus = search.faceStatus

  const { data, isLoading, isError, isPlaceholderData, refetch } = usePersons({
    page,
    pageSize,
    search: searchQuery,
    faceStatus,
  })

  const persons = data?.items ?? EMPTY_PERSONS
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
          <div className='min-w-0 flex-1'>
            <h2 className='text-2xl font-bold tracking-tight'>{t('title')}</h2>
            <p className='max-w-3xl text-sm text-muted-foreground'>
              {t('description')}
            </p>
          </div>
          <PersonsPrimaryButtons search={searchQuery} faceStatus={faceStatus} />
        </div>

        {/* 核心数据表格。翻页时 `placeholderData` 会继续显示上一页，若不给任何
            信号，用户会以为点击没生效 —— 用降低不透明度做弱提示。 */}
        <div
          aria-busy={isPlaceholderData}
          className={
            isPlaceholderData
              ? 'opacity-60 transition-opacity'
              : 'transition-opacity'
          }
        >
          <PersonsTable
            data={persons}
            total={total}
            isLoading={isLoading}
            isError={isError}
            onRetry={() => void refetch()}
            search={search}
            navigate={navigate}
          />
        </div>
      </Main>

      <PersonsUrlSync personId={search.personId} navigate={navigate} />
      <PersonsReextractResume />
      <PersonsDialogs />
    </>
  )
}

export function Persons() {
  return (
    <PersonsProvider>
      <PersonsContent />
    </PersonsProvider>
  )
}

export default Persons
