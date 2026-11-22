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
  counts: { roi: number; mask: number; tripwires?: number }
  /** 任务级裁剪窗口；`null` = 全幅。与 ROI/MASK 同处一个撤销栈。 */
  crop: Rect | null
  /** 载入新的 ROI 基线（切换算法时调用）。传入 `nextScopeKey` 时保留历史栈以保护 Crop 与其它算法撤销记录。 */
  reset: (value: string, nextScopeKey?: string) => void
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

/** 一次撤销快照按操作对象分类：region 归属各自算法作用域，crop 归属通道级；共用单一有序时间线。 */
type EditorHistoryEntry =
  | { kind: 'region'; scopeKey: string; draft: RegionDraft }
  | { kind: 'crop'; crop: Rect | null }

interface PendingSnapshot {
  scopeKey: string
  draft: RegionDraft
  crop: Rect | null
}

function findLatestUndoIndex(
  history: EditorHistoryEntry[],
  scopeKey: string
): number {
  for (let i = history.length - 1; i >= 0; i -= 1) {
    const entry = history[i]
    if (entry.kind === 'crop' || entry.scopeKey === scopeKey) {
      return i
    }
  }
  return -1
}

function appendHistoryEntry(
  history: EditorHistoryEntry[],
  entry: EditorHistoryEntry
): EditorHistoryEntry[] {
  const next = [...history, entry]
  return next.length > MAX_HISTORY
    ? next.slice(next.length - MAX_HISTORY)
    : next
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
  initialCrop: Rect | null = null,
  initialScopeKey: string = 'default'
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
  const [scopeKey, setScopeKey] = useState<string>(initialScopeKey)
  const [history, setHistory] = useState<EditorHistoryEntry[]>([])
  const [selected, setSelected] = useState<RegionSelection | null>(null)
  const [tool, setTool] = useState<RegionTool>('select')

  // 事件回调里同步维护最新引用，既避免把它们塞进 useCallback 依赖，也防止同批多次调用读到陈旧值。
  const draftRef = useRef(draft)
  const historyRef = useRef(history)
  const cropRef = useRef(crop)
  const scopeKeyRef = useRef(scopeKey)
  const pendingSnapshotRef = useRef<PendingSnapshot | null>(null)

  useEffect(() => {
    draftRef.current = draft
  }, [draft])
  useEffect(() => {
    historyRef.current = history
  }, [history])
  useEffect(() => {
    cropRef.current = crop
  }, [crop])
  useEffect(() => {
    scopeKeyRef.current = scopeKey
  }, [scopeKey])

  const pushHistory = useCallback((entry: EditorHistoryEntry) => {
    const next = appendHistoryEntry(historyRef.current, entry)
    historyRef.current = next
    setHistory(next)
  }, [])

  const consumePendingAs = useCallback(
    (kind: 'region' | 'crop') => {
      const pending = pendingSnapshotRef.current
      if (!pending) return
      pendingSnapshotRef.current = null
      if (kind === 'crop') {
        pushHistory({ kind: 'crop', crop: pending.crop })
      } else {
        pushHistory({
          kind: 'region',
          scopeKey: pending.scopeKey,
          draft: pending.draft,
        })
      }
    },
    [pushHistory]
  )

  const reset = useCallback(
    (value: string, nextScopeKey?: string) => {
      const next = toDraft(parseRegionConfig(value), makeId)
      pendingSnapshotRef.current = null
      draftRef.current = next
      setDraft(next)
      setBaseline(next)
      if (nextScopeKey !== undefined) {
        scopeKeyRef.current = nextScopeKey
        setScopeKey(nextScopeKey)
      } else {
        // 未指定新作用域时仅清理当前算法的区域栈，保留任务级 Crop 撤销记录。
        const kept = historyRef.current.filter(
          (entry) =>
            entry.kind === 'crop' || entry.scopeKey !== scopeKeyRef.current
        )
        historyRef.current = kept
        setHistory(kept)
      }
      setSelected(null)
      setTool('select')
    },
    [makeId]
  )

  // ⚠️ 切算法会调 reset()，但 crop 属于任务而不是算法：这里绝不碰 crop；
  // 只有切换相机调 resetCrop() 时才清空整条通道的撤销历史。
  const resetCrop = useCallback((value: Rect | null) => {
    pendingSnapshotRef.current = null
    cropRef.current = value
    historyRef.current = []
    setCropState(value)
    setCropBaseline(value)
    setHistory([])
    setSelected(null)
  }, [])

  const snapshot = useCallback(() => {
    pendingSnapshotRef.current = {
      scopeKey: scopeKeyRef.current,
      draft: draftRef.current,
      crop: cropRef.current,
    }
  }, [])

  const undo = useCallback(() => {
    const prev = historyRef.current
    const targetIdx = findLatestUndoIndex(prev, scopeKeyRef.current)
    if (targetIdx === -1) return
    pendingSnapshotRef.current = null
    const target = prev[targetIdx]
    const nextHistory = prev.filter((_, i) => i !== targetIdx)
    historyRef.current = nextHistory
    setHistory(nextHistory)
    if (target.kind === 'crop') {
      cropRef.current = target.crop
      setCropState(target.crop)
    } else {
      draftRef.current = target.draft
      setDraft(target.draft)
      setSelected(null)
    }
  }, [])

  const changeDraft = useCallback(
    (next: RegionDraft) => {
      consumePendingAs('region')
      draftRef.current = next
      setDraft(next)
    },
    [consumePendingAs]
  )

  const createRect = useCallback(
    (category: RegionCategory, rect: Rect) => {
      const items = draftItems(draftRef.current, category)
      if (items.length >= MAX_RECTS_PER_CATEGORY) {
        pendingSnapshotRef.current = null
        return
      }
      consumePendingAs('region')
      const next = withDraftItems(draftRef.current, category, [
        ...items,
        { id: makeId(), rect },
      ])
      draftRef.current = next
      setDraft(next)
      setSelected({ category, index: items.length })
    },
    [consumePendingAs, makeId]
  )

  const deleteRect = useCallback(
    (category: RegionCategory, index: number) => {
      pendingSnapshotRef.current = null
      pushHistory({
        kind: 'region',
        scopeKey: scopeKeyRef.current,
        draft: draftRef.current,
      })
      const items = draftItems(draftRef.current, category)
      const next = withDraftItems(
        draftRef.current,
        category,
        items.filter((_, i) => i !== index)
      )
      draftRef.current = next
      setDraft(next)
      setSelected(null)
    },
    [pushHistory]
  )

  const select = useCallback((selection: RegionSelection | null) => {
    setSelected(selection)
  }, [])

  const setCrop = useCallback(
    (rect: Rect | null) => {
      consumePendingAs('crop')
      cropRef.current = rect
      setCropState(rect)
    },
    [consumePendingAs]
  )

  const clearCrop = useCallback(() => {
    pendingSnapshotRef.current = null
    pushHistory({ kind: 'crop', crop: cropRef.current })
    cropRef.current = null
    setCropState(null)
  }, [pushHistory])

  const canUndo = useMemo(
    () => findLatestUndoIndex(history, scopeKey) !== -1,
    [history, scopeKey]
  )

  const isDirty = useMemo(
    () =>
      serializeRegionConfig(fromDraft(draft)) !==
        serializeRegionConfig(fromDraft(baseline)) ||
      !cropEqual(crop, cropBaseline),
    [draft, baseline, crop, cropBaseline]
  )

  const counts = useMemo(
    () => ({
      roi: draft.roi.length,
      mask: draft.mask.length,
      tripwires: draft.tripwires?.length ?? 0,
    }),
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

  return useMemo(
    () => ({
      draft,
      selected,
      tool,
      canUndo,
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
    }),
    [
      draft,
      selected,
      tool,
      canUndo,
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
    ]
  )
}
