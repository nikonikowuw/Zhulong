import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { type RegionTool } from '../components/region-drag-types'
import {
  MAX_RECTS_PER_CATEGORY,
  cropEqual,
  draftItems,
  fromDraft,
  isFullFrame,
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

/** 未配置区域时的 wire 契约字面量。 */
const EMPTY_REGION_JSON = '{}'

export interface RegionEditorController {
  draft: RegionDraft
  selected: RegionSelection | null
  tool: RegionTool
  canUndo: boolean
  isDirty: boolean
  counts: { roi: number; mask: number }
  /** 任务级裁剪窗口；`null` = 全幅。与 ROI/MASK 同处一个撤销栈。 */
  crop: Rect | null
  /** 载入新的 ROI 基线（切换算法时调用），并清空撤销历史。**不动 crop**。 */
  reset: (value: string) => void
  /** 载入新的 crop 基线（打开弹窗/切换相机时调用），并清空撤销历史。 */
  resetCrop: (value: Rect | null) => void
  setTool: (tool: RegionTool) => void
  select: (selection: RegionSelection | null) => void
  changeDraft: (next: RegionDraft) => void
  createRect: (category: RegionCategory, rect: Rect) => void
  deleteRect: (category: RegionCategory, index: number) => void
  /** 替换裁剪窗口；调用方负责先 {@link snapshot}。 */
  setCrop: (rect: Rect | null) => void
  /** 清空裁剪窗口（自带快照，可直接绑到按钮）。 */
  clearCrop: () => void
  /** 在一次操作真正变更前压入撤销快照（区域与裁剪一起压）。 */
  snapshot: () => void
  undo: () => void
  /** 序列化为 wire 契约 JSON。 */
  serialize: () => string
  /** 序列化裁剪窗口；整帧折叠为 `null`。 */
  serializeCrop: () => Rect | null
}

/** 一次撤销快照同时覆盖区域与裁剪：两套独立栈会让一次 Ctrl+Z 只回退一半。 */
interface EditorSnapshot {
  draft: RegionDraft
  crop: Rect | null
}

/**
 * 区域编辑器的状态容器：草稿、选中项、工具、撤销历史。
 *
 * 稳定 id 由本 hook 内的自增 ref 生成，因此 id 只存在于编辑期、不进入 wire 契约，
 * 也不会引入模块级可变状态。
 *
 * `initialValue` 只在**首次挂载**时读取。刻意不做「值变了就 setState」的同步 effect：
 * 那样会把外部真源与编辑缓冲变成双写。切换编辑对象时由调用方显式调 {@link reset}。
 */
export function useRegionEditor(
  initialValue: string = EMPTY_REGION_JSON,
  initialCrop: Rect | null = null
): RegionEditorController {
  const idSeqRef = useRef(0)
  const makeId = useCallback(() => {
    idSeqRef.current += 1
    return `region-${idSeqRef.current}`
  }, [])

  // 初始 id 由局部序号派生：渲染期不得读 ref（react-hooks/refs），
  // 而 `init-*` 前缀与运行期 `makeId` 的 `region-*` 天然不冲突。
  const [draft, setDraft] = useState<RegionDraft>(() => {
    let seq = 0
    return toDraft(parseRegionConfig(initialValue), () => `init-${++seq}`)
  })
  const [baseline, setBaseline] = useState<RegionDraft>(() => {
    let seq = 0
    return toDraft(parseRegionConfig(initialValue), () => `init-${++seq}`)
  })
  const [crop, setCropState] = useState<Rect | null>(initialCrop)
  const [cropBaseline, setCropBaseline] = useState<Rect | null>(initialCrop)
  const [history, setHistory] = useState<EditorSnapshot[]>([])
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
  const cropRef = useRef(crop)
  useEffect(() => {
    cropRef.current = crop
  }, [crop])

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

  // ⚠️ 切算法会调 reset()，但 crop 属于任务而不是算法：这里绝不碰 crop，
  // 否则「切一次算法 = 清空裁剪窗口」。
  const resetCrop = useCallback((value: Rect | null) => {
    setCropState(value)
    setCropBaseline(value)
    setHistory([])
    setSelected(null)
  }, [])

  const snapshot = useCallback(() => {
    setHistory((prev) => {
      const next = [...prev, { draft: draftRef.current, crop: cropRef.current }]
      return next.length > MAX_HISTORY
        ? next.slice(next.length - MAX_HISTORY)
        : next
    })
  }, [])

  const undo = useCallback(() => {
    const prev = historyRef.current
    if (prev.length === 0) return
    const last = prev[prev.length - 1]
    setDraft(last.draft)
    setCropState(last.crop)
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

  const setCrop = useCallback((rect: Rect | null) => {
    setCropState(rect)
  }, [])

  const clearCrop = useCallback(() => {
    snapshot()
    setCropState(null)
  }, [snapshot])

  const isDirty = useMemo(
    () =>
      serializeRegionConfig(fromDraft(draft)) !==
        serializeRegionConfig(fromDraft(baseline)) ||
      !cropEqual(crop, cropBaseline),
    [draft, baseline, crop, cropBaseline]
  )

  const counts = useMemo(
    () => ({ roi: draft.roi.length, mask: draft.mask.length }),
    [draft]
  )

  const serialize = useCallback(
    () => serializeRegionConfig(fromDraft(draftRef.current)),
    []
  )

  const serializeCrop = useCallback(() => {
    const value = cropRef.current
    return value && !isFullFrame(value) ? value : null
  }, [])

  return {
    draft,
    selected,
    tool,
    canUndo: history.length > 0,
    isDirty,
    counts,
    crop,
    reset,
    resetCrop,
    setTool,
    select,
    changeDraft,
    createRect,
    deleteRect,
    setCrop,
    clearCrop,
    snapshot,
    undo,
    serialize,
    serializeCrop,
  }
}
