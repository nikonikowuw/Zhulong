import { DropdownMenuTrigger } from '@radix-ui/react-dropdown-menu'
import { MixerHorizontalIcon } from '@radix-ui/react-icons'
import { type Column, type Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
} from '@/components/ui/dropdown-menu'

export type DataTableViewOptionsProps<TData> = {
  table: Table<TData>
  columnLabels?: Record<string, string>
}

// 辅助函数：将驼峰/下划线命名转换为可读的单词 (用于 fallback)
function formatColumnFallback(id: string): string {
  return id
    .replace(/([A-Z])/g, ' $1')
    .replace(/[_-]/g, ' ')
    .trim()
    .replace(/^\w/, (c) => c.toUpperCase())
}

export function DataTableViewOptions<TData>({
  table,
  columnLabels,
}: DataTableViewOptionsProps<TData>) {
  const { t, i18n } = useTranslation('common')

  const getColumnLabel = (column: Column<TData, unknown>) => {
    // 1. 优先使用显式注入的 columnLabels 字典
    if (columnLabels?.[column.id]) {
      return columnLabels[column.id]
    }

    // 2. 检查 meta.title 显式声明
    const metaTitle = column.columnDef.meta?.title
    if (typeof metaTitle === 'string' && metaTitle.trim() !== '') {
      return metaTitle
    }

    // 3. 检查静态 header 字符串
    if (typeof column.columnDef.header === 'string') {
      return column.columnDef.header
    }

    // 4. 动态尝试 i18n 候选键
    const candidates = [
      `table.columns.${column.id}`,
      `cameras:table.columns.${column.id}`,
      `network:table.columns.${column.id}`,
      `common:table.columns.${column.id}`,
    ]

    for (const key of candidates) {
      if (i18n.exists(key)) {
        return t(key)
      }
    }

    // 5. 格式化 fallback
    return formatColumnFallback(column.id)
  }

  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <Button
          variant='outline'
          size='sm'
          className='ms-auto hidden h-8 lg:flex'
        >
          <MixerHorizontalIcon className='size-4' />
          {t('table.view', { defaultValue: 'View' })}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end' className='w-40'>
        <DropdownMenuLabel>
          {t('table.toggleColumns', { defaultValue: 'Toggle columns' })}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {table
          .getAllColumns()
          .filter(
            (column) =>
              typeof column.accessorFn !== 'undefined' && column.getCanHide()
          )
          .map((column) => {
            return (
              <DropdownMenuCheckboxItem
                key={column.id}
                checked={column.getIsVisible()}
                onCheckedChange={(value) => column.toggleVisibility(!!value)}
              >
                {getColumnLabel(column)}
              </DropdownMenuCheckboxItem>
            )
          })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
