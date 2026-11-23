import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { ListPage } from '@/components/list-page'
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
      <ListPage
        title={t('pageTitle', '算法包管理')}
        description={t(
          'pageDesc',
          '管理系统视觉计算与边缘推理算法包，支持安装、热启停、资源监控与安全卸载。'
        )}
        actions={<AlgorithmsPrimaryButtons />}
      >
        <AlgorithmsTable
          data={algorithms}
          isLoading={isLoading}
          isError={isError}
          onRetry={() => void refetch()}
          search={search}
          navigate={navigate}
          onOpenUpload={() => setOpen('upload')}
        />
      </ListPage>

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
