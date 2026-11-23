import {
  ArrowDownIcon,
  ArrowUpIcon,
  CaretSortIcon,
  EyeNoneIcon,
} from '@radix-ui/react-icons'
import { type Column } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

type DataTableColumnHeaderProps<TData, TValue> =
  React.HTMLAttributes<HTMLDivElement> & {
    column: Column<TData, TValue>
    title: string
  }

export function DataTableColumnHeader<TData, TValue>({
  column,
  title,
  className,
}: DataTableColumnHeaderProps<TData, TValue>) {
  const { t } = useTranslation()
  if (!column.getCanSort()) {
    // 与可排序分支同盒模型（h-8 + px-3），文字左内缩才能与 ghost Button 的 px-3 对齐。
    // 但 px-3 会把表头单元格撑宽 24px，整表随之变宽——在 414px 视口里行中心会落到
    // 横向滚动容器之外，点行打开详情的命中区就没了。这里用 -mx-3 抵消内边距的占位、
    // 再用 start-3 做纯视觉右移：排版宽度不变，文字仍落在与可排序列一致的 20px 处。
    return (
      <div
        className={cn(
          'relative start-3 -mx-3 flex h-8 items-center px-3',
          className
        )}
      >
        {title}
      </div>
    )
  }

  return (
    <div className={cn('flex items-center space-x-2', className)}>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant='ghost'
            size='sm'
            className='h-8 data-[state=open]:bg-accent'
          >
            <span>{title}</span>
            {column.getIsSorted() === 'desc' ? (
              <ArrowDownIcon className='ms-2 h-4 w-4' />
            ) : column.getIsSorted() === 'asc' ? (
              <ArrowUpIcon className='ms-2 h-4 w-4' />
            ) : (
              <CaretSortIcon className='ms-2 h-4 w-4' />
            )}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='start'>
          <DropdownMenuItem onClick={() => column.toggleSorting(false)}>
            <ArrowUpIcon className='size-3.5 text-muted-foreground/70' />
            {t('components.columnHeader.asc')}
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => column.toggleSorting(true)}>
            <ArrowDownIcon className='size-3.5 text-muted-foreground/70' />
            {t('components.columnHeader.desc')}
          </DropdownMenuItem>
          {column.getCanHide() && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={() => column.toggleVisibility(false)}>
                <EyeNoneIcon className='size-3.5 text-muted-foreground/70' />
                {t('components.columnHeader.hide')}
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
