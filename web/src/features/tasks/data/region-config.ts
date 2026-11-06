import { z } from 'zod'
import { type CategoryHue } from '@/lib/category-palette'

/**
 * 矩形契约：[x1, y1, x2, y2]，严格归一化坐标 ∈ [0.0, 1.0]
 */
export const rectSchema = z
  .tuple([
    z.number().min(0).max(1),
    z.number().min(0).max(1),
    z.number().min(0).max(1),
    z.number().min(0).max(1),
  ])
  .refine(
    ([x1, y1, x2, y2]) => x2 - x1 >= 0.01 - 1e-9 && y2 - y1 >= 0.01 - 1e-9,
    {
      message: '矩形最小边长不得小于 0.01 且 x1 < x2, y1 < y2',
    }
  )

export type Rect = z.infer<typeof rectSchema>

export const MAX_RECTS_PER_CATEGORY = 8
export const MIN_SIDE_LENGTH = 0.01

/**
 * 任务级裁剪窗口的最小边长。
 *
 * 刻意不复用 ROI 的 0.01：crop 的坐标要参与「窗口 → 全幅」的逆映射，窗口越小，
 * 归一化误差被放大的倍数越大（0.01 宽 ≈ 100×）。Go 侧 `region.MinCropSide` 是权威，
 * 这里只做保存前的提前拦截；两处必须同时改。
 */
export const MIN_CROP_SIDE = 0.02

/** 整帧窗口的规范形式；`null` 与它等价（都表示“不裁剪”）。 */
export const FULL_FRAME_CROP: Rect = [0, 0, 1, 1]

/**
 * 裁剪窗口契约：与 {@link rectSchema} 同构，但最小边长更严。
 *
 * 注意 `rectSchema` 本身不改：ROI/MASK 的历史数据里存在 0.01 边长，放宽/收紧都会
 * 让旧任务在打开弹窗时解析失败。
 */
export const cropRectSchema = rectSchema.refine(
  ([x1, y1, x2, y2]) =>
    x2 - x1 >= MIN_CROP_SIDE - 1e-9 && y2 - y1 >= MIN_CROP_SIDE - 1e-9,
  { message: '裁剪窗口最小边长不得小于 0.02' }
)

/** 比较两个窗口是否等价（整帧与 null 视为等价）。 */
export function cropEqual(a: Rect | null, b: Rect | null): boolean {
  const left = a && !isFullFrame(a) ? a : null
  const right = b && !isFullFrame(b) ? b : null
  if (left === null || right === null) return left === right
  return left.every((value, index) => Math.abs(value - right[index]) < 1e-9)
}

/** 是否覆盖整帧。 */
export function isFullFrame(rect: Rect): boolean {
  return rect[0] <= 0 && rect[1] <= 0 && rect[2] >= 1 && rect[3] >= 1
}

/**
 * 规范化 wire 值：整帧折叠为 `null`，非法值也按“不裁剪”处理。
 *
 * 服务端已经校验过一遍，但历史行可能来自手工 SQL；前端不因为一个坏字段
 * 让整个任务解析失败（`taskSchema.parse` 会抛）。
 */
export function parseCrop(value: unknown): Rect | null {
  const result = cropRectSchema.safeParse(value)
  if (!result.success) return null
  return isFullFrame(result.data) ? null : result.data
}

/**
 * 区域策略配置契约
 */
export const MAX_TRIPWIRES = 8
export const MIN_LINE_LENGTH = 0.01

export const tripwireSchema = z
  .object({
    id: z.string().regex(/^[A-Za-z0-9_-]{1,64}$/),
    name: z
      .string()
      .refine((name) => name.trim().length > 0 && [...name].length <= 64),
    points: z.tuple([
      z.tuple([z.number().min(0).max(1), z.number().min(0).max(1)]),
      z.tuple([z.number().min(0).max(1), z.number().min(0).max(1)]),
    ]),
    direction: z.enum(['a_to_b', 'b_to_a', 'both']),
  })
  .refine(
    ({ points: [a, b] }) =>
      Math.hypot(b[0] - a[0], b[1] - a[1]) >= MIN_LINE_LENGTH - 1e-9
  )

export type Tripwire = z.infer<typeof tripwireSchema>

