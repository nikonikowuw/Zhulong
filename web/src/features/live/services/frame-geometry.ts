/**
 * 视频画面在 canvas 元素盒内的几何计算。
 *
 * canvas 使用 `object-contain`：画面等比缩放后居中，元素盒宽高比与画面不一致时
 * 会在左右或上下留下 letterbox 留白。留白不属于坐标空间——区域叠加层必须精确覆盖
 * **内容区**，否则归一化坐标会被留白拉伸，画出来的框与真实画面错位。
 *
 * 抽成独立模块是为了能脱离 DOM 布局直接单测：测试环境不加载 Tailwind 样式，
 * 组件级的 `getBoundingClientRect()` 反映不了真实布局。
 */

/** 画面实际绘制区（不含 letterbox 留白）相对 canvas 元素盒的几何信息。 */
export interface ImageRect {
  /** 内容区宽度（CSS px），不等于元素盒宽度。 */
  width: number
  /** 内容区高度（CSS px），不等于元素盒高度。 */
  height: number
  /** 画面固有宽度（像素）。 */
  naturalWidth: number
  /** 画面固有高度（像素）。 */
  naturalHeight: number
  /** 内容区在元素盒内的水平居中偏移。 */
  offsetX: number
  /** 内容区在元素盒内的垂直居中偏移。 */
  offsetY: number
}

/**
 * 按 `object-contain` 语义计算内容区尺寸与居中偏移。
 * 画面或元素盒尺寸为 0 时返回 null（尚未渲染首帧或容器不可见）。
 */
export function measureContentRect(
  canvas: HTMLCanvasElement
): ImageRect | null {
  const naturalWidth = canvas.width
  const naturalHeight = canvas.height
  if (naturalWidth === 0 || naturalHeight === 0) return null

  const box = canvas.getBoundingClientRect()
  if (box.width === 0 || box.height === 0) return null

  const scale = Math.min(box.width / naturalWidth, box.height / naturalHeight)
  const width = naturalWidth * scale
  const height = naturalHeight * scale
  return {
    width,
    height,
    naturalWidth,
    naturalHeight,
    offsetX: (box.width - width) / 2,
    offsetY: (box.height - height) / 2,
  }
}

/** 几何是否完全一致。用于避免按帧率提交状态、进而按帧率重渲染叠加层。 */
export function isSameRect(a: ImageRect | null, b: ImageRect | null): boolean {
  if (a === b) return true
  if (!a || !b) return false
  return (
    a.width === b.width &&
    a.height === b.height &&
    a.naturalWidth === b.naturalWidth &&
    a.naturalHeight === b.naturalHeight &&
    a.offsetX === b.offsetX &&
    a.offsetY === b.offsetY
  )
}
