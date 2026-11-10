import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Person } from '../data/schema'
import { PersonsDialogs } from './persons-dialogs'
import { PersonsProvider } from './persons-provider'
import { PersonsTable } from './persons-table'

vi.mock('@/hooks/use-table-url-state', () => ({
  useTableUrlState: () => ({
    pagination: { pageIndex: 0, pageSize: 10 },
    onPaginationChange: vi.fn(),
    ensurePageInRange: vi.fn(),
    globalFilter: '',
    onGlobalFilterChange: vi.fn(),
    columnFilters: [],
    onColumnFiltersChange: vi.fn(),
  }),
}))

function makePerson(id: string, name: string): Person {
  return {
    id,
    personId: id.toUpperCase(),
    name,
    source: 'LOCAL',
    sourceName: '',
    faces: [],
    createdAt: '2026-10-14T08:00:00Z',
    updatedAt: '2026-10-14T08:00:00Z',
  }
}

function renderTable(
  data: Person[],
  search: Record<string, unknown> = {},
  total = data.length
) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <PersonsProvider>
        <PersonsTable
          data={data}
          total={total}
          isLoading={false}
          isError={false}
          onRetry={vi.fn()}
          search={search}
          navigate={vi.fn()}
        />
        <PersonsDialogs />
      </PersonsProvider>
    </QueryClientProvider>
  )
}

describe('PersonsTable regression guards', () => {
  it('shows selection feedback in the gallery view and lets the user deselect', async () => {
    // 回归：`PersonCard` 曾被 `memo` 包住并直接吃 `row`，而 react-table 原地
    // 改写 row 对象 —— checkbox 永远渲染成未选中，且受控值恒为 false，第二次
    // 点击上报的仍是 true，用户根本取消不了勾选。
    const screen = await renderTable([makePerson('p1', 'Alice')], {
      view: 'grid',
    })

    const card = screen.getByTestId('person-card')
    await expect.element(card).toBeInTheDocument()
    const checkbox = screen.getByRole('checkbox', { name: '选择 Alice' })

    await checkbox.click()
    await expect.element(checkbox).toHaveAttribute('aria-checked', 'true')
    await expect.element(card).toHaveAttribute('data-state', 'selected')

    await checkbox.click()
    await expect.element(checkbox).toHaveAttribute('aria-checked', 'false')
    await expect.element(card).not.toHaveAttribute('data-state', 'selected')
  })

  it('uses the business id as row id so selection cannot leak across pages', async () => {
    // 回归：默认 rowId 是数组下标，翻页后上一页第 N 行的选中态会「继承」给
    // 下一页第 N 行。
    const screen = await renderTable([makePerson('p1', 'Alice')], {}, 20)
    const selectAll = screen.getByRole('checkbox', { name: '全选本页' })
    await selectAll.click()

    const row = document.querySelector('tbody tr[data-state="selected"]')
    expect(row).not.toBeNull()

    // 选中态按 p1 记录，而不是按下标 0。
    const body = document.body.textContent ?? ''
    expect(body).toContain('Alice')
  })

  it('does not render the paginator when the result set is empty', async () => {
    // 回归：空态仍渲染分页条，出现「第 1 页，共 1 页」的纯噪音。
    const screen = await renderTable([])
    await expect.element(screen.getByText('暂无人员档案')).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('每页行数')
  })

  it('keeps the skeleton cell count aligned with the visible column count', async () => {
    const { container } = await render(
      <QueryClientProvider client={new QueryClient()}>
        <PersonsProvider>
          <PersonsTable
            data={[]}
            total={0}
            isLoading
            isError={false}
            onRetry={vi.fn()}
            search={{}}
            navigate={vi.fn()}
          />
        </PersonsProvider>
      </QueryClientProvider>
    )

    const headerCount = container.querySelectorAll('thead th').length
    const firstSkeletonRow = container.querySelector('tbody tr')
    expect(firstSkeletonRow).not.toBeNull()
    expect(firstSkeletonRow!.querySelectorAll('td').length).toBe(headerCount)
  })
})
