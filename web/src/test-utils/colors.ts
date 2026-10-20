/**
 * 「调色板类名 → 实际渲染色」闭环断言工具（测试专用）。
 *
 * 分层使用：
 *   - `expectClassesApplied` 确认组件引用了调色板的完整类串；
 *   - `expectPaletteRendersAs` 确认工具类已生成，且实际计算色/alpha 与它引用的 CSS token 一致；
 *   - `expectLevelHueFamily` 为四级状态色提供独立的语义色相期望，避免 token 与渲染色同源导致的假通过；
 *   - `expectDiffersInDarkMode` 确认实际渲染色在 light/dark 间确实变化。
 *
 * 因此，**token 是否映射到正确语义色相**需由调用方提供独立 hue-family 期望；
 * 单独调用 `expectPaletteRendersAs` 只验证渲染结果与运行时 token 一致。
 *
 * ⚠️ 两个必须遵守的实现约束：
 *   1. **断言前必须禁用 transition/animation**。Badge 基类含
 *      `transition-[color,box-shadow]`，切换 `.dark` 后同步读取 `color` 会拿到
 *      过渡前的旧值（border/bg 不在过渡列表里则瞬间生效，于是表现为「只有 text 不切换」）。
 *   2. **颜色比对必须跨色彩空间归一化**。`--destructive` 是 `oklch(...)`，而
 *      `/20` 透明度合成后的计算值是 `oklab(...)`，直接字符串比较必然不等。
 *      统一经 `color-mix(in oklab, … 100%, transparent)` 转换后再比。
 */
import { expect } from 'vitest'

const KIND_TO_PROP = {
  border: 'borderTopColor',
  bg: 'backgroundColor',
  text: 'color',
} as const

type Kind = keyof typeof KIND_TO_PROP

/** 解析 `[dark:]border-teal-200` / `bg-destructive/20` 这类工具类。 */
const UTILITY_RE = /^(dark:)?(border|bg|text)-(.+?)(?:\/(\d+))?$/

let canvasCtx: CanvasRenderingContext2D | null = null

function ctx(): CanvasRenderingContext2D {
  if (!canvasCtx) {
    canvasCtx = document.createElement('canvas').getContext('2d')!
  }
  return canvasCtx
}

/**
 * 用 canvas 解析颜色。输入非法时**抛错**而不是沿用上一次的 fillStyle
 * （否则会静默拿到 `#000000`，让断言以最隐蔽的方式失真）。
 */
function readColor(css: string): string {
  const c = ctx()
  const sentinel = '#123456'
  c.fillStyle = sentinel
  c.fillStyle = css
  const out = c.fillStyle
  if (out === sentinel && css.trim() !== sentinel) {
    throw new Error(`无效的颜色值: "${css}"`)
  }
  return out
}

/** 归一化到 oklab（`color-mix` 会做色彩空间转换，canvas 不会）。 */
function toOKLab(css: string): string {
  return readColor(`color-mix(in oklab, ${css} 100%, transparent)`)
}

/** 取 alpha（无 alpha 时为 1）。 */
function alphaOf(css: string): number {
  const m = toOKLab(css).match(/\/\s*([\d.]+)%?\s*\)$/)
  return m ? Number(m[1]) : 1
}

/**
 * 数值比对两个颜色。
 *
 * ⚠️ 不能用字符串相等：Tailwind 生成的透明度合成是
 * `color-mix(in srgb, … N%, transparent)`，而本模块归一化走 `in oklab`，
 * 两条路径的舍入在第 7 位小数上会差 1（实测 `0.0511765` vs `0.0511766`）。
 * oklab 下任何**可见**色差都在 1e-2 量级，因此 1e-3 的容差既能吸收舍入噪声，
 * 又远小于任何可感知差异。
 */
function expectSameColor(actual: string, expected: string, msg: string): void {
  const A = oklabOf(actual)
  const B = oklabOf(expected)
  expect(A.l, `${msg} (L)`).toBeCloseTo(B.l, 3)
  expect(A.a, `${msg} (a)`).toBeCloseTo(B.a, 3)
  expect(A.b, `${msg} (b)`).toBeCloseTo(B.b, 3)
}

/** 读取 `:root` 上的语义 token；优先 Tailwind 规范名 `--color-*`，回落裸名。 */
function resolveToken(name: string): string {
  const root = getComputedStyle(document.documentElement)
  return (
    root.getPropertyValue(`--color-${name}`).trim() ||
    root.getPropertyValue(`--${name}`).trim()
  )
}

