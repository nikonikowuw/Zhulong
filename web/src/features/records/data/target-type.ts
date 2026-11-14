import { type TFunction } from 'i18next'
import { categoryBadgeClass, type CategoryHue } from '@/lib/category-palette'

/**
 * 抓拍目标类型 → §1.3.6 分类色板槽位。
 *
 * `targetType` 是后端开放字符串（schema 中为 `z.string()`），并非封闭枚举，
 * 因此只显式映射已知类型，长尾一律回落 §1.3.6 的中性兜底。
 *
 * 该映射集中于此，避免同一取值在「表格视图」与「卡片视图」中被渲染成不同颜色。
 */
const TARGET_TYPE_HUE: Record<string, CategoryHue> = {
  person: 'blue',
  vehicle: 'violet',
  face: 'emerald',
}

/** 取目标类型徽章类名；未声明的类型回落中性兜底。 */
export function targetTypeBadgeClass(targetType?: string): string {
  return categoryBadgeClass(
    targetType ? TARGET_TYPE_HUE[targetType] : undefined
  )
}

/**
 * 本地化格式化目标类型名称。
 *
 * 优先从 `records.targetTypes.<type>` 获取翻译，若未配置或为长尾未知类型，
 * 则直接原样返回该类型字符串（或回落至未知占位）。
 */
export function formatTargetType(t: TFunction, targetType?: string): string {
  if (!targetType || !targetType.trim()) {
    return t('targetTypes.unknown')
  }
  const key = `targetTypes.${targetType}`
  return t(key, { defaultValue: targetType })
}
