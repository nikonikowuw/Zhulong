import { describe, expect, it } from 'vitest'
import {
  STATUS_DOT_CLASS,
  STATUS_LEVELS,
  STATUS_LEVEL_CLASS,
  STATUS_TEXT_CLASS,
  statusDotClass,
  statusLevelClass,
  statusTextClass,
} from './status-palette'

/** §1.3.3 四级状态语义的规范类名（逐字锁定，防止暗色配对再次漂移）。 */
const EXPECTED_LEVEL_CLASS: Record<string, string> = {
  active:
    'border-teal-200 bg-teal-100/30 text-teal-900 dark:border-teal-800 dark:bg-teal-950/30 dark:text-teal-200',
  warning:
    'border-amber-200 bg-amber-100/30 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200',
  destructive:
    'border-destructive/20 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/20 dark:text-destructive',
  inactive:
    'border-neutral-300 bg-neutral-300/40 text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40',
}

const EXPECTED_DOT_CLASS: Record<string, string> = {
  active: 'bg-emerald-500',
  warning: 'bg-amber-500',
  destructive: 'bg-destructive',
  inactive: 'bg-muted-foreground/60',
}

describe('status palette (§1.3.3)', () => {
  it('defines exactly the four semantic levels', () => {
    expect([...STATUS_LEVELS]).toEqual([
      'active',
      'warning',
      'destructive',
      'inactive',
    ])
  })

  it.each([...STATUS_LEVELS])(
    'matches the spec string verbatim for %s',
    (level) => {
      expect(STATUS_LEVEL_CLASS[level]).toBe(EXPECTED_LEVEL_CLASS[level])
    }
  )

  it.each([...STATUS_LEVELS])(
    'pairs every absolute colour with a dark-mode variant for %s',
    (level) => {
      const classes = STATUS_LEVEL_CLASS[level]
      /**
       * 语义 token（`text-muted-foreground`、`text-destructive` 等）本身随主题切换，
       * 无需 `dark:` 配对；但绝对色板色（`border-teal-200`、`bg-neutral-300/40`）
       * 必须成对出现，否则暗色下会「极暗」（§1.3.5 的 ❌ 形态）。
       */
      for (const prop of ['border', 'bg', 'text']) {
        const hasAbsoluteLight = new RegExp(
          `(^| )${prop}-(teal|amber|neutral|emerald)-\\d`
        ).test(classes)
        if (hasAbsoluteLight) {
          expect(classes).toMatch(new RegExp(`dark:${prop}-`))
        }
      }
    }
  )

  it.each([...STATUS_LEVELS])('matches the dot string for %s', (level) => {
    expect(STATUS_DOT_CLASS[level]).toBe(EXPECTED_DOT_CLASS[level])
  })

  it('falls back to inactive for an undeclared status', () => {
    expect(statusLevelClass(undefined)).toBe(STATUS_LEVEL_CLASS.inactive)
    expect(statusDotClass(undefined)).toBe(STATUS_DOT_CLASS.inactive)
    expect(statusTextClass(undefined)).toBe(STATUS_TEXT_CLASS.inactive)
    expect(statusLevelClass('active')).toBe(STATUS_LEVEL_CLASS.active)
  })

  it('keeps every level visually distinct', () => {
    const levels = STATUS_LEVELS.map((level) => STATUS_LEVEL_CLASS[level])
    expect(new Set(levels).size).toBe(STATUS_LEVELS.length)
    const dots = STATUS_LEVELS.map((level) => STATUS_DOT_CLASS[level])
    expect(new Set(dots).size).toBe(STATUS_LEVELS.length)
  })

  it('uses literal class strings so Tailwind can statically extract them', () => {
    for (const level of STATUS_LEVELS) {
      expect(STATUS_LEVEL_CLASS[level]).not.toContain('${')
      expect(STATUS_DOT_CLASS[level]).not.toContain('${')
      expect(STATUS_TEXT_CLASS[level]).not.toContain('${')
    }
  })

  it('never uses a non-semantic grey/slate/zinc utility', () => {
    for (const level of STATUS_LEVELS) {
      expect(STATUS_LEVEL_CLASS[level]).not.toMatch(
        /(bg|border|text)-(zinc|slate|gray)-/
      )
    }
  })
})
