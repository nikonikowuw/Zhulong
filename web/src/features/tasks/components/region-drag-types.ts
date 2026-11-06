import {
  type RegionCategory,
  type Rect,
  type RegionDraft,
  type RegionSelection,
} from '../data/region-config'
import { type DragHandle } from './region-geometry'

/**
 * 区域画布的交互契约。
 *
 * 单独成模块的原因：工具类型与回调签名被画布、工具条、编辑器 hook 共同消费，
 * 让它们只依赖「契约」而不是依赖 `use-region-drag` 这个实现模块，可以避免
 * 为了一个类型而把整个拖拽 hook 拉进依赖图。
 */

/**
 * 当前工具：绘制 ROI、绘制 MASK、绘制绊线、编辑任务级裁剪窗口，或仅选择/调整。
 *
 * `crop` 不依赖“已勾选算法”（它属于任务，不属于某个算法），但同样要求画面已出首帧。
 */
export type RegionTool = RegionCategory | 'select' | 'crop' | 'tripwire'

export interface UseRegionDragOptions {
  draft: RegionDraft
  tool: RegionTool
  disabled: boolean
  onChange: (next: RegionDraft) => void
  /**
   * 绘制出一条合法新矩形。由调用方分配稳定 id 并决定是否选中——
   * 画布不掌握 id 生成器，避免在此处引入模块级可变状态。
   */
  onCreate: (category: RegionCategory, rect: Rect) => void
  onSelect: (selection: RegionSelection | null) => void
  /**
   * 在一次用户操作**真正产生变更之前**恰好调用一次，供调用方压入撤销快照。
   * 拖动过程中不会重复调用，因此历史记录里不会堆满中间态。
   */
  onBeforeChange?: () => void
  /** 任务级裁剪窗口；`null` 表示全幅（此时不渲染窗口图元）。 */
  crop?: Rect | null
  /** 裁剪窗口变更（仅 crop 工具/手柄触发）。 */
  onCropChange?: (rect: Rect | null) => void
  /** 裁剪窗口最小边长，默认 {@link MIN_CROP_SIDE}。 */
  cropMinSide?: number
}

export interface UseRegionDragResult {
  containerRef: React.RefObject<HTMLDivElement | null>
  /** 正在绘制的预览矩形（仅绘制期间非空）。 */
  creatingPreview: Rect | null
  /** 预览矩形是否为裁剪窗口（决定预览用青色而非分类色）。 */
  creatingCrop: boolean
  getNormCoords: (
    e: MouseEvent | React.MouseEvent
  ) => { x: number; y: number } | null
  /** 画布背景按下：绘制新矩形，或在选择工具下取消选中。 */
  handleBackgroundMouseDown: (e: React.MouseEvent<HTMLDivElement>) => void
  /** 已有矩形按下：选中并开始拖动。 */
  startMove: (
    category: RegionCategory,
    index: number,
    rect: Rect
  ) => (e: React.MouseEvent<HTMLDivElement>) => void
  /** 缩放手柄按下：开始调整尺寸。 */
  startResize: (
    category: RegionCategory,
    index: number,
    handle: DragHandle,
    rect: Rect
  ) => (e: React.MouseEvent<HTMLDivElement>) => void
  /** 裁剪窗口主体按下：选中（切到 crop 工具）并开始平移。 */
  startCropMove: (rect: Rect) => (e: React.MouseEvent<HTMLDivElement>) => void
  /** 裁剪窗口缩放手柄按下：开始调整尺寸。 */
  startCropResize: (
    handle: DragHandle,
    rect: Rect
  ) => (e: React.MouseEvent<HTMLDivElement>) => void
}
