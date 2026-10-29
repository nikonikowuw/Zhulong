import {
  type ColumnDef,
  getCoreRowModel,
  useReactTable,
} from '@tanstack/react-table'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { DataTablePagination } from './pagination'

type Row = { id: number }
const columns: ColumnDef<Row>[] = [{ accessorKey: 'id' }]

/**
 * 复现真实布局：分页条自己不是容器，`@2xl/content` 这类查询以
 * `SidebarInset` 上的 `@container/content` 为参照（见 authenticated-layout.tsx）。
 * 不给这个容器，两段页码块会同时可见，测试就测不到真实的二选一行为。
 */
function Harness({ width, totalPages }: { width: number; totalPages: number }) {
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable<Row>({
    data: [],
    columns,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    pageCount: totalPages,
    state: { pagination: { pageIndex: 0, pageSize: 20 } },
  })
  return (
    <div className='@container/content' style={{ width }}>
      <DataTablePagination table={table} />
    </div>
  )
}

/** 文本实际占用的行数：换行时 Range 会返回多个 line box rect。 */
function lineCount(el: Element): number {
  const range = document.createRange()
  range.selectNodeContents(el)
  return range.getClientRects().length
}

function pageIndicators(container: Element): Element[] {
  return Array.from(container.querySelectorAll('div')).filter(
    (el) =>
      el.children.length === 0 &&
      /^第 \d+ 页，共 \d+ 页$/.test(el.textContent ?? '')
  )
}

/**
 * 等样式表落到 DOM 之后再测量：Vite 注入 CSS 是异步的，`render` 返回时
 * container query 可能还没生效，此时量到的是两段页码块同时可见的中间态。
 */
async function waitForSingleVisibleIndicator(
  container: Element
): Promise<Element> {
  return vi.waitFor(() => {
    const visible = pageIndicators(container).filter(
      (el) => el.getBoundingClientRect().height > 0
    )
    expect(visible).toHaveLength(1)
    return visible[0]
  })
}

describe('DataTablePagination page indicator', () => {
  it('keeps 第 1 页，共 223 页 on a single line at desktop width', async () => {
    const { container } = await render(
      <Harness width={1024} totalPages={223} />
    )

    const el = await waitForSingleVisibleIndicator(container)
    expect(el.textContent).toBe('第 1 页，共 223 页')
    expect(lineCount(el)).toBe(1)
    // 不换行也不能溢出：内容宽度必须被容器容纳。
    const rect = el.getBoundingClientRect()
    expect(el.scrollWidth).toBeLessThanOrEqual(Math.ceil(rect.width) + 1)
  })

  it('keeps the indicator on one line in the compact layout', async () => {
    const { container } = await render(<Harness width={600} totalPages={223} />)

    const el = await waitForSingleVisibleIndicator(container)
    expect(lineCount(el)).toBe(1)
  })

  it('does not wrap even with a five-digit total', async () => {
    const { container } = await render(
      <Harness width={1024} totalPages={123456} />
    )

    const el = await waitForSingleVisibleIndicator(container)
    expect(lineCount(el)).toBe(1)
  })
})
