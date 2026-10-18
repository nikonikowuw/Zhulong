/**
 * §1.3.6 分类色板 (Category Palette)
 *
 * 用于「分类标签」—— 任务类型、解码后端、存储构成等，与 §1.3.3 的「状态」语义严格分离：
 * 分类徽章**不带状态圆点**，且**严禁**占用 teal / amber / destructive / neutral 四个状态色相。
 *
 * 完整规范见 `.trellis/spec/frontend/theme-and-i18n.md` §1.3.6。
 *
 * ⚠️ Tailwind CSS v4 采用**静态提取**：类名必须以完整字面量出现在源码中才会被生成，
 *    `border-${hue}-500/30` 这类模板拼接会被静默丢弃。因此本模块一律使用查表，
 *    严禁改写成动态插值。
 */

/** 分类色相槽位（有序）。新增分类按顺序取用，不得跳号或自造色相。 */
export const CATEGORY_HUES = [
  'blue',
  'violet',
  'emerald',
  'fuchsia',
  'cyan',
] as const

export type CategoryHue = (typeof CATEGORY_HUES)[number]

/**
 * 徽章渲染：亮色 `border/30 + bg/10 + text-600`，暗色成对升级为 `border/40 + bg/20 + text-400`。
 * 暗色不同步提升透明度会让 10% 底色在深色背景上几乎不可见。
 */
export const CATEGORY_BADGE_CLASS: Record<CategoryHue, string> = {
  blue: 'border-blue-500/30 bg-blue-500/10 text-blue-600 dark:border-blue-500/40 dark:bg-blue-500/20 dark:text-blue-400',
  violet:
    'border-violet-500/30 bg-violet-500/10 text-violet-600 dark:border-violet-500/40 dark:bg-violet-500/20 dark:text-violet-400',
  emerald:
    'border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:border-emerald-500/40 dark:bg-emerald-500/20 dark:text-emerald-400',
  fuchsia:
    'border-fuchsia-500/30 bg-fuchsia-500/10 text-fuchsia-600 dark:border-fuchsia-500/40 dark:bg-fuchsia-500/20 dark:text-fuchsia-400',
  cyan: 'border-cyan-500/30 bg-cyan-500/10 text-cyan-600 dark:border-cyan-500/40 dark:bg-cyan-500/20 dark:text-cyan-400',
}

/**
 * 实心渲染：堆叠条分段、图例圆点等不透明场景。
 * 双模一致（不加深浅分支），图例圆点必须与对应分段同色。
 */
export const CATEGORY_SOLID_CLASS: Record<CategoryHue, string> = {
  blue: 'bg-blue-500',
  violet: 'bg-violet-500',
  emerald: 'bg-emerald-500',
  fuchsia: 'bg-fuchsia-500',
  cyan: 'bg-cyan-500',
}

/** 「未分类 / 未知」兜底徽章：复用 §1.3.3 未激活档，不占用分类色相。 */
export const CATEGORY_NEUTRAL_BADGE_CLASS =
  'border-neutral-300 bg-neutral-300/40 text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40'

/** 「其它」兜底分段：语义中性色，随主题自适应。 */
export const CATEGORY_NEUTRAL_SOLID_CLASS = 'bg-muted-foreground/60'

/**
 * 安全取徽章类名：运行期出现未声明的分类串时回落到中性兜底。
 * 业务组件应传入显式映射后的槽位，而不是数组下标。
 */
export function categoryBadgeClass(hue: CategoryHue | undefined): string {
  return hue ? CATEGORY_BADGE_CLASS[hue] : CATEGORY_NEUTRAL_BADGE_CLASS
}

/** 安全取实心类名：未声明分类回落到中性兜底。 */
export function categorySolidClass(hue: CategoryHue | undefined): string {
  return hue ? CATEGORY_SOLID_CLASS[hue] : CATEGORY_NEUTRAL_SOLID_CLASS
}