/** 读取元素的计算色。 */
function computedColor(el: Element, prop: (typeof KIND_TO_PROP)[Kind]): string {
  return getComputedStyle(el).getPropertyValue(
    // getPropertyValue 只认 kebab-case
    prop === 'borderTopColor'
      ? 'border-top-color'
      : prop === 'backgroundColor'
        ? 'background-color'
        : 'color'
  )
}

/** 幂等安装「禁用过渡」样式表。每个测试文件独立 iframe，不会跨文件泄漏。 */
function suppressTransitions(): void {
  if (document.getElementById('color-assertion-env')) return
  const style = document.createElement('style')
  style.id = 'color-assertion-env'
  style.textContent =
    '*, *::before, *::after { transition: none !important; animation: none !important; }'
  document.head.appendChild(style)
}

/** 在指定明暗模式下执行，并保证还原（不污染同文件的其他用例）。 */
async function withDarkMode<T>(
  enabled: boolean,
  fn: () => T | Promise<T>
): Promise<T> {
  const root = document.documentElement
  const was = root.classList.contains('dark')
  root.classList.toggle('dark', enabled)
  try {
    return await fn()
  } finally {
    root.classList.toggle('dark', was)
  }
}

/** 色相族：`[min, max]`（度）或 `'neutral'`（色度需接近 0）。 */
type HueFamily = readonly [number, number] | 'neutral'

/**
 * §1.3.3 四档语义色相族。
 *
 * 范围刻意取宽（只要求「不跑到别的语义色相上」），因此调色板换色阶
 * （如 amber-800 → amber-700）不会误报，但把破坏色改成蓝色这类错误必被抓住。
 */
export const LEVEL_HUE_FAMILY: Record<
  'active' | 'warning' | 'destructive' | 'inactive',
  HueFamily
> = {
  active: [140, 215], // teal / emerald 系
  warning: [40, 115], // amber / orange 系
  destructive: [0, 35], // 红系
  inactive: 'neutral',
}

/** 把任意 CSS 颜色归一化成 oklab 三分量。 */
function oklabOf(css: string): { l: number; a: number; b: number } {
  const m = toOKLab(css).match(/oklab\(([^)]+)\)/)
  if (!m) throw new Error(`无法解析为 oklab: "${css}"`)
  const [l, a, b] = m[1].split('/')[0].trim().split(/\s+/).map(Number)
  if ([l, a, b].some((v) => !Number.isFinite(v))) {
    throw new Error(`oklab 分量非数值: "${css}" -> "${m[1]}"`)
  }
  return { l, a, b }
}

/** oklch 色相（度，0..360）。 */
function tokenHue(css: string): number {
  const { a, b } = oklabOf(css)
  return ((Math.atan2(b, a) * 180) / Math.PI + 360) % 360
}

/** oklch 色度（0 = 纯中性灰）。 */
function tokenChroma(css: string): number {
  const { a, b } = oklabOf(css)
  return Math.hypot(a, b)
}

/** 从调色板类串里取出代表该档语义的 border token 名。 */
function borderTokenOf(paletteClasses: string): string | null {
  for (const cls of paletteClasses.split(/\s+/)) {
    const m = cls.match(/^border-(.+?)(?:\/\d+)?$/)
    if (m) return m[1]
  }
  return null
}

/**
 * 断言一档语义色的**色相族**（light + dark 两个模式都查）。
 *
 * ⚠️ 这一层是必需的，不能省：`expectPaletteRendersAs` 比的是「元素渲染色 vs 运行时
 * 解析出的 token」，两边**同源** —— 一旦 token 自身在 `theme.css` 里被改错，
 * 两边会一起变、永远相等，断言退化为同义反复。要抓住「类名写对了但 token
 * 定义错了」，必须有独立于运行时的语义期望，那就是「这档色应该是什么色相」。
 *
 * @param paletteClasses 调色板类串（取其 border token 作为该档代表色）
 * @param family 期望的色相族
 */
export async function expectLevelHueFamily(
  paletteClasses: string,
  family: HueFamily
): Promise<void> {
  const tokenName = borderTokenOf(paletteClasses)
  expect(tokenName, '调色板类串里应有 border-* 作为该档代表色').not.toBeNull()

  for (const dark of [false, true]) {
    await withDarkMode(dark, () => {
      const mode = dark ? 'dark' : 'light'
      const raw = resolveToken(tokenName!)
      expect(raw, `[${mode}] 无法解析 token ${tokenName}`).not.toBe('')

      if (family === 'neutral') {
        expect(
          tokenChroma(raw),
          `[${mode}] ${tokenName} 应为中心中性色（色度≈0）`
        ).toBeLessThan(0.02)
        return
      }

      const [min, max] = family
      const hue = tokenHue(raw)
      expect(
        hue >= min && hue <= max,
        `[${mode}] ${tokenName} 的色相 ${hue.toFixed(1)}° 应落在 ${min}°–${max}°`
      ).toBe(true)
    })
  }
}

