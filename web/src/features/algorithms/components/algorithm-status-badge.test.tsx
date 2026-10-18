import { describe, it, expect } from 'vitest'
import { render } from 'vitest-browser-react'
import { type AlgorithmStatus } from '../data/schema'
import { AlgorithmStatusBadge } from './algorithm-status-badge'

/**
 * §1.3.3 经典四级状态语义调色板断言：每档校验代表类名 + 成对的 dark: 类名 + 圆点类名。
 * 用于防止再次退回 emerald/zinc/sky/rose 的 500 级透明色「私造」样式。
 */
const LEVELS = {
  active: {
    badge: 'border-teal-200',
    dark: 'dark:border-teal-800',
    dot: 'bg-emerald-500',
  },
  warning: {
    badge: 'border-amber-200',
    dark: 'dark:border-amber-800',
    dot: 'bg-amber-500',
  },
  destructive: {
    badge: 'border-destructive/20',
    dark: 'dark:border-destructive/40',
    dot: 'bg-destructive',
  },
  inactive: {
    badge: 'border-neutral-300',
    dark: 'dark:border-neutral-700',
    dot: 'bg-muted-foreground/60',
  },
} as const

type Level = keyof typeof LEVELS

const CASES: Array<[AlgorithmStatus, string, Level]> = [
  ['ACTIVE', '已激活', 'active'],
  ['RUNNING', '运行中', 'active'],
  ['STARTING', '启动中', 'warning'],
  ['ROLLING_BACK', '回滚中', 'warning'],
  ['CRASHED', '崩溃', 'destructive'],
  ['CRASH_LOOP', '反复崩溃', 'destructive'],
  ['DISABLED', '已停用', 'inactive'],
]

describe('AlgorithmStatusBadge', () => {
  it.each(CASES)(
    '%s renders %s on the %s level',
    async (status, label, level) => {
      const { getByText } = await render(
        <AlgorithmStatusBadge status={status} />
      )

      await expect.element(getByText(label)).toBeInTheDocument()

      const badge = (getByText(label).element() as HTMLElement).closest(
        '[data-slot="badge"]'
      )
      expect(badge).not.toBeNull()
      const classes = (badge as HTMLElement).className

      expect(classes).toContain(LEVELS[level].badge)
      expect(classes).toContain(LEVELS[level].dark)
      // 严禁非语义灰阶与 500 级透明色私造样式
      expect(classes).not.toMatch(/(bg|border|text)-(zinc|slate|gray)-/)
      expect(classes).not.toMatch(/(emerald|sky|rose)-500\//)

      const dot = (badge as HTMLElement).querySelector('span.rounded-full')
      expect(dot).not.toBeNull()
      expect((dot as HTMLElement).className).toContain(LEVELS[level].dot)
    }
  )
})
