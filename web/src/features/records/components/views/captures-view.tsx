import { useMemo } from 'react'
import {
  flexRender,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { AlertCircle, Camera, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
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
import { DataTablePagination } from '@/components/data-table/pagination'
import { type CameraNameLookup } from '../../data/camera-label'
import { type Capture } from '../../data/schema'
import { CaptureCard } from '../captures/capture-card'
import { SelectableCardWrapper } from '../common/selectable-card-wrapper'
import { VehicleCaptureCard } from '../vehicles/vehicle-capture-card'
import { buildCaptureColumns } from './captures-columns'
import { createPaginationChangeHandler } from './pagination'
import { TableSelectionProvider } from './selection-column'

export interface CapturesViewProps {
  items: Capture[]
  total: number
  page: number
  pageSize: number
  view: 'grid' | 'table'
  mode?: 'captures' | 'vehicles'
  /** `cameraId → 名称` 解析器，见 data/camera-label.ts */
  cameraName?: CameraNameLookup
  isLoading: boolean
  isError: boolean
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
  onSelect: (item: Capture) => void
  onRetry: () => void
  /** 多选支持 */
  isSelected?: (id: string) => boolean
  onToggleSelect?: (item: Capture) => void
  isAllPageSelected?: boolean
  isPartiallySelected?: boolean
  onToggleSelectAll?: () => void
}

export function CapturesView({
  items,
  total,
  page,
  pageSize,
  view,
  mode = 'captures',
  cameraName,
  isLoading,
  isError,
  onPageChange,
  onPageSizeChange,
  onSelect,
  onRetry,
  isSelected,
  onToggleSelect,
  isAllPageSelected,
  isPartiallySelected,
  onToggleSelectAll,
}: CapturesViewProps) {
  const { t } = useTranslation('records')

  // 骨架数量对齐真实行数：加载态与结算态高度一致，切 Tab 时页面不再上下跳
  const placeholderCount = Math.max(1, Math.min(pageSize, total || pageSize))

  const columns = useMemo(
    () => buildCaptureColumns(mode, t, cameraName),
    [mode, t, cameraName]
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
    onPaginationChange: createPaginationChangeHandler({
      page,
      pageSize,
      onPageChange,
      onPageSizeChange,
    }),
  })

  // 1. Grid 模式
  if (view === 'grid') {
    if (isLoading) {
      return (
        <div className='space-y-4'>
          <div className='grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6'>
            {Array.from({ length: placeholderCount }).map((_, idx) => (
              <div
                key={`cap-skel-${idx}`}
                className='overflow-hidden rounded-lg border border-border bg-card'
              >
                <Skeleton
                  className={cn(
                    'w-full',
                    mode === 'vehicles' ? 'aspect-[3/2]' : 'aspect-square'
                  )}
                />
                <div className='space-y-2 p-3'>
                  <Skeleton className='h-4 w-3/4' />
                  <Skeleton className='h-3 w-1/2' />
                </div>
              </div>
            ))}
          </div>
          {total > 0 && <DataTablePagination table={table} />}
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
            <Camera className='size-6 text-muted-foreground' />
          </div>
          <h3 className='text-base font-semibold'>
            {t(mode === 'vehicles' ? 'table.emptyVehicles' : 'table.empty')}
          </h3>
          <p className='mt-1 text-sm text-muted-foreground'>
            {t(
              mode === 'vehicles'
                ? 'table.emptyVehiclesDesc'
                : 'table.emptyDesc'
            )}
          </p>
        </div>
      )
    }

    return (
      <div className='space-y-4'>
        <div className='grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6'>
          {items.map((item) => (
            <SelectableCardWrapper
              key={item.id}
              item={item}
              selected={isSelected?.(item.id) ?? false}
              onToggle={onToggleSelect}
            >
              {mode === 'vehicles' ? (
                <VehicleCaptureCard
                  item={item}
                  cameraName={cameraName}
                  onSelect={onSelect}
                />
              ) : (
                <CaptureCard
                  item={item}
                  cameraName={cameraName}
                  onSelect={onSelect}
                />
              )}
            </SelectableCardWrapper>
          ))}
        </div>
        {total > 0 && <DataTablePagination table={table} />}
      </div>
    )
  }

  // 2. Table 模式（表头常驻）
  return (
    <TableSelectionProvider
      isSelected={isSelected}
      onToggleSelect={onToggleSelect}
      isAllPageSelected={isAllPageSelected}
      isPartiallySelected={isPartiallySelected}
      onToggleSelectAll={onToggleSelectAll}
    >
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
                Array.from({ length: placeholderCount }).map((_, rIdx) => (
                  <TableRow key={`cap-table-skel-${rIdx}`}>
                    {columns.map((_, cIdx) => (
                      <TableCell key={`cap-table-skel-c-${cIdx}`}>
                        {cIdx === 0 ? (
                          <Skeleton className='mx-auto size-4 rounded' />
                        ) : cIdx === 1 ? (
                          <Skeleton
                            className={cn(
                              'rounded-md',
                              mode === 'vehicles' ? 'h-14 w-40' : 'size-10'
                            )}
                          />
                        ) : (
                          <Skeleton className='h-4 w-24' />
                        )}
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
                    data-state={
                      isSelected?.(row.original.id) ? 'selected' : undefined
                    }
                    className={cn(
                      'cursor-pointer hover:bg-muted/50',
                      isSelected?.(row.original.id) && 'bg-muted/60'
                    )}
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
                      <Camera className='size-8 text-muted-foreground' />
                      <p className='text-sm font-medium'>
                        {t(
                          mode === 'vehicles'
                            ? 'table.emptyVehicles'
                            : 'table.empty'
                        )}
                      </p>
                      <p className='text-xs text-muted-foreground'>
                        {t(
                          mode === 'vehicles'
                            ? 'table.emptyVehiclesDesc'
                            : 'table.emptyDesc'
                        )}
                      </p>
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>

        {!isError && total > 0 && <DataTablePagination table={table} />}
      </div>
    </TableSelectionProvider>
  )
}
