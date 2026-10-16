import { useMemo } from 'react'
import {
  type ColumnDef,
  getCoreRowModel,
  useReactTable,
  flexRender,
} from '@tanstack/react-table'
import { format } from 'date-fns'
import { AlertCircle, RefreshCw, UserCheck, UserX } from 'lucide-react'
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
import { type Recognition } from '../../data/schema'
import { MediaImage } from '../common/media-image'
import { RecognitionCard } from '../recognitions/recognition-card'

export interface RecognitionsViewProps {
  items: Recognition[]
  total: number
  page: number
  pageSize: number
  view: 'grid' | 'table'
  isLoading: boolean
  isError: boolean
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  onSelect: (item: Recognition) => void
  onRetry: () => void
}

export function RecognitionsView({
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
}: RecognitionsViewProps) {
  const { t } = useTranslation('records')

  const columns = useMemo<ColumnDef<Recognition>[]>(
    () => [
      {
        accessorKey: 'faceImagePath',
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
              path={row.original.faceImagePath || row.original.imagePath}
              alt={row.original.personName || 'Face'}
              className='size-full'
              fallbackText='Face'
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
        accessorKey: 'personName',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('table.target')} />
        ),
        enableSorting: false,
        cell: ({ row }) => {
          const { matched, personName, personId } = row.original
          return (
            <div className='flex items-center gap-2'>
              {matched ? (
                <Badge
                  variant='outline'
                  className='border-emerald-500/50 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 font-medium'
                >
                  <UserCheck className='size-3 me-1' />
                  {personName || personId || t('card.matched')}
                </Badge>
              ) : (
                <Badge
                  variant='outline'
                  className='border-amber-500/50 bg-amber-500/10 text-amber-600 dark:text-amber-400 font-medium'
                >
                  <UserX className='size-3 me-1' />
                  {t('card.stranger')}
                </Badge>
              )}
            </div>
          )
        },
      },
      {
        accessorKey: 'similarity',
        header: ({ column }) => (
          <DataTableColumnHeader column={column} title={t('table.score')} />
        ),
        enableSorting: false,
        cell: ({ row }) => {
          const sim = row.original.similarity
          if (!row.original.matched || sim <= 0) {
            return <span className='text-xs text-muted-foreground'>-</span>
          }
          return (
            <span className='font-mono text-xs font-semibold'>
              {Math.round(sim * 100)}%
            </span>
          )
        },
      },
      {
        accessorKey: 'trackId',
        header: ({ column }) => (
          <DataTableColumnHeader
            column={column}
            title={t('table.trackId')}
          />
        ),
        enableSorting: false,
        cell: ({ row }) => (
          <span className='font-mono text-xs text-muted-foreground'>
            {row.original.trackId > 0 ? `#${row.original.trackId}` : '-'}
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

  // 1. Grid 画廊视图
  if (view === 'grid') {
    if (isLoading) {
      return (
        <div className='space-y-4'>
          <div className='grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6'>
            {Array.from({ length: pageSize > 12 ? 12 : pageSize }).map(
              (_, idx) => (
                <div
                  key={`rec-skel-${idx}`}
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
            <UserCheck className='size-6 text-muted-foreground' />
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
            <RecognitionCard key={item.id} item={item} onSelect={onSelect} />
          ))}
        </div>
        <DataTablePagination table={table} />
      </div>
    )
  }

  // 2. Table 紧凑表格视图（表头始终完整呈现，对齐 AuditTable / CamerasTable 规范）
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
                <TableRow key={`rec-table-skel-${rIdx}`}>
                  {columns.map((_, cIdx) => (
                    <TableCell key={`rec-table-skel-c-${cIdx}`}>
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
                    <UserCheck className='size-8 text-muted-foreground' />
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
