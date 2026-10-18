import { describe, expect, it } from 'vitest'
import {
  CATEGORY_BADGE_CLASS,
  CATEGORY_NEUTRAL_BADGE_CLASS,
} from '@/lib/category-palette'
import { targetTypeBadgeClass } from './target-type'

describe('targetTypeBadgeClass (§1.3.6)', () => {
  it.each([
    ['person', 'blue'],
    ['vehicle', 'violet'],
    ['face', 'emerald'],
  ] as const)('maps %s to the %s slot', (targetType, hue) => {
    expect(targetTypeBadgeClass(targetType)).toBe(CATEGORY_BADGE_CLASS[hue])
  })

  it('falls back to the neutral slot for an undeclared or missing type', () => {
    // targetType 是后端开放字符串（z.string()），长尾必须走中性兜底
    expect(targetTypeBadgeClass('unknown_thing')).toBe(
      CATEGORY_NEUTRAL_BADGE_CLASS
    )
    expect(targetTypeBadgeClass(undefined)).toBe(CATEGORY_NEUTRAL_BADGE_CLASS)
    expect(targetTypeBadgeClass('')).toBe(CATEGORY_NEUTRAL_BADGE_CLASS)
  })

  it('always yields a dark-mode-paired badge class', () => {
    for (const value of ['person', 'vehicle', 'face', 'unknown_thing', '']) {
      const classes = targetTypeBadgeClass(value)
      expect(classes).toMatch(/dark:(border|bg)-/)
    }
  })

  it('gives every declared type a distinct slot', () => {
    // 表格视图与卡片视图此前把同一取值渲染成不同颜色（blue vs sky）
    const classes = ['person', 'vehicle', 'face'].map((v) =>
      targetTypeBadgeClass(v)
    )
    expect(new Set(classes).size).toBe(3)
  })
})
