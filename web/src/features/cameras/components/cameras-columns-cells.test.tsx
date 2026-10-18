import { describe, it, expect } from 'vitest'
import { render } from 'vitest-browser-react'
import { type Camera, type CameraHealth } from '../data/schema'
import { CameraStatusBadge } from './cameras-columns-cells'

/** §1.3.3 四级调色板代表类名（含成对的 dark: 与圆点）。 */
const LEVELS = {
  active: {
    badge: 'border-teal-200',
    dark: 'dark:border-teal-800',
    dot: 'bg-emerald-500',
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

const CASES: Array<[CameraHealth, string, Level]> = [
  ['online', '在线', 'active'],
  // 规范 §1.3.3 将 Offline 归入「失败 / 异常 / 破坏性」档
  ['offline', '离线', 'destructive'],
  ['error', '异常', 'destructive'],
  ['unknown', '未知', 'inactive'],
]

function buildCamera(health: CameraHealth): Camera {
  return {
    id: 'cam-1',
    name: 'Front Gate',
    enabled: true,
    revision: 1,
    health,
    session: 'running',
    degraded: false,
    stale: false,
    reason: '',
    streams: [],
    createdAt: '',
    updatedAt: '',
  }
}

describe('CameraStatusBadge', () => {
  it.each(CASES)(
    '%s renders %s on the %s level',
    async (health, label, level) => {
      const { getByText } = await render(
        <CameraStatusBadge camera={buildCamera(health)} />
      )

      await expect.element(getByText(label)).toBeInTheDocument()

      const badge = (getByText(label).element() as HTMLElement).closest(
        '[data-slot="badge"]'
      )
      expect(badge).not.toBeNull()
      const classes = (badge as HTMLElement).className

      expect(classes).toContain(LEVELS[level].badge)
      expect(classes).toContain(LEVELS[level].dark)
      expect(classes).not.toMatch(/(bg|border|text)-(zinc|slate|gray)-/)
      expect(classes).not.toMatch(/(emerald|sky|rose)-500\//)

      const dot = (badge as HTMLElement).querySelector('span.rounded-full')
      expect(dot).not.toBeNull()
      expect((dot as HTMLElement).className).toContain(LEVELS[level].dot)
    }
  )
})
