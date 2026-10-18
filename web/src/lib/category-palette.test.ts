import { describe, expect, it } from 'vitest'
import {
  CATEGORY_BADGE_CLASS,
  CATEGORY_HUES,
  CATEGORY_NEUTRAL_BADGE_CLASS,
  CATEGORY_NEUTRAL_SOLID_CLASS,
  CATEGORY_SOLID_CLASS,
  categoryBadgeClass,
  categorySolidClass,
} from './category-palette'

/** §1.3.3 状态语义色相 —— 分类色板严禁占用（否则会被误读为状态）。 */
const STATUS_HUES = ['teal', 'amber', 'destructive', 'neutral']

describe('category palette (§1.3.6)', () => {
  it('defines the five ordered slots', () => {
    expect([...CATEGORY_HUES]).toEqual([
      'blue',
      'violet',
      'emerald',
      'fuchsia',
      'cyan',
    ])
  })

  it.each([...CATEGORY_HUES])(
    'emits the full light + dark pair for %s',
    (hue) => {
      const classes = CATEGORY_BADGE_CLASS[hue]
      for (const expected of [
        `border-${hue}-500/30`,
        `bg-${hue}-500/10`,
        `text-${hue}-600`,
        `dark:border-${hue}-500/40`,
        `dark:bg-${hue}-500/20`,
        `dark:text-${hue}-400`,
      ]) {
        expect(classes).toContain(expected)
      }
    }
  )

  it.each([...CATEGORY_HUES])('never borrows a status hue for %s', (hue) => {
    for (const statusHue of STATUS_HUES) {
      expect(CATEGORY_BADGE_CLASS[hue]).not.toContain(`${statusHue}-`)
      expect(CATEGORY_SOLID_CLASS[hue]).not.toContain(`${statusHue}-`)
    }
  })

  it.each([...CATEGORY_HUES])('renders a solid fill for %s', (hue) => {
    expect(CATEGORY_SOLID_CLASS[hue]).toBe(`bg-${hue}-500`)
  })

  it('reuses the §1.3.3 inactive palette for the neutral fallback', () => {
    expect(CATEGORY_NEUTRAL_BADGE_CLASS).toBe(
      'border-neutral-300 bg-neutral-300/40 text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40'
    )
    expect(CATEGORY_NEUTRAL_SOLID_CLASS).toBe('bg-muted-foreground/60')
  })

  it('falls back to neutral for an undeclared category', () => {
    expect(categoryBadgeClass(undefined)).toBe(CATEGORY_NEUTRAL_BADGE_CLASS)
    expect(categorySolidClass(undefined)).toBe(CATEGORY_NEUTRAL_SOLID_CLASS)
    expect(categoryBadgeClass('blue')).toBe(CATEGORY_BADGE_CLASS.blue)
    expect(categorySolidClass('cyan')).toBe(CATEGORY_SOLID_CLASS.cyan)
  })

  it('keeps every slot visually distinct', () => {
    const solids = CATEGORY_HUES.map((hue) => CATEGORY_SOLID_CLASS[hue])
    expect(new Set(solids).size).toBe(CATEGORY_HUES.length)
  })

  it('uses literal class strings so Tailwind can statically extract them', () => {
    for (const hue of CATEGORY_HUES) {
      // 模板拼接（含 ${）不会被 Tailwind v4 扫描生成，必须保持完整字面量
      expect(CATEGORY_BADGE_CLASS[hue]).not.toContain('${')
      expect(CATEGORY_SOLID_CLASS[hue]).not.toContain('${')
    }
  })
})
