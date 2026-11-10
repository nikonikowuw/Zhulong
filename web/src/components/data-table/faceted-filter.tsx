import * as React from 'react'
import { CheckIcon, PlusCircledIcon } from '@radix-ui/react-icons'
import { type Column } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import { Separator } from '@/components/ui/separator'

type DataTableFacetedFilterProps<TData, TValue> = {
  column?: Column<TData, TValue>
  title?: string
  options: {
    label: string
    value: string
    icon?: React.ComponentType<{ className?: string }>
  }[]
  /** 关闭分面计数。手动分页时计数只覆盖当前页，会被读成全库统计。 */
  showFacets?: boolean
  /**
   * 单选模式。
   *
   * 对后端只接受单值的筛选项（如 `faceStatus`）必须开启：多选在提交时会被压成
   * 一个值，界面上却显示两项都生效 —— 那是最糟的失败方式，用户以为在筛，实际
   * 拿到的是全量。
   */
  single?: boolean
}

/**
 * 把列过滤值归一化成字符串数组。
 *
 * `useTableUrlState` 的 column filter 有 string 与 array 两种形态，而这里同时
 * 服务于两者。直接 `new Set(value as string[])` 会把字符串拆成单字符集合，
 * 于是 `enrolled` 变成 `{'e','n','r','o','l','d'}` —— 每个选项都不匹配，筛选
 * 触发器永远显示"未选中"。
 */
function toSelectedValues(raw: unknown): string[] {
  if (Array.isArray(raw)) {
    return raw.filter((v): v is string => typeof v === 'string')
  }
  if (typeof raw === 'string' && raw !== '') {
    return [raw]
  }
  return []
}

export function DataTableFacetedFilter<TData, TValue>({
  column,
  title,
  options,
  showFacets = true,
  single = false,
}: DataTableFacetedFilterProps<TData, TValue>) {
  const { t } = useTranslation('common')
  const facets = showFacets ? column?.getFacetedUniqueValues() : undefined
  const selectedValues = toSelectedValues(column?.getFilterValue())

  const commit = (next: string[]) => {
    if (next.length === 0) {
      column?.setFilterValue(undefined)
      return
    }
    // 单选列过滤写回字符串，多选写回数组：`useTableUrlState` 会根据
    // `type: 'string' | 'array'` 分别序列化到 URL。
    column?.setFilterValue(single ? next[0] : next)
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant='outline' size='sm' className='h-8 border-dashed'>
          <PlusCircledIcon className='size-4' />
          {title}
          {selectedValues.length > 0 && (
            <>
              <Separator orientation='vertical' className='mx-2 h-4' />
              <Badge
                variant='secondary'
                className='rounded-sm px-1 font-normal lg:hidden'
              >
                {selectedValues.length}
              </Badge>
              <div className='hidden gap-1 lg:flex'>
                {selectedValues.length > 2 ? (
                  <Badge
                    variant='secondary'
                    className='rounded-sm px-1 font-normal'
                  >
                    {t('table.selectedCount', { count: selectedValues.length })}
                  </Badge>
                ) : (
                  options
                    .filter((option) => selectedValues.includes(option.value))
                    .map((option) => (
                      <Badge
                        variant='secondary'
                        key={option.value}
                        className='rounded-sm px-1 font-normal'
                      >
                        {option.label}
                      </Badge>
                    ))
                )}
              </div>
            </>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent className='w-50 p-0' align='start'>
        <Command>
          <CommandInput placeholder={title} />
          <CommandList>
            <CommandEmpty>{t('filter.noResults')}</CommandEmpty>
            <CommandGroup>
              {options.map((option) => {
                const isSelected = selectedValues.includes(option.value)
                return (
                  <CommandItem
                    key={option.value}
                    onSelect={() => {
                      if (single) {
                        commit(isSelected ? [] : [option.value])
                        return
                      }
                      const next = isSelected
                        ? selectedValues.filter((v) => v !== option.value)
                        : [...selectedValues, option.value]
                      commit(next)
                    }}
                  >
                    <div
                      className={cn(
                        'flex size-4 items-center justify-center border border-primary',
                        // 单选渲染成圆点，多选渲染成方框：形状本身就是"能选几个"的提示。
                        single ? 'rounded-full' : 'rounded-sm',
                        isSelected
                          ? 'bg-primary text-primary-foreground'
                          : 'opacity-50 [&_svg]:invisible'
                      )}
                    >
                      <CheckIcon className={cn('h-4 w-4 text-background')} />
                    </div>
                    {option.icon && (
                      <option.icon className='size-4 text-muted-foreground' />
                    )}
                    <span>{option.label}</span>
                    {facets?.get(option.value) && (
                      <span className='ms-auto flex h-4 w-4 items-center justify-center font-mono text-xs'>
                        {facets.get(option.value)}
                      </span>
                    )}
                  </CommandItem>
                )
              })}
            </CommandGroup>
            {selectedValues.length > 0 && (
              <>
                <CommandSeparator />
                <CommandGroup>
                  <CommandItem
                    onSelect={() => column?.setFilterValue(undefined)}
                    className='justify-center text-center'
                  >
                    {t('filter.clearFilters')}
                  </CommandItem>
                </CommandGroup>
              </>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
