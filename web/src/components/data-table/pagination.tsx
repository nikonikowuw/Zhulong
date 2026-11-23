import {
  ChevronLeftIcon,
  ChevronRightIcon,
  DoubleArrowLeftIcon,
  DoubleArrowRightIcon,
} from '@radix-ui/react-icons'
import { type Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { cn, getPageNumbers } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

type DataTablePaginationProps<TData> = {
  table: Table<TData>
  /** 当前筛选条件下的结果总数；传入后在分页栏左侧显示总条数 */
  total?: number
  className?: string
}

export function DataTablePagination<TData>({
  table,
  total,
  className,
}: DataTablePaginationProps<TData>) {
  const currentPage = table.getState().pagination.pageIndex + 1
  const totalPages = table.getPageCount()
  const pageNumbers = getPageNumbers(currentPage, totalPages)
  const { t } = useTranslation('common')

  return (
    <div
      className={cn(
        'flex items-center justify-between gap-4 overflow-clip px-2',
        '@max-2xl/content:flex-col-reverse @max-2xl/content:gap-3',
        className
      )}
      style={{ overflowClipMargin: 1 }}
    >
      <div className='flex items-center gap-4 @max-2xl/content:w-full @max-2xl/content:justify-between'>
        <div className='flex min-w-0 items-center gap-3'>
          {typeof total === 'number' && (
            <p className='shrink-0 text-sm whitespace-nowrap text-muted-foreground'>
              {t('table.pagination.totalItems', { total })}
            </p>
          )}
          <div className='flex shrink-0 items-center justify-center text-sm font-medium whitespace-nowrap @2xl/content:hidden'>
            {t('table.pagination.pageOf', { currentPage, totalPages })}
          </div>
        </div>
        <div className='flex shrink-0 items-center gap-2'>
          <p className='hidden text-sm whitespace-nowrap text-muted-foreground sm:block'>
            {t('table.pagination.rowsPerPage')}
          </p>
          <Select
            value={`${table.getState().pagination.pageSize}`}
            onValueChange={(value) => {
              table.setPageSize(Number(value))
            }}
          >
            <SelectTrigger
              size='sm'
              className='h-8 min-w-18'
              aria-label={t('table.pagination.rowsPerPage')}
            >
              <SelectValue placeholder={table.getState().pagination.pageSize} />
            </SelectTrigger>
            <SelectContent side='top'>
              {[10, 20, 30, 40, 50].map((pageSize) => (
                <SelectItem key={pageSize} value={`${pageSize}`}>
                  {pageSize}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className='flex items-center gap-4 lg:gap-6'>
        <div className='flex shrink-0 items-center justify-center text-sm font-medium whitespace-nowrap @max-2xl/content:hidden'>
          {t('table.pagination.pageOf', { currentPage, totalPages })}
        </div>
        <div className='flex items-center gap-1.5'>
          <Button
            variant='outline'
            className='size-8 p-0 @max-md/content:hidden'
            onClick={() => table.setPageIndex(0)}
            disabled={!table.getCanPreviousPage()}
          >
            <span className='sr-only'>{t('table.pagination.firstPage')}</span>
            <DoubleArrowLeftIcon className='h-4 w-4' />
          </Button>
          <Button
            variant='outline'
            className='size-8 p-0'
            onClick={() => table.previousPage()}
            disabled={!table.getCanPreviousPage()}
          >
            <span className='sr-only'>
              {t('table.pagination.previousPage')}
            </span>
            <ChevronLeftIcon className='h-4 w-4' />
          </Button>

          {/* Page number buttons */}
          {pageNumbers.map((pageNumber, index) => (
            <div key={`${pageNumber}-${index}`} className='flex items-center'>
              {pageNumber === '...' ? (
                <span className='px-1 text-sm text-muted-foreground'>...</span>
              ) : (
                <Button
                  variant={currentPage === pageNumber ? 'default' : 'outline'}
                  className='h-8 min-w-8 px-2'
                  onClick={() => table.setPageIndex((pageNumber as number) - 1)}
                >
                  <span className='sr-only'>
                    {t('table.pagination.goToPage', { pageNumber })}
                  </span>
                  {pageNumber}
                </Button>
              )}
            </div>
          ))}

          <Button
            variant='outline'
            className='size-8 p-0'
            onClick={() => table.nextPage()}
            disabled={!table.getCanNextPage()}
          >
            <span className='sr-only'>{t('table.pagination.nextPage')}</span>
            <ChevronRightIcon className='h-4 w-4' />
          </Button>
          <Button
            variant='outline'
            className='size-8 p-0 @max-md/content:hidden'
            onClick={() => table.setPageIndex(table.getPageCount() - 1)}
            disabled={!table.getCanNextPage()}
          >
            <span className='sr-only'>{t('table.pagination.lastPage')}</span>
            <DoubleArrowRightIcon className='h-4 w-4' />
          </Button>
        </div>
      </div>
    </div>
  )
}
