import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { type RegionTool } from '../components/region-drag-types'
import {
  MAX_RECTS_PER_CATEGORY,
  draftItems,
  emptyDraft,
  fromDraft,
  parseRegionConfig,
  serializeRegionConfig,
  toDraft,
  withDraftItems,
  type Rect,
  type RegionCategory,
  type RegionDraft,
  type RegionSelection,
} from '../data/region-config'

/** 撤销栈上限：一次会话内的绘制操作远达不到，仅用于兜住异常长会话的内存。 */
const MAX_HISTORY = 50

export interface RegionEditorController {
  draft: RegionDraft
  selected: RegionSelection | null
  tool: RegionTool
  canUndo: boolean
  isDirty: boolean
  counts: { roi: number; mask: number }
  /** 载入新的基线配置（打开弹窗时调用），并清空撤销历史。 */
  reset: (value: string) => void
  setTool: (tool: RegionTool) => void
  select: (selection: RegionSelection | null) => void
  changeDraft: (next: RegionDraft) => void
  createRect: (category: RegionCategory, rect: Rect) => void
  deleteRect: (category: RegionCategory, index: number) => void
  /** 在一次操作真正变更前压入撤销快照。 */
  snapshot: () => void
  undo: () => void
  /** 序列化为 wire 契约 JSON。 */
  serialize: () => string
}

/**
 * 区域编辑器的状态容器：草稿、选中项、工具、撤销历史。
 *
 * 稳定 id 由本 hook 内的自增 ref 生成，因此 id 只存在于编辑期、不进入 wire 契约，
 * 也不会引入模块级可变状态。
 */
export function useRegionEditor(): RegionEditorController {
  const idSeqRef = useRef(0)
  const makeId = useCallback(() => {
    idSeqRef.current += 1
    return `region-${idSeqRef.current}`
  }, [])

  const [draft, setDraft] = useState<RegionDraft>(emptyDraft)
  const [baseline, setBaseline] = useState<RegionDraft>(emptyDraft)
  const [history, setHistory] = useState<RegionDraft[]>([])
  const [selected, setSelected] = useState<RegionSelection | null>(null)
  const [tool, setTool] = useState<RegionTool>('select')

  // 事件回调里读取「最新草稿/历史」，避免把它们塞进 useCallback 依赖导致回调频繁重建。
  // 必须在 effect 里同步：渲染期写 ref 会触发 react-hooks/refs。
  const draftRef = useRef(draft)
  useEffect(() => {
    draftRef.current = draft
  }, [draft])
  const historyRef = useRef(history)
  useEffect(() => {
    historyRef.current = history
  }, [history])

  const reset = useCallback(
    (value: string) => {
      const next = toDraft(parseRegionConfig(value), makeId)
      setDraft(next)
      setBaseline(next)
      setHistory([])
      setSelected(null)
      setTool('select')
    },
    [makeId]
  )

  const snapshot = useCallback(() => {
    setHistory((prev) => {
      const next = [...prev, draftRef.current]
      return next.length > MAX_HISTORY
        ? next.slice(next.length - MAX_HISTORY)
        : next
    })
  }, [])

  const undo = useCallback(() => {
    const prev = historyRef.current
    if (prev.length === 0) return
    setDraft(prev[prev.length - 1])
    setHistory(prev.slice(0, -1))
    setSelected(null)
  }, [])

  const changeDraft = useCallback((next: RegionDraft) => {
    setDraft(next)
  }, [])

  const createRect = useCallback(
    (category: RegionCategory, rect: Rect) => {
      const items = draftItems(draftRef.current, category)
      if (items.length >= MAX_RECTS_PER_CATEGORY) return
      const next = withDraftItems(draftRef.current, category, [
        ...items,
        { id: makeId(), rect },
      ])
      setDraft(next)
      setSelected({ category, index: items.length })
    },
    [makeId]
  )

  const deleteRect = useCallback(
    (category: RegionCategory, index: number) => {
      snapshot()
      const items = draftItems(draftRef.current, category)
      setDraft(
        withDraftItems(
          draftRef.current,
          category,
          items.filter((_, i) => i !== index)
        )
      )
      setSelected(null)
    },
    [snapshot]
  )

  const select = useCallback((selection: RegionSelection | null) => {
    setSelected(selection)
  }, [])

  const isDirty = useMemo(
    () =>
      serializeRegionConfig(fromDraft(draft)) !==
      serializeRegionConfig(fromDraft(baseline)),
    [draft, baseline]
  )

  const counts = useMemo(
    () => ({ roi: draft.roi.length, mask: draft.mask.length }),
    [draft]
  )

  const serialize = useCallback(
    () => serializeRegionConfig(fromDraft(draftRef.current)),
    []
  )

  return {
    draft,
    selected,
    tool,
    canUndo: history.length > 0,
    isDirty,
    counts,
    reset,
    setTool,
    select,
    changeDraft,
    createRect,
    deleteRect,
    snapshot,
    undo,
    serialize,
  }
}
