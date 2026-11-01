export type Box4 = readonly [number, number, number, number]

/**
 * 归一化坐标序列化往返（float32 → JSON → double）后的容差。
 * 插件契约要求退化项的 `body_box` **逐位等于** `faces[0].box`
 * （见 native/sdk/include/zhulong/plugin.h 退化语义），因此 1e-4 足够宽。
 */
export const BOX_COINCIDENCE_EPSILON = 1e-4

/**
 * 判断两个归一化 `[x1, y1, x2, y2]` 框是否重合（逐分量差绝对值 < epsilon）。
 * 用于识别「纯人脸/纯车牌退化项」：这类项的父级框被原样别名成子级框，
 * 按外层框再画一次只会得到两个完全重叠的框。
 */
export function boxesCoincide(
  a: Box4,
  b: Box4,
  epsilon: number = BOX_COINCIDENCE_EPSILON
): boolean {
  return (
    Math.abs(a[0] - b[0]) < epsilon &&
    Math.abs(a[1] - b[1]) < epsilon &&
    Math.abs(a[2] - b[2]) < epsilon &&
    Math.abs(a[3] - b[3]) < epsilon
  )
}