/**
 * 断言元素应用了给定调色板类串里的**每一个**类。
 *
 * 按词元精确比对而不是子串包含：`toContain('text-teal-200')` 这类写法既会
 * 被 `dark:text-teal-200` 误命中，也经不起 twMerge 重排类名。
 */
export function expectClassesApplied(
  el: HTMLElement,
  paletteClasses: string
): void {
  const applied = el.className.split(/\s+/).filter(Boolean)
  for (const cls of paletteClasses.split(/\s+/).filter(Boolean)) {
    expect(applied, `应应用调色板类 ${cls}`).toContain(cls)
  }
}

/**
 * 断言调色板类串中的每个工具类都渲染成了它引用的语义 token。
 *
 * 覆盖 light 与 dark 两种模式：
 *   - 有 `dark:` 配对时，dark 模式下必须切到 dark 变体引用的 token；
 *   - 无 `dark:` 配对时（如实心分段色），dark 模式下必须与 light 保持一致。
 *
 * @param el 目标元素
 * @param paletteClasses 调色板类串，直接取自 `@/lib/status-palette` 等单一真实源
 */
export async function expectPaletteRendersAs(
  el: HTMLElement,
  paletteClasses: string
): Promise<void> {
  suppressTransitions()

  const classes = paletteClasses.split(/\s+/).filter(Boolean)
  const parsed = classes.map((cls) => {
    const m = cls.match(UTILITY_RE)
    return m
      ? {
          cls,
          dark: Boolean(m[1]),
          kind: m[2] as Kind,
          token: m[3],
          alpha: m[4] ? Number(m[4]) / 100 : 1,
        }
      : null
  })

  const darkKinds = new Set(parsed.filter((p) => p?.dark).map((p) => p!.kind))

  for (const dark of [false, true]) {
    await withDarkMode(dark, () => {
      for (const item of parsed) {
        if (!item) continue
        // light 模式跑亮色类，dark 模式跑 dark 类；
        // 若某 kind 没有 dark 配对，则在 dark 模式下仍校验亮色类（双模一致）
        const shouldCheck = item.dark
          ? dark
          : !dark || !darkKinds.has(item.kind)
        if (!shouldCheck) continue

        const actual = computedColor(el, KIND_TO_PROP[item.kind])
        const expected = resolveToken(item.token)
        const mode = dark ? 'dark' : 'light'

        expect(
          expected,
          `[${mode}] 无法解析 ${item.cls} 的语义 token`
        ).not.toBe('')
        expectSameColor(
          actual,
          expected,
          `[${mode}] ${item.cls} 的渲染色应等于 ${item.token} (${expected})`
        )
        expect(
          alphaOf(actual),
          `[${mode}] ${item.cls} 的 alpha 应为 ${item.alpha}`
        ).toBeCloseTo(item.alpha, 2)
      }
    })
  }
}

/**
 * 断言两个元素在同一属性上渲染出**相同的颜色**。
 *
 * 用于设计系统的「必须同色」契约，例如 §1.3.6 要求堆叠条的图例圆点
 * 与对应分段同色 —— 单靠断言两处类名相同是查不出「圆点写 blue-500、分段写 blue-600」的。
 */
export function expectSameComputedColor(
  a: Element,
  b: Element,
  prop: (typeof KIND_TO_PROP)[Kind],
  msg?: string
): void {
  const colorA = computedColor(a, prop)
  const colorB = computedColor(b, prop)
  const label = msg ?? `两处 ${prop} 应渲染色相同`
  expectSameColor(colorA, colorB, label)
  expect(alphaOf(colorA), `${label} (alpha)`).toBeCloseTo(alphaOf(colorB), 3)
}

/** 断言两种模式下计算色（含 alpha）**确实不同**，防止 dark 配对无实际变化。 */
export async function expectDiffersInDarkMode(
  el: HTMLElement,
  prop: (typeof KIND_TO_PROP)[Kind]
): Promise<void> {
  suppressTransitions()
  const lightColor = await withDarkMode(false, () => computedColor(el, prop))
  const darkColor = await withDarkMode(true, () => computedColor(el, prop))
  const light = oklabOf(lightColor)
  const dark = oklabOf(darkColor)
  const delta =
    Math.abs(light.l - dark.l) +
    Math.abs(light.a - dark.a) +
    Math.abs(light.b - dark.b) +
    Math.abs(alphaOf(lightColor) - alphaOf(darkColor))
  expect(delta, `${prop} 的 dark 变体不应与 light 相同`).toBeGreaterThan(1e-3)
}
