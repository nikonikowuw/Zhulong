import {
  getCoreRowModel,
  useReactTable,
  type ColumnDef,
} from '@tanstack/react-table'
import { describe, expect, it } from 'vitest'
import { render } from 'vitest-browser-react'
import '@/lib/i18n'
import { TableCell, TableRow } from '@/components/ui/table'
import { DataTableShell, type DataTableShellProps } from './shell'

type Person = { id: string; name: string }

const columns: ColumnDef<Person>[] = [
  {
    accessorKey: 'name',
    header: '姓名',
    cell: ({ row }) => row.original.name,
    meta: {
      className: 'w-40',
      thClassName: 'th-extra',
      tdClassName: 'td-extra',
    },
  },
]

const people: Person[] = [
  { id: 'p1', name: '张三' },
  { id: 'p2', name: '李四' },
]

type HarnessProps = Omit<DataTableShellProps<Person>, 'table' | 'totalRows'> & {
  data?: Person[]
  totalRows?: number
}

function Harness({ data = [], totalRows, ...props }: HarnessProps) {
  const table = useReactTable({
    data,
    columns,
    getCoreRowModel: getCoreRowModel(),
    getRowId: (row) => row.id,
  })

  return (
    <DataTableShell
      table={table}
      totalRows={totalRows ?? data.length}
      {...props}
    />
  )
}

describe('DataTableShell', () => {
  it('渲染工具栏行与容器 token，并让 meta 生效到 th / td', async () => {
    const { container } = await render(
      <Harness
        data={people}
        toolbar={<div>工具栏主体</div>}
        toolbarActions={<div>视图切换</div>}
      />
    )

    const toolbar = container.querySelector('[data-slot="data-table-toolbar"]')
    expect(toolbar?.className).toContain('flex-wrap')
    expect(toolbar?.className).toContain('justify-between')
    expect(toolbar?.textContent).toContain('工具栏主体')
    expect(toolbar?.textContent).toContain('视图切换')

    const shell = container.querySelector('[data-slot="data-table-container"]')
    expect(shell?.className).toContain('rounded-md')
    expect(shell?.className).toContain('border-border')
    expect(shell?.className).toContain('bg-card')

    expect(container.querySelector('th')?.className).toContain('th-extra')
    expect(container.querySelector('th')?.className).toContain('w-40')
    expect(container.querySelector('td')?.className).toContain('td-extra')
    expect(container.textContent).toContain('张三')
  })

  it('totalRows 为 0 时渲染空态且不渲染分页条', async () => {
    const { container } = await render(
      <Harness
        emptyState={
          <TableRow>
            <TableCell colSpan={1}>暂无数据</TableCell>
          </TableRow>
        }
      />
    )

    expect(container.textContent).toContain('暂无数据')
    // 空态铁律：0 行时不能出现任何分页控件（Page 1 of 0）。
    expect(container.querySelectorAll('button').length).toBe(0)
    expect(container.querySelectorAll('tbody tr').length).toBe(1)
  })

  it('有数据时渲染默认分页条并透传总条数', async () => {
    const { container } = await render(<Harness data={people} totalRows={42} />)

    expect(container.querySelectorAll('tbody tr').length).toBe(2)
    expect(container.querySelectorAll('button').length).toBeGreaterThan(0)
    expect(container.textContent).toContain('共 42 条')
  })

  it('加载态按 skeletonRows 渲染骨架行，且优先于空态', async () => {
    const { container } = await render(
      <Harness
        isLoading
        skeletonRows={2}
        emptyState={
          <TableRow>
            <TableCell>暂无数据</TableCell>
          </TableRow>
        }
      />
    )

    expect(container.querySelectorAll('tbody tr').length).toBe(2)
    expect(container.querySelectorAll('tbody td').length).toBe(2)
    expect(container.textContent).not.toContain('暂无数据')
  })

  it('错误态优先于空态', async () => {
    const { container } = await render(
      <Harness
        isError
        errorState={
          <TableRow>
            <TableCell>加载失败</TableCell>
          </TableRow>
        }
        emptyState={
          <TableRow>
            <TableCell>暂无数据</TableCell>
          </TableRow>
        }
      />
    )

    expect(container.textContent).toContain('加载失败')
    expect(container.textContent).not.toContain('暂无数据')
  })

  it('renderRow 可返回主行 + 展开行两行', async () => {
    const { container } = await render(
      <Harness
        data={[people[0]]}
        renderRow={(row) => (
          <>
            <TableRow>
              <TableCell>主行 {row.original.name}</TableCell>
            </TableRow>
            <TableRow>
              <TableCell>展开行</TableCell>
            </TableRow>
          </>
        )}
      />
    )

    expect(container.querySelectorAll('tbody tr').length).toBe(2)
    expect(container.textContent).toContain('主行 张三')
    expect(container.textContent).toContain('展开行')
  })
})
