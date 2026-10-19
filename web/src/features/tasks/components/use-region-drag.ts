import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
} from 'react'
import {
  draftItems,
  withDraftItems,
  type Rect,
  type RegionCategory,
} from '../data/region-config'
import {
  type RegionTool,
  type UseRegionDragOptions,
  type UseRegionDragResult,
} from './region-drag-types'
import {
  clamp,
  isLargeEnough,
  moveRect,
  rectFromCorners,
  resizeRect,
  type DragHandle,
} from './region-geometry'

export type { RegionTool, UseRegionDragOptions, UseRegionDragResult }

type DragMode =
  | {
      type: 'creating'
      startX: number
      startY: number
      currentX: number
      currentY: number
    }
  | {
      type: 'moving'
      category: RegionCategory
      index: number
      startX: number
      startY: number
      originRect: Rect
    }
  | {
      type: 'resizing'
      category: RegionCategory
      index: number
      handle: DragHandle
      startX: number
      startY: number
      originRect: Rect
    }
  | null

/**
 * 区域画布的拖拽状态机：绘制 / 平移 / 缩放。
 * 抽离为独立 hook，使画布组件只负责渲染；坐标换算见 `region-geometry`。
 *
 * 关键实现约束：window 级监听器只依赖「是否有拖拽在进行」，其余状态一律走 ref 读取。
 * 若把 draft / onChange 放进依赖数组，拖动期间每帧都会重新注册监听器。
 */
export function useRegionDrag({
  draft,
  tool,
  disabled,
  onChange,
  onCreate,
  onSelect,
  onBeforeChange,
}: UseRegionDragOptions): UseRegionDragResult {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const [drag, setDragState] = useState<DragMode>(null)
  const dragRef = useRef<DragMode>(null)
  const mutatedRef = useRef(false)

  /**
   * 事件回调一律从 latestRef 读取最新值。
   * 若把这些值放进 window 监听器的依赖数组，拖动期间每帧都会重新注册监听器。
   */
  const latestRef = useRef({
    draft,
    tool,
    disabled,
    onChange,
    onCreate,
    onSelect,
    onBeforeChange,
  })
  useEffect(() => {
    latestRef.current = {
      draft,
      tool,
      disabled,
      onChange,
      onCreate,
      onSelect,
      onBeforeChange,
    }
  })

  const setDrag = useCallback((next: DragMode) => {
    dragRef.current = next
    setDragState(next)
  }, [])

  const getNormCoords = useCallback(
    (e: MouseEvent | ReactMouseEvent): { x: number; y: number } | null => {
      const el = containerRef.current
      if (!el) return null
      const rect = el.getBoundingClientRect()
      if (rect.width === 0 || rect.height === 0) return null
      return {
        x: clamp((e.clientX - rect.left) / rect.width),
        y: clamp((e.clientY - rect.top) / rect.height),
      }
    },
    []
  )

  /** 首次真正变更前压入一次撤销快照。 */
  const beginMutation = useCallback(() => {
    if (mutatedRef.current) return
    mutatedRef.current = true
    latestRef.current.onBeforeChange?.()
  }, [])

  const handleBackgroundMouseDown = useCallback(
    (e: ReactMouseEvent<HTMLDivElement>) => {
      if (latestRef.current.disabled || e.button !== 0) return
      const pos = getNormCoords(e)
      if (!pos) return

      const activeTool = latestRef.current.tool
      if (activeTool === 'roi' || activeTool === 'mask') {
        mutatedRef.current = false
        setDrag({
          type: 'creating',
          startX: pos.x,
          startY: pos.y,
          currentX: pos.x,
          currentY: pos.y,
        })
      }
      latestRef.current.onSelect(null)
    },
    [getNormCoords, setDrag]
  )

  /**
   * 三个入口共用的守卫与起始快照。
   * 守卫必须在 `onStart`（选中回调）之前：禁用态下点击矩形既不能改变选中项，
   * 也不能吞掉事件，否则会与背景的取消选中逻辑打架。
   */
  const beginDrag = useCallback(
    (
      e: ReactMouseEvent<HTMLDivElement>,
      build: (pos: { x: number; y: number }) => DragMode,
      onStart?: () => void
    ) => {
      if (latestRef.current.disabled || e.button !== 0) return false
      const pos = getNormCoords(e)
      if (!pos) return false
      e.stopPropagation()
      onStart?.()
      mutatedRef.current = false
      setDrag(build(pos))
      return true
    },
    [getNormCoords, setDrag]
  )

  const startMove = useCallback(
    (category: RegionCategory, index: number, rect: Rect) =>
      (e: ReactMouseEvent<HTMLDivElement>) => {
        beginDrag(
          e,
          (pos) => ({
            type: 'moving',
            category,
            index,
            startX: pos.x,
            startY: pos.y,
            originRect: rect,
          }),
          () => latestRef.current.onSelect({ category, index })
        )
      },
    [beginDrag]
  )

  const startResize = useCallback(
    (category: RegionCategory, index: number, handle: DragHandle, rect: Rect) =>
      (e: ReactMouseEvent<HTMLDivElement>) => {
        beginDrag(
          e,
          (pos) => ({
            type: 'resizing',
            category,
            index,
            handle,
            startX: pos.x,
            startY: pos.y,
            originRect: rect,
          }),
          () => latestRef.current.onSelect({ category, index })
        )
      },
    [beginDrag]
  )

  const isDragging = drag !== null

  useEffect(() => {
    if (!isDragging) return

    const handleMouseMove = (e: MouseEvent) => {
      const current = dragRef.current
      if (!current) return
      const pos = getNormCoords(e)
      if (!pos) return

      if (current.type === 'creating') {
        setDrag({ ...current, currentX: pos.x, currentY: pos.y })
        return
      }

      const nextRect =
        current.type === 'moving'
          ? moveRect(
              current.originRect,
              pos.x - current.startX,
              pos.y - current.startY
            )
          : resizeRect(current.originRect, current.handle, pos)

      beginMutation()
      const items = draftItems(latestRef.current.draft, current.category)
      const nextItems = items.map((item, i) =>
        i === current.index ? { ...item, rect: nextRect } : item
      )
      latestRef.current.onChange(
        withDraftItems(latestRef.current.draft, current.category, nextItems)
      )
    }

    const handleMouseUp = () => {
      const current = dragRef.current
      if (current?.type === 'creating') {
        const newRect = rectFromCorners(
          current.startX,
          current.startY,
          current.currentX,
          current.currentY
        )
        // 小于最小边长的拖拽视为误触，不落库
        const activeTool = latestRef.current.tool
        if (
          isLargeEnough(newRect) &&
          (activeTool === 'roi' || activeTool === 'mask')
        ) {
          beginMutation()
          latestRef.current.onCreate(activeTool, newRect)
        }
      }
      mutatedRef.current = false
      setDrag(null)
    }

    window.addEventListener('mousemove', handleMouseMove)
    window.addEventListener('mouseup', handleMouseUp)
    return () => {
      window.removeEventListener('mousemove', handleMouseMove)
      window.removeEventListener('mouseup', handleMouseUp)
    }
  }, [isDragging, beginMutation, getNormCoords, setDrag])

  const creatingPreview =
    drag?.type === 'creating'
      ? rectFromCorners(drag.startX, drag.startY, drag.currentX, drag.currentY)
      : null

  return {
    containerRef,
    creatingPreview,
    getNormCoords,
    handleBackgroundMouseDown,
    startMove,
    startResize,
  }
}
