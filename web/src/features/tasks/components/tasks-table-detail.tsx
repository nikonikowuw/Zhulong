import React from 'react'
import { flexRender, type Table as TanstackTable } from '@tanstack/react-table'
import { AlertCircle, RefreshCw, Video } from 'lucide-react'
import { useTranslation } from 'react-i18next'
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
import { DataTablePagination } from '@/components/data-table'
import { type ChannelTaskGroup } from '../data/schema'
import { TaskSubTable } from './task-sub-table'

export interface TasksTableDetailProps {
  table: TanstackTable<ChannelTaskGroup>
  /** 列数：用于展开行与空态/错误态单元格的 colSpan。 */
  columnCount: number
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  isFiltered: boolean
  onOpenChannelConfig: (cameraId?: string) => void
}

/**
 * 明细视图：通道主行 + 展开后的算法订阅二级表。
 *
 * 从 `tasks-table.tsx` 拆出——该文件此前已越过开发规范的单文件 300 行上限。
 */
export function TasksTableDetail({
  table,
  columnCount,
  isLoading,
  isError,
  onRetry,
  isFiltered,
  onOpenChannelConfig,
}: TasksTableDetailProps): React.JSX.Element {
  const { t } = useTranslation('tasks')

  return (
    <>
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
              Array.from({ length: 5 }).map((_, index) => (
                <TableRow key={`skeleton-${index}`}>
                  <TableCell className='w-10 px-2'>
                    <Skeleton className='size-5' />
                  </TableCell>
                  <TableCell className='min-w-[240px]'>
                    <div className='flex items-center gap-2.5'>
                      <Skeleton className='size-8 rounded-lg' />
                      <div className='flex flex-col gap-1'>
                        <Skeleton className='h-3.5 w-28' />
                        <Skeleton className='h-3 w-36' />
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className='w-32'>
                    <Skeleton className='h-5 w-20' />
                  </TableCell>
                  <TableCell className='w-28'>
                    <Skeleton className='h-5 w-16' />
                  </TableCell>
                  <TableCell className='w-28'>
                    <Skeleton className='h-4 w-16' />
                  </TableCell>
                  <TableCell className='w-36'>
                    <Skeleton className='h-4 w-24' />
                  </TableCell>
                  <TableCell className='w-36 text-end'>
                    <Skeleton className='ms-auto h-7 w-20' />
                  </TableCell>
                </TableRow>
              ))
            ) : isError ? (
              <TableRow>
                <TableCell colSpan={columnCount} className='h-48 text-center'>
                  <div className='flex flex-col items-center justify-center gap-2 text-destructive'>
                    <AlertCircle className='h-8 w-8' />
                    <span className='font-medium'>
                      {t('table.loadFailed', '加载分析任务失败')}
                    </span>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={onRetry}
                      className='mt-2'
                    >
                      <RefreshCw className='me-1 h-4 w-4' />
                      {t('actions.retry', '重试')}
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ) : table.getRowModel().rows?.length ? (
              table.getRowModel().rows.map((row) => (
                <React.Fragment key={row.id}>
                  <TableRow
                    data-state={row.getIsSelected() && 'selected'}
                    className='group transition-colors hover:bg-muted/30'
                  >
                    {row.getVisibleCells().map((cell) => (
                      <TableCell
                        key={cell.id}
                        className={cell.column.columnDef.meta?.className}
                      >
                        {flexRender(
                          cell.column.columnDef.cell,
                          cell.getContext()
                        )}
                      </TableCell>
                    ))}
                  </TableRow>
                  {row.getIsExpanded() && (
                    <TableRow className='hover:bg-transparent'>
                      <TableCell colSpan={columnCount} className='p-0'>
                        <TaskSubTable group={row.original} />
                      </TableCell>
                    </TableRow>
                  )}
                </React.Fragment>
              ))
            ) : (
              <TableRow>
                <TableCell colSpan={columnCount} className='h-64 text-center'>
                  <div className='flex flex-col items-center justify-center gap-2'>
                    <div className='flex h-12 w-12 items-center justify-center rounded-full bg-muted'>
                      <Video className='h-6 w-6 text-muted-foreground' />
                    </div>
                    <span className='text-base font-semibold text-foreground'>
                      {isFiltered
                        ? t('table.filterEmpty', '未找到匹配的分析任务')
                        : t('table.empty', '暂无分析任务记录')}
                    </span>
                    <p className='max-w-sm text-sm text-muted-foreground'>
                      {isFiltered
                        ? t(
                            'table.filterEmptyDesc',
                            '请调整搜索关键词或筛选条件后重试。'
                          )
                        : t(
                            'table.emptyDesc',
                            '尚未向任何摄像头分配分析算法，点击「配置通道任务」开始。'
                          )}
                    </p>
                    {isFiltered ? (
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => {
                          table.resetGlobalFilter()
                          table.resetColumnFilters()
                        }}
                        className='mt-2'
                      >
                        {t('table.resetFilter', '重置筛选')}
                      </Button>
                    ) : (
                      <Button
                        size='sm'
                        onClick={() => onOpenChannelConfig()}
                        className='mt-2'
                      >
                        {t('newChannelTask', '配置通道任务')}
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </div>

      {/* 无数据时不渲染分页（否则会显示 “Page 1 of 0”） */}
      {table.getFilteredRowModel().rows.length > 0 && (
        <DataTablePagination table={table} />
      )}
    </>
  )
}
