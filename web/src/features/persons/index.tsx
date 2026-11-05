import { useCallback, useEffect, useRef } from 'react'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
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
import { usePerson, usePersons } from './hooks/use-persons'

const route = getRouteApi('/_authenticated/persons/')

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
  useEffect(() => {
    if (
      personId &&
      targetPersonQuery.data &&
      hasOpenedRef.current !== personId
    ) {
      hasOpenedRef.current = personId
      setCurrentRow(targetPersonQuery.data)
      setOpen('faces')
    }
  }, [personId, targetPersonQuery.data, setCurrentRow, setOpen])

  // 弹窗关闭时清除 URL 中的 personId 参数
  const prevOpenRef = useRef(open)
  useEffect(() => {
    if (prevOpenRef.current && !open && personId) {
      hasOpenedRef.current = null
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

  const selectedFaceStatuses = Array.isArray(search.faceStatus)
    ? search.faceStatus
    : search.faceStatus
      ? [search.faceStatus]
      : []
  const faceStatus =
    selectedFaceStatuses.length === 1 ? selectedFaceStatuses[0] : undefined

  const { data, isLoading, isError, refetch } = usePersons({
    page,
    pageSize,
    search: searchQuery,
    faceStatus,
  })

  const handleRetry = useCallback(() => {
    void refetch()
  }, [refetch])

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
          <div>
            <h2 className='text-2xl font-bold tracking-tight'>{t('title')}</h2>
            <p className='text-sm text-muted-foreground'>{t('description')}</p>
          </div>
          <PersonsPrimaryButtons search={searchQuery} faceStatus={faceStatus} />
        </div>

        {/* 核心数据表格 */}
        <PersonsTable
          data={persons}
          total={total}
          isLoading={isLoading}
          isError={isError}
          onRetry={handleRetry}
          search={search}
          navigate={navigate}
        />
      </Main>

      <PersonsUrlSync personId={search.personId} navigate={navigate} />
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
