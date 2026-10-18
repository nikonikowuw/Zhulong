/**
 * §1.3.3 经典四级状态语义调色板 (Status Palette)
 *
 * 四个语义级别 —— 活跃 / 进行中 / 破坏性 / 未激活 —— 的**唯一**类名来源。
 * 禁止在业务组件内就地手写色串：本会话已修复多起因复制粘贴导致的「暗色配对缺失」漂移。
 *
 * 三种渲染形态（同一级别，语义一致）：
 * - 徽章 / 状态面板：`STATUS_LEVEL_CLASS`（border + bg + text 三元组，已含双模配对）
 * - 指示圆点：`STATUS_DOT_CLASS`
 * - 独立状态文字：`STATUS_TEXT_CLASS`
 *
 * 完整规范见 `.trellis/spec/frontend/theme-and-i18n.md` §1.3.3。
 *
 * ⚠️ Tailwind CSS v4 采用**静态提取**：类名必须以完整字面量出现才会生成，
 *    因此本模块一律查表，严禁改写成 `border-${hue}-200` 这类模板拼接。
 */

/** 四个语义级别。 */
export const STATUS_LEVELS = [
  'active',
  'warning',
  'destructive',
  'inactive',
] as const

export type StatusLevel = (typeof STATUS_LEVELS)[number]

/**
 * 徽章与状态面板共用：亮色 `border-200 + bg-100/30 + text-900`，
 * 暗色成对降级为 `dark:border-800 + dark:bg-950/30 + dark:text-200`。
 */
export const STATUS_LEVEL_CLASS: Record<StatusLevel, string> = {
  active:
    'border-teal-200 bg-teal-100/30 text-teal-900 dark:border-teal-800 dark:bg-teal-950/30 dark:text-teal-200',
  warning:
    'border-amber-200 bg-amber-100/30 text-amber-900 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200',
  destructive:
    'border-destructive/20 bg-destructive/10 text-destructive dark:border-destructive/40 dark:bg-destructive/20 dark:text-destructive',
  inactive:
    'border-neutral-300 bg-neutral-300/40 text-muted-foreground dark:border-neutral-700 dark:bg-neutral-800/40',
}

/** 内嵌指示圆点（Dot-Indicator Outline 模式）。 */
export const STATUS_DOT_CLASS: Record<StatusLevel, string> = {
  active: 'bg-emerald-500',
  warning: 'bg-amber-500',
  destructive: 'bg-destructive',
  inactive: 'bg-muted-foreground/60',
}

/** 独立状态文字（数值高亮等），保持强调感而不使用徽章底色。 */
export const STATUS_TEXT_CLASS: Record<StatusLevel, string> = {
  active: 'text-teal-600 dark:text-teal-400',
  warning: 'text-amber-600 dark:text-amber-400',
  destructive: 'text-destructive',
  inactive: 'text-muted-foreground',
}

/** 安全取级别类名：运行期出现未声明状态时回落「未激活」。 */
export function statusLevelClass(level: StatusLevel | undefined): string {
  return level ? STATUS_LEVEL_CLASS[level] : STATUS_LEVEL_CLASS.inactive
}

/** 安全取圆点类名。 */
export function statusDotClass(level: StatusLevel | undefined): string {
  return level ? STATUS_DOT_CLASS[level] : STATUS_DOT_CLASS.inactive
}

/** 安全取状态文字类名。 */
export function statusTextClass(level: StatusLevel | undefined): string {
  return level ? STATUS_TEXT_CLASS[level] : STATUS_TEXT_CLASS.inactive
}
