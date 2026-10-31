import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { AlgorithmsDialogs } from './components/algorithms-dialogs'
import { AlgorithmsPrimaryButtons } from './components/algorithms-primary-buttons'
import {
  AlgorithmsProvider,
  useAlgorithmsContext,
} from './components/algorithms-provider'
import { AlgorithmsTable } from './components/algorithms-table'
import { useAlgorithms } from './hooks/use-algorithms'

const route = getRouteApi('/_authenticated/algorithms/')

function AlgorithmsContent(): React.JSX.Element {
  const { t } = useTranslation('algorithms')
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const { setOpen } = useAlgorithmsContext()

  const { data: algorithms = [], isLoading, isError, refetch } = useAlgorithms()

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
        <div className='flex flex-wrap items-end justify-between gap-3'>
          <div>
            <h2 className='text-2xl font-bold tracking-tight'>
              {t('pageTitle', '算法包管理')}
            </h2>
            <p className='text-sm text-muted-foreground'>
              {t(
                'pageDesc',
                '管理系统视觉计算与边缘推理算法包，支持安装、热启停、资源监控与安全卸载。'
              )}
            </p>
          </div>
          <AlgorithmsPrimaryButtons />
        </div>

        {/* 核心算法包列表（TanStack Table 数据密集型工作区） */}
        <AlgorithmsTable
          data={algorithms}
          isLoading={isLoading}
          isError={isError}
          onRetry={() => void refetch()}
          search={search}
          navigate={navigate}
          onOpenUpload={() => setOpen('upload')}
        />
      </Main>

      {/* 集中式弹窗挂载 */}
      <AlgorithmsDialogs />
    </>
  )
}

export function Algorithms(): React.JSX.Element {
  return (
    <AlgorithmsProvider>
      <AlgorithmsContent />
    </AlgorithmsProvider>
  )
}
