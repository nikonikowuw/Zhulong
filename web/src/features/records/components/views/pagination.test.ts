import { type PaginationState } from '@tanstack/react-table'
import { describe, expect, it, vi } from 'vitest'
import { createPaginationChangeHandler } from './pagination'

/** 构造一个绑定到固定窗口的 handler 与两个 spy。 */
function setup(page: number, pageSize: number) {
  const onPageChange = vi.fn()
  const onPageSizeChange = vi.fn()
  const handler = createPaginationChangeHandler({
    page,
    pageSize,
    onPageChange,
    onPageSizeChange,
  })
  return { handler, onPageChange, onPageSizeChange }
}

describe('createPaginationChangeHandler', () => {
  it('next page only triggers onPageChange', () => {
    const { handler, onPageChange, onPageSizeChange } = setup(1, 20)

    // tanstack table.nextPage() 走的就是这条函数式 updater。
    handler((old: PaginationState) => ({
      ...old,
      pageIndex: old.pageIndex + 1,
    }))

    expect(onPageChange).toHaveBeenCalledExactlyOnceWith(2)
    // 关键回归点：这里若也调 onPageSizeChange，父级会把 page 归 1，翻页失效。
    expect(onPageSizeChange).not.toHaveBeenCalled()
  })

  it('previous page only triggers onPageChange', () => {
    const { handler, onPageChange, onPageSizeChange } = setup(3, 20)

    handler((old: PaginationState) => ({
      ...old,
      pageIndex: old.pageIndex - 1,
    }))

    expect(onPageChange).toHaveBeenCalledExactlyOnceWith(2)
    expect(onPageSizeChange).not.toHaveBeenCalled()
  })

  it('jumping to a page number only triggers onPageChange', () => {
    const { handler, onPageChange, onPageSizeChange } = setup(5, 10)

    handler({ pageIndex: 0, pageSize: 10 })

    expect(onPageChange).toHaveBeenCalledExactlyOnceWith(1)
    expect(onPageSizeChange).not.toHaveBeenCalled()
  })

  it('page size change only triggers onPageSizeChange', () => {
    const { handler, onPageChange, onPageSizeChange } = setup(1, 20)

    // tanstack setPageSize 会同时重算 pageIndex；页码归位交父级，这里只看 pageSize。
    handler((old: PaginationState) => ({ ...old, pageIndex: 2, pageSize: 50 }))

    expect(onPageSizeChange).toHaveBeenCalledExactlyOnceWith(50)
    expect(onPageChange).not.toHaveBeenCalled()
  })

  it('re-selecting the current page or page size is a no-op', () => {
    const { handler, onPageChange, onPageSizeChange } = setup(2, 20)

    handler({ pageIndex: 1, pageSize: 20 })
    handler((old: PaginationState) => ({ ...old }))

    expect(onPageChange).not.toHaveBeenCalled()
    expect(onPageSizeChange).not.toHaveBeenCalled()
  })
})
