import { describe, it, expect } from 'vitest'
import {
  isSameRect,
  measureContentRect,
  type ImageRect,
} from './frame-geometry'

/** 构造只带测量所需接口的 canvas 替身，脱离真实布局直接验证几何换算。 */
function fakeCanvas(
  bitmap: [number, number],
  box: [number, number]
): HTMLCanvasElement {
  return {
    width: bitmap[0],
    height: bitmap[1],
    getBoundingClientRect: () => ({
      width: box[0],
      height: box[1],
      left: 0,
      top: 0,
      right: box[0],
      bottom: box[1],
      x: 0,
      y: 0,
      toJSON: () => ({}),
    }),
  } as unknown as HTMLCanvasElement
}

describe('measureContentRect (object-contain geometry)', () => {
  it('letterboxes a wide frame with equal top and bottom padding', () => {
    // 800x200 画面放进 400x200 盒子：缩放 0.5 → 内容 400x100，上下各留白 50
    expect(measureContentRect(fakeCanvas([800, 200], [400, 200]))).toEqual({
      width: 400,
      height: 100,
      naturalWidth: 800,
      naturalHeight: 200,
      offsetX: 0,
      offsetY: 50,
    })
  })

  it('pillarboxes a tall frame with equal left and right padding', () => {
    // 200x800 画面放进 400x200 盒子：缩放 0.25 → 内容 50x200，左右各留白 175
    expect(measureContentRect(fakeCanvas([200, 800], [400, 200]))).toEqual({
      width: 50,
      height: 200,
      naturalWidth: 200,
      naturalHeight: 800,
      offsetX: 175,
      offsetY: 0,
    })
  })

  it('leaves no padding when the aspect ratios already match', () => {
    // 1920x1080 放进 960x540：比例一致，内容填满盒子
    const rect = measureContentRect(fakeCanvas([1920, 1080], [960, 540]))
    expect(rect).not.toBeNull()
    expect(rect?.width).toBeCloseTo(960, 5)
    expect(rect?.height).toBeCloseTo(540, 5)
    expect(rect?.offsetX).toBeCloseTo(0, 5)
    expect(rect?.offsetY).toBeCloseTo(0, 5)
  })

  it('never upscales beyond the box when the frame is smaller', () => {
    // 320x240 画面放进 960x540 盒子：contain 会放大到 720x540 并左右留白
    const rect = measureContentRect(fakeCanvas([320, 240], [960, 540]))
    expect(rect?.height).toBeCloseTo(540, 5)
    expect(rect?.width).toBeCloseTo(720, 5)
    expect(rect?.offsetX).toBeCloseTo(120, 5)
    expect(rect?.offsetY).toBeCloseTo(0, 5)
  })

  it('returns null before the first frame or when the box is not laid out', () => {
    expect(measureContentRect(fakeCanvas([0, 0], [400, 200]))).toBeNull()
    expect(measureContentRect(fakeCanvas([800, 200], [0, 0]))).toBeNull()
    expect(measureContentRect(fakeCanvas([800, 200], [400, 0]))).toBeNull()
  })
})

describe('isSameRect', () => {
  const base: ImageRect = {
    width: 400,
    height: 100,
    naturalWidth: 800,
    naturalHeight: 200,
    offsetX: 0,
    offsetY: 50,
  }

  it('treats identical geometry as unchanged so per-frame updates are skipped', () => {
    expect(isSameRect(base, { ...base })).toBe(true)
    expect(isSameRect(base, base)).toBe(true)
    expect(isSameRect(null, null)).toBe(true)
  })

  it('detects any geometry change', () => {
    expect(isSameRect(base, { ...base, width: 401 })).toBe(false)
    expect(isSameRect(base, { ...base, naturalHeight: 201 })).toBe(false)
    expect(isSameRect(base, { ...base, offsetY: 51 })).toBe(false)
    expect(isSameRect(base, null)).toBe(false)
    expect(isSameRect(null, base)).toBe(false)
  })
})
