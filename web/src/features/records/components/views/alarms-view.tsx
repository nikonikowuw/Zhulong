import { useMemo } from 'react'
import {
  type ColumnDef,
  getCoreRowModel,
  useReactTable,
  flexRender,
} from '@tanstack/react-table'
import { format } from 'date-fns'
import { AlertCircle, Bell, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { DataTableColumnHeader } from '@/components/data-table/column-header'
import { DataTablePagination } from '@/components/data-table/pagination'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { algorithmLabel, type Alarm } from '../../data/schema'
import { AlarmCard } from '../alarms/alarm-card'
import { MediaImage } from '../common/media-image'

export interface AlarmsViewProps {
  items: Alarm[]
  total: number
  page: number
  pageSize: number
  view: 'grid' | 'table'
  isLoading: boolean
  isError: boolean
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  onSelect: (item: Alarm) => void
  onRetry: () => void
}

export function AlarmsView({
  items,
  total,
  page,
  pageSize,
  view,
  isLoading,
  isError,
  onPageChange,
  onPageSizeChange,
  onSelect,
  onRetry,
}: AlarmsViewProps) {
  const { t } = useTranslation('records')

  const columns = useMemo<ColumnDef<Alarm>[]>(
    () => [
      {
        accessorKey: 'cropPath',
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('table.thumbnail')}
          />
        ),
        size: 90,
        enableSorting: false,
        cell: ({ row }) => (
          <div className='size-10 overflow-hidden rounded-md border border-border bg-muted/40'>
            <MediaImage
              path={row.original.cropPath || row.original.imagePath}
              alt={row.original.alarmType}
              className='size-full'
              fallbackText='Alarm'
            />
          </div>
        ),
      },
      {
        accessorKey: 'capturedAt',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('table.time')} />
        ),
        enableSorting: false,
        cell: ({ row }) => {
          const val = row.original.capturedAt
          return (
            <span className='font-mono text-xs'>
              {val ? format(new Date(val), 'yyyy-MM-dd HH:mm:ss') : '-'}
            </span>
          )
        },
      },
      {
        accessorKey: 'cameraId',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('table.camera')} />
        ),
        enableSorting: false,
        cell: ({ row }) => (
          <span className='text-xs font-medium'>{row.original.cameraId}</span>
        ),
      },
      {
        accessorKey: 'alarmType',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('table.target')} />
        ),
        enableSorting: false,
        cell: ({ row }) => (
          <Badge
            variant='outline'
            className='border-destructive/40 bg-destructive/10 text-destructive font-medium'
          >
            <Bell className='size-3 me-1' />
            {row.original.alarmType}
          </Badge>
        ),
      },
      {
        accessorKey: 'score',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('table.score')} />
        ),
        enableSorting: false,
        cell: ({ row }) => {
          const score = row.original.score
          return (
            <span className='font-mono text-xs font-semibold'>
              {score > 0 ? `${Math.round(score * 100)}%` : '-'}
            </span>
          )
        },
      },
      {
        accessorKey: 'algorithmId',
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('table.algorithmId')}
          />
        ),
        enableSorting: false,
        cell: ({ row }) => (
          <span className='font-mono text-xs text-muted-foreground'>
            {algorithmLabel(row.original) || '-'}
          </span>
        ),
      },
    ],
    [t]
  )

  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data: items,
    columns,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    pageCount: Math.ceil(total / pageSize) || 1,
    state: {
      pagination: {
        pageIndex: Math.max(0, page - 1),
        pageSize,
      },
    },
    onPaginationChange: (updater) => {
      if (typeof updater === 'function') {
        const next = updater({
          pageIndex: Math.max(0, page - 1),
          pageSize,
        })
        onPageChange(next.pageIndex + 1)
        onPageSizeChange(next.pageSize)
      } else {
        onPageChange(updater.pageIndex + 1)
        onPageSizeChange(updater.pageSize)
      }
    },
  })

  // 1. Grid 模式
  if (view === 'grid') {
    if (isLoading) {
      return (
        <div className='space-y-4'>
          <div className='grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6'>
            {Array.from({ length: pageSize > 12 ? 12 : pageSize }).map(
              (_, idx) => (
                <div
                  key={`alarm-skel-${idx}`}
                  className='overflow-hidden rounded-lg border border-border bg-card'
                >
                  <Skeleton className='aspect-square w-full' />
                  <div className='space-y-2 p-3'>
                    <Skeleton className='h-4 w-3/4' />
                    <Skeleton className='h-3 w-1/2' />
                  </div>
                </div>
              )
            )}
          </div>
          <DataTablePagination table={table} />
        </div>
      )
    }

    if (isError) {
      return (
        <div className='flex flex-col items-center justify-center rounded-lg border border-dashed border-destructive/50 p-12 text-center'>
          <AlertCircle className='mb-3 size-10 text-destructive' />
          <h3 className='text-base font-semibold'>{t('table.error')}</h3>
          <p className='mt-1 mb-4 text-sm text-muted-foreground'>
            {t('table.emptyDesc')}
          </p>
          <Button variant='outline' size='sm' onClick={onRetry}>
            <RefreshCw className='me-1.5 size-3.5' />
            {t('table.retry')}
          </Button>
        </div>
      )
    }

    if (items.length === 0) {
      return (
        <div className='flex flex-col items-center justify-center rounded-lg border border-dashed border-border p-12 text-center'>
          <div className='mb-3 rounded-full bg-muted p-3'>
            <Bell className='size-6 text-muted-foreground' />
          </div>
          <h3 className='text-base font-semibold'>{t('table.empty')}</h3>
          <p className='mt-1 text-sm text-muted-foreground'>
            {t('table.emptyDesc')}
          </p>
        </div>
      )
    }

    return (
      <div className='space-y-4'>
        <div className='grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6'>
          {items.map((item) => (
            <AlarmCard key={item.id} item={item} onSelect={onSelect} />
          ))}
        </div>
        <DataTablePagination table={table} />
      </div>
    )
  }

  // 2. Table 模式（表头常驻）
  return (
    <div className='space-y-4'>
      <div className='rounded-md border border-border bg-card'>
        <Table className='border-separate border-spacing-0'>
          <TableHeader>
            {table.getHeaderGroups().map((headerGroup) => (
              <TableRow key={headerGroup.id}>
                {headerGroup.headers.map((header) => (
                  <TableHead
                    key={header.id}
                    colSpan={header.colSpan}
                    className={header.column.columnDef.meta?.className}
                  >
                    {header.isPlaceholder
                      ? null
                      : flexRender(
                          header.column.columnDef.header,
                          header.getContext()
                        )}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {isLoading ? (
              Array.from({ length: 6 }).map((_, rIdx) => (
                <TableRow key={`alarm-table-skel-${rIdx}`}>
                  {columns.map((_, cIdx) => (
                    <TableCell key={`alarm-table-skel-c-${cIdx}`}>
                      <Skeleton className='h-5 w-full' />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : isError ? (
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className='h-48 text-center'
                >
                  <div className='flex flex-col items-center justify-center gap-2 text-destructive'>
                    <AlertCircle className='size-8' />
                    <p className='text-sm font-medium'>{t('table.error')}</p>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={onRetry}
                      className='mt-2'
                    >
                      <RefreshCw className='me-1.5 size-3.5' />
                      {t('table.retry')}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : table.getRowModel().rows?.length ? (
              table.getRowModel().rows.map((row) => (
                <TableRow
                  key={row.original.id}
                  className='cursor-pointer hover:bg-muted/50'
                  onClick={() => onSelect(row.original)}
                >
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id}>
                      {flexRender(
                        cell.column.columnDef.cell,
                        cell.getContext()
                      )}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : (
              <TableRow>
                <TableCell
                  colSpan={columns.length}
                  className='h-48 text-center'
                >
                  <div className='flex flex-col items-center justify-center gap-2'>
                    <Bell className='size-8 text-muted-foreground' />
                    <p className='text-sm font-medium'>{t('table.empty')}</p>
                    <p className='text-xs text-muted-foreground'>
                      {t('table.emptyDesc')}
                    </p>
                  </div>
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      <DataTablePagination table={table} />
    </div>
  )
}
