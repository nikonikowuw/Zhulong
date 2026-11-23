import {
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
} from '@tanstack/react-table'
import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
import '@/lib/i18n'
import { Table, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { DataTableColumnHeader } from './column-header'

type Row = { name: string; status: string }

const columns: ColumnDef<Row, unknown>[] = [
  { accessorKey: 'name', header: '姓名', enableSorting: true },
  { accessorKey: 'status', header: '状态', enableSorting: false },
]

function Harness() {
  const table = useReactTable({
    data: [],
    columns,
    getCoreRowModel: getCoreRowModel(),
  })

  return (
    <Table>
      <TableHeader>
        {table.getHeaderGroups().map((headerGroup) => (
          <TableRow key={headerGroup.id}>
            {headerGroup.headers.map((header) => (
              <TableHead key={header.id} data-testid={`th-${header.column.id}`}>
                <DataTableColumnHeader
                  column={header.column}
                  title={String(header.column.columnDef.header)}
                />
              </TableHead>
            ))}
          </TableRow>
        ))}
      </TableHeader>
    </Table>
  )
}

function headerBox(container: HTMLElement, columnId: string) {
  const cell = container.querySelector(`[data-testid="th-${columnId}"]`)
  const box = cell?.firstElementChild
  expect(box).not.toBeNull()
  return box as HTMLElement
}

describe('DataTableColumnHeader', () => {
  it('可排序与不可排序表头的盒模型高度一致（32px）', async () => {
    const { container } = await render(<Harness />)

    const sortable = headerBox(container, 'name')
    const plain = headerBox(container, 'status')

    expect(Math.round(sortable.getBoundingClientRect().height)).toBe(32)
    expect(Math.round(plain.getBoundingClientRect().height)).toBe(32)
  })

  it('不可排序表头补齐内边距，与可排序按钮的 px-3 对齐', async () => {
    const { container } = await render(<Harness />)

    const plain = headerBox(container, 'status')
    expect(plain.className).toContain('h-8')
    expect(plain.className).toContain('items-center')
    expect(plain.className).toContain('px-3')
  })
})
