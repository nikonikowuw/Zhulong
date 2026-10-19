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
 * 画布叠加层：实心分类色边框 + 半透明填充。
 * 用于「贴在视频画面上」的分类图元（如 ROI / MASK 区域矩形）——这类图元需要比徽章更强的
 * 边界对比度，因此边框取 500 满色而非徽章的 `/30` 半透明。
 */
export const CATEGORY_REGION_CLASS: Record<CategoryHue, string> = {
  blue: 'border-blue-500 bg-blue-500/20',
  violet: 'border-violet-500 bg-violet-500/20',
  emerald: 'border-emerald-500 bg-emerald-500/20',
  fuchsia: 'border-fuchsia-500 bg-fuchsia-500/20',
  cyan: 'border-cyan-500 bg-cyan-500/20',
}

/**
 * 画布拖拽预览：虚线边框 + 略高填充透明度，与落定后的实心边框图元区分。
 * 调用方需自行叠加 `border-dashed`。
 */
export const CATEGORY_REGION_PREVIEW_CLASS: Record<CategoryHue, string> = {
  blue: 'border-blue-400 bg-blue-500/25',
  violet: 'border-violet-400 bg-violet-500/25',
  emerald: 'border-emerald-400 bg-emerald-500/25',
  fuchsia: 'border-fuchsia-400 bg-fuchsia-500/25',
  cyan: 'border-cyan-400 bg-cyan-500/25',
}

/** 独立分类文字（工具按钮、列表分组标题），不带底色，双模成对。 */
export const CATEGORY_TEXT_CLASS: Record<CategoryHue, string> = {
  blue: 'text-blue-600 dark:text-blue-400',
  violet: 'text-violet-600 dark:text-violet-400',
  emerald: 'text-emerald-600 dark:text-emerald-400',
  fuchsia: 'text-fuchsia-600 dark:text-fuchsia-400',
  cyan: 'text-cyan-600 dark:text-cyan-400',
}

/** 未声明分类的画布图元兜底：中性色，不占用任何分类色相。 */
export const CATEGORY_NEUTRAL_REGION_CLASS =
  'border-muted-foreground/60 bg-muted-foreground/20'

/** 未声明分类的独立文字兜底。 */
export const CATEGORY_NEUTRAL_TEXT_CLASS = 'text-muted-foreground'

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

/** 安全取画布图元类名：未声明分类回落到中性兜底。 */
export function categoryRegionClass(hue: CategoryHue | undefined): string {
  return hue ? CATEGORY_REGION_CLASS[hue] : CATEGORY_NEUTRAL_REGION_CLASS
}

/** 安全取画布预览类名：未声明分类回落到中性兜底。 */
export function categoryRegionPreviewClass(
  hue: CategoryHue | undefined
): string {
  return hue
    ? CATEGORY_REGION_PREVIEW_CLASS[hue]
    : CATEGORY_NEUTRAL_REGION_CLASS
}

/** 安全取独立文字类名：未声明分类回落到中性兜底。 */
export function categoryTextClass(hue: CategoryHue | undefined): string {
  return hue ? CATEGORY_TEXT_CLASS[hue] : CATEGORY_NEUTRAL_TEXT_CLASS
}
