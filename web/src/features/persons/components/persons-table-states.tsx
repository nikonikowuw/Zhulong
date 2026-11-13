import { AlertCircle, RefreshCw, Users as UsersIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { TableCell, TableRow } from '@/components/ui/table'

/**
 * 按列 id 匹配真实单元格形状的骨架，避免加载完后整表发生可见的宽度跳变。
 */
export function PersonCellSkeleton({ columnId }: { columnId: string }) {
  switch (columnId) {
    case 'select':
      return <Skeleton className='mx-auto h-4 w-4 rounded-sm' />
    case 'avatar':
      return <Skeleton className='mx-auto h-9 w-9 rounded-full' />
    case 'faces':
      return <Skeleton className='h-7 w-20 rounded-md' />
    case 'actions':
      return <Skeleton className='ms-auto h-8 w-8 rounded-md' />
    case 'source':
      return <Skeleton className='h-5 w-16 rounded-full' />
    case 'createdAt':
      return <Skeleton className='h-4 w-30' />
    default:
      return <Skeleton className='h-4 w-28' />
  }
}

export function PersonsTableErrorRow({
  colSpan,
  onRetry,
}: {
  colSpan: number
  onRetry: () => void
}) {
  const { t } = useTranslation('persons')
  return (
    <TableRow>
      <TableCell colSpan={colSpan} className='h-48 text-center'>
        <div className='flex flex-col items-center justify-center gap-2 text-destructive'>
          <AlertCircle className='h-8 w-8' />
          <span className='font-medium'>{t('table.loadFailed')}</span>
          <Button
            variant='outline'
            size='sm'
            onClick={onRetry}
            className='mt-2'
          >
            <RefreshCw className='me-1 h-4 w-4' />
            {t('actions.retry')}
          </Button>
        </div>
      </TableCell>
    </TableRow>
  )
}

export function PersonsTableEmptyRow({
  colSpan,
  isFiltered,
  onResetFilters,
  onCreate,
}: {
  colSpan: number
  isFiltered: boolean
  onResetFilters: () => void
  onCreate: () => void
}) {
  const { t } = useTranslation('persons')
  return (
    <TableRow>
      <TableCell colSpan={colSpan} className='h-64 text-center'>
        <div className='flex flex-col items-center justify-center gap-2'>
          <div className='flex h-12 w-12 items-center justify-center rounded-full bg-muted'>
            <UsersIcon className='h-6 w-6 text-muted-foreground' />
          </div>
          <span className='text-base font-semibold text-foreground'>
            {isFiltered ? t('table.filterEmpty') : t('table.empty')}
          </span>
          <p className='max-w-sm text-sm text-muted-foreground'>
            {isFiltered ? t('table.filterEmptyDesc') : t('table.emptyDesc')}
          </p>
          {isFiltered ? (
            <Button
              variant='outline'
              size='sm'
              onClick={onResetFilters}
              className='mt-2'
            >
              {t('table.resetFilter')}
            </Button>
          ) : (
            <Button size='sm' onClick={onCreate} className='mt-2'>
              {t('actions.create')}
            </Button>
          )}
        </div>
      </TableCell>
    </TableRow>
  )
}
