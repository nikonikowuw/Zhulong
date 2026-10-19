import { MIN_SIDE_LENGTH, type Rect } from '../data/region-config'

/**
 * 区域矩形的纯几何运算。
 *
 * 与事件/状态机分离，一是让画布 hook 专注于交互，二是这些换算是坐标错位的唯一来源，
 * 必须能脱离 DOM 直接单测。
 *
 * 全部坐标都是 `[0, 1]` 的归一化值，顺序恒为 `[x1, y1, x2, y2]` 且 `x1 < x2`、`y1 < y2`。
 */

export type DragHandle = 'tl' | 'tr' | 'bl' | 'br'

export interface Point {
  x: number
  y: number
}

export function clamp(v: number): number {
  return Math.max(0, Math.min(1, v))
}

/** 归一化并夹取到 `[0, 1]`，保留 3 位小数以免浮点噪声进入持久化 JSON。 */
export function normalizeRect(
  x1: number,
  y1: number,
  x2: number,
  y2: number
): Rect {
  return [
    Math.round(clamp(x1) * 1000) / 1000,
    Math.round(clamp(y1) * 1000) / 1000,
    Math.round(clamp(x2) * 1000) / 1000,
    Math.round(clamp(y2) * 1000) / 1000,
  ]
}

/** 由任意两个对角点构造矩形（自动排序，支持反向拖拽）。 */
export function rectFromCorners(
  ax: number,
  ay: number,
  bx: number,
  by: number
): Rect {
  return normalizeRect(
    Math.min(ax, bx),
    Math.min(ay, by),
    Math.max(ax, bx),
    Math.max(ay, by)
  )
}

/** 矩形是否达到最小边长；小于该值的拖拽视为误触。 */
export function isLargeEnough(rect: Rect): boolean {
  return (
    rect[2] - rect[0] >= MIN_SIDE_LENGTH && rect[3] - rect[1] >= MIN_SIDE_LENGTH
  )
}

/** 整体平移，宽高保持不变。 */
export function moveRect(originRect: Rect, dx: number, dy: number): Rect {
  const [x1, y1, x2, y2] = originRect
  const w = x2 - x1
  const h = y2 - y1

  let nx1 = clamp(x1 + dx)
  let ny1 = clamp(y1 + dy)
  let nx2 = nx1 + w
  let ny2 = ny1 + h

  // 贴到右下边界时整体回推，保持宽高不变，避免矩形被压扁
  if (nx2 > 1) {
    nx2 = 1
    nx1 = 1 - w
  }
  if (ny2 > 1) {
    ny2 = 1
    ny1 = 1 - h
  }
  return normalizeRect(nx1, ny1, nx2, ny2)
}

/** 拖动某个角调整尺寸，对边保持不动，且不允许跌破最小边长。 */
export function resizeRect(
  originRect: Rect,
  handle: DragHandle,
  pos: Point
): Rect {
  const [x1, y1, x2, y2] = originRect
  let nx1 = x1
  let ny1 = y1
  let nx2 = x2
  let ny2 = y2

  if (handle === 'tl') {
    nx1 = Math.min(pos.x, x2 - MIN_SIDE_LENGTH)
    ny1 = Math.min(pos.y, y2 - MIN_SIDE_LENGTH)
  } else if (handle === 'tr') {
    nx2 = Math.max(pos.x, x1 + MIN_SIDE_LENGTH)
    ny1 = Math.min(pos.y, y2 - MIN_SIDE_LENGTH)
  } else if (handle === 'bl') {
    nx1 = Math.min(pos.x, x2 - MIN_SIDE_LENGTH)
    ny2 = Math.max(pos.y, y1 + MIN_SIDE_LENGTH)
  } else {
    nx2 = Math.max(pos.x, x1 + MIN_SIDE_LENGTH)
    ny2 = Math.max(pos.y, y1 + MIN_SIDE_LENGTH)
  }
  return normalizeRect(nx1, ny1, nx2, ny2)
}
