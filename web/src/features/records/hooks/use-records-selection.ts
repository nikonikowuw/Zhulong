import { useCallback, useMemo, useState } from 'react'
import { type RecordsTab } from '../data/schema'

export interface Identifiable {
  id: string
}

export function useRecordsSelection<T extends Identifiable>(
  currentTab: RecordsTab,
  currentPageItems: T[] = []
) {
  const [selectedMap, setSelectedMap] = useState<Map<string, T>>(
    () => new Map()
  )

  // Tab 切换时清空勾选（单 Tab 隔离原则）。
  //
  // 在 render 阶段同步重置而不是放进 useEffect：effect 在提交之后才跑，
  // 切换 Tab 的第一帧会带着上一页的计数渲染一次浮动条，视觉上就是闪一下。
  const [prevTab, setPrevTab] = useState(currentTab)
  if (prevTab !== currentTab) {
    setPrevTab(currentTab)
    setSelectedMap(new Map())
  }

  const selectedCount = selectedMap.size
  const selectedItems = useMemo(
    () => Array.from(selectedMap.values()),
    [selectedMap]
  )

  const isSelected = useCallback(
    (id: string) => selectedMap.has(id),
    [selectedMap]
  )

  const toggleItem = useCallback((item: T) => {
    setSelectedMap((prev) => {
      const next = new Map(prev)
      if (next.has(item.id)) {
        next.delete(item.id)
      } else {
        next.set(item.id, item)
      }
      return next
    })
  }, [])

  const selectItems = useCallback((items: T[]) => {
    setSelectedMap((prev) => {
      const next = new Map(prev)
      for (const item of items) {
        next.set(item.id, item)
      }
      return next
    })
  }, [])

  const deselectItems = useCallback((items: T[]) => {
    setSelectedMap((prev) => {
      const next = new Map(prev)
      for (const item of items) {
        next.delete(item.id)
      }
      return next
    })
  }, [])

  const clearSelection = useCallback(() => {
    setSelectedMap(new Map())
  }, [])

  // 当前页选中计算
  const { isAllPageSelected, isPartiallySelected } = useMemo(() => {
    if (!currentPageItems || currentPageItems.length === 0) {
      return { isAllPageSelected: false, isPartiallySelected: false }
    }
    const pageSelectedCount = currentPageItems.filter((item) =>
      selectedMap.has(item.id)
    ).length

    const isAll = pageSelectedCount === currentPageItems.length
    const isPartial = pageSelectedCount > 0 && !isAll
    return { isAllPageSelected: isAll, isPartiallySelected: isPartial }
  }, [currentPageItems, selectedMap])

  // 切换当前页全选 / 全不选
  const toggleSelectAllCurrentPage = useCallback(() => {
    if (isAllPageSelected) {
      deselectItems(currentPageItems)
    } else {
      selectItems(currentPageItems)
    }
  }, [isAllPageSelected, deselectItems, selectItems, currentPageItems])

  return {
    selectedMap,
    selectedCount,
    selectedItems,
    isSelected,
    toggleItem,
    selectItems,
    deselectItems,
    clearSelection,
    isAllPageSelected,
    isPartiallySelected,
    toggleSelectAllCurrentPage,
  }
}
