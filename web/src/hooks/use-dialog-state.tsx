import { useCallback, useState } from 'react'

/**
 * 对话框开关状态。
 *
 * 语义是**赋值**而不是切换：`setOpen('faces')` 在已经打开 `'faces'` 时仍然是
 * 打开。早期实现写成 `prev === next ? null : next`，导致「已打开 A 弹窗时再点另
 * 一个 A」会把弹窗关掉（在人员画廊里表现为点第二张卡片的「人脸管理」反而关闭
 * 抽屉），排查成本极高且没有任何业务语义支持这种切换。
 *
 * setter 引用恒定（`useCallback`）：Provider 常把它放进 `useMemo` 依赖数组，
 * 每次渲染新建函数会让「仅订阅动作」的消费者全部重渲染，`memo` 行/卡片也随之
 * 失效。恒定引用是这个 Hook 的契约，不是优化。
 *
 * @param initialState 初始状态
 * @returns `[open, setOpen]`
 * @example const [open, setOpen] = useDialogState<"approve" | "reject">()
 */
export default function useDialogState<T extends string | boolean>(
  initialState: T | null = null
) {
  const [open, setOpenState] = useState<T | null>(initialState)

  const setOpen = useCallback((next: T | null) => {
    setOpenState(next)
  }, [])

  return [open, setOpen] as const
}
