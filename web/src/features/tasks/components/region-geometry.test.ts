import { describe, it, expect } from 'vitest'
import { MIN_SIDE_LENGTH, type Rect } from '../data/region-config'
import {
  clamp,
  isLargeEnough,
  moveRect,
  normalizeRect,
  rectFromCorners,
  resizeRect,
} from './region-geometry'

describe('region-geometry clamp and normalizeRect', () => {
  it('clamps to the normalized range', () => {
    expect(clamp(-0.5)).toBe(0)
    expect(clamp(0.25)).toBe(0.25)
    expect(clamp(1.5)).toBe(1)
  })

  it('clamps out-of-range corners and rounds to 3 decimals', () => {
    expect(normalizeRect(-0.2, 0.123456, 1.7, 0.5)).toEqual([0, 0.123, 1, 0.5])
  })
})

describe('rectFromCorners', () => {
  it('orders corners so a reverse drag yields a valid rect', () => {
    expect(rectFromCorners(0.8, 0.9, 0.2, 0.1)).toEqual([0.2, 0.1, 0.8, 0.9])
    expect(rectFromCorners(0.2, 0.1, 0.8, 0.9)).toEqual([0.2, 0.1, 0.8, 0.9])
  })

  it('flattens degenerate drags to a zero-area rect', () => {
    expect(rectFromCorners(0.5, 0.5, 0.5, 0.5)).toEqual([0.5, 0.5, 0.5, 0.5])
  })
})

describe('isLargeEnough', () => {
  it('accepts rects at or above the minimum side length', () => {
    // 以 0.25 为基准：0.1 + 0.01 在 IEEE754 下略小于 0.11，会把边界值误判成「不足」
    expect(isLargeEnough([0.25, 0.25, 0.25 + MIN_SIDE_LENGTH, 0.75])).toBe(true)
    expect(isLargeEnough([0.1, 0.1, 0.5, 0.5])).toBe(true)
  })

  it('rejects rects thinner than the minimum in either axis', () => {
    expect(isLargeEnough([0.25, 0.25, 0.25 + MIN_SIDE_LENGTH / 2, 0.75])).toBe(
      false
    )
    expect(isLargeEnough([0.25, 0.25, 0.75, 0.25 + MIN_SIDE_LENGTH / 2])).toBe(
      false
    )
    expect(isLargeEnough([0.5, 0.5, 0.5, 0.5])).toBe(false)
  })
})

describe('moveRect', () => {
  const rect: Rect = [0.2, 0.2, 0.4, 0.4]

  it('translates while preserving width and height', () => {
    expect(moveRect(rect, 0.1, 0.2)).toEqual([0.3, 0.4, 0.5, 0.6])
  })

  it('parks against the right and bottom edges instead of squashing the rect', () => {
    const moved = moveRect(rect, 0.9, 0.9)
    expect(moved).toEqual([0.8, 0.8, 1, 1])
    // 宽高必须原样保留
    expect(moved[2] - moved[0]).toBeCloseTo(0.2, 5)
    expect(moved[3] - moved[1]).toBeCloseTo(0.2, 5)
  })

  it('clamps at the origin without shrinking', () => {
    const moved = moveRect(rect, -0.9, -0.9)
    expect(moved).toEqual([0, 0, 0.2, 0.2])
    expect(moved[2] - moved[0]).toBeCloseTo(0.2, 5)
  })
})

describe('resizeRect', () => {
  const rect: Rect = [0.2, 0.2, 0.6, 0.6]

  it('moves only the dragged corner, keeping the opposite one anchored', () => {
    expect(resizeRect(rect, 'tl', { x: 0.3, y: 0.4 })).toEqual([
      0.3, 0.4, 0.6, 0.6,
    ])
    expect(resizeRect(rect, 'br', { x: 0.9, y: 0.8 })).toEqual([
      0.2, 0.2, 0.9, 0.8,
    ])
    expect(resizeRect(rect, 'tr', { x: 0.9, y: 0.3 })).toEqual([
      0.2, 0.3, 0.9, 0.6,
    ])
    expect(resizeRect(rect, 'bl', { x: 0.3, y: 0.9 })).toEqual([
      0.3, 0.2, 0.6, 0.9,
    ])
  })

  it('never lets a handle collapse the rect below the minimum side', () => {
    const collapsed = resizeRect(rect, 'br', { x: 0.1, y: 0.1 })
    expect(collapsed[2] - collapsed[0]).toBeCloseTo(MIN_SIDE_LENGTH, 5)
    expect(collapsed[3] - collapsed[1]).toBeCloseTo(MIN_SIDE_LENGTH, 5)

    const collapsedTl = resizeRect(rect, 'tl', { x: 0.99, y: 0.99 })
    expect(collapsedTl[2] - collapsedTl[0]).toBeCloseTo(MIN_SIDE_LENGTH, 5)
    expect(collapsedTl[3] - collapsedTl[1]).toBeCloseTo(MIN_SIDE_LENGTH, 5)
  })

  it('clamps handles dragged outside the canvas', () => {
    expect(resizeRect(rect, 'br', { x: 1.8, y: 1.8 })).toEqual([0.2, 0.2, 1, 1])
    expect(resizeRect(rect, 'tl', { x: -0.5, y: -0.5 })).toEqual([
      0, 0, 0.6, 0.6,
    ])
  })
})
