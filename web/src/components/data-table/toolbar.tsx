import { useEffect, useRef, useState } from 'react'
import { Cross2Icon } from '@radix-ui/react-icons'
import { type Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { DataTableFacetedFilter } from './faceted-filter'
import { DataTableViewOptions } from './view-options'

type DataTableToolbarProps<TData> = {
  table: Table<TData>
  searchPlaceholder?: string
  searchKey?: string
  filters?: {
    columnId: string
    title: string
    options: {
      label: string
      value: string
      icon?: React.ComponentType<{ className?: string }>
    }[]
    /** 关闭分面计数。手动分页时计数只覆盖当前页，会被误读成全库统计。 */
    showFacets?: boolean
    /** 单选模式：勾选另一项会替换而不是累加。 */
    single?: boolean
  }[]
  columnLabels?: Record<string, string>
  /**
   * 检索输入的应用延迟（毫秒）。
   *
   * 后端检索是 URL 驱动的：每敲一个字符就 `navigate` 一次等于一串请求 +
   * 一串历史记录。给 0（默认）时保持逐字应用，供本地过滤类表格使用。
   */
  debounceMs?: number
}

/**
 * 受控检索框。
 *
 * 输入立即反映用户按键（本地 state），对外通知走 debounce；清空/重置时把本地
 * state 同步回外部值，避免外部被清空后输入框还留着一串旧字符。
 */
function ToolbarSearchInput({
  value,
  placeholder,
  debounceMs,
  onChange,
}: {
  value: string
  placeholder: string
  debounceMs: number
  onChange: (next: string) => void
}) {
  const [draft, setDraft] = useState(value)
  const [syncedValue, setSyncedValue] = useState(value)
  const onChangeRef = useRef(onChange)

  useEffect(() => {
    onChangeRef.current = onChange
  }, [onChange])

  // 外部值变化（清空筛选、切换路由、浏览器后退）时同步回输入框。
  // 必须在渲染期派生而不是 `useEffect`：否则会先画一帧旧字符再跳变。
  if (value !== syncedValue) {
    setSyncedValue(value)
    setDraft(value)
  }

  useEffect(() => {
    if (draft === value) return
    if (debounceMs <= 0) {
      onChangeRef.current(draft)
      return
    }
    const timer = setTimeout(() => onChangeRef.current(draft), debounceMs)
    return () => clearTimeout(timer)
  }, [draft, value, debounceMs])

  return (
    <Input
      placeholder={placeholder}
      value={draft}
      onChange={(event) => setDraft(event.target.value)}
      className='h-8 w-37.5 lg:w-62.5'
    />
  )
}

export function DataTableToolbar<TData>({
  table,
  searchPlaceholder = 'Filter...',
  searchKey,
  filters = [],
  columnLabels,
  debounceMs = 0,
}: DataTableToolbarProps<TData>) {
  const { t } = useTranslation('common')
  const isFiltered =
    table.getState().columnFilters.length > 0 || table.getState().globalFilter

  // `searchKey` 走列过滤，其余走全局过滤；两条路径的读写都统一到这里，
  // 避免 ToolbarSearchInput 分成两套。
  const column = searchKey ? table.getColumn(searchKey) : undefined
  const currentValue = searchKey
    ? ((column?.getFilterValue() as string | undefined) ?? '')
    : (table.getState().globalFilter ?? '')

  const applySearch = (next: string) => {
    if (searchKey) {
      column?.setFilterValue(next)
    } else {
      table.setGlobalFilter(next)
    }
  }

  return (
    <div className='flex items-center justify-between'>
      <div className='flex flex-1 flex-col-reverse items-start gap-y-2 sm:flex-row sm:items-center sm:space-x-2'>
        <ToolbarSearchInput
          value={currentValue}
          placeholder={searchPlaceholder}
          debounceMs={debounceMs}
          onChange={applySearch}
        />
        <div className='flex gap-x-2'>
          {filters.map((filter) => {
            const filterColumn = table.getColumn(filter.columnId)
            if (!filterColumn) return null
            return (
              <DataTableFacetedFilter
                key={filter.columnId}
                column={filterColumn}
                title={filter.title}
                options={filter.options}
                showFacets={filter.showFacets}
                single={filter.single}
              />
            )
          })}
        </div>
        {isFiltered && (
          <Button
            variant='ghost'
            onClick={() => {
              table.resetColumnFilters()
              table.setGlobalFilter('')
            }}
            className='h-8 px-2 lg:px-3'
          >
            {t('table.reset')}
            <Cross2Icon className='ms-2 h-4 w-4' />
          </Button>
        )}
      </div>
      <DataTableViewOptions table={table} columnLabels={columnLabels} />
    </div>
  )
}
