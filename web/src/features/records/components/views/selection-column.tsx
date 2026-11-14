import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Checkbox } from '@/components/ui/checkbox'

export interface TableSelectionState<T> {
  isSelected?: (id: string) => boolean
  onToggleSelect?: (item: T) => void
  isAllPageSelected?: boolean
  isPartiallySelected?: boolean
  onToggleSelectAll?: () => void
}

const TableSelectionContext = createContext<TableSelectionState<unknown>>({})

export function TableSelectionProvider<T>({
  isSelected,
  onToggleSelect,
  isAllPageSelected,
  isPartiallySelected,
  onToggleSelectAll,
  children,
}: TableSelectionState<T> & { children: ReactNode }) {
  const value = useMemo<TableSelectionState<unknown>>(
    () => ({
      isSelected,
      onToggleSelect: onToggleSelect as ((item: unknown) => void) | undefined,
      isAllPageSelected,
      isPartiallySelected,
      onToggleSelectAll,
    }),
    [
      isSelected,
      onToggleSelect,
      isAllPageSelected,
      isPartiallySelected,
      onToggleSelectAll,
    ]
  )

  return (
    <TableSelectionContext.Provider value={value}>
      {children}
    </TableSelectionContext.Provider>
  )
}

export function SelectionHeaderCell() {
  const { t } = useTranslation('records')
  const { isAllPageSelected, isPartiallySelected, onToggleSelectAll } =
    useContext(TableSelectionContext)

  return (
    <div className='flex items-center justify-center px-1'>
      <Checkbox
        checked={
          isAllPageSelected
            ? true
            : isPartiallySelected
              ? 'indeterminate'
              : false
        }
        onCheckedChange={() => onToggleSelectAll?.()}
        aria-label={t('selection.selectAll')}
        className='size-4'
      />
    </div>
  )
}

export function SelectionRowCell<T extends { id: string }>({
  item,
}: {
  item: T
}) {
  const { t } = useTranslation('records')
  const { isSelected, onToggleSelect } = useContext(TableSelectionContext)

  return (
    <div
      className='flex items-center justify-center px-1'
      onClick={(e) => e.stopPropagation()}
    >
      <Checkbox
        checked={isSelected?.(item.id) ?? false}
        onCheckedChange={() => onToggleSelect?.(item)}
        aria-label={t('selection.selectRow', { id: item.id })}
        className='size-4'
      />
    </div>
  )
}
