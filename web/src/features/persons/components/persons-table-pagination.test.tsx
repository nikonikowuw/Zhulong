import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { render } from 'vitest-browser-react'
import { type NavigateFn } from '@/hooks/use-table-url-state'
import { type Person } from '../data/schema'
import { PersonsProvider } from './persons-provider'
import { PersonsTable } from './persons-table'

/**
 * 深链分页的回归用例。
 *
 * 刻意**不** mock `@/hooks/use-table-url-state`：要验证的正是「表格是否在错误
 * 的时机调用了真实的 `ensurePageInRange`」，mock 掉等于把被测对象换成桩。
 */

function makePerson(id: string): Person {
  return {
    id,
    personId: id.toUpperCase(),
    name: 'Alice',
    source: 'LOCAL',
    sourceName: '',
    faces: [],
    createdAt: '2026-10-14T08:00:00Z',
    updatedAt: '2026-10-14T08:00:00Z',
  }
}

function renderTable(props: {
  data: Person[]
  total: number
  isLoading?: boolean
  search?: Record<string, unknown>
  navigate: NavigateFn & ReturnType<typeof vi.fn>
}) {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <PersonsProvider>
        <PersonsTable
          data={props.data}
          total={props.total}
          isLoading={props.isLoading ?? false}
          isError={false}
          onRetry={vi.fn()}
          search={props.search ?? {}}
          navigate={props.navigate}
        />
      </PersonsProvider>
    </QueryClientProvider>
  )
}

describe('deep link pagination', () => {
  it('does not clobber ?page=3 while the first fetch is still in flight', async () => {
    // 回归：`pageCount = Math.ceil(total / pageSize) || 1` 在 total=0 时等于 1，
    // 挂载即触发 `ensurePageInRange(1)` → `navigate({ replace: true })` 把
    // `?page=3` 抹掉。分享链接与刷新都会静默跳到第 1 页，而且回不去。
    const navigate = vi.fn() as unknown as NavigateFn & ReturnType<typeof vi.fn>
    await renderTable({
      data: [],
      total: 0,
      isLoading: true,
      search: { page: 3, pageSize: 10 },
      navigate,
    })

    expect(navigate).not.toHaveBeenCalled()
  })

  it('clamps an out-of-range page once the real total arrives', async () => {
    // 反向保证：数据到货后仍要夹紧，否则会停在一个不存在的页码上。
    const navigate = vi.fn() as unknown as NavigateFn & ReturnType<typeof vi.fn>
    await renderTable({
      data: [makePerson('p1')],
      total: 5,
      search: { page: 9, pageSize: 10 },
      navigate,
    })

    expect(navigate).toHaveBeenCalled()
    const calls = navigate.mock.calls
    const [opts] = calls[calls.length - 1]!
    expect(opts.replace).toBe(true)
    const next = (
      opts.search as (p: Record<string, unknown>) => Record<string, unknown>
    )({
      page: 9,
      pageSize: 10,
    })
    expect(next.page).toBeUndefined()
  })

  it('leaves an in-range deep link alone', async () => {
    const navigate = vi.fn() as unknown as NavigateFn & ReturnType<typeof vi.fn>
    await renderTable({
      data: [makePerson('p1')],
      total: 45,
      search: { page: 3, pageSize: 10 },
      navigate,
    })

    expect(navigate).not.toHaveBeenCalled()
  })
})
