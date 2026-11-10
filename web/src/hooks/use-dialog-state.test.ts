import { describe, expect, it, vi } from 'vitest'
import { renderHook } from 'vitest-browser-react'
import useDialogState from '@/hooks/use-dialog-state'
import { type NavigateFn, useTableUrlState } from '@/hooks/use-table-url-state'

function lastNavigateOpts(navigate: ReturnType<typeof vi.fn>) {
  const calls = navigate.mock.calls
  return calls[calls.length - 1]?.[0]
}

function applyLastSearchFn(
  navigate: ReturnType<typeof vi.fn>,
  prev: Record<string, unknown>
) {
  const opts = lastNavigateOpts(navigate)
  if (!opts) return undefined
  const s = opts.search
  if (typeof s === 'function') {
    return s(prev) as Record<string, unknown>
  }
  return s as Record<string, unknown>
}

describe('useDialogState', () => {
  it('keeps the setter identity stable across renders', async () => {
    // Provider 把 setter 放进 useMemo 依赖数组：引用一变，「只订阅动作」的
    // 消费者（表格行、画廊卡片）全部重渲染，memo 随之失效。
    const { result, act } = await renderHook(() => useDialogState<'a' | 'b'>())
    const first = result.current[1]
    await act(() => first('a'))
    expect(result.current[1]).toBe(first)
  })

  it('assigns instead of toggling, so re-selecting the open dialog keeps it open', async () => {
    // 旧实现是 `prev === next ? null : next`：在人员画廊里点第二张卡片的
    // 「人脸管理」会把已经打开的抽屉关掉。
    const { result, act } = await renderHook(() => useDialogState<'faces'>())
    await act(() => result.current[1]('faces'))
    expect(result.current[0]).toBe('faces')
    await act(() => result.current[1]('faces'))
    expect(result.current[0]).toBe('faces')
    await act(() => result.current[1](null))
    expect(result.current[0]).toBeNull()
  })
})

describe('useTableUrlState URL hardening', () => {
  it('clamps pageSize=0 so the paginator cannot compute Infinity pages', async () => {
    // `Math.ceil(total / 0)` 是 Infinity：分页条会渲染「共 Infinity 页」。
    const { result } = await renderHook(() =>
      useTableUrlState({
        search: { pageSize: 0 },
        navigate: vi.fn() as unknown as NavigateFn,
        pagination: { defaultPage: 1, defaultPageSize: 10 },
      })
    )
    expect(result.current.pagination.pageSize).toBe(10)
  })

  it('clamps pageSize above the backend max and non-finite values', async () => {
    const { result: huge } = await renderHook(() =>
      useTableUrlState({
        search: { pageSize: 5000 },
        navigate: vi.fn() as unknown as NavigateFn,
        pagination: { defaultPage: 1, defaultPageSize: 10, maxPageSize: 100 },
      })
    )
    expect(huge.current.pagination.pageSize).toBe(100)

    const { result: nan } = await renderHook(() =>
      useTableUrlState({
        search: { pageSize: Number.NaN },
        navigate: vi.fn() as unknown as NavigateFn,
        pagination: { defaultPage: 1, defaultPageSize: 10 },
      })
    )
    expect(nan.current.pagination.pageSize).toBe(10)
  })

  it('clamps a negative or zero page back to the first page', async () => {
    const { result } = await renderHook(() =>
      useTableUrlState({
        search: { page: 0 },
        navigate: vi.fn() as unknown as NavigateFn,
        pagination: { defaultPage: 1, defaultPageSize: 10 },
      })
    )
    expect(result.current.pagination.pageIndex).toBe(0)
  })

  it('applies the global filter with replace so typing does not spam history', async () => {
    const navigate = vi.fn()
    const { result, act } = await renderHook(() =>
      useTableUrlState({
        search: {},
        navigate: navigate as unknown as NavigateFn,
        pagination: { defaultPage: 1, defaultPageSize: 10 },
        globalFilter: { enabled: true, key: 'search' },
      })
    )

    await act(() => result.current.onGlobalFilterChange?.('a'))
    await act(() => result.current.onGlobalFilterChange?.('al'))

    expect(navigate.mock.calls.every(([opts]) => opts.replace === true)).toBe(
      true
    )
    expect(applyLastSearchFn(navigate, { search: 'a' })).toMatchObject({
      search: 'al',
    })
  })

  it('does not use replace for pagination, so back/forward still walks pages', async () => {
    const navigate = vi.fn()
    const { result, act } = await renderHook(() =>
      useTableUrlState({
        search: {},
        navigate: navigate as unknown as NavigateFn,
        pagination: { defaultPage: 1, defaultPageSize: 10 },
      })
    )

    await act(() =>
      result.current.onPaginationChange({ pageIndex: 1, pageSize: 10 })
    )

    expect(lastNavigateOpts(navigate)?.replace).toBeUndefined()
  })
})
