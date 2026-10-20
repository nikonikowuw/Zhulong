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
import { type AlgorithmStatus } from '../data/schema'
import { AlgorithmStatusBadge } from './algorithm-status-badge'

/**
 * §1.3.3 经典四级状态语义调色板断言。
 *
 * 分三层，缺一不可：
 *   1. `expectClassesApplied` —— 组件必须取用调色板里的那一档类串（单一真实源，
 *      防止有人就地手写色串）；
 *   2. `expectPaletteRendersAs` —— 这些类名必须**真的渲染成**它们引用的语义 token，
 *      且 light / dark 双模各自成立（防止类被覆盖、Tailwind 没生成该类、
 *      dark 配对缺失）；
 *   3. `expectLevelHueFamily` —— 该档 token 的**色相族**必须正确。第 2 层是
 *      「渲染色 vs 运行时 token」的同源比对，token 被改错时两边会一起变，
 *      因此只有这一层能抓住「类名对了但 `theme.css` 把破坏色改成了蓝色」。
 *
 * 期望值全部从 `@/lib/status-palette` 推导，不在测试里硬编码任何色值或 token 名。
 */
type Level = keyof typeof STATUS_LEVEL_CLASS

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
      ) as HTMLElement
      expect(badge).not.toBeNull()

      expectClassesApplied(badge, STATUS_LEVEL_CLASS[level])
      await expectPaletteRendersAs(badge, STATUS_LEVEL_CLASS[level])
      await expectLevelHueFamily(
        STATUS_LEVEL_CLASS[level],
        LEVEL_HUE_FAMILY[level]
      )
      // dark 配对必须真的改变渲染色：若 theme.css 删掉 .dark 的 token 覆盖，
      // 同源比对会两边一起变而通过，只有这一条能抓住「双模同色」。
      await expectDiffersInDarkMode(badge, 'borderTopColor')

      // 严禁非语义灰阶与 500 级透明色私造样式
      expect(badge.className).not.toMatch(/(bg|border|text)-(zinc|slate|gray)-/)
      expect(badge.className).not.toMatch(/(emerald|sky|rose)-500\//)

      const dot = badge.querySelector('span.rounded-full') as HTMLElement
      expect(dot).not.toBeNull()
      expectClassesApplied(dot, STATUS_DOT_CLASS[level])
      await expectPaletteRendersAs(dot, STATUS_DOT_CLASS[level])
    }
  )
})
