import { type OnChangeFn, type PaginationState } from '@tanstack/react-table'

/** `createPaginationChangeHandler` 的输入：当前窗口 + 父级的两个跳转入口。 */
export interface RecordsPaginationChangeOptions {
  page: number
  pageSize: number
  onPageChange: (page: number) => void
  onPageSizeChange: (pageSize: number) => void
}

/**
 * 把 tanstack-table 的 `onPaginationChange` 映射成「一次交互 = 一次路由跳转」。
 *
 * 为什么不能同时调用两个回调：父级的 `onPageSizeChange` 会顺带把 `page` 归 1
 * （改每页行数时回到第一页是产品预期）。于是点「下一页」时页码先被写成 2、
 * 又被写回 1，URL 与数据都停在第 1 页 —— 翻页看起来毫无反应，而后端收到的
 * 请求完全合法，日志里查不出任何异常。
 *
 * 这里按实际变化的维度择一触发：pageSize 变则只走 pageSize 分支（由父级归 1
 * 页码），否则页码变才走翻页分支。两者都没变（点当前页）不发请求。
 */
export function createPaginationChangeHandler({
  page,
  pageSize,
  onPageChange,
  onPageSizeChange,
}: RecordsPaginationChangeOptions): OnChangeFn<PaginationState> {
  return (updater) => {
    const current: PaginationState = {
      pageIndex: Math.max(0, page - 1),
      pageSize,
    }
    const next = typeof updater === 'function' ? updater(current) : updater

    if (next.pageSize !== current.pageSize) {
      onPageSizeChange(next.pageSize)
      return
    }
    if (next.pageIndex !== current.pageIndex) {
      onPageChange(next.pageIndex + 1)
    }
  }
}
