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
import { PersonsProvider } from './components/persons-provider'
import { PersonsTable } from './components/persons-table'
import { usePersons } from './hooks/use-persons'

const route = getRouteApi('/_authenticated/persons/')

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

  const { data, isLoading, isError, refetch } = usePersons({
    page,
    pageSize,
    search: searchQuery,
  })

  const persons = data?.items ?? []
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
          <PersonsPrimaryButtons />
        </div>

        {/* 核心数据表格 */}
        <PersonsTable
          data={persons}
          total={total}
          isLoading={isLoading}
          isError={isError}
          onRetry={() => void refetch()}
          search={search}
          navigate={navigate}
        />
      </Main>

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
