import { type ColumnDef } from '@tanstack/react-table'
import { SelectionHeaderCell, SelectionRowCell } from './selection-column'

/**
 * 构建稳定的选择列：勾选态通过 `TableSelectionContext` 订阅，
 * 避免每次单选/全选都重建整张表的 `columns` 数组。
 */
export function createSelectionColumn<
  T extends { id: string },
>(): ColumnDef<T> {
  return {
    id: 'select',
    header: () => <SelectionHeaderCell />,
    cell: ({ row }) => <SelectionRowCell item={row.original} />,
    size: 40,
    enableSorting: false,
  }
}
