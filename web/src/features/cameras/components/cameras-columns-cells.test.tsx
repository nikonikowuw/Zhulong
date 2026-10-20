import {
  expectClassesApplied,
  expectDiffersInDarkMode,
  expectLevelHueFamily,
  expectPaletteRendersAs,
  LEVEL_HUE_FAMILY,
} from '@/test-utils/colors'
import { describe, it, expect } from 'vitest'
import { render } from 'vitest-browser-react'
import { STATUS_DOT_CLASS, STATUS_LEVEL_CLASS } from '@/lib/status-palette'
import { type Camera, type CameraHealth } from '../data/schema'
import { CameraStatusBadge } from './cameras-columns-cells'

/**
 * §1.3.3 四级调色板断言（三层，说明见 algorithm-status-badge.test.tsx）：
 * 类串取自 `@/lib/status-palette` 单一真实源，不在此硬编码任何色值。
 */
type Level = keyof typeof STATUS_LEVEL_CLASS

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
      ) as HTMLElement
      expect(badge).not.toBeNull()

      expectClassesApplied(badge, STATUS_LEVEL_CLASS[level])
      await expectPaletteRendersAs(badge, STATUS_LEVEL_CLASS[level])
      await expectLevelHueFamily(
        STATUS_LEVEL_CLASS[level],
        LEVEL_HUE_FAMILY[level]
      )
      await expectDiffersInDarkMode(badge, 'borderTopColor')

      expect(badge.className).not.toMatch(/(bg|border|text)-(zinc|slate|gray)-/)
      expect(badge.className).not.toMatch(/(emerald|sky|rose)-500\//)

      const dot = badge.querySelector('span.rounded-full') as HTMLElement
      expect(dot).not.toBeNull()
      expectClassesApplied(dot, STATUS_DOT_CLASS[level])
      await expectPaletteRendersAs(dot, STATUS_DOT_CLASS[level])
    }
  )
})
