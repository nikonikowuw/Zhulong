import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  type Column,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { DataTableFacetedFilter } from './faceted-filter'

type Row = { id: string; status: string }

const columns: Column<Row, unknown>[] = []

function Harness({
  filterValue,
  onChange,
  single,
}: {
  filterValue: unknown
  onChange: (next: unknown) => void
  single?: boolean
}) {
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable<Row>({
    data: [{ id: 'a', status: 'enrolled' }],
    columns: [
      {
        id: 'status',
        accessorKey: 'status',
        header: 'Status',
      },
    ],
    getCoreRowModel: getCoreRowModel(),
    state: { columnFilters: [{ id: 'status', value: filterValue }] },
    onColumnFiltersChange: (updater) => {
      const next =
        typeof updater === 'function'
          ? updater([{ id: 'status', value: filterValue }])
          : updater
      onChange(next.find((f) => f.id === 'status')?.value)
    },
    manualFiltering: true,
  })
  void columns

  return (
    <DataTableFacetedFilter
      column={table.getColumn('status')}
      title='Status'
      options={[
        { label: 'Enrolled', value: 'enrolled' },
        { label: 'Missing', value: 'missing' },
      ]}
      showFacets={false}
      single={single}
    />
  )
}

describe('DataTableFacetedFilter', () => {
  it('reads a string filter value without splitting it into characters', async () => {
    // 回归：`new Set(value as string[])` 会把 'enrolled' 拆成单字符集合，
    // 于是每个选项都不匹配，触发器永远显示"未选中"。
    const onChange = vi.fn()
    const screen = await render(
      <QueryClientProvider client={new QueryClient()}>
        <Harness filterValue='enrolled' onChange={onChange} />
      </QueryClientProvider>
    )

    await expect
      .element(screen.getByRole('button', { name: /Status/ }))
      .toBeInTheDocument()
    // 已选中的标签渲染在触发器里
    await expect.element(screen.getByText('Enrolled')).toBeInTheDocument()
  })

  it('writes a single string when single mode is on', async () => {
    const onChange = vi.fn()
    const screen = await render(
      <QueryClientProvider client={new QueryClient()}>
        <Harness filterValue={undefined} onChange={onChange} single />
      </QueryClientProvider>
    )

    await screen.getByRole('button', { name: /Status/ }).click()
    await screen.getByText('Missing').click()

    expect(onChange).toHaveBeenCalledWith('missing')
  })

  it('clears the filter when the selected option is clicked again', async () => {
    const onChange = vi.fn()
    const screen = await render(
      <QueryClientProvider client={new QueryClient()}>
        <Harness filterValue='enrolled' onChange={onChange} single />
      </QueryClientProvider>
    )

    await screen.getByRole('button', { name: /Status/ }).click()
    // 触发器里也有一个 "Enrolled" 标签，取下拉列表里的那一个。
    const option = screen.getByRole('option', { name: /Enrolled/ })
    await option.click()

    expect(onChange).toHaveBeenCalledWith(undefined)
  })
})