export const regionConfigSchema = z.object({
  roi: z.array(rectSchema).max(MAX_RECTS_PER_CATEGORY).default([]),
  mask: z.array(rectSchema).max(MAX_RECTS_PER_CATEGORY).default([]),
  tripwires: z
    .array(tripwireSchema)
    .max(MAX_TRIPWIRES)
    .refine(
      (lines) => new Set(lines.map((line) => line.id)).size === lines.length
    )
    .optional(),
})

export type RegionConfig = z.infer<typeof regionConfigSchema>

export function parseRegionConfig(
  jsonStr: string | undefined | null
): RegionConfig {
  if (!jsonStr || jsonStr.trim() === '' || jsonStr.trim() === '{}') {
    return { roi: [], mask: [] }
  }
  try {
    const raw = JSON.parse(jsonStr)
    const result = regionConfigSchema.safeParse(raw)
    if (result.success) {
      return result.data
    }
  } catch {
    // Return empty on error
  }
  return { roi: [], mask: [] }
}

export function serializeRegionConfig(cfg: RegionConfig): string {
  if (cfg.roi.length === 0 && cfg.mask.length === 0 && !cfg.tripwires?.length) {
    return '{}'
  }
  return JSON.stringify({
    roi: cfg.roi,
    mask: cfg.mask,
    ...(cfg.tripwires?.length ? { tripwires: cfg.tripwires } : {}),
  })
}

/** 区域分类：ROI（关注区 / 白名单）与 MASK（屏蔽区 / 黑名单）。 */
export const REGION_CATEGORIES = ['roi', 'mask'] as const

export type RegionCategory = (typeof REGION_CATEGORIES)[number]

/**
 * 区域分类 → 分类色板槽位（theme-and-i18n §1.3.6）。
 * ROI/MASK 回答的是「这是哪一类区域」而非「现在什么状态」，因此取分类色相；
 * 严禁占用 teal / amber / destructive / neutral 四个状态色相。
 */
export const REGION_CATEGORY_HUE: Record<RegionCategory, CategoryHue> = {
  roi: 'emerald',
  mask: 'violet',
}

/**
 * 编辑器内部的矩形条目：为每条矩形携带稳定 id。
 * wire 契约（`Rect[]`）不携带标识，因此 id 只存在于编辑期，供 React key 与撤销历史使用，
 * 序列化时由 {@link fromDraft} 丢弃。用数组下标当 key 会在删除/重排时让 React 复用错位的 DOM。
 */
export interface RegionItem {
  id: string
  rect: Rect
}

/** 编辑器内部草稿：与 wire 契约同构，但每条矩形带稳定 id。 */
export interface RegionDraft {
  roi: RegionItem[]
  mask: RegionItem[]
  tripwires?: Tripwire[]
}

/** 一条被选中的矩形。分类与下标必须成对出现，因此合并为一个类型。 */
export interface RegionSelection {
  category: RegionCategory
  index: number
}

/** 构造空草稿。每次返回新对象，避免共享可变状态。 */
export function emptyDraft(): RegionDraft {
  return { roi: [], mask: [] }
}

/**
 * 把 wire 契约转换为带稳定 id 的草稿。
 * id 由调用方提供（通常是组件内的自增 ref），避免模块级可变状态。
 */
export function toDraft(cfg: RegionConfig, makeId: () => string): RegionDraft {
  return {
    roi: cfg.roi.map((rect) => ({ id: makeId(), rect })),
    mask: cfg.mask.map((rect) => ({ id: makeId(), rect })),
    ...(cfg.tripwires?.length ? { tripwires: cfg.tripwires } : {}),
  }
}

/** 把草稿还原为 wire 契约（丢弃 id）。 */
export function fromDraft(draft: RegionDraft): RegionConfig {
  return {
    roi: draft.roi.map((item) => item.rect),
    mask: draft.mask.map((item) => item.rect),
    ...(draft.tripwires?.length ? { tripwires: draft.tripwires } : {}),
  }
}

/** 按分类取草稿中的条目列表。 */
export function draftItems(
  draft: RegionDraft,
  category: RegionCategory
): RegionItem[] {
  return category === 'roi' ? draft.roi : draft.mask
}

/** 按分类替换草稿中的条目列表，其余分类原样保留。 */
export function withDraftItems(
  draft: RegionDraft,
  category: RegionCategory,
  items: RegionItem[]
): RegionDraft {
  return category === 'roi'
    ? { ...draft, roi: items }
    : { ...draft, mask: items }
}
