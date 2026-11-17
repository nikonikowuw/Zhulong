import { z } from 'zod'
import { type Algorithm } from '@/features/algorithms/contract'

/**
 * 任务算法的事件录像配置（`TaskAlgorithm.clipConfigJson`）。
 *
 * 与 `regionConfigJson` 一样以 JSON 字符串往返；`''` / `'{}'` 视为「关闭 + 默认值」，
 * 这是存量订阅升级后的行为（默认不录像）。
 */
export const CLIP_PRE_SECONDS_MIN = 1
export const CLIP_PRE_SECONDS_MAX = 15
export const CLIP_PRE_SECONDS_DEFAULT = 5
export const CLIP_POST_SECONDS_MIN = 1
export const CLIP_POST_SECONDS_MAX = 60
export const CLIP_POST_SECONDS_DEFAULT = 10

export const CLIP_RECOGNITION_FILTERS = ['all', 'stranger', 'known'] as const
export type ClipRecognitionFilter = (typeof CLIP_RECOGNITION_FILTERS)[number]

export interface ClipConfig {
  enabled: boolean
  preSeconds: number
  postSeconds: number
  recognitionFilter: ClipRecognitionFilter
}

export const DEFAULT_CLIP_CONFIG: ClipConfig = {
  enabled: false,
  preSeconds: CLIP_PRE_SECONDS_DEFAULT,
  postSeconds: CLIP_POST_SECONDS_DEFAULT,
  recognitionFilter: 'all',
}

function clampInt(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, Math.round(value)))
}

/**
 * 宽松解析：逐字段 `catch` 回落默认值，而不是整份作废 ——
 * 某个字段越界不应该把用户已开启的录像开关一并清掉。
 */
const clipConfigSchema = z.object({
  enabled: z.boolean().catch(DEFAULT_CLIP_CONFIG.enabled),
  preSeconds: z
    .number()
    .finite()
    .transform((v) => clampInt(v, CLIP_PRE_SECONDS_MIN, CLIP_PRE_SECONDS_MAX))
    .catch(DEFAULT_CLIP_CONFIG.preSeconds),
  postSeconds: z
    .number()
    .finite()
    .transform((v) => clampInt(v, CLIP_POST_SECONDS_MIN, CLIP_POST_SECONDS_MAX))
    .catch(DEFAULT_CLIP_CONFIG.postSeconds),
  recognitionFilter: z
    .enum(CLIP_RECOGNITION_FILTERS)
    .catch(DEFAULT_CLIP_CONFIG.recognitionFilter),
})

/** 解析 `clipConfigJson`；空串、`{}`、非法 JSON 都回落到关闭态默认值。 */
export function parseClipConfig(raw: string | undefined | null): ClipConfig {
  const text = raw?.trim()
  if (!text) return { ...DEFAULT_CLIP_CONFIG }
  let json: unknown
  try {
    json = JSON.parse(text)
  } catch {
    return { ...DEFAULT_CLIP_CONFIG }
  }
  if (typeof json !== 'object' || json === null || Array.isArray(json)) {
    return { ...DEFAULT_CLIP_CONFIG }
  }
  return clipConfigSchema.parse(json)
}

/** 序列化为提交载荷：关闭时统一归一化为 `{}`（避免开关切换引发无谓重启）；数值钳到合法区间。 */
export function serializeClipConfig(config: ClipConfig): string {
  if (!config.enabled) return '{}'
  return JSON.stringify({
    enabled: config.enabled,
    preSeconds: clampInt(
      config.preSeconds,
      CLIP_PRE_SECONDS_MIN,
      CLIP_PRE_SECONDS_MAX
    ),
    postSeconds: clampInt(
      config.postSeconds,
      CLIP_POST_SECONDS_MIN,
      CLIP_POST_SECONDS_MAX
    ),
    recognitionFilter: config.recognitionFilter,
  })
}

/**
 * 只有告警 / 识别类算法会触发事件录像；`vehicle_structure` 只写目标抓拍，不触发片段。
 */
export function supportsClipRecording(
  taskType: Algorithm['taskType'] | undefined
): boolean {
  return taskType !== undefined && taskType !== 'vehicle_structure'
}

/**
 * 识别过滤只对识别类算法有意义：后端只有 person_structure 产出识别记录
 * （陌生人 / 已知人员之分），检测类算法产出的是告警，没有这一维度。
 */
export function supportsRecognitionFilter(
  taskType: Algorithm['taskType'] | undefined
): boolean {
  return taskType === 'person_structure'
}
